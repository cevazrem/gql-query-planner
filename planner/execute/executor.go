package execute

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/cevazrem/gql-query-planner/planner/logical"
	"github.com/cevazrem/gql-query-planner/planner/physical"
	"github.com/cevazrem/gql-query-planner/planner/registry"

	"golang.org/x/sync/errgroup"
)

type Executor struct {
	reg *registry.Registry
}

func New(reg *registry.Registry) *Executor {
	return &Executor{reg: reg}
}

func (e *Executor) ExecuteRoot(ctx context.Context, root *logical.Node, plan *physical.Plan) (map[string]any, error) {
	data, _, err := e.ExecuteRootWithRuntime(ctx, root, plan)
	return data, err
}

func (e *Executor) ExecuteRootWithRuntime(ctx context.Context, root *logical.Node, plan *physical.Plan) (map[string]any, *Runtime, error) {
	if root == nil {
		return nil, nil, fmt.Errorf("executor: nil root")
	}
	rt := NewRuntime()
	gate := make(chan struct{}, maxInt(1, plan.MaxInFlight))
	cache := newRequestCache()

	out := make(map[string]any, len(root.Children))
	for _, child := range root.Children {
		val, err := e.executeNode(ctx, child, plan, nil, rt, gate, cache)
		if err != nil {
			return nil, rt, err
		}
		out[responseKey(child)] = val
	}
	return out, rt, nil
}

func (e *Executor) executeNode(ctx context.Context, node *logical.Node, plan *physical.Plan, parent any, rt *Runtime, gate chan struct{}, cache *requestCache) (any, error) {
	if node == nil {
		return nil, nil
	}
	spec, ok := e.reg.Lookup(node.ParentType, node.FieldName)
	if !ok {
		return nil, fmt.Errorf("executor: no field spec for %s.%s", node.ParentType, node.FieldName)
	}

	rt.AddLogicalCalls(node.ID, 1)
	strat, _ := plan.Strategies[node.ID]

	resolveDirect := func() (any, error) {
		gate <- struct{}{}
		done := rt.BeginPhysical(node.ID)
		value, err := spec.Direct(ctx, registry.ResolveRequest{
			ParentType: node.ParentType,
			FieldName:  node.FieldName,
			Parent:     parent,
			Args:       node.Args,
		})
		done()
		<-gate
		return value, err
	}

	var (
		value any
		err   error
	)

	if shouldUseNodeCache(node, strat) {
		key := cacheKeyForNode(node, parent)
		lookup := cache.lookupOrReserve(key)
		switch lookup.state {
		case cacheLookupHitReady:
			rt.AddCacheLookup(node.ID, CacheLookupHit)
			value, err = lookup.entry.val, lookup.entry.err
		case cacheLookupHitPending:
			rt.AddCacheLookup(node.ID, CacheLookupWait)
			value, err = lookup.entry.wait(ctx)
		case cacheLookupMissOwner:
			rt.AddCacheLookup(node.ID, CacheLookupMiss)
			value, err = resolveDirect()
			cache.complete(key, value, err)
		default:
			value, err = resolveDirect()
		}
	} else {
		value, err = resolveDirect()
	}

	if err != nil {
		return nil, err
	}

	rt.AddRowsOut(node.ID, countRowsOut(node.IsList, value))
	rt.AddWidthBytes(node.ID, estimateObservedWidthBytes(value, node.IsList))

	if value == nil || len(node.Children) == 0 {
		return value, nil
	}
	if node.IsList {
		return e.executeListValue(ctx, node, plan, value, rt, gate, cache)
	}
	return e.executeObjectValue(ctx, node, plan, value, rt, gate, cache)
}

