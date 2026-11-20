package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
)

// detailBytes is how much of an error body an HTTPStatusError keeps.
const detailBytes = 256

// streamable is the Streamable HTTP transport: every message is a POST to the
// endpoint; the reply is JSON or an SSE stream that ends with the response.
type streamable struct {
	doer Doer
	cfg  ClientConfig

	mu        sync.Mutex
	sessionID string
	version   string
}

func newStreamable(d Doer, cfg ClientConfig) *streamable { return &streamable{doer: d, cfg: cfg} }

func (t *streamable) setVersion(v string) {
	t.mu.Lock()
	t.version = v
	t.mu.Unlock()
}

// post sends msg and returns the open response. The caller closes the body.
func (t *streamable) post(ctx context.Context, msg Message) (*http.Response, error) {
	body, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("encoding %s: %w", msg.Method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building MCP request: %w", err)
	}
	copyHeader(req.Header, t.cfg.Header)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	t.mu.Lock()
	session, version := t.sessionID, t.version
	t.mu.Unlock()
	if session != "" {
		req.Header.Set(HeaderSessionID, session)
	}
	if version != "" {
		req.Header.Set(HeaderProtocolVersion, version)
	}
	resp, err := t.doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sending %s to MCP server: %w", msg.Method, err)
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound && session != "" {
			return nil, ErrSessionExpired
		}
		return nil, statusError(resp)
	}
	return resp, nil
}

func statusError(resp *http.Response) *HTTPStatusError {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, detailBytes))
	return &HTTPStatusError{Status: resp.StatusCode, Detail: strings.TrimSpace(string(b))}
}

func (t *streamable) call(ctx context.Context, req Message) (Message, error) {
	resp, err := t.post(ctx, req)
	if err != nil {
		return Message{}, err
	}
	defer resp.Body.Close()
	if req.Method == MethodInitialize {
		if id := resp.Header.Get(HeaderSessionID); id != "" {
			t.mu.Lock()
			t.sessionID = id
			t.mu.Unlock()
		}
	}
	if resp.StatusCode == http.StatusAccepted {
		return Message{}, fmt.Errorf("MCP server accepted %s without answering", req.Method)
	}
	mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	switch mt {
	case "application/json":
		return t.readJSON(resp.Body, req)
	case "text/event-stream":
		return t.readStream(resp.Body, req)
	default:
		return Message{}, fmt.Errorf("MCP server answered %s with unsupported content type %q", req.Method, resp.Header.Get("Content-Type"))
	}
}

// readJSON decodes a JSON reply: one response, or a batch holding it.
func (t *streamable) readJSON(r io.Reader, req Message) (Message, error) {
	b, err := io.ReadAll(io.LimitReader(r, int64(t.cfg.MaxResponseBytes)+1))
	if err != nil {
		return Message{}, fmt.Errorf("reading MCP response: %w", err)
	}
	if len(b) > t.cfg.MaxResponseBytes {
		return Message{}, fmt.Errorf("MCP response exceeds %d bytes", t.cfg.MaxResponseBytes)
	}
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] == '[' {
		var batch []Message
		if err := json.Unmarshal(b, &batch); err != nil {
			return Message{}, fmt.Errorf("decoding MCP response: %w", err)
		}
		for _, m := range batch {
			if m.IsResponse() && m.IDKey() == req.IDKey() {
				return m, nil
			}
		}
		return Message{}, fmt.Errorf("MCP batch reply has no response to %s", req.Method)
	}
	var m Message
	if err := json.Unmarshal(b, &m); err != nil {
		return Message{}, fmt.Errorf("decoding MCP response: %w", err)
	}
	if !m.IsResponse() || m.IDKey() != req.IDKey() {
		return Message{}, fmt.Errorf("MCP reply to %s is not the matching response", req.Method)
	}
	return m, nil
}

// readStream reads an SSE reply until the matching response arrives. Other
// messages (progress notifications, server requests) are ignored.
func (t *streamable) readStream(r io.Reader, req Message) (Message, error) {
	events := newSSEReader(r, t.cfg.MaxResponseBytes)
	for {
		ev, err := events.Next()
		if errors.Is(err, io.EOF) {
			return Message{}, fmt.Errorf("MCP event stream for %s ended without a response", req.Method)
		}
		if err != nil {
			return Message{}, err
		}
		if ev.Name != "" && ev.Name != "message" {
			continue
		}
		var m Message
		if json.Unmarshal([]byte(ev.Data), &m) != nil {
			continue
		}
		if m.IsResponse() && m.IDKey() == req.IDKey() {
			return m, nil
		}
	}
}

func (t *streamable) notify(ctx context.Context, n Message) error {
	resp, err := t.post(ctx, n)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, int64(t.cfg.MaxResponseBytes)))
	return nil
}

// close ends the session with DELETE when one was opened. Servers may refuse
// (405); that is not an error.
func (t *streamable) close(ctx context.Context) error {
	t.mu.Lock()
	session, version := t.sessionID, t.version
	t.mu.Unlock()
	if session == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, t.cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, t.cfg.Endpoint, http.NoBody)
	if err != nil {
		return fmt.Errorf("building MCP session delete: %w", err)
	}
	copyHeader(req.Header, t.cfg.Header)
	req.Header.Set(HeaderSessionID, session)
	if version != "" {
		req.Header.Set(HeaderProtocolVersion, version)
	}
	resp, err := t.doer.Do(req)
	if err != nil {
		return fmt.Errorf("ending MCP session: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, detailBytes))
	return nil
}

func copyHeader(dst, src http.Header) {
	for k, vs := range src {
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}
