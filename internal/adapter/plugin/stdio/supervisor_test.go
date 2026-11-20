package stdio_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"log/slog"

	"github.com/engi-hq/engi/internal/adapter/plugin/pluginnode"
	"github.com/engi-hq/engi/internal/adapter/plugin/stdio"
	"github.com/engi-hq/engi/pkg/pluginproto"
)

func stringsReader(s string) *strings.Reader { return strings.NewReader(s) }

func longLine() string { return strings.Repeat("y", 500) }

type logSink struct {
	mu   sync.Mutex
	recs []slog.Record
}

func (l *logSink) Enabled(context.Context, slog.Level) bool { return true }
func (l *logSink) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	l.recs = append(l.recs, r)
	l.mu.Unlock()
	return nil
}
func (l *logSink) WithAttrs(as []slog.Attr) slog.Handler { return &attrHandler{l, as} }
func (l *logSink) WithGroup(string) slog.Handler         { return l }

type attrHandler struct {
	*logSink
	attrs []slog.Attr
}

func (h *attrHandler) Handle(ctx context.Context, r slog.Record) error {
	r.AddAttrs(h.attrs...)
	return h.logSink.Handle(ctx, r)
}
func (h *attrHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return &attrHandler{h.logSink, append(slices.Clone(h.attrs), as...)}
}

func (l *logSink) messages() []slog.Record {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.recs)
}

type fakeCB struct{}

func (fakeCB) ReadBlob(context.Context, pluginproto.BinaryRef) ([]byte, error) {
	return []byte("payload"), nil
}

func (fakeCB) WriteBlob(_ context.Context, name, mime string, data []byte) (pluginproto.BinaryRef, error) {
	return pluginproto.BinaryRef{Ref: "blob://1", Name: name, MIME: mime, Size: int64(len(data))}, nil
}
func (fakeCB) Eval(context.Context, string, int) (any, error) { return 42.0, nil }
func (fakeCB) Log(string, string, map[string]any)             {}

// logCB records the log lines a call routes to its node logger.
type logCB struct {
	fakeCB
	mu   sync.Mutex
	logs []string
}

func (c *logCB) Log(level, msg string, _ map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logs = append(c.logs, level+":"+msg)
}

func (c *logCB) messages() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.logs)
}

// serverLogs returns the messages the server log recorded, with their callId.
func serverLogs(sink *logSink) map[string]string {
	out := map[string]string{}
	for _, r := range sink.messages() {
		var id string
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "callId" {
				id = a.Value.String()
			}
			return true
		})
		out[r.Message] = id
	}
	return out
}

var _ pluginnode.Callbacks = fakeCB{}

func newSup(t *testing.T, mode string, edit func(*stdio.Config)) (*stdio.Supervisor, *logSink) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cfg := stdio.Config{
		Name: "testplugin", Dir: t.TempDir(), Command: []string{exe},
		Env:      testEnv(mode),
		HostInfo: pluginproto.Info{Name: "engi", Version: "test"}, Declared: []string{nodeType},
		MaxMessageBytes: 1 << 20, MaxConcurrentCalls: 8,
		StartTimeout: 10 * time.Second, CallTimeout: 10 * time.Second, ShutdownGrace: 2 * time.Second,
		RestartBackoff: 10 * time.Millisecond, MaxLogLineBytes: 100,
	}
	if edit != nil {
		edit(&cfg)
	}
	sink := &logSink{}
	s := stdio.New(cfg, slog.New(sink))
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return s, sink
}

var callSeq atomic.Int64

func params(action string, extra map[string]any) pluginproto.ExecuteParams {
	n := callSeq.Add(1)
	p := map[string]any{"action": action}
	for k, v := range extra {
		p[k] = v
	}
	return pluginproto.ExecuteParams{
		CallID: fmt.Sprintf("call-%s-%d", action, n),
		Node:   pluginproto.NodeRef{Type: nodeType, Version: 1},
		Inputs: [][]pluginproto.Item{{{JSON: map[string]any{}}}}, Params: []map[string]any{p},
	}
}

func run(t *testing.T, s *stdio.Supervisor, action string) map[string]any {
	t.Helper()
	res, err := s.Execute(context.Background(), params(action, nil), fakeCB{})
	if err != nil {
		t.Fatalf("%s: %v", action, err)
	}
	return res.Outputs[0][0].JSON
}

