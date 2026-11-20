// Package aiagent implements the AI Agent node (engi.ai.agent). For each input
// item it runs a tool-using loop: call the model, run the tools it asks for
// (in parallel), feed the results back, and repeat until it answers or a limit
// is reached. The model, optional memory and tools come from sub-nodes
// connected to the node's ai.* inputs.
package aiagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/engi-hq/engi/internal/node/aikit"
	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.ai.agent"

// DefaultPrompt is the prompt expression: the chatInput a chat trigger sets.
const DefaultPrompt = "={{ $json.chatInput }}"

// onLimit values.
const (
	limitError  = "error"
	limitReturn = "return"
)

// Config is what the node needs from the host.
type Config struct {
	// Limits are the defaults; a node may override iterations, tokens and time.
	Limits Limits
	// Clock times tool calls.
	Clock Clock
}

// Node is the AI Agent node.
type Node struct{ cfg Config }

// New returns an AI Agent node.
func New(cfg Config) *Node { return &Node{cfg: cfg} }

// Describe implements nodesdk.Node.
func (n *Node) Describe() nodesdk.Descriptor {
	props := []nodesdk.Property{
		{Name: "prompt", DisplayName: "Prompt", Type: nodesdk.PropString, Required: true, Default: DefaultPrompt,
			Description: "The user message. The default reads chatInput, which the chat trigger sets along with sessionId."},
		{Name: "systemPrompt", DisplayName: "System prompt", Type: nodesdk.PropString,
			Description: "Instructions for the agent, for example its role and rules for using tools."},
	}
	props = append(props, aikit.FormatProperties()...)
	props = append(props,
		nodesdk.Property{Name: "maxIterations", DisplayName: "Max iterations", Type: nodesdk.PropNumber,
			Description: "Model calls per item. Empty uses nodes.ai.agent.maxIterations."},
		nodesdk.Property{Name: "maxTotalTokens", DisplayName: "Max total tokens", Type: nodesdk.PropNumber,
			Description: "Input plus output tokens per item, over all calls. Empty uses nodes.ai.agent.maxTotalTokens."},
		nodesdk.Property{Name: "timeoutSeconds", DisplayName: "Timeout (seconds)", Type: nodesdk.PropNumber,
			Description: "Time limit per item, tool runs included. Empty uses nodes.ai.agent.timeout."},
		nodesdk.Property{Name: "onLimit", DisplayName: "When a limit is reached", Type: nodesdk.PropOptions, Default: limitError, Options: []nodesdk.Option{
			nodekit.Opt(limitError, "Fail the item"), nodekit.Opt(limitReturn, "Return what the agent has so far")}},
		nodesdk.Property{Name: "saveToolMessages", DisplayName: "Remember tool calls", Type: nodesdk.PropBoolean, Default: false,
			Description: "Store the tool calls and results in memory too. Off keeps only the question and the final answer."},
	)
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "AI Agent", Group: nodesdk.GroupAI, Icon: "bot",
		Description: "An agent that calls tools to answer: connect a model, optional memory and any number of tools.",
		Inputs: []nodesdk.PortSpec{
			nodekit.Main("main", ""),
			{Name: "model", Kind: nodesdk.PortAIModel, DisplayName: "Model", Required: true, MaxConnections: 1},
			{Name: "memory", Kind: nodesdk.PortAIMemory, DisplayName: "Memory", MaxConnections: 1},
			{Name: "tools", Kind: nodesdk.PortAITool, DisplayName: "Tools"},
		},
		Outputs:    []nodesdk.PortSpec{nodekit.Main("main", "")},
		Properties: props,
	}
}

