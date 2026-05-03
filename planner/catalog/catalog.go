package catalog

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
)

type FieldCapabilities struct {
	Batchable     bool
	BatchKey      string
	MaxBatchSize  int
	Cacheable     bool
	CacheKey      string
	Stable        bool
	SideEffecting bool
	DefaultHeavy  bool
}

type PlannerHints struct {
	ExpectedFanout int
	CostWeight     float64
	NonParallel    bool
	Expensive      bool
}

type FieldMeta struct {
	ParentType string
	FieldName  string
	ReturnType string
	ElemType   string
	IsList     bool
	IsScalar   bool
	IsObject   bool
	Nullable   bool
	Args       []string
	Caps       FieldCapabilities
	Hints      PlannerHints
}

type Catalog struct {
	Schema *ast.Schema
	fields map[string]FieldMeta
}

func New(schema *ast.Schema) (*Catalog, error) {
	if schema == nil {
		return nil, fmt.Errorf("catalog: schema is nil")
	}

	c := &Catalog{Schema: schema, fields: map[string]FieldMeta{}}
	for typeName, def := range schema.Types {
		if def == nil || strings.HasPrefix(typeName, "__") {
			continue
		}
		if def.Kind != ast.Object && def.Kind != ast.Interface {
			continue
		}
		for _, f := range def.Fields {
			if f != nil {
				c.fields[fieldKey(typeName, f.Name)] = buildFieldMeta(typeName, f, schema)
			}
		}
	}
	return c, nil
}

func (c *Catalog) Lookup(parentType, fieldName string) (FieldMeta, bool) {
	m, ok := c.fields[fieldKey(parentType, fieldName)]
	return m, ok
}

func (c *Catalog) Fields() []FieldMeta {
	out := make([]FieldMeta, 0, len(c.fields))
	for _, f := range c.fields {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ParentType == out[j].ParentType {
			return out[i].FieldName < out[j].FieldName
		}
		return out[i].ParentType < out[j].ParentType
	})
	return out
}

func fieldKey(parentType, fieldName string) string {
	return parentType + "." + fieldName
}

func buildFieldMeta(parentType string, field *ast.FieldDefinition, schema *ast.Schema) FieldMeta {
	named := unwrapNamed(field.Type)
	isList := containsList(field.Type)
	elemType := named
	retType := named
	if isList {
		retType = "[]" + named
	}

	args := make([]string, 0, len(field.Arguments))
	for _, a := range field.Arguments {
		args = append(args, a.Name)
	}
	sort.Strings(args)

	kind := ast.Scalar
	if td := schema.Types[named]; td != nil {
		kind = td.Kind
	}

	caps := FieldCapabilities{
		Stable:        true,
		Cacheable:     false,
		CacheKey:      "",
		Batchable:     false,
		BatchKey:      "",
		MaxBatchSize:  0,
		DefaultHeavy:  isList,
		SideEffecting: parentType == "Mutation",
	}
	applyDirectiveCaps(&caps, field)

	return FieldMeta{
		ParentType: parentType,
		FieldName:  field.Name,
		ReturnType: retType,
		ElemType:   elemType,
		IsList:     isList,
		IsScalar:   kind == ast.Scalar || kind == ast.Enum,
		IsObject:   kind == ast.Object || kind == ast.Interface,
		Nullable:   !field.Type.NonNull,
		Args:       args,
		Caps:       caps,
		Hints: PlannerHints{
			ExpectedFanout: defaultFanout(isList),
			CostWeight:     defaultCostWeight(isList, kind),
		},
	}
}

func applyDirectiveCaps(caps *FieldCapabilities, field *ast.FieldDefinition) {
	if caps == nil || field == nil {
		return
	}
	for _, d := range field.Directives {
		if d == nil {
			continue
		}
		switch d.Name {
		case "batchable":
			caps.Batchable = true
			if caps.MaxBatchSize <= 0 {
				caps.MaxBatchSize = 100
			}
			for _, a := range d.Arguments {
				if a == nil || a.Value == nil {
					continue
				}
				switch a.Name {
				case "maxBatchSize":
					if n, ok := parseIntValue(a.Value); ok && n > 0 {
						caps.MaxBatchSize = n
					}
				case "key":
					if s, ok := parseStringValue(a.Value); ok {
						caps.BatchKey = s
					}
				}
			}
		case "cacheable":
			caps.Cacheable = true
			for _, a := range d.Arguments {
				if a == nil || a.Value == nil {
					continue
				}
				switch a.Name {
				case "key":
					if s, ok := parseStringValue(a.Value); ok {
						caps.CacheKey = s
					}
				}
			}
		}
	}
	if caps.CacheKey == "" {
		caps.CacheKey = caps.BatchKey
	}
}

func parseIntValue(v *ast.Value) (int, bool) {
	if v == nil {
		return 0, false
	}
	if v.Raw != "" {
		n, err := strconv.Atoi(v.Raw)
		if err == nil {
			return n, true
		}
	}
	return 0, false
}

func parseStringValue(v *ast.Value) (string, bool) {
	if v == nil {
		return "", false
	}
	if v.Raw != "" {
		return strings.Trim(v.Raw, `"`), true
	}
	return "", false
}

func defaultFanout(isList bool) int {
	if isList {
		return 0
	}
	return 1
}

func defaultCostWeight(isList bool, kind ast.DefinitionKind) float64 {
	if isList {
		return 3
	}
	if kind == ast.Object || kind == ast.Interface {
		return 2
	}
	return 1
}

func unwrapNamed(t *ast.Type) string {
	if t == nil {
		return ""
	}
	if t.Elem != nil {
		return unwrapNamed(t.Elem)
	}
	return t.NamedType
}

func containsList(t *ast.Type) bool {
	return t != nil && t.Elem != nil
}
