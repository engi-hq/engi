package nodekit

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Clone deep-copies JSON-like data.
func Clone(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return CloneMap(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = Clone(e)
		}
		return out
	}
	return v
}

// CloneMap deep-copies an object; nil stays nil.
func CloneMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = Clone(v)
	}
	return out
}

// Key returns a canonical string for a value, usable as a map key to compare
// values for equality (object keys are sorted by encoding/json). Integers and
// floats with the same value share a key.
func Key(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%T:%v", v, v)
	}
	return string(raw)
}

// Equal reports deep equality of two JSON-like values.
func Equal(a, b any) bool { return Key(a) == Key(b) }

// IsEmpty reports whether v is nil, an empty string, array or object.
func IsEmpty(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	case []any:
		return len(t) == 0
	case map[string]any:
		return len(t) == 0
	}
	return false
}

// ToString renders a scalar as text. Objects and arrays render as JSON.
func ToString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	}
	if f, ok := numberOf(v); ok {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(raw)
}

// numberOf converts any Go numeric type to float64.
func numberOf(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int32:
		return float64(t), true
	case int64:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	}
	return 0, false
}

// ToFloat converts a number, or with loose set a numeric string or bool, to
// float64.
func ToFloat(v any, loose bool) (float64, error) {
	if f, ok := numberOf(v); ok {
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, fmt.Errorf("%v is not a finite number", f)
		}
		return f, nil
	}
	if !loose {
		return 0, fmt.Errorf("expected a number, got %s", TypeName(v))
	}
	switch t := v.(type) {
	case nil:
		return 0, nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, fmt.Errorf("cannot convert %q to a number", t)
		}
		return f, nil
	case bool:
		if t {
			return 1, nil
		}
		return 0, nil
	}
	return 0, fmt.Errorf("cannot convert %s to a number", TypeName(v))
}

// ToBool converts a bool, or with loose set the strings "true"/"false" and the
// numbers 0/1, to bool.
func ToBool(v any, loose bool) (bool, error) {
	if b, ok := v.(bool); ok {
		return b, nil
	}
	if !loose {
		return false, fmt.Errorf("expected a boolean, got %s", TypeName(v))
	}
	switch t := v.(type) {
	case nil:
		return false, nil
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "true", "1", "yes":
			return true, nil
		case "false", "0", "no", "":
			return false, nil
		}
		return false, fmt.Errorf("cannot convert %q to a boolean", t)
	}
	if f, ok := numberOf(v); ok {
		return f != 0, nil
	}
	return false, fmt.Errorf("cannot convert %s to a boolean", TypeName(v))
}

// TypeName names the JSON type of v for error messages.
func TypeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case bool:
		return "boolean"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	if _, ok := numberOf(v); ok {
		return "number"
	}
	return fmt.Sprintf("%T", v)
}

// Compare orders two values of the same JSON type: numbers numerically,
// strings lexically, booleans false before true, times are not handled here.
// Values of different types order by type name.
func Compare(a, b any) int {
	if fa, ok := numberOf(a); ok {
		if fb, ok := numberOf(b); ok {
			switch {
			case fa < fb:
				return -1
			case fa > fb:
				return 1
			}
			return 0
		}
	}
	sa, aok := a.(string)
	sb, bok := b.(string)
	if aok && bok {
		return strings.Compare(sa, sb)
	}
	ba, aok := a.(bool)
	bb, bok := b.(bool)
	if aok && bok {
		switch {
		case ba == bb:
			return 0
		case !ba:
			return -1
		}
		return 1
	}
	return strings.Compare(TypeName(a)+Key(a), TypeName(b)+Key(b))
}
