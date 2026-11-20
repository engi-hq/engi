// Package code implements the Code node: user JavaScript run in a sandboxed
// goja runtime, once for all items or once per item.
//
// The runtime has no host objects: no require, timers, process, filesystem or
// network. Built-ins are frozen, run time and recursion are bounded, and the
// result size is capped. Each Execute call uses a fresh runtime.
package code

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dop251/goja"

	"github.com/engi-hq/engi/internal/jsfn"
	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.code.javascript"

// Modes.
const (
	modeAll  = "runOnceForAllItems"
	modeEach = "runOnceForEachItem"
)

// defaultCode is the code a new node starts with.
const defaultCode = "// $input.all() returns every item. Return an array of items.\nreturn $input.all();"

// Variables in scope of user code, in call order.
const scopeParams = "$json,$binary,$input,$itemIndex,$runIndex,$execution,$workflow,$now,$today,console,$,$node,$vars,$env,$fn"

// hardenScript freezes the built-ins so user code cannot pollute the runtime.
const hardenScript = `(function (g) {
  ["Object","Function","Array","String","Number","Boolean","Symbol","Date","RegExp","Error",
   "TypeError","RangeError","SyntaxError","ReferenceError","EvalError","URIError","Map","Set",
   "WeakMap","WeakSet","Promise","ArrayBuffer"].forEach(function (n) {
    var c = g[n];
    if (c) { Object.freeze(c); if (c.prototype) { Object.freeze(c.prototype); } }
  });
  ["Math","JSON","Reflect"].forEach(function (n) { if (g[n]) { Object.freeze(g[n]); } });
  Object.freeze(g);
})(this);`

// Clock tells the current time for $now and $today.
type Clock interface {
	Now() time.Time
}

// Limits bound one Code node run.
type Limits struct {
	// Timeout bounds the run time of the user code.
	Timeout time.Duration
	// MaxCallStackDepth bounds JavaScript recursion.
	MaxCallStackDepth int
	// MaxOutputBytes bounds the serialized size of the result.
	MaxOutputBytes int
}

// Node is the Code node.
type Node struct {
	limits Limits
	clock  Clock
}

// New returns a Code node enforcing limits.
func New(limits Limits, clock Clock) nodesdk.Node { return Node{limits: limits, clock: clock} }

// Describe implements nodesdk.Node.
func (Node) Describe() nodesdk.Descriptor {
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "Code (JavaScript)", Group: nodesdk.GroupTransform, Icon: "code",
		Description: "Run JavaScript in a sandbox to transform items.",
		Inputs:      []nodesdk.PortSpec{nodekit.Main("main", "")},
		Outputs:     []nodesdk.PortSpec{nodekit.Main("main", "")},
		Properties: []nodesdk.Property{
			{Name: "mode", DisplayName: "Mode", Type: nodesdk.PropOptions, Default: modeAll, Options: []nodesdk.Option{
				{Name: "Run once for all items", Value: modeAll, Description: "$input.all() holds every item; return an array of items (or plain objects)."},
				{Name: "Run once for each item", Value: modeEach, Description: "$json is the current item; return an object, an array of objects, or null to drop it."}}},
			{Name: "jsCode", DisplayName: "JavaScript", Type: nodesdk.PropCode, Default: defaultCode, Required: true,
				Description: "Function body. In scope: $json, $binary, $input (all/first/last/item), $(\"Node\"), $node, $vars, $env, $fn, $itemIndex, $runIndex, $execution, $workflow, $now, $today, console.log."},
		},
	}
}

// Execute implements nodesdk.Node.
func (n Node) Execute(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	mode, err := nodesdk.ParamString(ctx, in, "mode", 0)
	if err != nil {
		return nodesdk.Output{}, fmt.Errorf("reading mode: %w", err)
	}
	src, err := nodesdk.ParamString(ctx, in, "jsCode", 0)
	if err != nil {
		return nodesdk.Output{}, fmt.Errorf("reading jsCode: %w", err)
	}
	prog, err := goja.Compile("code", "(function("+scopeParams+`){"use strict";`+src+"\n})", true)
	if err != nil {
		return nodesdk.Output{}, &nodesdk.NodeError{Message: "JavaScript syntax error", Description: err.Error(), Err: err}
	}
	vm := goja.New()
	vm.SetMaxCallStackSize(n.limits.MaxCallStackDepth)
	if _, err := vm.RunString(hardenScript); err != nil {
		return nodesdk.Output{}, fmt.Errorf("hardening sandbox: %w", err)
	}
	stop := n.arm(ctx, vm)
	defer stop()

	r := &run{n: n, vm: vm, prog: prog, in: in, fn: jsfn.New(vm)}
	switch mode {
	case modeAll, "":
		return r.all(ctx)
	case modeEach:
		return r.each(ctx)
	}
	return nodesdk.Output{}, fmt.Errorf("unknown mode %q", mode)
}

