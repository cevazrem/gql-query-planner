package planner

import (
	"encoding/json"
	"reflect"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func normalizeForJSON(v any, marshal protojson.MarshalOptions) (any, error) {
	if v == nil {
		return nil, nil
	}
	switch x := v.(type) {
	case uuid.UUID:
		return x.String(), nil
	case *uuid.UUID:
		if x == nil {
			return nil, nil
		}
		return x.String(), nil
	case *time.Time:
		if x == nil {
			return nil, nil
		}
		return x.Format(time.RFC3339Nano), nil
	case time.Time:
		return x.Format(time.RFC3339Nano), nil
	case proto.Message:
		b, err := marshal.Marshal(x)
		if err != nil {
			return nil, err
		}
		var out any
		if err := json.Unmarshal(b, &out); err != nil {
			return nil, err
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, v2 := range x {
			nv, err := normalizeForJSON(v2, marshal)
			if err != nil {
				return nil, err
			}
			out[k] = nv
		}
		return out, nil
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			nv, err := normalizeForJSON(item, marshal)
			if err != nil {
				return nil, err
			}
			out[i] = nv
		}
		return out, nil
	}
	rv := reflect.ValueOf(v)
	if rv.IsValid() && rv.Kind() == reflect.Slice {
		out := make([]any, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			nv, err := normalizeForJSON(rv.Index(i).Interface(), marshal)
			if err != nil {
				return nil, err
			}
			out[i] = nv
		}
		return out, nil
	}
	if rv.IsValid() && rv.Kind() == reflect.Ptr && !rv.IsNil() {
		return normalizeForJSON(rv.Elem().Interface(), marshal)
	}
	return v, nil
}
