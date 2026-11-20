package aiagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/engi-hq/engi/internal/port"
)

// Stop reasons of the loop (the output's stopReason).
const (
	// StopEnd means the model answered without asking for more tools.
	StopEnd = "end"
	// StopMaxIterations means the iteration limit was reached.
	StopMaxIterations = "maxIterations"
	// StopTokenBudget means the token budget was used up.
	StopTokenBudget = "tokenBudget"
	// StopTimeout means the overall time limit passed.
	StopTimeout = "timeout"
)

// Clock tells the time, to measure how long tools run.
type Clock interface{ Now() time.Time }

// Limits bound one agent loop (one item).
type Limits struct {
	MaxIterations      int
	MaxTotalTokens     int
	Timeout            time.Duration
	ToolConcurrency    int
	MaxToolResultBytes int
}

// Step is one tool call the model made.
type Step struct {
	Tool       string
	CallID     string
	Iteration  int
	Input      json.RawMessage
	Output     string
	IsError    bool
	DurationMs int64
}

// Outcome is what the loop produced.
type Outcome struct {
	// Output is the final reply text; on a limit it is the last text the model
	// wrote, possibly empty.
	Output     string
	Steps      []Step
	Usage      port.LLMUsage
	Iterations int
	StopReason string
	Model      string
	// Exchange holds the messages of this run: the user message first.
	Exchange []port.LLMMessage
}

// loop is one agent run's dependencies.
type loop struct {
	llm    port.LLM
	tmpl   port.LLMRequest
	tools  *port.Toolset
	limits Limits
	clock  Clock
}

// run drives the model until it stops calling tools or a limit is hit. history
// precedes user. Tool failures never abort the run: they are fed back to the
// model. Model errors and cancellation of ctx do.
func (l *loop) run(parent context.Context, history []port.LLMMessage, user string) (Outcome, error) {
	ctx, cancel := context.WithTimeout(parent, l.limits.Timeout)
	defer cancel()

	userMsg := port.LLMMessage{Role: port.RoleUser, Content: user}
	msgs := append(append([]port.LLMMessage{}, history...), userMsg)
	out := Outcome{Exchange: []port.LLMMessage{userMsg}}
	defs := l.tools.Definitions()

	limited := func(reason string) (Outcome, error) {
		out.StopReason = reason
		return out, nil
	}
	timedOut := func() bool { return ctx.Err() != nil && parent.Err() == nil }

	for {
		if out.Iterations >= l.limits.MaxIterations {
			return limited(StopMaxIterations)
		}
		if total := out.Usage.InputTokens + out.Usage.OutputTokens; total >= l.limits.MaxTotalTokens {
			return limited(StopTokenBudget)
		}
		req := l.tmpl
		req.Messages = append([]port.LLMMessage{}, msgs...)
		req.Tools = defs
		resp, err := l.llm.Chat(ctx, req)
		if err != nil {
			if timedOut() {
				return limited(StopTimeout)
			}
			if parent.Err() != nil {
				return out, fmt.Errorf("agent canceled: %w", parent.Err())
			}
			return out, fmt.Errorf("calling the model: %w", err)
		}
		out.Iterations++
		out.Usage.InputTokens += resp.Usage.InputTokens
		out.Usage.OutputTokens += resp.Usage.OutputTokens
		out.Model = resp.Model
		out.Output = resp.Content
		assistant := port.LLMMessage{Role: port.RoleAssistant, Content: resp.Content, ToolCalls: resp.ToolCalls}
		msgs = append(msgs, assistant)
		out.Exchange = append(out.Exchange, assistant)
		if len(resp.ToolCalls) == 0 {
			out.StopReason = StopEnd
			return out, nil
		}
		steps, results := l.runTools(ctx, out.Iterations, resp.ToolCalls)
		if parent.Err() != nil {
			return out, fmt.Errorf("agent canceled: %w", parent.Err())
		}
		if timedOut() {
			return limited(StopTimeout)
		}
		out.Steps = append(out.Steps, steps...)
		msgs = append(msgs, results...)
		out.Exchange = append(out.Exchange, results...)
	}
}

// runTools executes one turn's tool calls with bounded parallelism. Steps and
// result messages follow the order of calls.
func (l *loop) runTools(ctx context.Context, iteration int, calls []port.LLMToolCall) ([]Step, []port.LLMMessage) {
	steps := make([]Step, len(calls))
	msgs := make([]port.LLMMessage, len(calls))
	sem := make(chan struct{}, l.limits.ToolConcurrency)
	var wg sync.WaitGroup
	for i, call := range calls {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			start := l.clock.Now()
			res := l.callTool(ctx, call)
			res.Content = truncate(res.Content, l.limits.MaxToolResultBytes)
			steps[i] = Step{
				Tool: call.Name, CallID: call.ID, Iteration: iteration, Input: call.Arguments,
				Output: res.Content, IsError: res.IsError, DurationMs: l.clock.Now().Sub(start).Milliseconds(),
			}
			msgs[i] = port.LLMMessage{Role: port.RoleTool, ToolCallID: call.ID, Content: res.Content, IsError: res.IsError}
		}()
	}
	wg.Wait()
	return steps, msgs
}

// callTool runs one tool, turning every failure (unknown tool, error, panic)
// into an error result for the model.
func (l *loop) callTool(ctx context.Context, call port.LLMToolCall) (res port.ToolResult) {
	defer func() {
		if r := recover(); r != nil {
			res = port.ToolResult{Content: fmt.Sprintf("tool %q crashed: %v", call.Name, r), IsError: true}
		}
	}()
	args := call.Arguments
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	res, err := l.tools.Call(ctx, call.Name, args)
	switch {
	case errors.Is(err, port.ErrUnknownTool):
		return port.ToolResult{Content: fmt.Sprintf("there is no tool named %q; available tools: %v", call.Name, l.tools.Names()), IsError: true}
	case err != nil:
		return port.ToolResult{Content: fmt.Sprintf("tool %q could not run: %v", call.Name, err), IsError: true}
	}
	return res
}

// truncate cuts s to at most max bytes on a rune boundary and says so.
func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return fmt.Sprintf("%s\n[truncated: %d bytes omitted]", s[:cut], len(s)-cut)
}
