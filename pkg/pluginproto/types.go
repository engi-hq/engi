package pluginproto

import (
	"encoding/json"
	"time"
)

// ProtocolVersion is the Engi Plugin Protocol version this package speaks.
const ProtocolVersion = 1

// Methods sent by the host to a plugin.
const (
	MethodInitialize = "initialize"
	MethodExecute    = "execute"
	MethodHealth     = "health"
	MethodShutdown   = "shutdown"
	// MethodCancel tells the peer that the sender gave up on a request. Either
	// side may send it.
	MethodCancel = "$/cancel"
)

// Methods sent by a plugin to the host (stdio only).
const (
	MethodLog            = "log"
	MethodBlobRead       = "blob.read"
	MethodBlobWrite      = "blob.write"
	MethodEvalExpression = "expression.eval"
)

// Log levels of LogParams.
const (
	LogDebug = "debug"
	LogInfo  = "info"
	LogWarn  = "warn"
	LogError = "error"
)

// CancelParams names the request the sender gave up on.
type CancelParams struct {
	ID json.RawMessage `json:"id"`
}

// Info names a host or a plugin.
type Info struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializeParams opens a session.
type InitializeParams struct {
	ProtocolVersion int  `json:"protocolVersion"`
	Host            Info `json:"host"`
}

// InitializeResult describes the plugin and its node types.
type InitializeResult struct {
	ProtocolVersion int          `json:"protocolVersion"`
	Plugin          Info         `json:"plugin"`
	Nodes           []Descriptor `json:"nodes"`
}

// NodeRef names a node type and version.
type NodeRef struct {
	Type    string `json:"type"`
	Version int    `json:"version"`
}

// ExecuteParams runs one node. See docs/15 section 15.3.
type ExecuteParams struct {
	// CallID is chosen by the host; callbacks carry it to name their call.
	CallID string  `json:"callId"`
	Node   NodeRef `json:"node"`
	// Inputs has one item slice per main input port.
	Inputs [][]Item `json:"inputs"`
	// Params holds every property resolved against item i of input 0; it has
	// at least one entry.
	Params []map[string]any `json:"params"`
	// ParamErrors[i] maps a property that failed to resolve to the message.
	ParamErrors []map[string]string `json:"paramErrors,omitempty"`
	// Credentials holds the declared and bound credentials, decrypted.
	Credentials map[string]Credential `json:"credentials,omitempty"`
	// CredentialErrors maps a declared credential that could not be loaded to
	// the message.
	CredentialErrors map[string]string `json:"credentialErrors,omitempty"`
	Execution        ExecutionInfo     `json:"execution"`
}

// ExecuteResult is a node's output.
type ExecuteResult struct {
	// Outputs has one item slice per output port.
	Outputs [][]Item `json:"outputs"`
	Wait    *Wait    `json:"wait,omitempty"`
}

// Wait pauses the execution, like nodesdk.Wait.
type Wait struct {
	Until       *time.Time `json:"until,omitempty"`
	ResumeToken string     `json:"resumeToken,omitempty"`
}

// Item is the wire form of nodesdk.Item.
type Item struct {
	JSON       map[string]any       `json:"json"`
	Binary     map[string]BinaryRef `json:"binary,omitempty"`
	PairedItem []PairedItem         `json:"pairedItem,omitempty"`
	Error      *ItemError           `json:"error,omitempty"`
}

// BinaryRef is the wire form of nodesdk.BinaryRef.
type BinaryRef struct {
	Ref  string `json:"ref"`
	MIME string `json:"mime,omitempty"`
	Name string `json:"name,omitempty"`
	Size int64  `json:"size,omitempty"`
}

// PairedItem is the wire form of nodesdk.PairedItem.
type PairedItem struct {
	Input int `json:"input"`
	Item  int `json:"item"`
}

// ItemError is the wire form of nodesdk.ItemError.
type ItemError struct {
	Node        string `json:"node,omitempty"`
	Message     string `json:"message"`
	Description string `json:"description,omitempty"`
	HTTPStatus  int    `json:"httpStatus,omitempty"`
}

