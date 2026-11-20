// Package mcpserver is the use-case layer of the MCP server trigger: it finds
// the trigger node that serves an MCP path, and turns each tools/list and
// tools/call into the listed workflows and synchronous runs of them. The HTTP
// transport, authentication and size limits belong to the adapter in
// internal/adapter/http/mcpserver.
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/mcp"
	"github.com/engi-hq/engi/internal/service/execution"
	"github.com/engi-hq/engi/internal/service/trigger"
)

// Router resolves an MCP path to its trigger node; *trigger.Service implements
// it. It wraps domain.ErrNotFound when no active workflow serves the path.
type Router interface {
	ResolveMCP(ctx context.Context, path string) (trigger.Route, error)
}

// Workflows loads workflows; port.WorkflowStore satisfies it.
type Workflows interface {
	Get(ctx context.Context, id domain.WorkflowID) (domain.Workflow, error)
}

// Executions runs a workflow and waits for its result; *execution.Service
// implements it.
type Executions interface {
	RunSync(ctx context.Context, req execution.StartRequest) (domain.Execution, error)
}

// Deps are the collaborators of the Service. Logger is optional.
type Deps struct {
	Router     Router
	Workflows  Workflows
	Executions Executions
	// SubworkflowTrigger is the node type a workflow must start with to be
	// callable as a tool (the Execute Workflow Trigger).
	SubworkflowTrigger string
	Logger             *slog.Logger
}

// Service resolves MCP endpoints and runs their tools. It is safe for
// concurrent use.
type Service struct {
	cfg config.MCPServerConfig
	d   Deps
	// slots is a counting semaphore over running tools/call requests.
	slots chan struct{}
}

// New builds a Service.
func New(cfg config.MCPServerConfig, d Deps) (*Service, error) {
	if d.Router == nil || d.Workflows == nil || d.Executions == nil || d.SubworkflowTrigger == "" {
		return nil, errors.New("MCP server service requires a router, workflows, executions and the sub-workflow trigger type")
	}
	if cfg.MaxConcurrentCalls <= 0 || cfg.CallTimeout <= 0 {
		return nil, errors.New("MCP server service requires positive maxConcurrentCalls and callTimeout")
	}
	if d.Logger == nil {
		d.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Service{cfg: cfg, d: d, slots: make(chan struct{}, cfg.MaxConcurrentCalls)}, nil
}

// Endpoint is one MCP server trigger, ready to serve. It implements
// mcp.ToolHandler. Auth and Credential tell the adapter how to authenticate a
// caller before it reads the body.
type Endpoint struct {
	// Auth is one of mcp.ServerAuthNone, ServerAuthBearer or ServerAuthHeader.
	Auth string
	// Credential is the bound credential; empty when Auth is none.
	Credential domain.CredentialID
	// WorkflowID and Node identify the trigger node, for logs.
	WorkflowID domain.WorkflowID
	Node       string

	svc   *Service
	tools []mcp.WorkflowTool
}

var _ mcp.ToolHandler = (*Endpoint)(nil)

// Open finds the active MCP server trigger that serves path (relative to the
// MCP base path). It wraps domain.ErrNotFound when there is none.
func (s *Service) Open(ctx context.Context, path string) (*Endpoint, error) {
	route, err := s.d.Router.ResolveMCP(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("resolving MCP endpoint /%s: %w", path, err)
	}
	wf, err := s.d.Workflows.Get(ctx, route.WorkflowID)
	if err != nil {
		return nil, fmt.Errorf("loading workflow %s: %w", route.WorkflowID, err)
	}
	node, ok := wf.NodeByName(route.Node)
	if !ok {
		return nil, fmt.Errorf("workflow %s has no node %q: %w", wf.ID, route.Node, domain.ErrNotFound)
	}
	tools, err := mcp.ParseWorkflowTools(node.Parameters[mcp.ParamTools])
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", route.Node, err)
	}
	return &Endpoint{
		Auth: route.Spec.Auth, Credential: route.Credential, WorkflowID: route.WorkflowID, Node: route.Node,
		svc: s, tools: tools,
	}, nil
}

// ListTools implements mcp.ToolHandler.
func (e *Endpoint) ListTools(context.Context) ([]mcp.ToolInfo, error) {
	out := make([]mcp.ToolInfo, len(e.tools))
	for i, t := range e.tools {
		out[i] = mcp.ToolInfo{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema}
	}
	return out, nil
}

