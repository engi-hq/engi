package stdio

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"sync"
	"time"

	"github.com/engi-hq/engi/pkg/pluginproto"
)

// process is one running plugin executable and the connection to it.
type process struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	conn  *pluginproto.Conn
	// stderr keeps the last lines the process wrote, for start errors.
	stderr *stderrTail

	// done is closed once the process has been reaped and its connection has
	// stopped; waitErr is valid after that.
	done    chan struct{}
	waitErr error
}

// spawn starts the program and the goroutines that own it: one reaping it and
// one running the connection. Both end when the process exits.
func spawn(ctx context.Context, cfg Config, log *slog.Logger, h pluginproto.Handler) (*process, error) {
	prog, err := resolveProgram(cfg.Dir, cfg.Command[0])
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(prog, cfg.Command[1:]...) //nolint:gosec // the command comes from the operator's plugin manifest
	cmd.Dir = cfg.Dir
	// A non-nil slice keeps the child from inheriting the host's environment
	// when the caller passes none.
	cmd.Env = append([]string{}, cfg.Env...)
	cmd.WaitDelay = cfg.ShutdownGrace

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("opening stdin: %w", err)
	}
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	tail := newStderrTail(cfg.StartErrorLines)
	errLog := newLineLogger(log, cfg.MaxLogLineBytes, tail)
	cmd.Stderr = errLog

	if err := cmd.Start(); err != nil {
		_ = pr.Close()
		_ = pw.Close()
		return nil, fmt.Errorf("starting %s: %w", prog, err)
	}
	p := &process{
		cmd: cmd, stdin: stdin, done: make(chan struct{}), stderr: tail,
		conn: pluginproto.NewConn(pr, stdin, h, pluginproto.ConnOptions{
			MaxMessageBytes: cfg.MaxMessageBytes, MaxConcurrent: cfg.MaxConcurrentCalls,
		}),
	}

	// The process outlives the call that started it, so its connection must
	// not die with that call's cancellation.
	runCtx := context.WithoutCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := p.conn.Run(runCtx)
		// The connection ended (EOF or a protocol error); make sure nothing
		// blocks writing to an abandoned pipe, and that the process goes.
		_ = pr.CloseWithError(err)
		_ = cmd.Process.Kill()
	}()
	go func() {
		p.waitErr = cmd.Wait()
		_ = pw.Close()
		errLog.flush()
		wg.Wait()
		close(p.done)
	}()
	return p, nil
}

func (p *process) exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// exitStatus describes how the process ended, for error messages.
func (p *process) exitStatus() string {
	if p.waitErr == nil {
		return "exit status 0"
	}
	return p.waitErr.Error()
}

func (p *process) kill() { _ = p.cmd.Process.Kill() }

// stop asks the plugin to finish, waits up to grace for it to exit and kills
// it after that or when ctx ends. It returns once the process is reaped.
func (p *process) stop(ctx context.Context, grace time.Duration, log *slog.Logger) {
	if !p.exited() {
		_ = p.conn.Notify(pluginproto.MethodShutdown, struct{}{})
		_ = p.stdin.Close()
		t := time.NewTimer(grace)
		defer t.Stop()
		select {
		case <-p.done:
			return
		case <-t.C:
			log.Warn("plugin ignored shutdown, killing it")
		case <-ctx.Done():
		}
		p.kill()
	}
	<-p.done
}
