package pluginsdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"

	"github.com/engi-hq/engi/pkg/nodesdk"
	"github.com/engi-hq/engi/pkg/pluginproto"
)

type nodeKey struct {
	typ     string
	version int
}

// server holds the registered nodes and answers protocol requests for both
// transports. conn is nil over HTTP.
type server struct {
	info  pluginproto.Info
	opts  Options
	nodes map[nodeKey]nodesdk.Node
	order []nodesdk.Node

	conn *pluginproto.Conn

	mu       sync.Mutex
	stopping bool
	active   sync.WaitGroup
	stop     chan struct{}
	stopOnce sync.Once
}

func newServer(info pluginproto.Info, opts Options, nodes []nodesdk.Node) (*server, error) {
	if len(nodes) == 0 {
		return nil, errors.New("plugin has no nodes")
	}
	s := &server{info: info, opts: opts.withDefaults(), nodes: map[nodeKey]nodesdk.Node{}, stop: make(chan struct{})}
	for _, n := range nodes {
		d := n.Describe()
		k := nodeKey{d.Type, d.Version}
		if _, dup := s.nodes[k]; dup {
			return nil, fmt.Errorf("node %q version %d is registered twice", d.Type, d.Version)
		}
		s.nodes[k] = n
		s.order = append(s.order, n)
	}
	if s.opts.HTTPClient == nil {
		s.opts.HTTPClient = &http.Client{Timeout: DefaultHTTPTimeout}
	}
	return s, nil
}

// Serve runs the plugin on stdin and stdout until the host sends shutdown,
// closes stdin or ctx ends. It is the only place the SDK touches the process's
// standard streams.
func Serve(ctx context.Context, info pluginproto.Info, nodes ...nodesdk.Node) error {
	return ServeConn(ctx, os.Stdin, os.Stdout, info, Options{}, nodes...)
}

// ServeConn is Serve over arbitrary streams. When r is an io.Closer it is
// closed on shutdown so the read loop ends.
func ServeConn(ctx context.Context, r io.Reader, w io.Writer, info pluginproto.Info, opts Options, nodes ...nodesdk.Node) error {
	s, err := newServer(info, opts, nodes)
	if err != nil {
		return err
	}
	s.conn = pluginproto.NewConn(r, w, s.handle, pluginproto.ConnOptions{
		MaxMessageBytes: s.opts.MaxMessageBytes, MaxConcurrent: s.opts.MaxConcurrent,
	})
	runCtx, cancelRun := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelRun()
	runErr := make(chan error, 1)
	go func() { runErr <- s.conn.Run(runCtx) }()

	select {
	case err := <-runErr:
		return err
	case <-ctx.Done():
		// Aborted: do not wait for a read that closing stdin may not unblock.
		cancelRun()
		if c, ok := r.(io.Closer); ok {
			_ = c.Close()
		}
		return fmt.Errorf("serving plugin: %w", ctx.Err())
	case <-s.stop:
		s.active.Wait()
		cancelRun()
	}
	closer, ok := r.(io.Closer)
	if !ok {
		return nil
	}
	_ = closer.Close()
	if err := <-runErr; err != nil && !errors.Is(err, io.ErrClosedPipe) && !errors.Is(err, os.ErrClosed) {
		return fmt.Errorf("serving plugin: %w", err)
	}
	return nil
}

func (s *server) handle(ctx context.Context, req pluginproto.Request) (any, error) {
	switch req.Method {
	case pluginproto.MethodInitialize:
		return s.initialize(req.Params)
	case pluginproto.MethodHealth:
		return struct{}{}, nil
	case pluginproto.MethodShutdown:
		if s.conn != nil {
			s.mu.Lock()
			s.stopping = true
			s.mu.Unlock()
			s.stopOnce.Do(func() { close(s.stop) })
		}
		return struct{}{}, nil
	case pluginproto.MethodExecute:
		return s.execute(ctx, req.Params)
	}
	return nil, &pluginproto.Error{Code: pluginproto.CodeMethodNotFound, Message: "unknown method " + req.Method}
}

func (s *server) initialize(raw json.RawMessage) (any, error) {
	var p pluginproto.InitializeParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &pluginproto.Error{Code: pluginproto.CodeInvalidParams, Message: "invalid initialize params: " + err.Error()}
	}
	res := pluginproto.InitializeResult{ProtocolVersion: min(p.ProtocolVersion, pluginproto.ProtocolVersion), Plugin: s.info}
	for _, n := range s.order {
		res.Nodes = append(res.Nodes, pluginproto.FromDescriptor(n.Describe()))
	}
	return res, nil
}

func (s *server) execute(ctx context.Context, raw json.RawMessage) (res any, err error) {
	var p pluginproto.ExecuteParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &pluginproto.Error{Code: pluginproto.CodeInvalidParams, Message: "invalid execute params: " + err.Error()}
	}
	n, ok := s.nodes[nodeKey{p.Node.Type, p.Node.Version}]
	if !ok {
		return nil, &pluginproto.Error{Code: pluginproto.CodeInvalidParams, Message: fmt.Sprintf("unknown node %q version %d", p.Node.Type, p.Node.Version)}
	}
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		return nil, &pluginproto.Error{Code: pluginproto.CodeCancelled, Message: "plugin is shutting down"}
	}
	s.active.Add(1)
	s.mu.Unlock()
	defer s.active.Done()
	defer func() {
		if r := recover(); r != nil {
			res, err = nil, &pluginproto.Error{Code: pluginproto.CodeInternalError, Message: fmt.Sprintf("node panicked: %v", r)}
		}
	}()

	out, err := n.Execute(ctx, s.newInput(p))
	if err != nil {
		return nil, pluginproto.ErrorFrom(err)
	}
	return pluginproto.FromOutput(out), nil
}

func (s *server) newInput(p pluginproto.ExecuteParams) *input {
	in := &input{p: p, client: s.opts.HTTPClient}
	if s.conn != nil {
		in.host = s.conn
		in.logger = slog.New(&hostLogHandler{n: s.conn, callID: p.CallID})
	} else if in.logger = s.opts.Logger; in.logger == nil {
		in.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return in
}
