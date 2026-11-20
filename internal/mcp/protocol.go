package mcp

import (
	"encoding/json"
	"slices"
)

// MCP method names.
const (
	MethodInitialize  = "initialize"
	MethodInitialized = "notifications/initialized"
	MethodPing        = "ping"
	MethodToolsList   = "tools/list"
	MethodToolsCall   = "tools/call"
)

// Protocol revisions Engi speaks, newest first. A client offers the first; a
// server answers with the client's version when it is in this list and with the
// first otherwise (the spec's negotiation rule).
var supportedVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

// LatestVersion is the newest protocol revision Engi implements.
func LatestVersion() string { return supportedVersions[0] }

// SupportedVersion reports whether Engi speaks the given protocol revision.
func SupportedVersion(v string) bool { return slices.Contains(supportedVersions, v) }

// negotiate picks the protocol version a server answers an initialize with.
func negotiate(requested string) string {
	if SupportedVersion(requested) {
		return requested
	}
	return LatestVersion()
}

// HTTP header names of the Streamable HTTP transport.
const (
	HeaderSessionID       = "Mcp-Session-Id"
	HeaderProtocolVersion = "MCP-Protocol-Version"
)

// Implementation names an MCP client or server.
type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type initializeParams struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ClientInfo      Implementation `json:"clientInfo"`
}

// InitializeResult is the server's answer to initialize.
type InitializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ServerInfo      Implementation `json:"serverInfo"`
	Instructions    string         `json:"instructions,omitempty"`
}

// ToolInfo describes one tool of an MCP server.
type ToolInfo struct {
	Name        string          `json:"name"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type listToolsParams struct {
	Cursor string `json:"cursor,omitempty"`
}

type listToolsResult struct {
	Tools      []ToolInfo `json:"tools"`
	NextCursor string     `json:"nextCursor,omitempty"`
}

type callToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// Content is one part of a tool result. Only the fields of the part's Type are
// set: text for "text", data and mimeType for "image" and "audio", uri for
// "resource_link", resource for "resource".
type Content struct {
	Type     string           `json:"type"`
	Text     string           `json:"text,omitempty"`
	Data     string           `json:"data,omitempty"`
	MimeType string           `json:"mimeType,omitempty"`
	URI      string           `json:"uri,omitempty"`
	Name     string           `json:"name,omitempty"`
	Resource *EmbeddedContent `json:"resource,omitempty"`
}

// EmbeddedContent is the resource of a "resource" content part.
type EmbeddedContent struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
}

// CallToolResult is the result of tools/call. IsError marks a tool that ran and
// failed; protocol problems are JSON-RPC errors instead.
type CallToolResult struct {
	Content           []Content       `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
	IsError           bool            `json:"isError,omitempty"`
}

// TextResult builds a result with one text part.
func TextResult(text string, isError bool) CallToolResult {
	return CallToolResult{Content: []Content{{Type: "text", Text: text}}, IsError: isError}
}
