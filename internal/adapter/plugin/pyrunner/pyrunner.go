// Package pyrunner runs the Python Code node's user code in a child Python
// process. Every run starts a fresh interpreter with an empty environment and
// talks to it over the Engi Plugin Protocol (line-delimited JSON-RPC on
// stdin/stdout), so nothing persists between runs and a runaway script can be
// killed.
//
// The embedded runner script parses and checks the code before running it.
// That is a guard against mistakes, not a security boundary.
package pyrunner

import (
	"bytes"
	"context"
	_ "embed" // the runner script
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/engi-hq/engi/internal/node/codepython"
	"github.com/engi-hq/engi/pkg/nodesdk"
	"github.com/engi-hq/engi/pkg/pluginproto"
)

//go:embed runner.py
var runnerScript string

// InterpreterKey is the config key that names the interpreter. Errors mention
// it so operators know what to change.
const InterpreterKey = "nodes.code.python.interpreter"

const (
	// defaultStderrBytes is how much of the child's stderr is kept for error
	// messages when Config.MaxStderrBytes is zero.
	defaultStderrBytes = 4 << 10
	// messageHeadroomBytes is the protocol overhead allowed on top of the
	// payload when sizing the connection's message limit.
	messageHeadroomBytes = 64 << 10
	// killWaitDelay bounds how long Wait lingers on the pipes after a kill.
	killWaitDelay = 2 * time.Second
)

// Config configures a Runner.
type Config struct {
	// Interpreter is the Python 3 executable (nodes.code.python.interpreter).
	Interpreter string
	// MaxStderrBytes caps the captured stderr; zero uses a small default.
	MaxStderrBytes int
}

// Runner implements codepython.Runner with one child process per run.
type Runner struct {
	cfg Config
}

var _ codepython.Runner = (*Runner)(nil)

// New returns a Runner.
func New(cfg Config) *Runner {
	if cfg.MaxStderrBytes <= 0 {
		cfg.MaxStderrBytes = defaultStderrBytes
	}
	return &Runner{cfg: cfg}
}

// executeParams is the params object of the runner's execute request.
type executeParams struct {
	CallID         string                    `json:"callId"`
	Code           string                    `json:"code"`
	Mode           string                    `json:"mode"`
	Items          []pluginproto.Item        `json:"items"`
	AllowedModules []string                  `json:"allowedModules"`
	MaxOutputBytes int                       `json:"maxOutputBytes"`
	Execution      pluginproto.ExecutionInfo `json:"execution"`
}

// executeResult is the runner's answer.
type executeResult struct {
	Items   []pluginproto.Item   `json:"items"`
	PerItem [][]pluginproto.Item `json:"perItem"`
}

// Run implements codepython.Runner.
func (r *Runner) Run(ctx context.Context, job codepython.Job) (codepython.Result, error) {
	if job.Timeout <= 0 {
		return codepython.Result{}, errors.New("python job needs a positive timeout")
	}
	path, err := exec.LookPath(r.cfg.Interpreter)
	if err != nil {
		return codepython.Result{}, &nodesdk.NodeError{
			Message:     fmt.Sprintf("Python interpreter %q was not found", r.cfg.Interpreter),
			Description: fmt.Sprintf("Install Python 3 or set %s to its path.", InterpreterKey), Err: err,
		}
	}
	request, err := json.Marshal(executeParams{
		CallID: "py", Code: job.Code, Mode: job.Mode, Items: pluginproto.FromItems(job.Items),
		AllowedModules: nonNil(job.AllowedModules), MaxOutputBytes: job.MaxOutputBytes,
		Execution: pluginproto.FromExecutionInfo(job.Execution),
	})
	if err != nil {
		return codepython.Result{}, fmt.Errorf("encoding python job: %w", err)
	}

	runCtx, cancel := context.WithTimeout(ctx, job.Timeout)
	defer cancel()
	child, err := startChild(runCtx, path, r.cfg.MaxStderrBytes)
	if err != nil {
		return codepython.Result{}, fmt.Errorf("starting %q: %w", r.cfg.Interpreter, err)
	}

	limit := max(job.MaxOutputBytes, len(request)) + messageHeadroomBytes
	conn := pluginproto.NewConn(child.stdout, child.stdin, hostHandler(job), pluginproto.ConnOptions{MaxMessageBytes: limit, MaxConcurrent: 1})
	connDone := make(chan error, 1)
	go func() { connDone <- conn.Run(runCtx) }()

	var res executeResult
	callErr := conn.Call(runCtx, pluginproto.MethodExecute, json.RawMessage(request), &res)
	// The runner exits after answering; closing stdin also frees one that
	// stopped listening. Cancel only after the process is told to finish.
	_ = child.stdin.Close()
	if callErr != nil {
		cancel() // make sure a stuck child dies before we wait for it
	}
	connErr := <-connDone
	waitErr := child.wait()

	if callErr != nil {
		return codepython.Result{}, r.classify(ctx, runCtx, job, callErr, connErr, waitErr, child.stderr.String())
	}
	return toResult(job.Mode, res)
}

