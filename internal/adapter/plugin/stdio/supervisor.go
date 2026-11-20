// Package stdio runs a plugin as a child process speaking the Engi Plugin
// Protocol over stdin and stdout, and restarts it on demand.
package stdio

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/engi-hq/engi/internal/adapter/plugin/pluginnode"
	"github.com/engi-hq/engi/pkg/pluginproto"
)

// Config describes one stdio plugin. Every duration and limit comes from the
// host's configuration; there are no defaults here.
type Config struct {
	Name string
	// Dir is the plugin's working directory; a relative program path in
	// Command is resolved against it.
	Dir     string
	Command []string
	// Env is the child's complete environment. The supervisor never reads the
	// host's.
	Env      []string
	HostInfo pluginproto.Info
	// Declared lists the node types the manifest declares.
	Declared           []string
	MaxMessageBytes    int
	MaxConcurrentCalls int
	StartTimeout       time.Duration
	CallTimeout        time.Duration
	ShutdownGrace      time.Duration
	// IdleTimeout stops the process after that long without a call; zero
	// keeps it running.
	IdleTimeout     time.Duration
	RestartBackoff  time.Duration
	MaxLogLineBytes int
	// StartErrorLines is how many of the last stderr lines a failed start
	// reports; zero reports none.
	StartErrorLines int
	// Clock is the time source for restart backoff; nil means the real clock.
	Clock Clock
}

// Clock is the supervisor's view of time.
type Clock interface {
	Now() time.Time
	// Sleep waits for d or until ctx ends.
	Sleep(ctx context.Context, d time.Duration) error
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("waiting to restart plugin: %w", ctx.Err())
	}
}

// Supervisor owns one plugin process. It implements pluginnode.Caller.
type Supervisor struct {
	cfg   Config
	log   *slog.Logger
	clock Clock
	sem   chan struct{}

	// startMu serialises (re)starts; mu guards the fields below.
	startMu   sync.Mutex
	mu        sync.Mutex
	proc      *process
	initial   *pluginproto.InitializeResult
	lastStart time.Time
	closed    bool
	inflight  int
	idle      *time.Timer

	callsMu sync.Mutex
	calls   map[string]pluginnode.Callbacks
}

var _ pluginnode.Caller = (*Supervisor)(nil)

// New returns a supervisor; nothing runs until Start or the first Execute.
func New(cfg Config, log *slog.Logger) *Supervisor {
	clock := cfg.Clock
	if clock == nil {
		clock = realClock{}
	}
	return &Supervisor{
		cfg: cfg, log: log.With("plugin", cfg.Name), clock: clock,
		sem:   make(chan struct{}, max(cfg.MaxConcurrentCalls, 1)),
		calls: map[string]pluginnode.Callbacks{},
	}
}

// Start launches the plugin if it is not running, performs the handshake and
// returns the descriptors it declared.
func (s *Supervisor) Start(ctx context.Context) (pluginproto.InitializeResult, error) {
	if _, err := s.ensure(ctx); err != nil {
		return pluginproto.InitializeResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return *s.initial, nil
}

// ensure returns the running process, starting it first when needed.
func (s *Supervisor) ensure(ctx context.Context) (*process, error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, fmt.Errorf("plugin %s: supervisor is closed", s.cfg.Name)
	}
	if p := s.proc; p != nil && !p.exited() {
		s.mu.Unlock()
		return p, nil
	}
	last := s.lastStart
	s.mu.Unlock()

	if !last.IsZero() {
		if wait := s.cfg.RestartBackoff - s.clock.Now().Sub(last); wait > 0 {
			if err := s.clock.Sleep(ctx, wait); err != nil {
				return nil, err
			}
		}
	}

	s.mu.Lock()
	s.lastStart = s.clock.Now()
	s.mu.Unlock()
	p, res, err := s.launch(ctx)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		p.kill()
		<-p.done
		return nil, fmt.Errorf("plugin %s: supervisor is closed", s.cfg.Name)
	}
	if s.initial != nil && !reflect.DeepEqual(s.initial.Nodes, res.Nodes) {
		p.kill()
		<-p.done
		return nil, fmt.Errorf("plugin %s: descriptors changed after restart", s.cfg.Name)
	}
	if s.initial == nil {
		s.initial = &res
	}
	s.proc = p
	return p, nil
}