// arm interrupts the runtime on timeout or cancellation. The whole Execute call
// (all items of the per-item mode included) shares one time budget.
func (n Node) arm(ctx context.Context, vm *goja.Runtime) (stop func()) {
	timer := time.AfterFunc(n.limits.Timeout, func() { vm.Interrupt(errTimeout) })
	stopCtx := context.AfterFunc(ctx, func() { vm.Interrupt(ctx.Err()) })
	return func() { timer.Stop(); stopCtx() }
}

var errTimeout = errors.New("code execution timed out")

type run struct {
	n    Node
	vm   *goja.Runtime
	prog *goja.Program
	in   nodesdk.Input
	fn   *goja.Object
}

func (r *run) all(ctx context.Context) (nodesdk.Output, error) {
	items := r.in.Items(0)
	res, err := r.call(ctx, nil, -1, items)
	if err != nil {
		return nodesdk.Output{}, err
	}
	out, err := r.itemsFrom(res)
	if err != nil {
		return nodesdk.Output{}, err
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}

func (r *run) each(ctx context.Context) (nodesdk.Output, error) {
	items := r.in.Items(0)
	var out []nodesdk.Item
	for i := range items {
		res, err := r.call(ctx, &items[i], i, items)
		if err != nil {
			return nodesdk.Output{}, nodekit.ItemError(i, err)
		}
		if res == nil {
			continue
		}
		produced, err := r.itemsFrom(res)
		if err != nil {
			return nodesdk.Output{}, nodekit.ItemError(i, err)
		}
		for _, p := range produced {
			if len(p.PairedItem) == 0 {
				p.PairedItem = []nodesdk.PairedItem{{Input: 0, Item: i}}
			}
			out = append(out, p)
		}
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}

// call runs the user function. cur is the current item in per-item mode.
func (r *run) call(ctx context.Context, cur *nodesdk.Item, index int, all []nodesdk.Item) (any, error) {
	fnVal, err := r.vm.RunProgram(r.prog)
	if err != nil {
		return nil, r.classify(err)
	}
	fn, ok := goja.AssertFunction(fnVal)
	if !ok {
		return nil, errors.New("code wrapper is not a function")
	}
	info := r.in.ExecutionInfo()
	now := r.n.clock.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	// $json and $binary read the current item in per-item mode and the first
	// item in all-items mode; with no input they are empty objects.
	var jsonVal, binaryVal goja.Value = r.vm.NewObject(), r.vm.NewObject()
	itemIndex := max(index, 0)
	focus := cur
	if focus == nil && len(all) > 0 {
		focus = &all[0]
	}
	if focus != nil {
		jsonVal, binaryVal = toJS(r.vm, focus.JSON), binaryToJS(r.vm, focus.Binary)
	}
	scope := workflowScope{vm: r.vm, in: r.in, ctx: ctx, itemIndex: itemIndex}
	args := []goja.Value{
		jsonVal, binaryVal, r.inputObject(all, cur), r.vm.ToValue(itemIndex), r.vm.ToValue(info.RunIndex),
		toJS(r.vm, map[string]any{"id": info.ExecutionID, "mode": info.Mode}),
		toJS(r.vm, map[string]any{"id": info.WorkflowID, "name": info.WorkflowName}),
		r.date(now), r.date(today), r.console(),
		scope.nodeFunc(), scope.view("$node"), scope.view("$vars"), scope.view("$env"), r.fn,
	}
	out, err := fn(goja.Undefined(), args...)
	if err != nil {
		return nil, r.classify(err)
	}
	exported := normalize(out.Export())
	if err := checkSize(exported, r.n.limits.MaxOutputBytes); err != nil {
		return nil, err
	}
	return exported, nil
}

// checkSize rejects results that cannot be serialized or exceed maxBytes.
func checkSize(v any, maxBytes int) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return &nodesdk.NodeError{Message: "the code returned data that cannot be serialized", Err: err}
	}
	if len(raw) > maxBytes {
		return &nodesdk.NodeError{Message: fmt.Sprintf("the code returned %d bytes, the limit is %d", len(raw), maxBytes)}
	}
	return nil
}

