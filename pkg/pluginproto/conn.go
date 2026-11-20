package pluginproto

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
)

// ErrClosed is returned by Call and Notify once the connection is closed.
var ErrClosed = errors.New("plugin connection closed")

// ErrMessageTooLarge is returned when a message exceeds ConnOptions.MaxMessageBytes.
var ErrMessageTooLarge = errors.New("plugin message too large")

// Request is an incoming request, or a notification when ID is nil.
type Request struct {
	ID     json.RawMessage
	Method string
	Params json.RawMessage
}

// Handler serves incoming requests and notifications. The context is
// cancelled when the peer sends $/cancel for the request or the connection
// closes. The result of a notification is discarded. Return an *Error to
// choose the JSON-RPC error code.
type Handler func(ctx context.Context, req Request) (any, error)

// ConnOptions bounds a connection.
type ConnOptions struct {
	// MaxMessageBytes caps one message in either direction.
	MaxMessageBytes int
	// MaxConcurrent caps incoming requests handled at once; a request above
	// the cap is answered with CodeBusy. Notifications run in the read loop
	// and do not count.
	MaxConcurrent int
}

// Conn is a bidirectional JSON-RPC 2.0 connection over line-delimited JSON.
// Either side may send requests; responses are matched by id.
type Conn struct {
	r       *bufio.Reader
	w       io.Writer
	handler Handler
	opts    ConnOptions

	wmu sync.Mutex

	mu       sync.Mutex
	nextID   int64
	pending  map[string]chan message
	inflight map[string]context.CancelFunc
	closed   bool

	sem  chan struct{}
	wg   sync.WaitGroup
	done chan struct{}
}

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

const jsonrpcVersion = "2.0"

// readBufferBytes is the initial read buffer; lines grow past it up to
// MaxMessageBytes.
const readBufferBytes = 64 << 10

// NewConn returns a connection reading r and writing w. Call Run to start it.
func NewConn(r io.Reader, w io.Writer, h Handler, opts ConnOptions) *Conn {
	return &Conn{
		r: bufio.NewReaderSize(r, readBufferBytes), w: w, handler: h, opts: opts,
		pending: map[string]chan message{}, inflight: map[string]context.CancelFunc{},
		sem: make(chan struct{}, max(opts.MaxConcurrent, 1)), done: make(chan struct{}),
	}
}

// Done is closed when Run has returned.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Run reads messages until the reader ends, the context is cancelled (the
// reader must then be closed by the caller to unblock it) or a protocol error
// occurs. On return, pending calls fail with ErrClosed and running handlers
// are cancelled and awaited. A clean end of input returns nil.
func (c *Conn) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer func() {
		cancel()
		c.shutdown()
		c.wg.Wait()
		close(c.done)
	}()
	for {
		line, err := c.readLine()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var m message
		if err := json.Unmarshal(line, &m); err != nil {
			_ = c.send(message{ID: json.RawMessage("null"), Error: &Error{Code: CodeParseError, Message: "invalid JSON"}})
			continue
		}
		c.dispatch(ctx, m)
	}
}

func (c *Conn) readLine() ([]byte, error) {
	var buf []byte
	for {
		chunk, err := c.r.ReadSlice('\n')
		if len(buf)+len(chunk) > c.opts.MaxMessageBytes {
			return nil, fmt.Errorf("reading message: %w (limit %d bytes)", ErrMessageTooLarge, c.opts.MaxMessageBytes)
		}
		buf = append(buf, chunk...)
		switch {
		case err == nil:
			return buf, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) && len(bytes.TrimSpace(buf)) > 0:
			return buf, nil
		default:
			return nil, fmt.Errorf("reading message: %w", err)
		}
	}
}

