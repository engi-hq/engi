package pluginproto

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/engi-hq/engi/pkg/nodesdk"
)

// JSON-RPC error codes: the standard ones and Engi's (docs/15 section 15.3).
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603

	// CodeNodeError is a failure shown to the user like a built-in node's
	// error; Data is a NodeErrorData.
	CodeNodeError = -32001
	// CodeCancelled answers a request that was cancelled.
	CodeCancelled = -32002
	// CodeUnsupported answers a method the peer or transport does not offer.
	CodeUnsupported = -32003
	// CodeBusy answers a request above the peer's concurrency cap.
	CodeBusy = -32004
)

// Error is a JSON-RPC error object.
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Error implements error.
func (e *Error) Error() string { return fmt.Sprintf("%s (code %d)", e.Message, e.Code) }

// NodeErrorData is the data of a CodeNodeError error.
type NodeErrorData struct {
	Description string `json:"description,omitempty"`
	HTTPStatus  int    `json:"httpStatus,omitempty"`
	ItemIndex   *int   `json:"itemIndex,omitempty"`
}

// ErrorFrom converts err to a wire error: an *Error is kept, a
// *nodesdk.NodeError becomes CodeNodeError, a cancelled context becomes
// CodeCancelled and anything else CodeInternalError.
func ErrorFrom(err error) *Error {
	var we *Error
	if errors.As(err, &we) {
		return we
	}
	var ne *nodesdk.NodeError
	if errors.As(err, &ne) {
		data, _ := json.Marshal(NodeErrorData{Description: ne.Description, HTTPStatus: ne.HTTPStatus, ItemIndex: ne.ItemIndex})
		return &Error{Code: CodeNodeError, Message: ne.Error(), Data: data}
	}
	if errors.Is(err, context.Canceled) {
		return &Error{Code: CodeCancelled, Message: err.Error()}
	}
	return &Error{Code: CodeInternalError, Message: err.Error()}
}

// NodeError converts a wire error to the error a node returns: CodeNodeError
// becomes a *nodesdk.NodeError carrying its data, anything else a NodeError
// with the message.
func (e *Error) NodeError() *nodesdk.NodeError {
	ne := &nodesdk.NodeError{Message: e.Message, Err: e}
	if e.Code == CodeNodeError {
		var d NodeErrorData
		if json.Unmarshal(e.Data, &d) == nil {
			ne.Description, ne.HTTPStatus, ne.ItemIndex = d.Description, d.HTTPStatus, d.ItemIndex
		}
		ne.Err = nil
	}
	return ne
}

// Unsupported returns a CodeUnsupported error naming what is missing.
func Unsupported(what string) *Error {
	return &Error{Code: CodeUnsupported, Message: what + " is not supported over this transport"}
}
