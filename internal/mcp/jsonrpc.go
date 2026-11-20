package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// JSONRPCVersion is the only JSON-RPC version MCP uses.
const JSONRPCVersion = "2.0"

// Standard JSON-RPC error codes.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
	// CodeServerBusy is the implementation-defined code (-32000..-32099) of a
	// server that refuses a call because it is at its concurrency limit.
	CodeServerBusy = -32000
)

// Message is any JSON-RPC message: a request (ID and Method), a notification
// (Method only) or a response (ID and Result or Error).
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// IsRequest reports whether m expects a response.
func (m Message) IsRequest() bool { return m.Method != "" && hasID(m.ID) }

// IsNotification reports whether m is a request without an id.
func (m Message) IsNotification() bool { return m.Method != "" && !hasID(m.ID) }

// IsResponse reports whether m answers a request.
func (m Message) IsResponse() bool {
	return m.Method == "" && hasID(m.ID) && (m.Error != nil || m.Result != nil)
}

func hasID(id json.RawMessage) bool {
	return len(id) > 0 && !bytes.Equal(bytes.TrimSpace(id), []byte("null"))
}

// IDKey is the id as a comparable string, for matching responses to requests.
func (m Message) IDKey() string { return string(bytes.TrimSpace(m.ID)) }

// RPCError is a JSON-RPC error object. It implements error so a tool handler
// can return one to choose the error code of the reply.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Error implements error.
func (e *RPCError) Error() string { return fmt.Sprintf("MCP error %d: %s", e.Code, e.Message) }

// newRequest builds a request with a numeric id.
func newRequest(id int64, method string, params any) (Message, error) {
	m := Message{JSONRPC: JSONRPCVersion, ID: json.RawMessage(fmt.Sprint(id)), Method: method}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return Message{}, fmt.Errorf("encoding %s params: %w", method, err)
		}
		m.Params = raw
	}
	return m, nil
}

func newNotification(method string) Message {
	return Message{JSONRPC: JSONRPCVersion, Method: method}
}

// errorResponse builds the reply to a failed request; id may be nil (null).
func errorResponse(id json.RawMessage, code int, msg string) Message {
	if !hasID(id) {
		id = json.RawMessage("null")
	}
	return Message{JSONRPC: JSONRPCVersion, ID: id, Error: &RPCError{Code: code, Message: msg}}
}

// resultResponse builds the reply to a successful request.
func resultResponse(id json.RawMessage, result any) (Message, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return Message{}, fmt.Errorf("encoding result: %w", err)
	}
	return Message{JSONRPC: JSONRPCVersion, ID: id, Result: raw}, nil
}