// Execute implements nodesdk.Node.
func (n *Node) Execute(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	items := in.Items(0)
	if len(items) == 0 {
		return nodesdk.Output{Ports: [][]nodesdk.Item{nil}}, nil
	}
	out := make([]nodesdk.Item, 0, len(items))
	for i := range items {
		res, err := n.runItem(ctx, in, i)
		if err != nil {
			return nodesdk.Output{}, nodekit.ItemError(i, err)
		}
		out = append(out, nodekit.Paired(nodesdk.Item{JSON: res}, i))
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}

// settings are the per-item parameters.
type settings struct {
	prompt, system string
	format         port.ResponseFormat
	formatID       string
	limits         Limits
	onLimit        string
	saveTools      bool
}

func (n *Node) settings(ctx context.Context, in nodesdk.Input, i int) (settings, error) {
	s := settings{limits: n.cfg.Limits}
	var err error
	if s.prompt, err = nodesdk.ParamString(ctx, in, "prompt", i); err != nil {
		return s, err //nolint:wrapcheck // SDK errors are descriptive
	}
	if strings.TrimSpace(s.prompt) == "" {
		return s, errors.New("prompt is empty: the item has no chatInput (a chat trigger sets it); set the prompt explicitly")
	}
	if s.system, err = nodesdk.ParamString(ctx, in, "systemPrompt", i); err != nil {
		return s, err //nolint:wrapcheck // SDK errors are descriptive
	}
	if s.format, s.formatID, err = aikit.ReadFormat(ctx, in, i); err != nil {
		return s, err //nolint:wrapcheck // descriptive
	}
	for _, p := range []struct {
		name string
		dst  *int
	}{{"maxIterations", &s.limits.MaxIterations}, {"maxTotalTokens", &s.limits.MaxTotalTokens}} {
		v, err := nodesdk.ParamInt(ctx, in, p.name, i)
		if err != nil {
			return s, err //nolint:wrapcheck // SDK errors are descriptive
		}
		if v < 0 {
			return s, fmt.Errorf("%s must not be negative", p.name)
		}
		if v > 0 {
			*p.dst = v
		}
	}
	secs, err := nodekit.ParamFloat(ctx, in, "timeoutSeconds", i)
	if err != nil {
		return s, err //nolint:wrapcheck // descriptive
	}
	if secs < 0 {
		return s, errors.New("timeoutSeconds must not be negative")
	}
	if secs > 0 {
		s.limits.Timeout = time.Duration(secs * float64(time.Second))
	}
	if s.onLimit, err = nodesdk.ParamString(ctx, in, "onLimit", i); err != nil {
		return s, err //nolint:wrapcheck // SDK errors are descriptive
	}
	if s.onLimit != limitReturn {
		s.onLimit = limitError
	}
	s.saveTools, err = nodesdk.ParamBool(ctx, in, "saveToolMessages", i)
	return s, err //nolint:wrapcheck // SDK errors are descriptive
}

func (n *Node) runItem(ctx context.Context, in nodesdk.Input, i int) (map[string]any, error) {
	s, err := n.settings(ctx, in, i)
	if err != nil {
		return nil, err
	}
	provider, err := modelOf(ctx, in, i)
	if err != nil {
		return nil, err
	}
	llm, tmpl, err := provider.Model(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolving the model: %w", err)
	}
	tmpl.System = joinSystem(tmpl.System, s.system)
	tmpl.Format = s.format
	tools, err := toolsOf(ctx, in, i)
	if err != nil {
		return nil, err
	}
	memory, err := memoryOf(ctx, in, i)
	if err != nil {
		return nil, err
	}
	var history []port.LLMMessage
	if memory != nil {
		if history, err = memory.History(ctx); err != nil {
			return nil, err //nolint:wrapcheck // descriptive
		}
	}
	l := &loop{llm: llm, tmpl: tmpl, tools: tools, limits: s.limits, clock: n.cfg.Clock}
	res, err := l.run(ctx, history, s.prompt)
	if err != nil {
		return nil, err
	}
	if res.StopReason != StopEnd && s.onLimit == limitError {
		return nil, limitFailure(res, s.limits)
	}
	item, err := result(res, s.formatID)
	if err != nil {
		return nil, err
	}
	if memory != nil && res.StopReason == StopEnd {
		if err := memory.Save(ctx, saved(res, s.saveTools)); err != nil {
			return nil, err //nolint:wrapcheck // descriptive
		}
	}
	return item, nil
}

func joinSystem(parts ...string) string {
	var kept []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "\n\n")
}

// saved picks the messages to remember: the question and the final answer, or
// the whole exchange.
func saved(res Outcome, all bool) []port.LLMMessage {
	if all {
		return res.Exchange
	}
	return []port.LLMMessage{res.Exchange[0], {Role: port.RoleAssistant, Content: res.Output}}
}

func limitFailure(res Outcome, l Limits) error {
	var what string
	switch res.StopReason {
	case StopMaxIterations:
		what = fmt.Sprintf("reached the limit of %d model calls", l.MaxIterations)
	case StopTokenBudget:
		what = fmt.Sprintf("used the token budget of %d", l.MaxTotalTokens)
	case StopTimeout:
		what = fmt.Sprintf("ran longer than %s", l.Timeout)
	default:
		what = "stopped early"
	}
	return &nodesdk.NodeError{
		Message:     "agent " + what,
		Description: fmt.Sprintf("It made %d tool calls in %d model calls. Raise the limit on the node, or set \"When a limit is reached\" to return the partial result.", len(res.Steps), res.Iterations),
	}
}

