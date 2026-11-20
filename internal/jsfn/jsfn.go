// Package jsfn provides the $fn helper namespace shared by the expression
// evaluator and the Code node sandboxes. Helpers are pure functions.
package jsfn

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/dop251/goja"
)

// dateTokenReplacer maps formatDate tokens to Go layout fragments.
var dateTokenReplacer = strings.NewReplacer(
	"YYYY", "2006", "MM", "01", "DD", "02", "HH", "15", "mm", "04", "ss", "05",
)

// New builds the $fn helper object for a runtime.
func New(vm *goja.Runtime) *goja.Object {
	fn := vm.NewObject()
	throw := func(err error) { panic(vm.NewGoError(err)) }
	arg := func(call goja.FunctionCall, i int) string {
		if i >= len(call.Arguments) || goja.IsUndefined(call.Arguments[i]) || goja.IsNull(call.Arguments[i]) {
			return ""
		}
		return call.Arguments[i].String()
	}
	set := func(name string, f func(goja.FunctionCall) goja.Value) { _ = fn.Set(name, f) }

	set("sha256", func(call goja.FunctionCall) goja.Value {
		sum := sha256.Sum256([]byte(arg(call, 0)))
		return vm.ToValue(hex.EncodeToString(sum[:]))
	})
	set("hmacSha256", func(call goja.FunctionCall) goja.Value {
		mac := hmac.New(sha256.New, []byte(arg(call, 0)))
		mac.Write([]byte(arg(call, 1)))
		return vm.ToValue(hex.EncodeToString(mac.Sum(nil)))
	})
	set("base64Encode", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(base64.StdEncoding.EncodeToString([]byte(arg(call, 0))))
	})
	set("base64Decode", func(call goja.FunctionCall) goja.Value {
		raw, err := base64.StdEncoding.DecodeString(arg(call, 0))
		if err != nil {
			throw(fmt.Errorf("base64Decode: %w", err))
		}
		return vm.ToValue(string(raw))
	})
	set("formatDate", func(call goja.FunctionCall) goja.Value {
		t, err := toTime(call.Argument(0))
		if err != nil {
			throw(fmt.Errorf("formatDate: %w", err))
		}
		layout := arg(call, 1)
		if layout == "" {
			return vm.ToValue(t.UTC().Format(time.RFC3339))
		}
		return vm.ToValue(t.UTC().Format(dateTokenReplacer.Replace(layout)))
	})
	return fn
}

func toTime(v goja.Value) (time.Time, error) {
	switch t := v.Export().(type) {
	case time.Time:
		return t, nil
	case string:
		parsed, err := time.Parse(time.RFC3339, t)
		if err != nil {
			return time.Time{}, fmt.Errorf("parsing %q as RFC 3339: %w", t, err)
		}
		return parsed, nil
	case int64:
		return time.UnixMilli(t), nil
	case float64:
		return time.UnixMilli(int64(t)), nil
	}
	return time.Time{}, fmt.Errorf("unsupported date value %v", v)
}
