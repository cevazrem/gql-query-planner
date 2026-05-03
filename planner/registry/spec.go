package registry

import (
	"context"
	"fmt"
)

type ResolveRequest struct {
	ParentType string
	FieldName  string
	Parent     any
	Args       map[string]any
}

type BatchResolveRequest struct {
	ParentType string
	FieldName  string
	Parents    []any
	Args       map[string]any
}

type DirectResolver func(ctx context.Context, req ResolveRequest) (any, error)
type BatchResolver func(ctx context.Context, req BatchResolveRequest) ([]any, error)

type FieldSpec struct {
	Direct     DirectResolver
	Vectorized BatchResolver
}

func Field(direct DirectResolver) FieldSpec {
	return FieldSpec{Direct: direct}
}

func BatchableField(direct DirectResolver, vectorized BatchResolver) FieldSpec {
	return FieldSpec{Direct: direct, Vectorized: vectorized}
}

// BatchableFromVectorized builds a field spec from a single vectorized implementation.
// For non-batched execution the planner calls the same implementation with one parent.
func BatchableFromVectorized(vectorized BatchResolver) FieldSpec {
	return FieldSpec{
		Direct: func(ctx context.Context, req ResolveRequest) (any, error) {
			vals, err := vectorized(ctx, BatchResolveRequest{
				ParentType: req.ParentType,
				FieldName:  req.FieldName,
				Parents:    []any{req.Parent},
				Args:       req.Args,
			})
			if err != nil {
				return nil, err
			}
			if len(vals) != 1 {
				return nil, fmt.Errorf("registry: vectorized resolver for %s.%s returned %d results for single parent", req.ParentType, req.FieldName, len(vals))
			}
			return vals[0], nil
		},
		Vectorized: vectorized,
	}
}

func (s FieldSpec) CanBatch() bool {
	return s.Vectorized != nil || s.Direct != nil
}

func (s FieldSpec) ResolveBatch(ctx context.Context, req BatchResolveRequest) ([]any, error) {
	if s.Vectorized != nil {
		return s.Vectorized(ctx, req)
	}
	if s.Direct == nil {
		return nil, fmt.Errorf("registry: missing resolver for %s.%s", req.ParentType, req.FieldName)
	}
	out := make([]any, len(req.Parents))
	for i, parent := range req.Parents {
		v, err := s.Direct(ctx, ResolveRequest{
			ParentType: req.ParentType,
			FieldName:  req.FieldName,
			Parent:     parent,
			Args:       req.Args,
		})
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}
