package jsoncodec

import (
	"encoding/json"
	"fmt"

	"github.com/engi-hq/engi/internal/port"
)

// storedToolCall is the stored shape of port.LLMToolCall.
type storedToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// EncodeToolCalls renders an assistant message's tool calls for storage. No
// calls give the empty string.
func EncodeToolCalls(calls []port.LLMToolCall) (string, error) {
	if len(calls) == 0 {
		return "", nil
	}
	out := make([]storedToolCall, len(calls))
	for i, c := range calls {
		args := c.Arguments
		if len(args) == 0 {
			args = json.RawMessage("{}")
		}
		out[i] = storedToolCall{ID: c.ID, Name: c.Name, Arguments: args}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("encoding tool calls: %w", err)
	}
	return string(b), nil
}

// DecodeToolCalls is the inverse of EncodeToolCalls.
func DecodeToolCalls(s string) ([]port.LLMToolCall, error) {
	if s == "" {
		return nil, nil
	}
	var in []storedToolCall
	if err := json.Unmarshal([]byte(s), &in); err != nil {
		return nil, fmt.Errorf("decoding tool calls: %w", err)
	}
	out := make([]port.LLMToolCall, len(in))
	for i, c := range in {
		out[i] = port.LLMToolCall{ID: c.ID, Name: c.Name, Arguments: c.Arguments}
	}
	return out, nil
}
