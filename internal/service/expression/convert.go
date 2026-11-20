package expression

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/dop251/goja"

	"github.com/engi-hq/engi/internal/domain"
)

// toJS converts JSON-like Go data into native JavaScript values. Going native
// (instead of wrapping Go maps) means expressions cannot mutate engine data
// and see ordinary arrays and objects.
func toJS(vm *goja.Runtime, v any) goja.Value {
	switch t := v.(type) {
	case nil:
		return goja.Null()
	case map[string]any:
		obj := vm.NewObject()
		for k, e := range t {
			_ = obj.Set(k, toJS(vm, e)) // Set on a fresh plain object cannot fail
		}
		return obj
	case []any:
		vals := make([]any, len(t))
		for i, e := range t {
			vals[i] = toJS(vm, e)
		}
		return vm.NewArray(vals...)
	case map[string]string:
		obj := vm.NewObject()
		for k, e := range t {
			_ = obj.Set(k, e)
		}
		return obj
	case json.Number:
		if f, err := t.Float64(); err == nil {
			return vm.ToValue(f)
		}
		return vm.ToValue(t.String())
	default:
		return vm.ToValue(v)
	}
}

func jsonToJS(vm *goja.Runtime, m map[string]any) goja.Value {
	if m == nil {
		return vm.NewObject()
	}
	return toJS(vm, m)
}

func binaryToJS(vm *goja.Runtime, b map[string]domain.BinaryRef) goja.Value {
	obj := vm.NewObject()
	for name, ref := range b {
		_ = obj.Set(name, toJS(vm, map[string]any{
			"ref": ref.Ref, "mime": ref.MIME, "name": ref.Name, "size": float64(ref.Size),
		}))
	}
	return obj
}

// itemToJS converts an item into {json, binary, error?}.
func itemToJS(vm *goja.Runtime, it domain.Item) goja.Value {
	obj := vm.NewObject()
	_ = obj.Set("json", jsonToJS(vm, it.JSON))
	_ = obj.Set("binary", binaryToJS(vm, it.Binary))
	if it.Error != nil {
		_ = obj.Set("error", toJS(vm, map[string]any{
			"message": it.Error.Message, "description": it.Error.Description,
			"httpStatus": float64(it.Error.HTTPStatus),
		}))
	}
	return obj
}

// checkResult validates that an exported value is plain data within the size
// limit.
func checkResult(v any, maxBytes int) error {
	switch t := v.(type) {
	case nil, bool, int64, int:
		return nil
	case string:
		if len(t) > maxBytes {
			return ErrOutputTooLarge
		}
		return nil
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return fmt.Errorf("%w: %v", ErrNotSerializable, t)
		}
		return nil
	case time.Time:
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		var unsupported *json.UnsupportedTypeError
		var unsupportedValue *json.UnsupportedValueError
		if errors.As(err, &unsupported) || errors.As(err, &unsupportedValue) {
			return fmt.Errorf("%w: %w", ErrNotSerializable, err)
		}
		return fmt.Errorf("measuring result: %w", err)
	}
	if len(raw) > maxBytes {
		return ErrOutputTooLarge
	}
	return nil
}

// stringify renders a value for interpolation into a larger string.
func stringify(v any) (string, error) {
	switch t := v.(type) {
	case nil:
		return "", nil
	case string:
		return t, nil
	case time.Time:
		return t.UTC().Format(time.RFC3339Nano), nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrNotSerializable, err)
	}
	return string(raw), nil
}

// normalize makes exported numbers uniform: every JavaScript number becomes a
// float64, matching the numbers of items decoded from JSON.
func normalize(v any) any {
	switch t := v.(type) {
	case int64:
		return float64(t)
	case int:
		return float64(t)
	case map[string]any:
		for k, e := range t {
			t[k] = normalize(e)
		}
		return t
	case []any:
		for i, e := range t {
			t[i] = normalize(e)
		}
		return t
	}
	return v
}
