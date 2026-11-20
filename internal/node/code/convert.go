package code

import (
	"time"

	"github.com/dop251/goja"

	"github.com/engi-hq/engi/pkg/nodesdk"
)

// toJS converts JSON-like Go data into native JavaScript values, so user code
// sees ordinary objects and arrays and cannot alias engine data.
func toJS(vm *goja.Runtime, v any) goja.Value {
	switch t := v.(type) {
	case nil:
		return goja.Null()
	case map[string]any:
		obj := vm.NewObject()
		for k, e := range t {
			_ = obj.Set(k, toJS(vm, e))
		}
		return obj
	case []any:
		vals := make([]any, len(t))
		for i, e := range t {
			vals[i] = toJS(vm, e)
		}
		return vm.NewArray(vals...)
	default:
		return vm.ToValue(v)
	}
}

func binaryToJS(vm *goja.Runtime, b map[string]nodesdk.BinaryRef) goja.Value {
	obj := vm.NewObject()
	for name, ref := range b {
		_ = obj.Set(name, toJS(vm, map[string]any{"ref": ref.Ref, "mime": ref.MIME, "name": ref.Name, "size": float64(ref.Size)}))
	}
	return obj
}

// itemToJS converts an item to {json, binary}.
func itemToJS(vm *goja.Runtime, it nodesdk.Item) goja.Value {
	obj := vm.NewObject()
	j := it.JSON
	if j == nil {
		j = map[string]any{}
	}
	_ = obj.Set("json", toJS(vm, j))
	_ = obj.Set("binary", binaryToJS(vm, it.Binary))
	return obj
}

// normalize makes exported values plain JSON data: every number is a float64
// and dates become RFC 3339 strings.
func normalize(v any) any {
	switch t := v.(type) {
	case int64:
		return float64(t)
	case int:
		return float64(t)
	case time.Time:
		return t.UTC().Format(time.RFC3339Nano)
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
