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
	"net/url"
	"sync"
	"time"
)

// sseTransport is the legacy HTTP+SSE transport: a long-lived GET stream
// delivers an "endpoint" event (where to POST) and then every response as a
// "message" event; requests are POSTed to that endpoint. One goroutine reads
// the stream; close cancels it and waits for it to finish.
type sseTransport struct {
	doer Doer
	cfg  ClientConfig

	cancel context.CancelFunc
	done   chan struct{} // closed when the reader goroutine has exited

	mu       sync.Mutex
	post     string // message endpoint, once announced
	version  string
	pending  map[string]chan Message
	closeErr error // why the stream ended
	ready    chan struct{}
	readyOne sync.Once
}

func newSSE(d Doer, cfg ClientConfig) *sseTransport {
	return &sseTransport{doer: d, cfg: cfg, pending: map[string]chan Message{}, ready: make(chan struct{}), done: make(chan struct{})}
}

func (t *sseTransport) setVersion(v string) {
	t.mu.Lock()
	t.version = v
	t.mu.Unlock()
}

// start opens the stream and waits for the endpoint event. ctx is the Connect
// context: it governs the stream for the transport's whole life.
func (t *sseTransport) start(ctx context.Context) error {
	streamCtx, cancel := context.WithCancel(ctx)
	t.cancel = cancel
	req, err := http.NewRequestWithContext(streamCtx, http.MethodGet, t.cfg.Endpoint, http.NoBody)
	if err != nil {
		cancel()
		close(t.done)
		return fmt.Errorf("building MCP stream request: %w", err)
	}
	copyHeader(req.Header, t.cfg.Header)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := t.doer.Do(req)
	if err != nil {
		cancel()
		close(t.done)
		return fmt.Errorf("opening MCP event stream: %w", err)
	}
	if mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); resp.StatusCode != http.StatusOK || mt != "text/event-stream" {
		defer resp.Body.Close()
		cancel()
		close(t.done)
		if resp.StatusCode != http.StatusOK {
			return statusError(resp)
		}
		return fmt.Errorf("MCP server answered the stream request with content type %q, not text/event-stream", resp.Header.Get("Content-Type"))
	}
	go t.read(resp.Body)

	timer := time.NewTimer(t.cfg.Timeout)
	defer timer.Stop()
	select {
	case <-t.ready:
		return t.streamErr()
	case <-t.done:
		if err := t.streamErr(); err != nil {
			return err
		}
		return errors.New("MCP event stream ended before announcing an endpoint")
	case <-timer.C:
		t.shutdown()
		return fmt.Errorf("MCP server did not announce a message endpoint within %s", t.cfg.Timeout)
	case <-ctx.Done():
		t.shutdown()
		return fmt.Errorf("waiting for MCP endpoint: %w", ctx.Err())
	}
}

func (t *sseTransport) streamErr() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closeErr
}

// read is the stream goroutine: it ends when the body ends or the context is
// cancelled, then fails every waiting call.
func (t *sseTransport) read(body io.ReadCloser) {
	defer close(t.done)
	defer body.Close()
	var err error
	events := newSSEReader(body, t.cfg.MaxResponseBytes)
	for err == nil {
		var ev sseEvent
		if ev, err = events.Next(); err != nil {
			break
		}
		switch ev.Name {
		case "endpoint":
			err = t.setEndpoint(ev.Data)
		case "", "message":
			t.deliver(ev.Data)
		}
	}
	t.mu.Lock()
	if errors.Is(err, io.EOF) {
		err = errors.New("MCP event stream closed by the server")
	}
	t.closeErr = err
	for id, ch := range t.pending {
		close(ch)
		delete(t.pending, id)
	}
	t.mu.Unlock()
	t.readyOne.Do(func() { close(t.ready) })
}

// setEndpoint resolves the announced message URL. It must stay on the
// stream's origin so a server cannot redirect credentials elsewhere.
func (t *sseTransport) setEndpoint(data string) error {
	base, err := url.Parse(t.cfg.Endpoint)
	if err != nil {
		return fmt.Errorf("parsing MCP endpoint: %w", err)
	}
	ref, err := url.Parse(data)
	if err != nil {
		return fmt.Errorf("MCP server announced an invalid message endpoint: %w", err)
	}
	u := base.ResolveReference(ref)
	if u.Scheme != base.Scheme || u.Host != base.Host {
		return fmt.Errorf("MCP server announced a message endpoint on another origin (%s)", u.Host)
	}
	t.mu.Lock()
	t.post = u.String()
	t.mu.Unlock()
	t.readyOne.Do(func() { close(t.ready) })
	return nil
}

func (t *sseTransport) deliver(data string) {
	var m Message
	if json.Unmarshal([]byte(data), &m) != nil || !m.IsResponse() {
		return
	}
	t.mu.Lock()
	ch, ok := t.pending[m.IDKey()]
	delete(t.pending, m.IDKey())
	t.mu.Unlock()
	if ok {
		ch <- m // buffered: never blocks
	}
}

// send POSTs a message to the announced endpoint; the answer is on the stream.
func (t *sseTransport) send(ctx context.Context, msg Message) error {
	t.mu.Lock()
	endpoint, version := t.post, t.version
	t.mu.Unlock()
	if endpoint == "" {
		return errors.New("MCP message endpoint is not known yet")
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("encoding %s: %w", msg.Method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("building MCP request: %w", err)
	}
	copyHeader(req.Header, t.cfg.Header)
	req.Header.Set("Content-Type", "application/json")
	if version != "" {
		req.Header.Set(HeaderProtocolVersion, version)
	}
	resp, err := t.doer.Do(req)
	if err != nil {
		return fmt.Errorf("sending %s to MCP server: %w", msg.Method, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return statusError(resp)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, detailBytes))
	return nil
}

func (t *sseTransport) call(ctx context.Context, req Message) (Message, error) {
	ch := make(chan Message, 1)
	t.mu.Lock()
	if t.closeErr != nil {
		err := t.closeErr
		t.mu.Unlock()
		return Message{}, err
	}
	t.pending[req.IDKey()] = ch
	t.mu.Unlock()
	if err := t.send(ctx, req); err != nil {
		t.forget(req.IDKey())
		return Message{}, err
	}
	select {
	case m, ok := <-ch:
		if !ok {
			return Message{}, t.streamErr()
		}
		return m, nil
	case <-ctx.Done():
		t.forget(req.IDKey())
		return Message{}, fmt.Errorf("waiting for %s response: %w", req.Method, ctx.Err())
	}
}

func (t *sseTransport) forget(id string) {
	t.mu.Lock()
	delete(t.pending, id)
	t.mu.Unlock()
}

func (t *sseTransport) notify(ctx context.Context, n Message) error { return t.send(ctx, n) }

// shutdown cancels the stream and waits for the reader goroutine.
func (t *sseTransport) shutdown() {
	if t.cancel != nil {
		t.cancel()
	}
	<-t.done
}

func (t *sseTransport) close(context.Context) error {
	t.shutdown()
	return nil
}
