package code

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dop251/goja"

	"github.com/engi-hq/engi/pkg/nodesdk"
)

// workflowScope exposes the engine's expression scope ($("Node"), $node, $vars,
// $env) to user code. Every access is a call to Input.EvalExpression, so user
// code sees exactly what an expression in the same node would see, including
// the environment allow-list and the expression sandbox limits. The context is
// captured per call, never stored.
type workflowScope struct {
	vm        *goja.Runtime
	in        nodesdk.Input
	ctx       context.Context //nolint:containedctx // lives only for one user-code call
	itemIndex int
}

// eval evaluates expr and converts the result for the user's runtime. A failure
// is thrown into the user code as a JavaScript exception.
func (s workflowScope) eval(expr string) goja.Value {
	v, err := s.in.EvalExpression(s.ctx, expr, s.itemIndex)
	if err != nil {
		panic(s.vm.NewGoError(err))
	}
	return toJS(s.vm, v)
}

// quote renders name as a JavaScript string literal.
func quote(name string) string {
	b, err := json.Marshal(name)
	if err != nil { // a string always marshals; keep the expression valid anyway
		return `""`
	}
	return string(b)
}

// nodeFunc builds $("Node Name"): an object with item, all(), first(), last()
// and isExecuted, backed by the engine's lineage lookup.
func (s workflowScope) nodeFunc() goja.Value {
	return s.vm.ToValue(func(call goja.FunctionCall) goja.Value {
		ref := "$(" + quote(call.Argument(0).String()) + ")"
		obj := s.vm.NewObject()
		getter := s.vm.ToValue(func(goja.FunctionCall) goja.Value { return s.eval(ref + ".item") })
		_ = obj.DefineAccessorProperty("item", getter, nil, goja.FLAG_FALSE, goja.FLAG_TRUE)
		_ = obj.Set("all", func(goja.FunctionCall) goja.Value { return s.eval(ref + ".all()") })
		_ = obj.Set("first", func(goja.FunctionCall) goja.Value { return s.eval(ref + ".first()") })
		_ = obj.Set("last", func(goja.FunctionCall) goja.Value { return s.eval(ref + ".last()") })
		_ = obj.Set("isExecuted", s.eval(ref+".isExecuted"))
		return obj
	})
}

// view builds a read-only object whose properties are the members of the
// expression variable name ($node, $vars or $env).
func (s workflowScope) view(variable string) goja.Value {
	return s.vm.NewDynamicObject(&readOnlyView{s: s, variable: variable})
}

// readOnlyView implements goja.DynamicObject over an expression variable.
type readOnlyView struct {
	s        workflowScope
	variable string
}

func (v *readOnlyView) Get(name string) goja.Value {
	val := v.s.eval(v.variable + "[" + quote(name) + "]")
	if goja.IsNull(val) {
		return nil // an absent member reads as undefined
	}
	return val
}

func (v *readOnlyView) Has(name string) bool { return v.Get(name) != nil }

func (*readOnlyView) Set(string, goja.Value) bool { return false }

func (*readOnlyView) Delete(string) bool { return false }

func (v *readOnlyView) Keys() []string {
	raw, err := v.s.in.EvalExpression(v.s.ctx, "Object.keys("+v.variable+")", v.s.itemIndex)
	if err != nil {
		panic(v.s.vm.NewGoError(fmt.Errorf("listing %s: %w", v.variable, err)))
	}
	list, _ := raw.([]any)
	keys := make([]string, 0, len(list))
	for _, k := range list {
		if name, ok := k.(string); ok {
			keys = append(keys, name)
		}
	}
	return keys
}
