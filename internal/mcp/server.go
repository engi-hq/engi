package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ToolHandler is what a Server serves: the tools it lists and runs. An error
// from CallTool that is (or wraps) an *RPCError is sent as that JSON-RPC error;
// any other error becomes an internal error. A tool that ran and failed should
// return a result with IsError set instead.
type ToolHandler interface {
	ListTools(ctx context.Context) ([]ToolInfo, error)
	CallTool(ctx context.Context, name string, args json.RawMessage) (CallToolResult, error)
}

// ErrUnknownTool is returned by a ToolHandler for a tool name it does not
// serve; the Server maps it to an invalid-params error, as the spec asks.
var ErrUnknownTool = errors.New("unknown tool")

// Server answers MCP JSON-RPC messages for one ToolHandler. It is stateless
// (it issues no session ids), so it is safe for concurrent use and any request
// may be the first.
type Server struct {
	info    Implementation
	handler ToolHandler
}

// NewServer builds a Server that identifies itself as info.
func NewServer(info Implementation, h ToolHandler) *Server {
	return &Server{info: info, handler: h}
}

// Reply is the transport-neutral outcome of handling one HTTP body.
type Reply struct {
	// Body is the JSON to send; empty when the input needs no response.
	Body []byte
	// Accepted is true when the input held only notifications and responses:
	// answer 202 with no body.
	Accepted bool
	// Invalid is true when the input was not valid JSON-RPC (answer 400).
	Invalid bool
}

// Handle processes one POST body: a single message or a batch. Notifications
// and responses get no reply.
func (s *Server) Handle(ctx context.Context, raw []byte) Reply {
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '[' {
		return s.handleBatch(ctx, raw)
	}
	var m Message
	if err := json.Unmarshal(raw, &m); err != nil {
		return invalid(errorResponse(nil, CodeParseError, "invalid JSON"))
	}
	resp, ok := s.dispatch(ctx, m)
	if !ok {
		return Reply{Accepted: true}
	}
	body, err := json.Marshal(resp)
	if err != nil {
		return invalid(errorResponse(m.ID, CodeInternalError, "encoding the response failed"))
	}
	return Reply{Body: body}
}

func (s *Server) handleBatch(ctx context.Context, raw []byte) Reply {
	var msgs []Message
	if err := json.Unmarshal(raw, &msgs); err != nil {
		return invalid(errorResponse(nil, CodeParseError, "invalid JSON"))
	}
	if len(msgs) == 0 {
		return invalid(errorResponse(nil, CodeInvalidRequest, "empty batch"))
	}
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		if resp, ok := s.dispatch(ctx, m); ok {
			out = append(out, resp)
		}
	}
	if len(out) == 0 {
		return Reply{Accepted: true}
	}
	body, err := json.Marshal(out)
	if err != nil {
		return invalid(errorResponse(nil, CodeInternalError, "encoding the response failed"))
	}
	return Reply{Body: body}
}

func invalid(m Message) Reply {
	body, _ := json.Marshal(m) // a message of plain strings always encodes
	return Reply{Body: body, Invalid: true}
}

// dispatch answers one message; ok is false when none is due.
func (s *Server) dispatch(ctx context.Context, m Message) (Message, bool) {
	if m.IsNotification() || m.IsResponse() {
		return Message{}, false
	}
	if !m.IsRequest() || m.JSONRPC != JSONRPCVersion {
		return errorResponse(m.ID, CodeInvalidRequest, "not a JSON-RPC 2.0 request"), true
	}
	result, err := s.route(ctx, m)
	if err != nil {
		var rpc *RPCError
		if errors.As(err, &rpc) {
			return Message{JSONRPC: JSONRPCVersion, ID: m.ID, Error: rpc}, true
		}
		return errorResponse(m.ID, CodeInternalError, "internal error"), true
	}
	resp, err := resultResponse(m.ID, result)
	if err != nil {
		return errorResponse(m.ID, CodeInternalError, "encoding the result failed"), true
	}
	return resp, true
}

func (s *Server) route(ctx context.Context, m Message) (any, error) {
	switch m.Method {
	case MethodInitialize:
		var p initializeParams
		if err := json.Unmarshal(m.Params, &p); err != nil || p.ProtocolVersion == "" {
			return nil, &RPCError{Code: CodeInvalidParams, Message: "initialize needs protocolVersion"}
		}
		return InitializeResult{
			ProtocolVersion: negotiate(p.ProtocolVersion),
			Capabilities:    map[string]any{"tools": map[string]any{"listChanged": false}},
			ServerInfo:      s.info,
		}, nil
	case MethodPing:
		return struct{}{}, nil
	case MethodToolsList:
		tools, err := s.handler.ListTools(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing tools: %w", err)
		}
		if tools == nil {
			tools = []ToolInfo{}
		}
		return listToolsResult{Tools: tools}, nil
	case MethodToolsCall:
		return s.callTool(ctx, m.Params)
	default:
		return nil, &RPCError{Code: CodeMethodNotFound, Message: "method not found: " + m.Method}
	}
}

func (s *Server) callTool(ctx context.Context, params json.RawMessage) (any, error) {
	var p callToolParams
	if err := json.Unmarshal(params, &p); err != nil || p.Name == "" {
		return nil, &RPCError{Code: CodeInvalidParams, Message: "tools/call needs a tool name"}
	}
	res, err := s.handler.CallTool(ctx, p.Name, p.Arguments)
	switch {
	case errors.Is(err, ErrUnknownTool):
		return nil, &RPCError{Code: CodeInvalidParams, Message: "unknown tool: " + p.Name}
	case err != nil:
		return nil, fmt.Errorf("calling tool %q: %w", p.Name, err)
	}
	if res.Content == nil {
		res.Content = []Content{}
	}
	return res, nil
}