func (e *Executor) executeObjectValue(ctx context.Context, node *logical.Node, plan *physical.Plan, obj any, rt *Runtime, gate chan struct{}, cache *requestCache) (map[string]any, error) {
	out := make(map[string]any, len(node.Children))
	strat, _ := plan.Strategies[node.ID]

	children := append([]*logical.Node(nil), node.Children...)
	sort.SliceStable(children, func(i, j int) bool { return responseKey(children[i]) < responseKey(children[j]) })

	if strat.FieldsMode == physical.FieldsParallel {
		var mu sync.Mutex
		g, gctx := errgroup.WithContext(ctx)
		sem := make(chan struct{}, maxInt(1, strat.Workers))

		for _, child := range children {
			child := child
			sem <- struct{}{}
			g.Go(func() error {
				defer func() { <-sem }()
				v, err := e.executeNode(gctx, child, plan, obj, rt, gate, cache)
				if err != nil {
					return err
				}
				mu.Lock()
				out[responseKey(child)] = v
				mu.Unlock()
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return nil, err
		}
		return out, nil
	}

	for _, child := range children {
		v, err := e.executeNode(ctx, child, plan, obj, rt, gate, cache)
		if err != nil {
			return nil, err
		}
		out[responseKey(child)] = v
	}
	return out, nil
}

func (e *Executor) executeListValue(ctx context.Context, node *logical.Node, plan *physical.Plan, value any, rt *Runtime, gate chan struct{}, cache *requestCache) ([]any, error) {
	items, err := reflectToAnySlice(value)
	if err != nil {
		return nil, err
	}
	if len(node.Children) == 0 {
		return items, nil
	}
	return e.materializeObjectList(ctx, items, node, plan, rt, gate, cache)
}

func (e *Executor) materializeObjectList(ctx context.Context, items []any, parentField *logical.Node, plan *physical.Plan, rt *Runtime, gate chan struct{}, cache *requestCache) ([]any, error) {
	out := make([]map[string]any, len(items))
	for i := range out {
		out[i] = map[string]any{}
	}

	parentStrat, _ := plan.Strategies[parentField.ID]
	children := append([]*logical.Node(nil), parentField.Children...)
	sort.SliceStable(children, func(i, j int) bool { return responseKey(children[i]) < responseKey(children[j]) })

	for _, child := range children {
		child := child
		childSpec, ok := e.reg.Lookup(child.ParentType, child.FieldName)
		if !ok {
			return nil, fmt.Errorf("executor: no child field spec for %s.%s", child.ParentType, child.FieldName)
		}
		childStrat, _ := plan.Strategies[child.ID]

		if child.IsList && childStrat.ListMode == physical.ListBatched && childSpec.CanBatch() {
			batchVals, err := e.resolveBatchedChild(ctx, items, child, childSpec, childStrat, rt, gate, cache)
			if err != nil {
				return nil, err
			}

			materialized, err := e.materializeBatchedChildValues(ctx, batchVals, child, plan, rt, gate, cache)
			if err != nil {
				return nil, err
			}
			for i, mv := range materialized {
				out[i][responseKey(child)] = mv
			}
			continue
		}

		if parentStrat.ListMode == physical.ListParallel || parentStrat.ListMode == physical.ListBatched {
			g, gctx := errgroup.WithContext(ctx)
			sem := make(chan struct{}, maxInt(1, parentStrat.Workers))
			var mu sync.Mutex

			for i, item := range items {
				i, item := i, item
				sem <- struct{}{}
				g.Go(func() error {
					defer func() { <-sem }()
					v, err := e.executeNode(gctx, child, plan, item, rt, gate, cache)
					if err != nil {
						return err
					}
					mu.Lock()
					out[i][responseKey(child)] = v
					mu.Unlock()
					return nil
				})
			}
			if err := g.Wait(); err != nil {
				return nil, err
			}
			continue
		}

		for i, item := range items {
			v, err := e.executeNode(ctx, child, plan, item, rt, gate, cache)
			if err != nil {
				return nil, err
			}
			out[i][responseKey(child)] = v
		}
	}

	res := make([]any, len(out))
	for i := range out {
		res[i] = out[i]
	}
	return res, nil
}

type batchedParent struct {
	idx    int
	parent any
	key    string
	entry  *cacheEntry
}

func (e *Executor) resolveBatchedChild(ctx context.Context, items []any, child *logical.Node, childSpec registry.FieldSpec, childStrat physical.NodeStrategy, rt *Runtime, gate chan struct{}, cache *requestCache) ([]any, error) {
	if len(items) == 0 {
		return nil, nil
	}

	batchSize := maxInt(1, childStrat.BatchSize)
	maxConcurrent := maxInt(1, childStrat.MaxConcurrentBatches)
	if childStrat.ListMode != physical.ListBatched {
		batchSize = len(items)
		maxConcurrent = 1
	}
	if batchSize > len(items) {
		batchSize = len(items)
	}

	rt.AddLogicalCalls(child.ID, int64(len(items)))
	out := make([]any, len(items))

	owners := make([]batchedParent, 0, len(items))
	pending := make([]batchedParent, 0)
	useCache := shouldUseNodeCache(child, childStrat)

	for i, item := range items {
		if !useCache {
			owners = append(owners, batchedParent{idx: i, parent: item})
			continue
		}

		key := cacheKeyForNode(child, item)
		lookup := cache.lookupOrReserve(key)
		switch lookup.state {
		case cacheLookupHitReady:
			rt.AddCacheLookup(child.ID, CacheLookupHit)
			out[i] = lookup.entry.val
			rt.AddRowsOut(child.ID, countRowsOut(child.IsList, lookup.entry.val))
			rt.AddWidthBytes(child.ID, estimateObservedWidthBytes(lookup.entry.val, child.IsList))
		case cacheLookupHitPending:
			rt.AddCacheLookup(child.ID, CacheLookupWait)
			pending = append(pending, batchedParent{idx: i, key: key, entry: lookup.entry})
		case cacheLookupMissOwner:
			rt.AddCacheLookup(child.ID, CacheLookupMiss)
			owners = append(owners, batchedParent{idx: i, parent: item, key: key, entry: lookup.entry})
		default:
			owners = append(owners, batchedParent{idx: i, parent: item})
		}
	}

	if len(owners) > 0 {
		if batchSize > len(owners) {
			batchSize = len(owners)
		}
		g, gctx := errgroup.WithContext(ctx)
		sem := make(chan struct{}, maxConcurrent)

		for start := 0; start < len(owners); start += batchSize {
			start := start
			end := start + batchSize
			if end > len(owners) {
				end = len(owners)
			}
			chunk := append([]batchedParent(nil), owners[start:end]...)
			parentsChunk := make([]any, 0, len(chunk))
			for _, owner := range chunk {
				parentsChunk = append(parentsChunk, owner.parent)
			}

			sem <- struct{}{}
			g.Go(func() error {
				defer func() { <-sem }()

				gate <- struct{}{}
				done := rt.BeginBatchPhysical(child.ID)
				vals, err := childSpec.ResolveBatch(gctx, registry.BatchResolveRequest{
					ParentType: child.ParentType,
					FieldName:  child.FieldName,
					Parents:    parentsChunk,
					Args:       child.Args,
				})
				done()
				<-gate
				if err != nil {
					for _, owner := range chunk {
						if owner.key != "" {
							cache.complete(owner.key, nil, err)
						}
					}
					return err
				}
				if len(vals) != len(parentsChunk) {
					err := fmt.Errorf("executor: batch result size mismatch for %s.%s", child.ParentType, child.FieldName)
					for _, owner := range chunk {
						if owner.key != "" {
							cache.complete(owner.key, nil, err)
						}
					}
					return err
				}

				for i, bv := range vals {
					owner := chunk[i]
					rt.AddRowsOut(child.ID, countRowsOut(child.IsList, bv))
					rt.AddWidthBytes(child.ID, estimateObservedWidthBytes(bv, child.IsList))
					out[owner.idx] = bv
					if owner.key != "" {
						cache.complete(owner.key, bv, nil)
					}
				}
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return nil, err
		}
	}

	for _, wait := range pending {
		v, err := wait.entry.wait(ctx)
		if err != nil {
			return nil, err
		}
		out[wait.idx] = v
		rt.AddRowsOut(child.ID, countRowsOut(child.IsList, v))
		rt.AddWidthBytes(child.ID, estimateObservedWidthBytes(v, child.IsList))
	}

	return out, nil
}

func (e *Executor) materializeBatchedChildValues(ctx context.Context, batchVals []any, child *logical.Node, plan *physical.Plan, rt *Runtime, gate chan struct{}, cache *requestCache) ([]any, error) {
	if len(batchVals) == 0 {
		return batchVals, nil
	}
	if len(child.Children) == 0 {
		return batchVals, nil
	}
	if !child.IsList {
		out := make([]any, len(batchVals))
		for i, bv := range batchVals {
			mat, err := e.materializeResolvedValue(ctx, child, plan, bv, rt, gate, cache)
			if err != nil {
				return nil, err
			}
			out[i] = mat
		}
		return out, nil
	}

	flatItems := make([]any, 0)
	spans := make([]int, len(batchVals))
	for i, bv := range batchVals {
		items, err := reflectToAnySlice(bv)
		if err != nil {
			return nil, err
		}
		spans[i] = len(items)
		flatItems = append(flatItems, items...)
	}
	if len(flatItems) == 0 {
		out := make([]any, len(batchVals))
		for i := range out {
			out[i] = []any{}
		}
		return out, nil
	}

	flatMaterialized, err := e.materializeObjectList(ctx, flatItems, child, plan, rt, gate, cache)
	if err != nil {
		return nil, err
	}
	if len(flatMaterialized) != len(flatItems) {
		return nil, fmt.Errorf("executor: flattened materialization size mismatch for %s.%s", child.ParentType, child.FieldName)
	}

	out := make([]any, len(batchVals))
	pos := 0
	for i, n := range spans {
		group := make([]any, n)
		copy(group, flatMaterialized[pos:pos+n])
		out[i] = group
		pos += n
	}
	return out, nil
}

func (e *Executor) materializeResolvedValue(ctx context.Context, node *logical.Node, plan *physical.Plan, value any, rt *Runtime, gate chan struct{}, cache *requestCache) (any, error) {
	if value == nil || len(node.Children) == 0 {
		return value, nil
	}
	if node.IsList {
		return e.executeListValue(ctx, node, plan, value, rt, gate, cache)
	}
	return e.executeObjectValue(ctx, node, plan, value, rt, gate, cache)
}
