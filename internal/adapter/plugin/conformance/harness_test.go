//go:build integration

package conformance_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/adapter/plugin/stdio"
	"github.com/engi-hq/engi/pkg/pluginproto"
)

// Limits the harness gives every plugin, matching the SDK and host defaults
// documented in docs/15-plugins.md.
const (
	maxMessageBytes = 32 << 20
	maxCalls        = 16
	startTimeout    = 60 * time.Second
	callTimeout     = 30 * time.Second
	shutdownGrace   = 3 * time.Second
	buildTimeout    = 10 * time.Minute
	// buildServerGrace is how long run waits for output pipes after the command exits.
	buildServerGrace = 2 * time.Second
	maxLogLine       = 4096
)

var (
	// repoRoot is the repository root; workDir holds build output for the run.
	repoRoot string
	workDir  string

	builds = &buildCache{done: map[string]*buildResult{}}
)

func TestMain(m *testing.M) {
	_, file, _, _ := runtime.Caller(0)
	// conformance -> plugin -> adapter -> internal -> root
	repoRoot = filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", ".."))
	dir, err := os.MkdirTemp("", "engi-conformance-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	workDir = dir
	code := m.Run()
	_ = os.RemoveAll(workDir)
	os.Exit(code)
}

// buildResult is the memoised outcome of one build step.
type buildResult struct {
	once sync.Once
	dir  string
	err  error
}

// buildCache runs each named build step at most once per test run.
type buildCache struct {
	mu   sync.Mutex
	done map[string]*buildResult
}

func (c *buildCache) get(name string, fn func() (string, error)) (string, error) {
	c.mu.Lock()
	r, ok := c.done[name]
	if !ok {
		r = &buildResult{}
		c.done[name] = r
	}
	c.mu.Unlock()
	r.once.Do(func() { r.dir, r.err = fn() })
	return r.dir, r.err
}

// run executes a build command and returns its combined output on failure.
func run(dir string, env []string, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), buildTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	// Build servers (dotnet's VBCSCompiler) inherit our pipes and outlive the
	// command; WaitDelay stops Run from waiting on them.
	cmd.WaitDelay = buildServerGrace
	if env != nil {
		cmd.Env = env
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %v: %w\n%s", name, args, err, out.String())
	}
	return nil
}

// need skips the test when a toolchain program is not on PATH.
func need(t *testing.T, tools ...string) {
	t.Helper()
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("toolchain %q not found on PATH", tool)
		}
	}
}

// plugin is a ready-to-start stdio plugin.
type plugin struct {
	dir     string
	command []string
	env     []string
	// nodes are the node types the plugin declares.
	nodes []string
}

// hostEnv is the child environment: only PATH and HOME from the test process,
// plus extra variables a runtime needs. dotnet needs DOTNET_ROOT when the SDK
// is installed outside the default location, and DOTNET_NOLOGO /
// DOTNET_CLI_TELEMETRY_OPTOUT keep it from printing banners or phoning home.
func hostEnv(extra ...string) []string {
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
	return append(env, extra...)
}

func dotnetEnv() []string {
	env := hostEnv("DOTNET_NOLOGO=1", "DOTNET_CLI_TELEMETRY_OPTOUT=1")
	if root := os.Getenv("DOTNET_ROOT"); root != "" {
		env = append(env, "DOTNET_ROOT="+root)
	}
	return env
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// supervisorConfig is the production-like stdio configuration for p.
func supervisorConfig(name string, p plugin) stdio.Config {
	return stdio.Config{
		Name: name, Dir: p.dir, Command: p.command, Env: p.env,
		HostInfo:           pluginproto.Info{Name: "engi-conformance", Version: "0.0.0"},
		Declared:           p.nodes,
		MaxMessageBytes:    maxMessageBytes,
		MaxConcurrentCalls: maxCalls,
		StartTimeout:       startTimeout,
		CallTimeout:        callTimeout,
		ShutdownGrace:      shutdownGrace,
		RestartBackoff:     time.Second,
		MaxLogLineBytes:    maxLogLine,
	}
}

// freePort returns a TCP port that was free a moment ago.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}
