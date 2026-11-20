package nodesdk

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// Node is an integration step. Implementations must be safe to construct per
// run and must not keep per-execution state between Execute calls.
type Node interface {
	Describe() Descriptor
	Execute(ctx context.Context, in Input) (Output, error)
}

// Value is a resolved parameter value: string, float64/int64, bool, nil,
// []any or map[string]any.
type Value = any

// Input gives a node its items and lazy, per-item access to parameters
// (expressions are resolved there).
type Input interface {
	// Items returns the items of the given input port. The slice and the items
	// belong to the node; modifying them does not affect other nodes.
	// port counts main inputs only; sub-ports are reached through SubPort.
	Items(port int) []Item
	// Param resolves a parameter for one item. Expressions are evaluated
	// against that item. Missing parameters fall back to the descriptor
	// default; a required parameter without a value yields ErrParamMissing.
	Param(ctx context.Context, name string, itemIndex int) (Value, error)
	// EvalExpression evaluates a JavaScript expression (the text inside {{ }})
	// in the same scope as a parameter of the given item: $json, $("Node"),
	// $node, $vars, $env, $fn and the rest, with the same sandbox limits. It
	// is for nodes that run user code and need that code to see the workflow
	// scope. The result is a plain JSON-like Value.
	EvalExpression(ctx context.Context, expression string, itemIndex int) (Value, error)
	// Credential returns the decrypted credential bound under name.
	Credential(ctx context.Context, name string) (Credential, error)
	// SubPort pulls the values supplied by the sub-nodes connected to the
	// parent's sub-port input of the given kind, in connection order. See
	// Supplier. itemIndex picks the parent item the sub-nodes' parameters and
	// expressions are resolved against (use 0 when not working per item).
	// Sub-nodes that are disabled are skipped. It fails when kind is not a
	// sub-port input of this node, when a Required port has no enabled
	// sub-node, or when a sub-node fails (the error names the sub-node).
	// Every call supplies afresh; the parent should keep what it needs.
	SubPort(ctx context.Context, kind PortKind, itemIndex int) ([]Supplied, error)
	Blob() BlobAccess
	// HTTP returns the shared client (timeouts, proxy and TLS from config).
	HTTP() HTTPClient
	Logger() *slog.Logger
	ExecutionInfo() ExecutionInfo
}

// Credential is a decrypted credential. Never log it.
type Credential struct {
	Type string
	// ID identifies the stored credential and Version changes whenever it is
	// edited. Together they key caches of expensive resources built from the
	// credential, such as database connection pools.
	ID      string
	Version int64
	Data    map[string]any
	// Auth is the type's authenticate declaration, nil if it has none.
	Auth *AuthSpec
}

// BlobAccess stores and reads binary data.
type BlobAccess interface {
	Put(ctx context.Context, name, mime string, r io.Reader) (BinaryRef, error)
	Open(ctx context.Context, ref BinaryRef) (io.ReadCloser, error)
}

// HTTPClient is the shared outbound HTTP client.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// ExecutionInfo describes the run the node is part of.
type ExecutionInfo struct {
	ExecutionID  string
	WorkflowID   string
	WorkflowName string
	Mode         string
	NodeName     string
	// RunIndex is the zero-based run number of this node in the execution.
	RunIndex int
	// Attempt is the one-based attempt number, above one when retrying.
	Attempt int
	// Depth is the sub-workflow nesting level of the execution (0 = top level).
	Depth int
}

// Output is what a node returns.
type Output struct {
	// Ports has one item slice per output port. An empty slice stops that branch.
	Ports [][]Item
	// Wait, when set, pauses the execution. Ports are delivered on resume.
	Wait *Wait
	// Response, when set, is the HTTP answer to the webhook call that started
	// the execution (Respond to Webhook). The host delivers the first one and
	// ignores the rest; outside webhook runs it is ignored.
	Response *WebhookResponse
}

// Wait asks the engine to pause the execution. At least one field must be set;
// if both are, whichever happens first resumes the execution.
type Wait struct {
	// Until resumes the execution at that time.
	Until time.Time
	// ResumeToken resumes the execution when a resume webhook presents it. Build
	// it with NewResumeToken: the host finds the execution from its prefix.
	ResumeToken string
}

// ResumeTokenSeparator joins the execution id and the secret of a resume token.
const ResumeTokenSeparator = "."

// NewResumeToken builds a resume token for the execution: "<executionID>.<secret>".
// The secret must be unguessable when the token is handed to outside parties.
func NewResumeToken(executionID, secret string) string {
	return executionID + ResumeTokenSeparator + secret
}

// ResumeSecretBytes is the size of the random secret of an execution (256 bits).
const ResumeSecretBytes = 32

// NewResumeSecret draws a resume secret from random (crypto/rand in production).
// The result is URL-safe, so it can be a path segment of a resume URL.
func NewResumeSecret(random io.Reader) (string, error) {
	b := make([]byte, ResumeSecretBytes)
	if _, err := io.ReadFull(random, b); err != nil {
		return "", fmt.Errorf("generating resume secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// ResumeSecreter is an optional capability of an Input: the secret that the
// execution shares by all its resume tokens, so one resume URL resumes any Wait
// node of the execution. A node that asks for a resume token needs it.
type ResumeSecreter interface {
	ResumeSecret() string
}
