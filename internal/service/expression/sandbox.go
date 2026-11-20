package expression

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/dop251/goja"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/jsfn"
	"github.com/engi-hq/engi/internal/port"
)

// wrapperParams are the variables in scope, in call order.
const wrapperParams = "$json,$binary,$input,$,$node,$vars,$env,$execution,$workflow,$now,$today,$itemIndex,$runIndex,$fn"

// hardenScript freezes the built-ins so a pooled VM cannot be polluted by one
// evaluation and observed by the next.
const hardenScript = `(function (g) {
  var names = ["Object","Function","Array","String","Number","Boolean","Symbol","Date",
    "RegExp","Error","TypeError","RangeError","SyntaxError","ReferenceError","EvalError",
    "URIError","Map","Set","WeakMap","WeakSet","Promise","ArrayBuffer"];
  names.forEach(function (n) {
    var c = g[n];
    if (c) { Object.freeze(c); if (c.prototype) { Object.freeze(c.prototype); } }
  });
  ["Math","JSON","Reflect"].forEach(function (n) { if (g[n]) { Object.freeze(g[n]); } });
  Object.freeze(g);
})(this);`

// sandbox is one pooled JavaScript runtime. It has no host objects at all: no
// require, no timers, no process, no filesystem, no network.
type sandbox struct {
	vm *goja.Runtime
	fn *goja.Object
}

func newSandbox(maxCallStack int) (*sandbox, error) {
	vm := goja.New()
	vm.SetMaxCallStackSize(maxCallStack)
	fn := jsfn.New(vm)
	if _, err := vm.RunString(hardenScript); err != nil {
		return nil, fmt.Errorf("hardening sandbox: %w", err)
	}
	return &sandbox{vm: vm, fn: fn}, nil
}

// compile turns a code block into a program that evaluates to the wrapper
// function taking the scope variables.
func compileBlock(code string) (*goja.Program, error) {
	src := "(function(" + wrapperParams + `){"use strict";return (` + code + "\n);})"
	prog, err := goja.Compile("expression", src, true)
	if err != nil {
		return nil, fmt.Errorf("compiling expression: %w", err)
	}
	return prog, nil
}

// run evaluates the program with the given scope arguments.
func (s *sandbox) run(prog *goja.Program, args []goja.Value) (any, error) {
	fnVal, err := s.vm.RunProgram(prog)
	if err != nil {
		return nil, fmt.Errorf("evaluating expression: %w", err)
	}
	call, ok := goja.AssertFunction(fnVal)
	if !ok {
		return nil, fmt.Errorf("evaluating expression: wrapper is not a function")
	}
	out, err := call(goja.Undefined(), args...)
	if err != nil {
		return nil, fmt.Errorf("evaluating expression: %w", err)
	}
	return out.Export(), nil
}

// scopeArgs builds the wrapper arguments for one evaluation.
func (s *sandbox) scopeArgs(sc port.ExpressionScope, now time.Time, env envView) []goja.Value {
	vm := s.vm
	date := func(t time.Time) goja.Value {
		v, err := vm.New(vm.Get("Date"), vm.ToValue(t.UnixMilli()))
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return v
	}
	now = now.UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	return []goja.Value{
		jsonToJS(vm, sc.Item.JSON),
		binaryToJS(vm, sc.Item.Binary),
		s.inputObject(sc),
		s.nodeFunc(sc),
		vm.NewDynamicObject(&nodeView{vm: vm, nodes: sc.Nodes}),
		toJS(vm, sc.Vars),
		vm.NewDynamicObject(envView{vm: vm, allow: env.allow, lookup: env.lookup}),
		toJS(vm, map[string]any{"id": string(sc.Execution.ID), "mode": string(sc.Execution.Mode), "resumeUrl": sc.Execution.ResumeURL}),
		toJS(vm, map[string]any{"id": string(sc.Workflow.ID), "name": sc.Workflow.Name}),
		date(now),
		date(today),
		vm.ToValue(sc.ItemIndex),
		vm.ToValue(sc.RunIndex),
		s.fn,
	}
}

