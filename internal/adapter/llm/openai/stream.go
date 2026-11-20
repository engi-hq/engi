package openai

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/engi-hq/engi/internal/llmkit"
	"github.com/engi-hq/engi/internal/port"
)

type partialCall struct {
	id, name string
	args     strings.Builder
}

// Stream implements port.LLM over the SSE stream of chat completions.
func (c *Client) Stream(ctx context.Context, req port.LLMRequest, emit func(port.StreamEvent) error) (port.LLMResponse, error) {
	body, err := c.buildBody(req, true)
	if err != nil {
		return port.LLMResponse{}, err
	}
	resp, err := c.send(ctx, req.Model, body)
	if err != nil {
		return port.LLMResponse{}, err
	}
	defer resp.Body.Close()

	var (
		out    = port.LLMResponse{StopReason: port.StopOther}
		text   strings.Builder
		calls  = map[int]*partialCall{}
		finish string
		done   bool
	)
	err = llmkit.ReadSSE(resp.Body, func(_, data string) error {
		if strings.TrimSpace(data) == "[DONE]" {
			done = true
			return nil
		}
		var ev struct {
			Model   string `json:"model"`
			Choices []struct {
				Delta struct {
					Content   string     `json:"content"`
					ToolCalls []wireCall `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *wireUsage `json:"usage"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return &port.LLMError{Provider: c.provider, Message: "malformed stream event", Err: err}
		}
		if ev.Error != nil {
			return &port.LLMError{Provider: c.provider, Message: ev.Error.Message}
		}
		if ev.Model != "" {
			out.Model = ev.Model
		}
		if ev.Usage != nil {
			out.Usage = ev.Usage.toUsage()
		}
		if len(ev.Choices) == 0 {
			return nil
		}
		ch := ev.Choices[0]
		for _, tc := range ch.Delta.ToolCalls {
			p := calls[tc.Index]
			if p == nil {
				p = &partialCall{}
				calls[tc.Index] = p
			}
			if tc.ID != "" {
				p.id = tc.ID
			}
			if tc.Function.Name != "" {
				p.name = tc.Function.Name
			}
			p.args.WriteString(tc.Function.Arguments)
		}
		if ch.FinishReason != "" {
			finish = ch.FinishReason
		}
		if ch.Delta.Content != "" {
			text.WriteString(ch.Delta.Content)
			return emit(port.StreamEvent{TextDelta: ch.Delta.Content})
		}
		return nil
	})
	if err != nil {
		return port.LLMResponse{}, err
	}
	if !done && finish == "" {
		return port.LLMResponse{}, &port.LLMError{Provider: c.provider, Message: "stream ended unexpectedly", Retryable: true}
	}
	out.Content = text.String()
	for i := 0; i < len(calls); i++ {
		p, ok := calls[i]
		if !ok {
			continue
		}
		out.ToolCalls = append(out.ToolCalls, port.LLMToolCall{ID: p.id, Name: p.name, Arguments: llmkit.NormalizeArgs(json.RawMessage(p.args.String()))})
	}
	out.StopReason = mapFinish(finish, len(out.ToolCalls) > 0)
	return out, nil
}