// Credential is a decrypted credential for one call. Never log it.
type Credential struct {
	Type    string         `json:"type"`
	ID      string         `json:"id"`
	Version int64          `json:"version"`
	Data    map[string]any `json:"data"`
}

// ExecutionInfo is the wire form of nodesdk.ExecutionInfo.
type ExecutionInfo struct {
	ExecutionID  string `json:"executionId"`
	WorkflowID   string `json:"workflowId"`
	WorkflowName string `json:"workflowName"`
	Mode         string `json:"mode"`
	NodeName     string `json:"nodeName"`
	RunIndex     int    `json:"runIndex"`
	Attempt      int    `json:"attempt"`
	Depth        int    `json:"depth"`
}

// LogParams is a log line from the plugin. CallID, when set, is the callId of
// the execute call the line was logged in; the host logs it with that call's
// node logger. Without it the line goes to the server log.
type LogParams struct {
	Level   string         `json:"level"`
	Message string         `json:"message"`
	Attrs   map[string]any `json:"attrs,omitempty"`
	CallID  string         `json:"callId,omitempty"`
}

// BlobReadParams asks for the bytes of a blob.
type BlobReadParams struct {
	CallID string    `json:"callId"`
	Ref    BinaryRef `json:"ref"`
}

// BlobReadResult carries the bytes (base64 in JSON).
type BlobReadResult struct {
	Data []byte `json:"data"`
}

// BlobWriteParams stores bytes as a blob; the result is a BinaryRef.
type BlobWriteParams struct {
	CallID string `json:"callId"`
	Name   string `json:"name"`
	MIME   string `json:"mime"`
	Data   []byte `json:"data"`
}

// EvalParams evaluates an expression in the scope of a parameter of item
// ItemIndex (the text inside {{ }}).
type EvalParams struct {
	CallID     string `json:"callId"`
	Expression string `json:"expression"`
	ItemIndex  int    `json:"itemIndex"`
}

// EvalResult is the value of an expression.
type EvalResult struct {
	Value any `json:"value"`
}

// Descriptor is the wire form of nodesdk.Descriptor.
type Descriptor struct {
	Type        string          `json:"type"`
	Version     int             `json:"version"`
	DisplayName string          `json:"displayName"`
	Description string          `json:"description,omitempty"`
	Group       string          `json:"group"`
	Icon        string          `json:"icon,omitempty"`
	Inputs      []PortSpec      `json:"inputs,omitempty"`
	Outputs     []PortSpec      `json:"outputs,omitempty"`
	Properties  []Property      `json:"properties,omitempty"`
	Credentials []CredentialRef `json:"credentials,omitempty"`
	MergePolicy string          `json:"mergePolicy,omitempty"`
}

// PortSpec is the wire form of nodesdk.PortSpec.
type PortSpec struct {
	Name           string `json:"name"`
	Kind           string `json:"kind,omitempty"`
	DisplayName    string `json:"displayName,omitempty"`
	Required       bool   `json:"required,omitempty"`
	MaxConnections int    `json:"maxConnections,omitempty"`
}

// CredentialRef is the wire form of nodesdk.CredentialRef.
type CredentialRef struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required,omitempty"`
}

// Property is the wire form of nodesdk.Property.
type Property struct {
	Name           string          `json:"name"`
	DisplayName    string          `json:"displayName,omitempty"`
	Type           string          `json:"type"`
	Default        any             `json:"default,omitempty"`
	Required       bool            `json:"required,omitempty"`
	Placeholder    string          `json:"placeholder,omitempty"`
	Description    string          `json:"description,omitempty"`
	Options        []Option        `json:"options,omitempty"`
	Children       []Property      `json:"children,omitempty"`
	DisplayOptions *DisplayOptions `json:"displayOptions,omitempty"`
}

// Option is the wire form of nodesdk.Option.
type Option struct {
	Name        string `json:"name"`
	Value       any    `json:"value"`
	Description string `json:"description,omitempty"`
}

// DisplayOptions is the wire form of nodesdk.DisplayOptions.
type DisplayOptions struct {
	Show map[string][]any `json:"show,omitempty"`
	Hide map[string][]any `json:"hide,omitempty"`
}