// classify turns a failed run into the error the node reports.
func (r *Runner) classify(ctx, runCtx context.Context, job codepython.Job, callErr, connErr, waitErr error, stderr string) error {
	switch {
	case ctx.Err() != nil:
		return fmt.Errorf("python run canceled: %w", ctx.Err())
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		return &nodesdk.NodeError{Message: fmt.Sprintf("Python code ran longer than %s", job.Timeout), Err: context.DeadlineExceeded}
	}
	var perr *pluginproto.Error
	if errors.As(callErr, &perr) {
		return perr.NodeError()
	}
	if errors.Is(connErr, pluginproto.ErrMessageTooLarge) || errors.Is(callErr, pluginproto.ErrMessageTooLarge) {
		return &nodesdk.NodeError{Message: fmt.Sprintf("the code returned more than the limit of %d bytes", job.MaxOutputBytes), Err: pluginproto.ErrMessageTooLarge}
	}
	desc := strings.TrimSpace(stderr)
	msg := "the Python process failed"
	if waitErr != nil {
		msg = fmt.Sprintf("the Python process failed: %v", waitErr)
	}
	return &nodesdk.NodeError{Message: msg, Description: desc, Err: callErr}
}

func toResult(mode string, res executeResult) (codepython.Result, error) {
	if mode == codepython.ModeAll {
		return codepython.Result{Items: pluginproto.ToItems(res.Items)}, nil
	}
	per := make([][]nodesdk.Item, len(res.PerItem))
	for i, items := range res.PerItem {
		if len(items) > 0 {
			per[i] = pluginproto.ToItems(items)
		}
	}
	return codepython.Result{PerItem: per}, nil
}

// hostHandler serves the callbacks the runner makes while user code runs.
func hostHandler(job codepython.Job) pluginproto.Handler {
	return func(ctx context.Context, req pluginproto.Request) (any, error) {
		switch req.Method {
		case pluginproto.MethodEvalExpression:
			var p pluginproto.EvalParams
			if err := json.Unmarshal(req.Params, &p); err != nil {
				return nil, &pluginproto.Error{Code: pluginproto.CodeInvalidParams, Message: "invalid expression.eval params"}
			}
			v, err := job.Eval(ctx, p.Expression, p.ItemIndex)
			if err != nil {
				return nil, err
			}
			return pluginproto.EvalResult{Value: v}, nil
		case pluginproto.MethodLog:
			var p pluginproto.LogParams
			if err := json.Unmarshal(req.Params, &p); err == nil && job.Log != nil {
				job.Log(p.Message)
			}
			return nil, nil
		}
		return nil, &pluginproto.Error{Code: pluginproto.CodeMethodNotFound, Message: "unknown method " + req.Method}
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// cappedBuffer keeps the first limit bytes written to it.
type cappedBuffer struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	limit int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if room := c.limit - c.buf.Len(); room > 0 {
		c.buf.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

func (c *cappedBuffer) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}