// launch starts the process and runs the initialize handshake.
func (s *Supervisor) launch(ctx context.Context) (*process, pluginproto.InitializeResult, error) {
	var res pluginproto.InitializeResult
	p, err := spawn(ctx, s.cfg, s.log, s.handle)
	if err != nil {
		return nil, res, fmt.Errorf("plugin %s: %w", s.cfg.Name, err)
	}
	fail := func(err error) (*process, pluginproto.InitializeResult, error) {
		p.kill()
		<-p.done
		return nil, res, withStderr(err, p.stderr)
	}
	ictx, cancel := context.WithTimeout(ctx, s.cfg.StartTimeout)
	defer cancel()
	params := pluginproto.InitializeParams{ProtocolVersion: pluginproto.ProtocolVersion, Host: s.cfg.HostInfo}
	if err := p.conn.Call(ictx, pluginproto.MethodInitialize, params, &res); err != nil {
		return fail(fmt.Errorf("plugin %s: initialize: %w", s.cfg.Name, s.describe(p, err)))
	}
	if res.ProtocolVersion != pluginproto.ProtocolVersion {
		return fail(fmt.Errorf("plugin %s speaks protocol version %d, host requires %d", s.cfg.Name, res.ProtocolVersion, pluginproto.ProtocolVersion))
	}
	for _, n := range res.Nodes {
		if err := pluginproto.CheckDescriptor(n.ToDescriptor(), s.cfg.Declared); err != nil {
			return fail(fmt.Errorf("plugin %s: %w", s.cfg.Name, err))
		}
	}
	return p, res, nil
}

// describe turns a closed connection into "plugin exited: <status>".
func (s *Supervisor) describe(p *process, err error) error {
	if !errors.Is(err, pluginproto.ErrClosed) {
		return err
	}
	t := time.NewTimer(s.cfg.ShutdownGrace)
	defer t.Stop()
	select {
	case <-p.done:
		return fmt.Errorf("plugin %s exited: %s", s.cfg.Name, p.exitStatus())
	case <-t.C:
		return fmt.Errorf("plugin %s connection closed: %w", s.cfg.Name, err)
	}
}

// Execute implements pluginnode.Caller.
func (s *Supervisor) Execute(ctx context.Context, p pluginproto.ExecuteParams, cb pluginnode.Callbacks) (pluginproto.ExecuteResult, error) {
	var res pluginproto.ExecuteResult
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		return res, fmt.Errorf("plugin %s: waiting for a free call slot: %w", s.cfg.Name, ctx.Err())
	}
	if !s.beginCall() {
		return res, fmt.Errorf("plugin %s: supervisor is closed", s.cfg.Name)
	}
	defer s.endCall()

	callCtx := ctx
	if s.cfg.CallTimeout > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, s.cfg.CallTimeout)
		defer cancel()
	}
	proc, err := s.ensure(callCtx)
	if err != nil {
		return res, err
	}
	if err := s.register(p.CallID, cb); err != nil {
		return res, err
	}
	defer s.unregister(p.CallID)

	err = proc.conn.Call(callCtx, pluginproto.MethodExecute, p, &res)
	switch {
	case err == nil:
		return res, nil
	case ctx.Err() == nil && errors.Is(callCtx.Err(), context.DeadlineExceeded):
		return res, fmt.Errorf("plugin %s: call exceeded %s: %w", s.cfg.Name, s.cfg.CallTimeout, context.DeadlineExceeded)
	}
	var pe *pluginproto.Error
	if errors.As(err, &pe) {
		return res, err
	}
	return res, fmt.Errorf("plugin %s: %w", s.cfg.Name, s.describe(proc, err))
}

// beginCall counts a call in flight and holds off the idle stop.
func (s *Supervisor) beginCall() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.inflight++
	if s.idle != nil {
		s.idle.Stop()
	}
	return true
}

func (s *Supervisor) endCall() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inflight--
	if s.inflight == 0 && s.cfg.IdleTimeout > 0 && !s.closed && s.proc != nil {
		if s.idle != nil {
			s.idle.Stop()
		}
		s.idle = time.AfterFunc(s.cfg.IdleTimeout, s.stopIdle)
	}
}

// stopIdle stops the process when no call arrived since the timer was set.
func (s *Supervisor) stopIdle() {
	s.mu.Lock()
	if s.closed || s.inflight > 0 || s.proc == nil {
		s.mu.Unlock()
		return
	}
	p := s.proc
	s.proc = nil
	s.mu.Unlock()
	s.log.Info("stopping idle plugin")
	p.stop(context.Background(), s.cfg.ShutdownGrace, s.log)
}

// Close stops the plugin: shutdown notification, stdin closed, a grace period,
// then a kill. It is safe to call more than once.
func (s *Supervisor) Close(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	if s.idle != nil {
		s.idle.Stop()
	}
	p := s.proc
	s.proc = nil
	s.mu.Unlock()
	if p != nil {
		p.stop(ctx, s.cfg.ShutdownGrace, s.log)
	}
	return nil
}

func (s *Supervisor) register(id string, cb pluginnode.Callbacks) error {
	s.callsMu.Lock()
	defer s.callsMu.Unlock()
	if _, dup := s.calls[id]; dup {
		return fmt.Errorf("plugin %s: call id %q is already in flight", s.cfg.Name, id)
	}
	s.calls[id] = cb
	return nil
}