func TestStartAndRoundTrip(t *testing.T) {
	s, _ := newSup(t, "normal", nil)
	res, err := s.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.ProtocolVersion != 1 || res.Plugin.Name != "testplugin" || len(res.Nodes) != 1 || res.Nodes[0].Type != nodeType {
		t.Fatalf("start = %+v", res)
	}
	out := run(t, s, "callbacks")
	if out["eval"] != 42.0 || out["blob"] != "payload" {
		t.Errorf("callbacks = %v", out)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Error(err)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Error("second Close must be a no-op:", err)
	}
	if _, err := s.Execute(context.Background(), params("echo", nil), nil); err == nil {
		t.Error("Execute after Close must fail")
	}
}

func TestUnknownCallIDRefused(t *testing.T) {
	// A callback for a call that is not in flight must be refused: the plugin
	// below calls back with the right id only while its call runs.
	s, _ := newSup(t, "normal", nil)
	if _, err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	p := params("callbacks", nil)
	// Execute with nil callbacks: registered as nil, so the plugin's callback is refused.
	_, err := s.Execute(context.Background(), p, nil)
	var pe *pluginproto.Error
	if !errors.As(err, &pe) || pe.Code != pluginproto.CodeInvalidParams {
		t.Errorf("err = %v", err)
	}
}

func TestEnvIsExactlyConfigured(t *testing.T) {
	t.Setenv("ENGI_SECRET_KEY", "hunter2")
	s, _ := newSup(t, "normal", nil)
	out := run(t, s, "env")
	var got []string
	for _, e := range out["env"].([]any) {
		got = append(got, e.(string))
	}
	slices.Sort(got)
	want := testEnv("normal")
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("env = %v, want %v", got, want)
	}
}

func TestCrashFailsCallAndNextCallRestarts(t *testing.T) {
	s, _ := newSup(t, "normal", nil)
	pid := run(t, s, "echo")["pid"]
	_, err := s.Execute(context.Background(), params("crash", nil), fakeCB{})
	if err == nil || !strings.Contains(err.Error(), "exited") || !strings.Contains(err.Error(), "exit status 3") {
		t.Fatalf("err = %v", err)
	}
	if pid2 := run(t, s, "echo")["pid"]; pid2 == pid {
		t.Error("the plugin should have been restarted")
	}
}

func TestCallTimeoutSendsCancel(t *testing.T) {
	s, _ := newSup(t, "normal", func(c *stdio.Config) { c.CallTimeout = 200 * time.Millisecond })
	marker := filepath.Join(t.TempDir(), "marker")
	start := time.Now()
	_, err := s.Execute(context.Background(), params("block", map[string]any{"marker": marker}), fakeCB{})
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("took too long")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the plugin never saw $/cancel")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestParentContextCancelled(t *testing.T) {
	s, _ := newSup(t, "normal", nil)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	_, err := s.Execute(ctx, params("block", map[string]any{"marker": filepath.Join(t.TempDir(), "m")}), fakeCB{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

func TestConcurrencyCap(t *testing.T) {
	s, _ := newSup(t, "normal", func(c *stdio.Config) { c.MaxConcurrentCalls = 2 })
	if _, err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	peaks := make([]float64, 6)
	for i := range peaks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			peaks[i] = run(t, s, "conc")["peak"].(float64)
		}()
	}
	wg.Wait()
	if mx := slices.Max(peaks); mx != 2 {
		t.Errorf("peak concurrency = %v, want 2", mx)
	}
}

func TestIdleStopAndRestart(t *testing.T) {
	s, _ := newSup(t, "normal", func(c *stdio.Config) { c.IdleTimeout = 150 * time.Millisecond })
	pid := run(t, s, "echo")["pid"]
	time.Sleep(600 * time.Millisecond)
	if pid2 := run(t, s, "echo")["pid"]; pid2 == pid {
		t.Error("the idle plugin should have been stopped and restarted")
	}
}

func TestCloseKillsStubbornPlugin(t *testing.T) {
	s, _ := newSup(t, "stubborn", func(c *stdio.Config) { c.ShutdownGrace = 200 * time.Millisecond })
	if _, err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("Close took %v", d)
	}
}

