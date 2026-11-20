package anthropic

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/engi-hq/engi/internal/llmkit"
	"github.com/engi-hq/engi/internal/port"
)

type streamBlock struct {
	kind  string // text or tool_use
	id    string
	name  string
	input strings.Builder
}

// Stream implements port.LLM over the SSE stream of the Messages API.
func (c *Client) Stream(ctx context.Context, req port.LLMRequest, emit func(port.StreamEvent) error) (port.LLMResponse, error) {
	body, forced, err := c.buildBody(req, true)
	if err != nil {
		return port.LLMResponse{}, err
	}
	resp, err := c.send(ctx, body)
	if err != nil {
		return port.LLMResponse{}, err
	}
	defer resp.Body.Close()

	var (
		out    port.LLMResponse
		text   strings.Builder
		blocks = map[int]*streamBlock{}
		order  []int
		stop   string
		in     usage
		done   bool
	)
	send := func(s string) error {
		if s == "" {
			return nil
		}
		text.WriteString(s)
		return emit(port.StreamEvent{TextDelta: s})
	}
	err = llmkit.ReadSSE(resp.Body, func(event, data string) error {
		var ev struct {
			Type    string `json:"type"`
			Index   int    `json:"index"`
			Message struct {
				Model string `json:"model"`
				Usage usage  `json:"usage"`
			} `json:"message"`
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
				Text string `json:"text"`
			} `json:"content_block"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Usage usage `json:"usage"`
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return errorf("malformed stream event: %v", err)
		}
		switch ev.Type {
		case "message_start":
			out.Model = ev.Message.Model
			in = ev.Message.Usage
		case "content_block_start":
			b := &streamBlock{kind: ev.ContentBlock.Type, id: ev.ContentBlock.ID, name: ev.ContentBlock.Name}
			blocks[ev.Index] = b
			order = append(order, ev.Index)
			if b.kind == "text" {
				return send(ev.ContentBlock.Text)
			}
		case "content_block_delta":
			b := blocks[ev.Index]
			if b == nil {
				return nil
			}
			switch ev.Delta.Type {
			case "text_delta":
				return send(ev.Delta.Text)
			case "input_json_delta":
				b.input.WriteString(ev.Delta.PartialJSON)
				if forced != "" && b.name == forced {
					return send(ev.Delta.PartialJSON)
				}
			}
		case "message_delta":
			if ev.Delta.StopReason != "" {
				stop = ev.Delta.StopReason
			}
			if ev.Usage.OutputTokens > 0 {
				in.OutputTokens = ev.Usage.OutputTokens
			}
		case "message_stop":
			done = true
		case "error":
			return &port.LLMError{Provider: port.LLMProviderAnthropic, Message: ev.Error.Type + ": " + ev.Error.Message,
				Retryable: ev.Error.Type == "overloaded_error" || ev.Error.Type == "api_error" || ev.Error.Type == "rate_limit_error"}
		}
		return nil
	})
	if err != nil {
		return port.LLMResponse{}, err
	}
	if !done {
		return port.LLMResponse{}, &port.LLMError{Provider: port.LLMProviderAnthropic, Message: "stream ended before message_stop", Retryable: true}
	}
	out.Content = text.String()
	out.StopReason = mapStop(stop)
	out.Usage = port.LLMUsage{InputTokens: in.input(), OutputTokens: in.OutputTokens}
	for _, i := range order {
		b := blocks[i]
		if b.kind != "tool_use" || (forced != "" && b.name == forced) {
			continue
		}
		out.ToolCalls = append(out.ToolCalls, port.LLMToolCall{ID: b.id, Name: b.name, Arguments: llmkit.NormalizeArgs(json.RawMessage(b.input.String()))})
	}
	if forced != "" && len(out.ToolCalls) == 0 && out.StopReason == port.StopToolUse {
		out.StopReason = port.StopEnd
	}
	return out, nil
}
