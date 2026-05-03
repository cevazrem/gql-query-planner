package frontend

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

func Prepare(schema *ast.Schema, req Request) (*PreparedQuery, error) {
	if schema == nil {
		return nil, fmt.Errorf("frontend: schema is nil")
	}
	if strings.TrimSpace(req.Query) == "" {
		return nil, fmt.Errorf("frontend: query is empty")
	}

	doc, err := gqlparser.LoadQuery(schema, req.Query)
	if err != nil {
		return nil, fmt.Errorf("frontend: parse/validate query: %w", err)
	}

	op := findOperation(doc, req.OperationName)
	if op == nil {
		if req.OperationName != "" {
			return nil, fmt.Errorf("frontend: operation %q not found", req.OperationName)
		}
		return nil, fmt.Errorf("frontend: operation not found")
	}

	vars := req.Variables
	if vars == nil {
		vars = map[string]any{}
	}

	rootType := "Query"
	switch op.Operation {
	case ast.Mutation:
		rootType = "Mutation"
	case ast.Subscription:
		rootType = "Subscription"
	}

	normalized := normalizeOperation(doc, op, vars)
	fp := sha1.Sum([]byte(normalized))

	return &PreparedQuery{
		RawQuery:        req.Query,
		OperationName:   req.OperationName,
		Variables:       vars,
		CanonicalDoc:    doc,
		CanonicalOp:     op,
		Fingerprint:     hex.EncodeToString(fp[:]),
		RootType:        rootType,
		NormalizedQuery: normalized,
	}, nil
}

func findOperation(doc *ast.QueryDocument, operationName string) *ast.OperationDefinition {
	if doc == nil {
		return nil
	}
	if operationName != "" {
		for _, op := range doc.Operations {
			if op != nil && op.Name == operationName {
				return op
			}
		}
		return nil
	}
	if len(doc.Operations) == 1 {
		return doc.Operations[0]
	}
	for _, op := range doc.Operations {
		if op != nil && op.Name == "" {
			return op
		}
	}
	return nil
}

func normalizeOperation(doc *ast.QueryDocument, op *ast.OperationDefinition, variables map[string]any) string {
	frags := make(map[string]*ast.FragmentDefinition, len(doc.Fragments))
	for _, f := range doc.Fragments {
		frags[f.Name] = f
	}

	var b strings.Builder
	b.WriteString(string(op.Operation))
	b.WriteString(" ")
	b.WriteString(op.Name)
	b.WriteString(" ")
	writeSelections(&b, op.SelectionSet, frags, variables)
	return b.String()
}

func writeSelections(b *strings.Builder, set ast.SelectionSet, frags map[string]*ast.FragmentDefinition, variables map[string]any) {
	fields := collectFields(set, frags)
	sort.SliceStable(fields, func(i, j int) bool {
		ki := fields[i].Alias
		if ki == "" {
			ki = fields[i].Name
		}
		kj := fields[j].Alias
		if kj == "" {
			kj = fields[j].Name
		}
		return ki < kj
	})

	b.WriteString("{")
	for idx, f := range fields {
		if idx > 0 {
			b.WriteString(" ")
		}
		key := f.Name
		if f.Alias != "" {
			key = f.Alias + ":" + f.Name
		}
		b.WriteString(key)

		args := f.ArgumentMap(variables)
		if len(args) > 0 {
			b.WriteString("(")
			writeArgs(b, args)
			b.WriteString(")")
		}
		if len(f.SelectionSet) > 0 {
			writeSelections(b, f.SelectionSet, frags, variables)
		}
	}
	b.WriteString("}")
}

func writeArgs(b *strings.Builder, args map[string]any) {
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(stableValue(args[k]))
	}
}

func stableValue(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return x
	case fmt.Stringer:
		return x.String()
	case []any:
		parts := make([]string, 0, len(x))
		for _, item := range x {
			parts = append(parts, stableValue(item))
		}
		return "[" + strings.Join(parts, ",") + "]"
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, k+":"+stableValue(x[k]))
		}
		return "{" + strings.Join(parts, ",") + "}"
	default:
		return fmt.Sprint(v)
	}
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
