package registry

import "fmt"

type Registry struct {
	fields map[string]FieldSpec
}

func New() *Registry {
	return &Registry{fields: make(map[string]FieldSpec)}
}

func makeKey(parentType, fieldName string) string {
	return parentType + "." + fieldName
}

func (r *Registry) Register(parentType, fieldName string, spec FieldSpec) {
	r.fields[makeKey(parentType, fieldName)] = spec
}

func (r *Registry) Lookup(parentType, fieldName string) (FieldSpec, bool) {
	spec, ok := r.fields[makeKey(parentType, fieldName)]
	return spec, ok
}

func (r *Registry) MustLookup(parentType, fieldName string) FieldSpec {
	spec, ok := r.Lookup(parentType, fieldName)
	if !ok {
		panic(fmt.Sprintf("registry: missing field spec %s.%s", parentType, fieldName))
	}
	return spec
}
