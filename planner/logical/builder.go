package logical

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/cevazrem/gql-query-planner/planner/catalog"
	"github.com/cevazrem/gql-query-planner/planner/frontend"

	"github.com/vektah/gqlparser/v2/ast"
)

type Builder struct {
	catalog *catalog.Catalog
}

func NewBuilder(cat *catalog.Catalog) *Builder {
	return &Builder{catalog: cat}
}

func (b *Builder) Build(prepared *frontend.PreparedQuery) (*Plan, error) {
	if prepared == nil {
		return nil, fmt.Errorf("logical: prepared query is nil")
	}
	if b.catalog == nil {
		return nil, fmt.Errorf("logical: catalog is nil")
	}

	frags := map[string]*ast.FragmentDefinition{}
	for _, f := range prepared.CanonicalDoc.Fragments {
		frags[f.Name] = f
	}

	root := &Node{ID: "root", ParentType: prepared.RootType, ReturnType: prepared.RootType}
	root.Children = b.buildSelections(prepared.RootType, prepared.CanonicalOp.SelectionSet, frags, prepared.Variables, "", 1, nil)

	return &Plan{
		QueryFingerprint: prepared.Fingerprint,
		OperationName:    prepared.OperationName,
		OperationType:    string(prepared.CanonicalOp.Operation),
		RootType:         prepared.RootType,
		Root:             root,
	}, nil
}

func (b *Builder) buildSelections(parentType string, sel ast.SelectionSet, frags map[string]*ast.FragmentDefinition, variables map[string]any, parentPath string, depth int, parentNode *Node) []*Node {
	fields := collectFields(sel, frags)
	out := make([]*Node, 0, len(fields))

	for _, f := range fields {
		meta, ok := b.catalog.Lookup(parentType, f.Name)
		if !ok {
			continue
		}

		respKey := f.Name
		if f.Alias != "" {
			respKey = f.Alias
		}

		path := respKey
		if parentPath != "" {
			path = parentPath + "." + respKey
		}

		args := f.ArgumentMap(variables)
		n := &Node{
			ID:          nodeID(parentType, f.Name, path),
			Kind:        NodeResolveField,
			ParentType:  parentType,
			FieldName:   f.Name,
			ResponseKey: respKey,
			Path:        normalizePath(path),
			ReturnType:  meta.ReturnType,
			ElemType:    meta.ElemType,
			IsList:      meta.IsList,
			Depth:       depth,
			Args:        args,
			ArgShape:    argShape(args),
			Meta:        meta,
		}
		if parentNode != nil {
			n.ParentResolverKey = parentNode.ResolverKey()
			n.ParentArgs = parentNode.Args
		}
		if len(f.SelectionSet) > 0 {
			n.Children = b.buildSelections(meta.ElemType, f.SelectionSet, frags, variables, n.Path, depth+1, n)
		}
		out = append(out, n)
	}

	return mergeByResponseKey(out)
}

func collectFields(set ast.SelectionSet, frags map[string]*ast.FragmentDefinition) []*ast.Field {
	var out []*ast.Field
	for _, sel := range set {
		switch s := sel.(type) {
		case *ast.Field:
			out = append(out, s)
		case *ast.FragmentSpread:
			if fr := frags[s.Name]; fr != nil {
				out = append(out, collectFields(fr.SelectionSet, frags)...)
			}
		case *ast.InlineFragment:
			out = append(out, collectFields(s.SelectionSet, frags)...)
		}
	}
	return out
}

func mergeByResponseKey(nodes []*Node) []*Node {
	if len(nodes) <= 1 {
		return nodes
	}
	byKey := map[string]*Node{}
	order := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if ex, ok := byKey[n.ResponseKey]; ok {
			ex.Children = append(ex.Children, n.Children...)
			ex.Children = mergeByResponseKey(ex.Children)
			continue
		}
		byKey[n.ResponseKey] = n
		order = append(order, n.ResponseKey)
	}
	out := make([]*Node, 0, len(order))
	for _, k := range order {
		out = append(out, byKey[k])
	}
	return out
}

func nodeID(parentType, fieldName, path string) string {
	h := sha1.Sum([]byte(parentType + "." + fieldName + "@" + path))
	return hex.EncodeToString(h[:8])
}

func normalizePath(path string) string {
	parts := strings.Split(path, ".")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	return strings.Join(parts, ".")
}

func argShape(args map[string]any) string {
	if len(args) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+":"+argType(args[k]))
	}
	return strings.Join(parts, ",")
}

func argType(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case int, int32, int64, float32, float64:
		return "num"
	case string:
		if strings.Contains(x, "-") && len(x) >= 8 {
			return "id"
		}
		return "str"
	case []any:
		return "list"
	case map[string]any:
		return "obj"
	default:
		return "other"
	}
}