func (r *run) classify(err error) error {
	var interrupted *goja.InterruptedError
	if errors.As(err, &interrupted) {
		if cause, ok := interrupted.Value().(error); ok {
			if errors.Is(cause, errTimeout) {
				return &nodesdk.NodeError{Message: fmt.Sprintf("code ran longer than %s", r.n.limits.Timeout), Err: errTimeout}
			}
			return fmt.Errorf("code canceled: %w", cause)
		}
	}
	var exc *goja.Exception
	if errors.As(err, &exc) {
		return &nodesdk.NodeError{Message: "JavaScript error: " + exc.Value().String(), Description: firstLines(exc.String(), 5), Err: err}
	}
	return &nodesdk.NodeError{Message: "JavaScript error", Err: err}
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

func (r *run) date(t time.Time) goja.Value {
	v, err := r.vm.New(r.vm.Get("Date"), r.vm.ToValue(t.UnixMilli()))
	if err != nil {
		panic(r.vm.NewGoError(err))
	}
	return v
}

// console exposes log/info/warn/error to the structured logger.
func (r *run) console() goja.Value {
	c := r.vm.NewObject()
	logf := func(level string) func(goja.FunctionCall) goja.Value {
		return func(call goja.FunctionCall) goja.Value {
			parts := make([]string, len(call.Arguments))
			for i, a := range call.Arguments {
				parts[i] = fmt.Sprint(a.Export())
			}
			r.in.Logger().Info("code output", "level", level, "message", strings.Join(parts, " "))
			return goja.Undefined()
		}
	}
	for _, level := range []string{"log", "info", "warn", "error"} {
		_ = c.Set(level, logf(level))
	}
	return c
}

// inputObject builds $input with all(), first(), last() and item.
func (r *run) inputObject(all []nodesdk.Item, cur *nodesdk.Item) goja.Value {
	vm := r.vm
	list := func() []any {
		out := make([]any, len(all))
		for i, it := range all {
			out[i] = itemToJS(vm, it)
		}
		return out
	}
	obj := vm.NewObject()
	_ = obj.Set("all", func(goja.FunctionCall) goja.Value { return vm.NewArray(list()...) })
	_ = obj.Set("first", func(goja.FunctionCall) goja.Value {
		if len(all) == 0 {
			return goja.Undefined()
		}
		return itemToJS(vm, all[0])
	})
	_ = obj.Set("last", func(goja.FunctionCall) goja.Value {
		if len(all) == 0 {
			return goja.Undefined()
		}
		return itemToJS(vm, all[len(all)-1])
	})
	if cur != nil {
		_ = obj.Set("item", itemToJS(vm, *cur))
	}
	return obj
}

// itemsFrom converts a returned value to items: an array of items, a single
// item, or plain objects. An element with an object "json" key is an item
// ({json, binary}); any other object becomes the item's JSON.
func (r *run) itemsFrom(v any) ([]nodesdk.Item, error) {
	var elems []any
	switch t := v.(type) {
	case nil:
		return nil, nil
	case []any:
		elems = t
	default:
		elems = []any{t}
	}
	out := make([]nodesdk.Item, 0, len(elems))
	for i, e := range elems {
		m, ok := e.(map[string]any)
		if !ok {
			return nil, &nodesdk.NodeError{Message: fmt.Sprintf("returned item %d must be an object, got %s", i, nodekit.TypeName(e))}
		}
		it := nodesdk.Item{JSON: m}
		if j, ok := m["json"].(map[string]any); ok {
			it = nodesdk.Item{JSON: j}
			if b, ok := m["binary"].(map[string]any); ok {
				it.Binary = binaryFrom(b)
			}
		}
		out = append(out, it)
	}
	return out, nil
}

func binaryFrom(m map[string]any) map[string]nodesdk.BinaryRef {
	out := make(map[string]nodesdk.BinaryRef, len(m))
	for name, raw := range m {
		b, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		ref := nodesdk.BinaryRef{}
		ref.Ref, _ = b["ref"].(string)
		ref.MIME, _ = b["mime"].(string)
		ref.Name, _ = b["name"].(string)
		if sz, ok := b["size"].(float64); ok {
			ref.Size = int64(sz)
		}
		out[name] = ref
	}
	return out
}