func (s *Supervisor) unregister(id string) {
	s.callsMu.Lock()
	delete(s.calls, id)
	s.callsMu.Unlock()
}

func (s *Supervisor) callbacks(id string) (pluginnode.Callbacks, error) {
	s.callsMu.Lock()
	cb := s.calls[id]
	s.callsMu.Unlock()
	if cb == nil {
		return nil, &pluginproto.Error{Code: pluginproto.CodeInvalidParams, Message: "unknown or finished call " + id}
	}
	return cb, nil
}

// handle serves the requests a plugin sends to the host.
func (s *Supervisor) handle(ctx context.Context, req pluginproto.Request) (any, error) {
	switch req.Method {
	case pluginproto.MethodLog:
		var p pluginproto.LogParams
		if err := decode(req.Params, &p); err != nil {
			return nil, err
		}
		s.logPlugin(ctx, p)
		return nil, nil
	case pluginproto.MethodBlobRead:
		var p pluginproto.BlobReadParams
		cb, err := s.route(req.Params, &p, func() string { return p.CallID })
		if err != nil {
			return nil, err
		}
		data, err := cb.ReadBlob(ctx, p.Ref)
		if err != nil {
			return nil, err //nolint:wrapcheck // converted to a wire error by the connection
		}
		return pluginproto.BlobReadResult{Data: data}, nil
	case pluginproto.MethodBlobWrite:
		var p pluginproto.BlobWriteParams
		cb, err := s.route(req.Params, &p, func() string { return p.CallID })
		if err != nil {
			return nil, err
		}
		return cb.WriteBlob(ctx, p.Name, p.MIME, p.Data) //nolint:wrapcheck // converted to a wire error by the connection
	case pluginproto.MethodEvalExpression:
		var p pluginproto.EvalParams
		cb, err := s.route(req.Params, &p, func() string { return p.CallID })
		if err != nil {
			return nil, err
		}
		v, err := cb.Eval(ctx, p.Expression, p.ItemIndex)
		if err != nil {
			return nil, err //nolint:wrapcheck // converted to a wire error by the connection
		}
		return pluginproto.EvalResult{Value: v}, nil
	}
	return nil, &pluginproto.Error{Code: pluginproto.CodeMethodNotFound, Message: "unknown method " + req.Method}
}

// route decodes params into dst and finds the callbacks of the call it names.
func (s *Supervisor) route(raw []byte, dst any, callID func() string) (pluginnode.Callbacks, error) {
	if err := decode(raw, dst); err != nil {
		return nil, err
	}
	return s.callbacks(callID())
}

// logPlugin records a log line from the plugin. A line that names a call in
// flight goes to that call's node logger, so it shows up with the run that
// produced it; any other line (no callId, or a call that already finished) goes
// to the server log. The message is cut to plugins.maxLogLineBytes either way.
func (s *Supervisor) logPlugin(ctx context.Context, p pluginproto.LogParams) {
	msg := s.cut(p.Message)
	if p.CallID != "" {
		if cb, err := s.callbacks(p.CallID); err == nil {
			cb.Log(p.Level, msg, p.Attrs)
			return
		}
	}
	var level slog.Level
	switch p.Level {
	case pluginproto.LogDebug:
		level = slog.LevelDebug
	case pluginproto.LogWarn:
		level = slog.LevelWarn
	case pluginproto.LogError:
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	keys := make([]string, 0, len(p.Attrs))
	for k := range p.Attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	attrs := make([]slog.Attr, 0, len(keys))
	for _, k := range keys {
		attrs = append(attrs, slog.Any(k, p.Attrs[k]))
	}
	record := []slog.Attr{slog.Any("attrs", attrs)}
	if p.CallID != "" {
		record = append(record, slog.String("callId", p.CallID))
	}
	s.log.LogAttrs(ctx, level, msg, record...)
}

// cut shortens a plugin's message to the configured line length.
func (s *Supervisor) cut(msg string) string {
	if s.cfg.MaxLogLineBytes > 0 && len(msg) > s.cfg.MaxLogLineBytes {
		return msg[:s.cfg.MaxLogLineBytes]
	}
	return msg
}

// resolveProgram makes a program path with a directory part absolute, relative
// to dir. A bare name is left for the OS to find on PATH.
func resolveProgram(dir, prog string) (string, error) {
	if !strings.ContainsRune(prog, os.PathSeparator) {
		return prog, nil
	}
	if !filepath.IsAbs(prog) {
		prog = filepath.Join(dir, prog)
	}
	abs, err := filepath.Abs(prog)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", prog, err)
	}
	return abs, nil
}