func TestStartRejectsBadPlugins(t *testing.T) {
	tests := []struct{ mode, want string }{
		{"badversion", "protocol version 2"},
		{"badtype", "reserved"},
		{"badgroup", "group"},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			s, _ := newSup(t, tt.mode, nil)
			_, err := s.Start(context.Background())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestStartFailures(t *testing.T) {
	s, _ := newSup(t, "normal", func(c *stdio.Config) { c.Command = []string{"./does-not-exist"} })
	if _, err := s.Start(context.Background()); err == nil {
		t.Error("a missing program must fail Start")
	}
	s, _ = newSup(t, "nosuchmode", nil)
	_, err := s.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "exited") {
		t.Errorf("a plugin exiting before initialize: err = %v", err)
	}
}

func TestStderrIsLogged(t *testing.T) {
	s, sink := newSup(t, "stderr", nil)
	if _, err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = s.Close(context.Background())
	var lines []string
	for _, r := range sink.messages() {
		var name string
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "plugin" {
				name = a.Value.String()
			}
			return true
		})
		if name == "testplugin" && r.Level == slog.LevelInfo {
			lines = append(lines, r.Message)
		}
	}
	if !slices.Contains(lines, "hello from stderr") {
		t.Errorf("lines = %v", lines)
	}
	if !slices.Contains(lines, strings.Repeat("y", 100)) {
		t.Errorf("the long line should be cut at 100 bytes, lines = %v", lines)
	}
}

// testEnv is the child's whole environment. GORACE spares race-enabled
// children the detector's one second exit delay.
func testEnv(mode string) []string {
	return []string{modeEnv + "=" + mode, "FOO=bar", "GORACE=atexit_sleep_ms=0"}
}

func TestLogWithCallIDGoesToNodeLogger(t *testing.T) {
	s, sink := newSup(t, "normal", nil)
	if _, err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	cb := &logCB{}
	if _, err := s.Execute(context.Background(), params("log", nil), cb); err != nil {
		t.Fatal(err)
	}
	if got, want := cb.messages(), []string{"info:from node"}; !slices.Equal(got, want) {
		t.Errorf("node logger got %v, want %v", got, want)
	}
	if _, ok := serverLogs(sink)["from node"]; ok {
		t.Error("a line that names a call must not reach the server log")
	}
}

func TestLogWithoutCallIDGoesToServerLog(t *testing.T) {
	s, sink := newSup(t, "rawlog", nil)
	if _, err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if id, ok := serverLogs(sink)["startup note"]; !ok || id != "" {
		t.Errorf("startup note: found=%v callId=%q, want server log without callId", ok, id)
	}
}

func TestLogAfterCallGoesToServerLogWithCallID(t *testing.T) {
	s, sink := newSup(t, "normal", nil)
	if _, err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	p := params("late-log", nil)
	if _, err := s.Execute(context.Background(), p, fakeCB{}); err != nil {
		t.Fatal(err)
	}
	var id string
	for range 100 {
		if id = serverLogs(sink)["too late"]; id != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if id != p.CallID {
		t.Errorf("late line callId = %q, want %q (server log)", id, p.CallID)
	}
}

func TestFailedStartReportsStderrTail(t *testing.T) {
	s, _ := newSup(t, "crashstart", func(c *stdio.Config) { c.StartErrorLines = 2 })
	_, err := s.Start(context.Background())
	if err == nil {
		t.Fatal("a plugin that exits during start must fail Start")
	}
	msg := err.Error()
	if !strings.Contains(msg, "exited") || !strings.Contains(msg, "startup line 4\nstartup line 5") {
		t.Errorf("err = %q, want the exit status and the last two stderr lines", msg)
	}
	if strings.Contains(msg, "startup line 3") {
		t.Errorf("err = %q, only the last two lines belong in it", msg)
	}
}

func TestFailedStartWithoutStartErrorLinesHasNoTail(t *testing.T) {
	s, _ := newSup(t, "crashstart", nil)
	_, err := s.Start(context.Background())
	if err == nil || strings.Contains(err.Error(), "startup line") {
		t.Errorf("err = %v, want no stderr tail when StartErrorLines is zero", err)
	}
}