// inputObject builds $input.
func (s *sandbox) inputObject(sc port.ExpressionScope) goja.Value {
	return s.itemList(
		func() []domain.Item { return sc.Input },
		func() (domain.Item, error) { return sc.Item, nil },
	)
}

// nodeFunc builds $("Node Name").
func (s *sandbox) nodeFunc(sc port.ExpressionScope) goja.Value {
	return s.vm.ToValue(func(call goja.FunctionCall) goja.Value {
		name := call.Argument(0).String()
		if sc.Nodes == nil {
			panic(s.vm.NewGoError(fmt.Errorf("node %q: no node data available: %w", name, domain.ErrNotFound)))
		}
		obj := s.itemList(
			func() []domain.Item { return s.mustOutput(sc.Nodes, name) },
			func() (domain.Item, error) { return sc.Nodes.PairedItem(name) },
		).ToObject(s.vm)
		_, err := sc.Nodes.Output(name)
		_ = obj.Set("isExecuted", !errors.Is(err, domain.ErrNotFound))
		return obj
	})
}

func (s *sandbox) mustOutput(nodes port.NodeData, name string) []domain.Item {
	items, err := nodes.Output(name)
	if err != nil {
		panic(s.vm.NewGoError(err))
	}
	return items
}

// itemList builds an object with .item (lazy), .all(), .first() and .last().
func (s *sandbox) itemList(all func() []domain.Item, current func() (domain.Item, error)) goja.Value {
	vm := s.vm
	obj := vm.NewObject()
	getter := vm.ToValue(func(goja.FunctionCall) goja.Value {
		it, err := current()
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return itemToJS(vm, it)
	})
	_ = obj.DefineAccessorProperty("item", getter, nil, goja.FLAG_FALSE, goja.FLAG_TRUE)

	_ = obj.Set("all", func(goja.FunctionCall) goja.Value {
		items := all()
		vals := make([]any, len(items))
		for i, it := range items {
			vals[i] = itemToJS(vm, it)
		}
		return vm.NewArray(vals...)
	})
	_ = obj.Set("first", func(goja.FunctionCall) goja.Value {
		items := all()
		if len(items) == 0 {
			return goja.Undefined()
		}
		return itemToJS(vm, items[0])
	})
	_ = obj.Set("last", func(goja.FunctionCall) goja.Value {
		items := all()
		if len(items) == 0 {
			return goja.Undefined()
		}
		return itemToJS(vm, items[len(items)-1])
	})
	return obj
}

// nodeView implements $node["Name"] as a read-only dynamic object.
type nodeView struct {
	vm    *goja.Runtime
	nodes port.NodeData
}

func (n *nodeView) Get(name string) goja.Value {
	if n.nodes == nil {
		panic(n.vm.NewGoError(fmt.Errorf("node %q: no node data available: %w", name, domain.ErrNotFound)))
	}
	it, err := n.nodes.PairedItem(name)
	if err != nil {
		panic(n.vm.NewGoError(err))
	}
	return itemToJS(n.vm, it)
}
func (*nodeView) Set(string, goja.Value) bool { return false }
func (*nodeView) Has(string) bool             { return true }
func (*nodeView) Delete(string) bool          { return false }
func (*nodeView) Keys() []string              { return nil }

// envView implements $env as a read-only view restricted to an allow-list.
type envView struct {
	vm     *goja.Runtime
	allow  []string
	lookup func(string) (string, bool)
}

func (e envView) Get(name string) goja.Value {
	if !slices.Contains(e.allow, name) {
		return nil
	}
	v, ok := e.lookup(name)
	if !ok {
		return nil
	}
	return e.vm.ToValue(v)
}
func (e envView) Set(string, goja.Value) bool { return false }
func (e envView) Has(name string) bool {
	if !slices.Contains(e.allow, name) {
		return false
	}
	_, ok := e.lookup(name)
	return ok
}
func (envView) Delete(string) bool { return false }
func (e envView) Keys() []string {
	var keys []string
	for _, name := range e.allow {
		if _, ok := e.lookup(name); ok {
			keys = append(keys, name)
		}
	}
	return keys
}
