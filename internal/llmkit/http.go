// Package llmkit holds what the model-provider and embedding adapters share:
// HTTP error mapping, SSE and NDJSON readers, and port.LLM decorators (timeout,
// metrics). It sits outside internal/adapter so that adapters never import
// another adapter package.
package llmkit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Limits of what the adapters read or echo back.
const (
	// MaxErrorBodyBytes bounds how much of an error response is read.
	MaxErrorBodyBytes = 64 << 10
	// maxErrorMessageRunes bounds the provider text kept in an error.
	maxErrorMessageRunes = 500
	// maxLineBytes bounds one SSE or NDJSON line (tool-call arguments can be large).
	maxLineBytes = 16 << 20
)

// Send performs req through httpc and returns the response when its status is
// 2xx. Any other outcome is an *port.LLMError for provider (429, 408 and 5xx
// are retryable); a cancelled context returns the context's error.
func Send(ctx context.Context, httpc nodesdk.HTTPClient, provider string, req *http.Request) (*http.Response, error) {
	resp, err := httpc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("calling %s: %w", provider, ctx.Err())
		}
		return nil, &port.LLMError{Provider: provider, Retryable: true, Err: err}
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp, nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, MaxErrorBodyBytes))
	return nil, &port.LLMError{
		Provider: provider, Status: resp.StatusCode, Message: ErrorMessage(body),
		Retryable:  RetryableStatus(resp.StatusCode),
		RetryAfter: retryAfter(resp.Header.Get("Retry-After")),
	}
}

// RetryableStatus reports whether an HTTP status is worth retrying.
func RetryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusRequestTimeout || status >= http.StatusInternalServerError
}

func retryAfter(v string) time.Duration {
	if secs, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && secs >= 0 {
		return time.Duration(secs * float64(time.Second))
	}
	return 0
}

// ErrorMessage extracts the human text of a provider error body: the
// error.message, error (string) or message field, else the trimmed body.
func ErrorMessage(body []byte) string {
	var env struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	msg := strings.TrimSpace(string(body))
	if json.Unmarshal(body, &env) == nil {
		var inner struct {
			Message string `json:"message"`
		}
		var s string
		switch {
		case json.Unmarshal(env.Error, &inner) == nil && inner.Message != "":
			msg = inner.Message
		case json.Unmarshal(env.Error, &s) == nil && s != "":
			msg = s
		case env.Message != "":
			msg = env.Message
		}
	}
	if r := []rune(msg); len(r) > maxErrorMessageRunes {
		msg = string(r[:maxErrorMessageRunes]) + "..."
	}
	return msg
}

// JSONRequest builds a POST with a JSON body.
func JSONRequest(ctx context.Context, url string, body any) (*http.Request, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encoding request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

// DecodeJSON reads and decodes a JSON response body, wrapping failures as a
// non-retryable *port.LLMError.
func DecodeJSON(provider string, r io.Reader, v any) error {
	if err := json.NewDecoder(r).Decode(v); err != nil {
		return &port.LLMError{Provider: provider, Message: "decoding response", Err: err}
	}
	return nil
}

// NormalizeArgs returns raw as a JSON object, "{}" when it is empty or null.
func NormalizeArgs(raw json.RawMessage) json.RawMessage {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || bytes.Equal(t, []byte("null")) {
		return json.RawMessage("{}")
	}
	return json.RawMessage(t)
}

// ReadSSE parses a Server-Sent Events stream, calling fn for each event with
// its name (empty when unnamed) and data (multi-line data joined by newline).
func ReadSSE(r io.Reader, fn func(event, data string) error) error {
	br := newLineReader(r)
	var event string
	var data []string
	flush := func() error {
		if len(data) == 0 {
			event = ""
			return nil
		}
		err := fn(event, strings.Join(data, "\n"))
		event, data = "", nil
		return err
	}
	for {
		line, err := br.next()
		if line != "" {
			switch {
			case strings.HasPrefix(line, ":"):
			case strings.HasPrefix(line, "event:"):
				event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			}
		} else if ferr := flush(); ferr != nil {
			return ferr
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return flush()
			}
			return fmt.Errorf("reading stream: %w", err)
		}
	}
}

// ReadNDJSON calls fn with each non-empty line of a newline-delimited stream.
func ReadNDJSON(r io.Reader, fn func(line []byte) error) error {
	br := newLineReader(r)
	for {
		line, err := br.next()
		if line = strings.TrimSpace(line); line != "" {
			if ferr := fn([]byte(line)); ferr != nil {
				return ferr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("reading stream: %w", err)
		}
	}
}
