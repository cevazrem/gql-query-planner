package execute

import (
	"fmt"
	"reflect"

	"github.com/cevazrem/gql-query-planner/planner/logical"
)

func responseKey(n *logical.Node) string {
	if n == nil {
		return ""
	}
	if n.ResponseKey != "" {
		return n.ResponseKey
	}
	return n.FieldName
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func reflectToAnySlice(v any) ([]any, error) {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return nil, nil
	}
	if rv.Kind() != reflect.Slice {
		return nil, fmt.Errorf("not a slice: %T", v)
	}
	out := make([]any, 0, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		out = append(out, rv.Index(i).Interface())
	}
	return out, nil
}

func countRowsOut(isList bool, value any) int64 {
	if value == nil {
		return 0
	}
	if !isList {
		return 1
	}
	items, err := reflectToAnySlice(value)
	if err != nil {
		return 0
	}
	return int64(len(items))
}

func estimateObservedWidthBytes(value any, isList bool) int64 {
	if value == nil {
		return 0
	}
	width := int64(observedValueWidth(reflect.ValueOf(value), 0))
	if width < 0 {
		return 0
	}
	return width
}

func observedValueWidth(v reflect.Value, depth int) int {
	if !v.IsValid() {
		return 0
	}
	for v.IsValid() && (v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer) {
		if v.IsNil() {
			return 0
		}
		v = v.Elem()
	}
	if !v.IsValid() {
		return 0
	}
	if depth > 3 {
		return 64
	}

	switch v.Kind() {
	case reflect.Bool:
		return 1
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return 8
	case reflect.String:
		return v.Len()
	case reflect.Slice, reflect.Array:
		n := v.Len()
		if n == 0 {
			return 0
		}
		limit := n
		if limit > 8 {
			limit = 8
		}
		total := 0
		for i := 0; i < limit; i++ {
			total += observedValueWidth(v.Index(i), depth+1)
		}
		if n > limit && limit > 0 {
			total = total * n / limit
		}
		return total
	case reflect.Map:
		iter := v.MapRange()
		total := 0
		count := 0
		for iter.Next() {
			total += observedValueWidth(iter.Key(), depth+1)
			total += observedValueWidth(iter.Value(), depth+1)
			count++
			if count >= 8 {
				break
			}
		}
		if v.Len() > count && count > 0 {
			total = total * v.Len() / count
		}
		return total
	case reflect.Struct:
		total := 0
		for i := 0; i < v.NumField(); i++ {
			total += observedValueWidth(v.Field(i), depth+1)
		}
		return total
	default:
		return 16
	}
}
