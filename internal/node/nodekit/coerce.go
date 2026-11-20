package nodekit

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Value types a field can be coerced to.
const (
	TypeAuto    = "auto"
	TypeString  = "string"
	TypeNumber  = "number"
	TypeBoolean = "boolean"
	TypeArray   = "array"
	TypeObject  = "object"
)

// TypeOptions lists the coercion targets for a property.
func TypeOptions() []nodesdk.Option {
	return []nodesdk.Option{
		{Name: "Auto (keep as is)", Value: TypeAuto}, {Name: "String", Value: TypeString},
		{Name: "Number", Value: TypeNumber}, {Name: "Boolean", Value: TypeBoolean},
		{Name: "Array", Value: TypeArray}, {Name: "Object", Value: TypeObject},
	}
}

// Coerce converts v to the named type. Strings holding JSON convert to arrays
// and objects. "auto" and the empty type keep the value.
func Coerce(v any, typ string) (any, error) {
	switch typ {
	case "", TypeAuto:
		return v, nil
	case TypeString:
		return ToString(v), nil
	case TypeNumber:
		return ToFloat(v, true)
	case TypeBoolean:
		return ToBool(v, true)
	case TypeArray:
		if s, ok := v.(string); ok {
			var arr []any
			if err := json.Unmarshal([]byte(s), &arr); err != nil {
				return nil, fmt.Errorf("cannot parse %q as an array: %w", s, err)
			}
			return arr, nil
		}
		if arr, ok := v.([]any); ok {
			return arr, nil
		}
		return nil, fmt.Errorf("cannot convert %s to an array", TypeName(v))
	case TypeObject:
		if s, ok := v.(string); ok {
			var m map[string]any
			if err := json.Unmarshal([]byte(s), &m); err != nil {
				return nil, fmt.Errorf("cannot parse %q as an object: %w", s, err)
			}
			return m, nil
		}
		if m, ok := v.(map[string]any); ok {
			return m, nil
		}
		return nil, fmt.Errorf("cannot convert %s to an object", TypeName(v))
	}
	return nil, fmt.Errorf("unknown type %q", typ)
}

// ItemError wraps err in a *nodesdk.NodeError that points at an input item.
func ItemError(itemIndex int, err error) error {
	return &nodesdk.NodeError{Message: fmt.Sprintf("item %d", itemIndex), ItemIndex: &itemIndex, Err: err}
}

// KeyValues reads a list of name/value pairs given either as an array of
// {name, value} objects or as a plain object. Entries with an empty name are
// dropped. Values keep their type. The order of an array is preserved; an
// object's keys are sorted for determinism.
func KeyValues(v any) ([]KeyValue, error) {
	switch t := v.(type) {
	case nil:
		return nil, nil
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		out := make([]KeyValue, len(keys))
		for i, k := range keys {
			out[i] = KeyValue{Name: k, Value: t[k]}
		}
		return out, nil
	case []any:
		var out []KeyValue
		for i, e := range t {
			m, ok := e.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("entry %d must be an object with name and value, got %s", i, TypeName(e))
			}
			name, _ := m["name"].(string)
			if name == "" {
				continue
			}
			out = append(out, KeyValue{Name: name, Value: m["value"]})
		}
		return out, nil
	}
	return nil, fmt.Errorf("expected a list of name/value pairs, got %s", TypeName(v))
}

// KeyValue is one name/value pair of a property list.
type KeyValue struct {
	Name  string
	Value any
}