func (c *Conn) dispatch(ctx context.Context, m message) {
	switch {
	case m.Method == "" && m.ID != nil:
		c.resolve(m)
	case m.Method == MethodCancel:
		var p CancelParams
		if json.Unmarshal(m.Params, &p) == nil {
			c.mu.Lock()
			if stop, ok := c.inflight[string(p.ID)]; ok {
				stop()
			}
			c.mu.Unlock()
		}
	case m.ID == nil:
		_, _ = c.handler(ctx, Request{Method: m.Method, Params: m.Params})
	default:
		c.serve(ctx, m)
	}
}

func (c *Conn) resolve(m message) {
	c.mu.Lock()
	ch, ok := c.pending[string(m.ID)]
	delete(c.pending, string(m.ID))
	c.mu.Unlock()
	if ok {
		ch <- m
	}
}

func (c *Conn) serve(ctx context.Context, m message) {
	select {
	case c.sem <- struct{}{}:
	default:
		_ = c.send(message{ID: m.ID, Error: &Error{Code: CodeBusy, Message: "too many concurrent requests"}})
		return
	}
	reqCtx, stop := context.WithCancel(ctx)
	key := string(m.ID)
	c.mu.Lock()
	c.inflight[key] = stop
	c.mu.Unlock()
	c.wg.Add(1)
	go func() {
		defer func() {
			c.mu.Lock()
			delete(c.inflight, key)
			c.mu.Unlock()
			stop()
			<-c.sem
			c.wg.Done()
		}()
		res, err := c.handler(reqCtx, Request{ID: m.ID, Method: m.Method, Params: m.Params})
		reply := message{ID: m.ID}
		if err != nil {
			reply.Error = ErrorFrom(err)
		} else if reply.Result, err = json.Marshal(res); err != nil {
			reply.Result, reply.Error = nil, &Error{Code: CodeInternalError, Message: "encoding result: " + err.Error()}
		}
		if err := c.send(reply); errors.Is(err, ErrMessageTooLarge) {
			_ = c.send(message{ID: m.ID, Error: &Error{Code: CodeInternalError, Message: err.Error()}})
		}
	}()
}

// Call sends a request and decodes the result into result (which may be nil).
// When ctx ends first, the peer is sent $/cancel and ctx's error is returned.
// A JSON-RPC error comes back as *Error.
func (c *Conn) Call(ctx context.Context, method string, params, result any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("encoding %s params: %w", method, err)
	}
	ch := make(chan message, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	c.nextID++
	id := json.RawMessage(strconv.FormatInt(c.nextID, 10))
	c.pending[string(id)] = ch
	c.mu.Unlock()

	if err := c.send(message{ID: id, Method: method, Params: raw}); err != nil {
		c.forget(id)
		return err
	}
	select {
	case m, ok := <-ch:
		if !ok {
			return ErrClosed
		}
		if m.Error != nil {
			return m.Error
		}
		if result == nil {
			return nil
		}
		if err := json.Unmarshal(m.Result, result); err != nil {
			return fmt.Errorf("decoding %s result: %w", method, err)
		}
		return nil
	case <-ctx.Done():
		c.forget(id)
		_ = c.Notify(MethodCancel, CancelParams{ID: id})
		return fmt.Errorf("%s: %w", method, ctx.Err())
	}
}

func (c *Conn) forget(id json.RawMessage) {
	c.mu.Lock()
	delete(c.pending, string(id))
	c.mu.Unlock()
}

// Notify sends a notification.
func (c *Conn) Notify(method string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("encoding %s params: %w", method, err)
	}
	return c.send(message{Method: method, Params: raw})
}

func (c *Conn) send(m message) error {
	m.JSONRPC = jsonrpcVersion
	b, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("encoding message: %w", err)
	}
	if len(b)+1 > c.opts.MaxMessageBytes {
		return fmt.Errorf("sending %d bytes: %w (limit %d bytes)", len(b)+1, ErrMessageTooLarge, c.opts.MaxMessageBytes)
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed && m.Method != "" {
		return ErrClosed
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if _, err := c.w.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("writing message: %w", err)
	}
	return nil
}

func (c *Conn) shutdown() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	for _, stop := range c.inflight {
		stop()
	}
}