// CallTool implements mcp.ToolHandler. It runs the tool's workflow with the
// arguments as its single input item and answers with the last node's output as
// JSON text. A workflow that cannot run or fails is a result with IsError set;
// only protocol problems (unknown tool, bad arguments, server busy) are errors.
func (e *Endpoint) CallTool(ctx context.Context, name string, args json.RawMessage) (mcp.CallToolResult, error) {
	tool, ok := e.find(name)
	if !ok {
		return mcp.CallToolResult{}, mcp.ErrUnknownTool
	}
	input, err := argumentsObject(args)
	if err != nil {
		return mcp.CallToolResult{}, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: err.Error()}
	}
	select {
	case e.svc.slots <- struct{}{}:
		defer func() { <-e.svc.slots }()
	default:
		return mcp.CallToolResult{}, &mcp.RPCError{Code: mcp.CodeServerBusy, Message: "too many tool calls are running; retry shortly"}
	}
	ctx, cancel := context.WithTimeout(ctx, e.svc.cfg.CallTimeout)
	defer cancel()
	return e.svc.run(ctx, tool, input), nil
}

func (e *Endpoint) find(name string) (mcp.WorkflowTool, bool) {
	for _, t := range e.tools {
		if t.Name == name {
			return t, true
		}
	}
	return mcp.WorkflowTool{}, false
}

// argumentsObject reads tools/call arguments: absent or null means {}; anything
// but a JSON object is refused.
func argumentsObject(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return map[string]any{}, nil
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, errors.New("arguments must be a JSON object")
	}
	return obj, nil
}

// run executes the tool's workflow and shapes the outcome as a tool result.
func (s *Service) run(ctx context.Context, tool mcp.WorkflowTool, input map[string]any) mcp.CallToolResult {
	wf, err := s.d.Workflows.Get(ctx, domain.WorkflowID(tool.WorkflowID))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return mcp.TextResult(fmt.Sprintf("the workflow behind tool %q does not exist", tool.Name), true)
		}
		s.d.Logger.Error("loading MCP tool workflow failed", "tool", tool.Name, "workflow", tool.WorkflowID, "error", err)
		return mcp.TextResult("the tool is unavailable", true)
	}
	start, ok := s.startNode(wf)
	if !ok {
		return mcp.TextResult(fmt.Sprintf("the workflow behind tool %q does not start with an Execute Workflow Trigger node", tool.Name), true)
	}
	ex, err := s.d.Executions.RunSync(ctx, execution.StartRequest{
		WorkflowID: wf.ID, Mode: domain.ModeWebhook, StartNode: start, Items: []domain.Item{{JSON: input}},
	})
	switch {
	case errors.Is(err, execution.ErrSyncTimeout), errors.Is(err, context.DeadlineExceeded):
		return mcp.TextResult(fmt.Sprintf("tool %q did not finish in time", tool.Name), true)
	case errors.Is(err, context.Canceled):
		return mcp.TextResult("the call was cancelled", true)
	case err != nil:
		s.d.Logger.Error("running MCP tool workflow failed", "tool", tool.Name, "workflow", tool.WorkflowID, "error", err)
		return mcp.TextResult(fmt.Sprintf("tool %q could not be run", tool.Name), true)
	}
	return s.result(tool, ex)
}

func (s *Service) result(tool mcp.WorkflowTool, ex domain.Execution) mcp.CallToolResult {
	switch ex.Status {
	case domain.StatusSuccess:
	case domain.StatusWaiting:
		return mcp.TextResult(fmt.Sprintf("tool %q is waiting and has no result yet (execution %s)", tool.Name, ex.ID), true)
	default:
		msg := "the workflow failed"
		if ex.Error != nil && ex.Error.Message != "" {
			msg += ": " + ex.Error.Message
		}
		return mcp.TextResult(msg, true)
	}
	items := ex.LastOutput()
	var payload any
	switch len(items) {
	case 1:
		payload = items[0].JSON
	default:
		list := make([]map[string]any, len(items))
		for i, it := range items {
			list[i] = it.JSON
		}
		payload = list
	}
	b, err := json.Marshal(payload)
	if err != nil {
		s.d.Logger.Error("encoding MCP tool result failed", "tool", tool.Name, "error", err)
		return mcp.TextResult("the workflow's output is not JSON-encodable", true)
	}
	return mcp.TextResult(string(b), false)
}

// startNode is the first enabled Execute Workflow Trigger in canvas order.
func (s *Service) startNode(wf domain.Workflow) (string, bool) {
	for _, name := range wf.NodesInCanvasOrder() {
		if n, ok := wf.NodeByName(name); ok && n.Type == s.d.SubworkflowTrigger && !n.Disabled {
			return name, true
		}
	}
	return "", false
}
