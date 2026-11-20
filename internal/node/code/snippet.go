package code

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/dop251/goja"

	"github.com/engi-hq/engi/internal/jsfn"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// snippetScope is what a Snippet sees: the argument object, the $fn helpers
// and console. There is no item or workflow scope.
const snippetScope = "args,$fn,console"

// Snippet is a compiled piece of user JavaScript that takes a JSON object and
// returns JSON data, run in the same sandbox as the Code node (frozen
// built-ins, no host objects, bounded time, recursion and result size). It is
// what code-based tools use.
type Snippet struct {
	prog *goja.Program
}

// NewSnippet compiles the function body src. A syntax error is a
// *nodesdk.NodeError.
func NewSnippet(src string) (*Snippet, error) {
	prog, err := goja.Compile("snippet", "(function("+snippetScope+`){"use strict";`+src+"\n})", true)
	if err != nil {
		return nil, &nodesdk.NodeError{Message: "JavaScript syntax error", Description: err.Error(), Err: err}
	}
	return &Snippet{prog: prog}, nil
}

// Run calls the snippet with args in scope and returns the plain JSON value it
// returned (nil for undefined). Each call uses a fresh runtime. console output
// goes to log.
func (s *Snippet) Run(ctx context.Context, limits Limits, args map[string]any, log *slog.Logger) (any, error) {
	vm := goja.New()
	vm.SetMaxCallStackSize(limits.MaxCallStackDepth)
	if _, err := vm.RunString(hardenScript); err != nil {
		return nil, fmt.Errorf("hardening sandbox: %w", err)
	}
	timer := time.AfterFunc(limits.Timeout, func() { vm.Interrupt(errTimeout) })
	stopCtx := context.AfterFunc(ctx, func() { vm.Interrupt(ctx.Err()) })
	defer func() { timer.Stop(); stopCtx() }()

	fnVal, err := vm.RunProgram(s.prog)
	if err != nil {
		return nil, classifySnippet(err, limits)
	}
	fn, ok := goja.AssertFunction(fnVal)
	if !ok {
		return nil, errors.New("code wrapper is not a function")
	}
	if args == nil {
		args = map[string]any{}
	}
	out, err := fn(goja.Undefined(), toJS(vm, args), jsfn.New(vm), snippetConsole(vm, log))
	if err != nil {
		return nil, classifySnippet(err, limits)
	}
	exported := normalize(out.Export())
	if err := checkSize(exported, limits.MaxOutputBytes); err != nil {
		return nil, err
	}
	return exported, nil
}

func classifySnippet(err error, limits Limits) error {
	return (&run{n: Node{limits: limits}}).classify(err)
}

func snippetConsole(vm *goja.Runtime, log *slog.Logger) goja.Value {
	c := vm.NewObject()
	for _, level := range []string{"log", "info", "warn", "error"} {
		_ = c.Set(level, func(call goja.FunctionCall) goja.Value {
			parts := make([]string, len(call.Arguments))
			for i, a := range call.Arguments {
				parts[i] = fmt.Sprint(a.Export())
			}
			if log != nil {
				log.Info("code tool output", "level", level, "message", strings.Join(parts, " "))
			}
			return goja.Undefined()
		})
	}
	return c
}
