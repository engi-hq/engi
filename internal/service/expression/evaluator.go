// Package expression evaluates Engi expression templates ("=Hello {{ $json.name }}")
// in a sandboxed JavaScript runtime (goja).
package expression

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

const (
	// templatePrefix marks a parameter value as an expression template.
	templatePrefix = "="
	// maxStackLines bounds the JavaScript stack excerpt kept in errors.
	maxStackLines = 5
)

// Evaluator resolves parameter values. It is safe for concurrent use.
type Evaluator struct {
	cfg       config.ExpressionConfig
	clock     port.Clock
	lookupEnv func(string) (string, bool)
	cache     *programCache
	pool      sync.Pool
}

var _ port.ExpressionEvaluator = (*Evaluator)(nil)

// New builds an Evaluator. lookupEnv backs $env (it is only consulted for names
// on the configured allow-list); pass os.LookupEnv from the composition root.
func New(cfg config.ExpressionConfig, clock port.Clock, lookupEnv func(string) (string, bool)) (*Evaluator, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid expression config: %w", err)
	}
	if clock == nil || lookupEnv == nil {
		return nil, errors.New("expression evaluator requires a clock and an environment lookup")
	}
	e := &Evaluator{cfg: cfg, clock: clock, lookupEnv: lookupEnv, cache: newProgramCache(cfg.ProgramCacheSize)}
	// Fail fast if a sandbox cannot be built, instead of on first use.
	sb, err := newSandbox(cfg.MaxCallStackDepth)
	if err != nil {
		return nil, err
	}
	e.pool.Put(sb)
	return e, nil
}

// Resolve implements port.ExpressionEvaluator.
func (e *Evaluator) Resolve(ctx context.Context, value any, scope port.ExpressionScope) (any, error) {
	switch v := value.(type) {
	case string:
		tpl, ok := strings.CutPrefix(v, templatePrefix)
		if !ok {
			return v, nil
		}
		return e.evalTemplate(ctx, tpl, scope)
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, item := range v {
			r, err := e.Resolve(ctx, item, scope)
			if err != nil {
				return nil, err
			}
			out[k] = r
		}
		return out, nil
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			r, err := e.Resolve(ctx, item, scope)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	default:
		return value, nil
	}
}

func (e *Evaluator) evalTemplate(ctx context.Context, tpl string, scope port.ExpressionScope) (any, error) {
	if len(tpl) > e.cfg.MaxTemplateLength {
		return nil, newError(tpl, fmt.Sprintf("template has %d bytes, limit is %d", len(tpl), e.cfg.MaxTemplateLength), ErrTemplateTooLong, "")
	}
	if err := ctx.Err(); err != nil {
		return nil, newError(tpl, "evaluation canceled", err, "")
	}
	compiled, err := e.compile(tpl)
	if err != nil {
		return nil, err
	}

	sb, err := e.acquire()
	if err != nil {
		return nil, newError(tpl, "creating sandbox", err, "")
	}
	defer e.release(sb)

	stop := e.armInterrupt(ctx, sb.vm)
	defer stop()

	args := sb.scopeArgs(scope, e.clock.Now(), envView{allow: e.cfg.EnvAllowList, lookup: e.lookupEnv})

	if compiled.single() {
		return e.runSegment(sb, tpl, compiled.segments[0].program, args)
	}
	var b strings.Builder
	for _, seg := range compiled.segments {
		if seg.program == nil {
			b.WriteString(seg.literal)
		} else {
			v, err := e.runSegment(sb, tpl, seg.program, args)
			if err != nil {
				return nil, err
			}
			s, err := stringify(v)
			if err != nil {
				return nil, newError(tpl, "rendering block", err, "")
			}
			b.WriteString(s)
		}
		if b.Len() > e.cfg.MaxOutputBytes {
			return nil, newError(tpl, fmt.Sprintf("result exceeds %d bytes", e.cfg.MaxOutputBytes), ErrOutputTooLarge, "")
		}
	}
	return b.String(), nil
}

func (e *Evaluator) runSegment(sb *sandbox, tpl string, prog *goja.Program, args []goja.Value) (any, error) {
	v, err := sb.run(prog, args)
	if err != nil {
		return nil, e.classify(tpl, err)
	}
	v = normalize(v)
	if err := checkResult(v, e.cfg.MaxOutputBytes); err != nil {
		return nil, newError(tpl, err.Error(), err, "")
	}
	return v, nil
}

// compile returns the cached compiled form of a template, building it on a miss.
func (e *Evaluator) compile(tpl string) (*compiledTemplate, error) {
	key := keyFor(tpl)
	if c, ok := e.cache.get(key); ok {
		return c, nil
	}
	segs, err := parseTemplate(tpl)
	if err != nil {
		return nil, newError(tpl, err.Error(), err, "")
	}
	compiled := &compiledTemplate{segments: make([]compiledSegment, len(segs))}
	for i, s := range segs {
		if !s.isCode {
			compiled.segments[i] = compiledSegment{literal: s.text}
			continue
		}
		prog, err := compileBlock(s.text)
		if err != nil {
			return nil, newError(tpl, "syntax error in expression", err, "")
		}
		compiled.segments[i] = compiledSegment{program: prog}
	}
	e.cache.put(key, compiled)
	return compiled, nil
}

func (e *Evaluator) acquire() (*sandbox, error) {
	if sb, ok := e.pool.Get().(*sandbox); ok {
		return sb, nil
	}
	return newSandbox(e.cfg.MaxCallStackDepth)
}

func (e *Evaluator) release(sb *sandbox) { e.pool.Put(sb) }

// armInterrupt makes vm.Interrupt fire on timeout or cancellation. The returned
// function disarms it and clears any pending interrupt so the VM can be reused.
func (e *Evaluator) armInterrupt(ctx context.Context, vm *goja.Runtime) (stop func()) {
	timer := time.AfterFunc(e.cfg.EvalTimeout, func() { vm.Interrupt(ErrTimeout) })
	stopCtx := context.AfterFunc(ctx, func() { vm.Interrupt(ctx.Err()) })
	return func() {
		timer.Stop()
		stopCtx()
		vm.ClearInterrupt()
	}
}

// classify turns a runtime failure into a *domain.ExpressionError.
func (e *Evaluator) classify(tpl string, err error) error {
	var interrupted *goja.InterruptedError
	if errors.As(err, &interrupted) {
		if cause, ok := interrupted.Value().(error); ok {
			if errors.Is(cause, ErrTimeout) {
				return newError(tpl, fmt.Sprintf("evaluation exceeded %s", e.cfg.EvalTimeout), ErrTimeout, "")
			}
			return newError(tpl, "evaluation canceled", cause, "")
		}
	}
	var exc *goja.Exception
	if errors.As(err, &exc) {
		return newError(tpl, exc.Value().String(), err, stackExcerpt(exc.String()))
	}
	return newError(tpl, err.Error(), err, "")
}

func newError(tpl, msg string, cause error, stack string) *domain.ExpressionError {
	return &domain.ExpressionError{Expression: tpl, Message: msg, Stack: stack, Err: cause}
}

func stackExcerpt(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) > maxStackLines {
		lines = lines[:maxStackLines]
	}
	return strings.Join(lines, "\n")
}