// result builds the output item JSON.
func result(res Outcome, formatID string) (map[string]any, error) {
	steps := make([]any, len(res.Steps))
	for i, s := range res.Steps {
		steps[i] = map[string]any{
			"tool": s.Tool, "callId": s.CallID, "iteration": float64(s.Iteration), "input": decode(s.Input),
			"output": decode([]byte(s.Output)), "isError": s.IsError, "durationMs": float64(s.DurationMs),
		}
	}
	item := map[string]any{
		"output": res.Output, "steps": steps, "iterations": float64(res.Iterations), "stopReason": res.StopReason, "model": res.Model,
		"usage": map[string]any{
			"inputTokens": float64(res.Usage.InputTokens), "outputTokens": float64(res.Usage.OutputTokens),
			"totalTokens": float64(res.Usage.InputTokens + res.Usage.OutputTokens),
		},
	}
	if formatID != aikit.FormatText && res.StopReason == StopEnd {
		parsed, err := aikit.ParseJSONReply(res.Output)
		if err != nil {
			return nil, fmt.Errorf("the final reply is not valid JSON: %w", err)
		}
		item["json"] = parsed
	}
	return item, nil
}

// decode returns JSON text as data, anything else as the string itself.
func decode(raw []byte) any {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	return v
}

func modelOf(ctx context.Context, in nodesdk.Input, i int) (port.ModelProvider, error) {
	got, err := in.SubPort(ctx, nodesdk.PortAIModel, i)
	if err != nil {
		return nil, fmt.Errorf("model: %w", err)
	}
	if len(got) == 0 {
		return nil, errors.New("no model connected")
	}
	p, ok := got[0].Value.(port.ModelProvider)
	if !ok {
		return nil, fmt.Errorf("sub-node %q does not supply a model", got[0].Node)
	}
	return p, nil
}

func memoryOf(ctx context.Context, in nodesdk.Input, i int) (port.ChatMemory, error) {
	got, err := in.SubPort(ctx, nodesdk.PortAIMemory, i)
	if err != nil {
		return nil, fmt.Errorf("memory: %w", err)
	}
	if len(got) == 0 {
		return nil, nil
	}
	m, ok := got[0].Value.(port.ChatMemory)
	if !ok {
		return nil, fmt.Errorf("sub-node %q does not supply memory", got[0].Node)
	}
	return m, nil
}

// toolPrefixer is implemented by tool providers whose names are unique only
// within one server (MCP clients). The agent asks them to prefix their names
// with the sub-node name, so two such sub-nodes cannot clash.
type toolPrefixer interface {
	WithToolPrefix(node string) port.ToolProvider
}

// toolsOf collects the tools of every tool sub-node connected to the agent.
// Names must be unique across all of them: a duplicate fails the item with an
// error naming both sub-nodes, since the model could not tell the tools apart.
func toolsOf(ctx context.Context, in nodesdk.Input, i int) (*port.Toolset, error) {
	got, err := in.SubPort(ctx, nodesdk.PortAITool, i)
	if err != nil {
		return nil, fmt.Errorf("tools: %w", err)
	}
	var all []port.Tool
	owner := map[string]string{} // tool name -> sub-node that offers it
	for _, g := range got {
		p, ok := g.Value.(port.ToolProvider)
		if !ok {
			return nil, fmt.Errorf("sub-node %q does not supply tools", g.Node)
		}
		if pf, ok := p.(toolPrefixer); ok {
			p = pf.WithToolPrefix(g.Node)
		}
		ts, err := p.Tools(ctx)
		if err != nil {
			return nil, fmt.Errorf("tools of %q: %w", g.Node, err)
		}
		for _, t := range ts {
			name := t.Definition().Name
			if prev, dup := owner[name]; dup {
				return nil, duplicateToolError(name, prev, g.Node)
			}
			owner[name] = g.Node
			all = append(all, t)
		}
	}
	set, err := port.NewToolset(all...)
	if err != nil {
		return nil, fmt.Errorf("collecting tools: %w", err)
	}
	return set, nil
}

// duplicateToolError names both sub-nodes that offer the same tool name.
func duplicateToolError(name, first, second string) error {
	if first == second {
		return fmt.Errorf("collecting tools: sub-node %q offers tool %q more than once: %w", first, name, port.ErrDuplicateTool)
	}
	return fmt.Errorf("collecting tools: tool name %q is offered by sub-node %q and by sub-node %q; rename one of the tools so the model can tell them apart: %w",
		name, first, second, port.ErrDuplicateTool)
}
