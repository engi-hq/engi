package expression

import "errors"

// Sentinel causes carried inside *domain.ExpressionError.
var (
	// ErrTimeout means an evaluation exceeded the configured time limit.
	ErrTimeout = errors.New("expression evaluation timed out")
	// ErrTemplateTooLong means the template exceeds the configured length.
	ErrTemplateTooLong = errors.New("expression template is too long")
	// ErrOutputTooLarge means the result exceeds the configured size.
	ErrOutputTooLarge = errors.New("expression result is too large")
	// ErrNotSerializable means the result cannot be represented as JSON data.
	ErrNotSerializable = errors.New("expression result is not JSON-serializable")
)
