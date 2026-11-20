package nodesdk

import (
	"errors"
	"fmt"
)

// ErrParamMissing is returned by Input.Param for a required parameter that is
// neither set nor defaulted.
var ErrParamMissing = errors.New("required parameter is missing")

// NodeError is the structured error a node returns to give the engine and the
// user more detail than a plain error. Plain errors are accepted too.
type NodeError struct {
	Message     string
	Description string
	// HTTPStatus is the upstream HTTP status, if any.
	HTTPStatus int
	// ItemIndex is the failing input item, if known.
	ItemIndex *int
	Err       error
}

// Error implements error.
func (e *NodeError) Error() string {
	switch {
	case e.Message != "" && e.Err != nil:
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	case e.Message != "":
		return e.Message
	case e.Err != nil:
		return e.Err.Error()
	}
	return "node error"
}

// Unwrap returns the underlying cause.
func (e *NodeError) Unwrap() error { return e.Err }
