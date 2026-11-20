package pyrunner

import (
	"context"
	"fmt"
	"io"
	"os/exec"
)

// child is one running interpreter process.
type child struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr *cappedBuffer
}

// startChild starts `python -I -c <runner script>` with an empty environment.
// ctx cancellation kills the process.
func startChild(ctx context.Context, interpreter string, stderrBytes int) (*child, error) {
	cmd := exec.CommandContext(ctx, interpreter, "-I", "-c", runnerScript)
	cmd.Env = []string{} // non-nil and empty: the child inherits nothing
	cmd.WaitDelay = killWaitDelay
	c := &child{cmd: cmd, stderr: &cappedBuffer{limit: stderrBytes}}
	cmd.Stderr = c.stderr
	var err error
	if c.stdin, err = cmd.StdinPipe(); err != nil {
		return nil, fmt.Errorf("opening stdin: %w", err)
	}
	if c.stdout, err = cmd.StdoutPipe(); err != nil {
		return nil, fmt.Errorf("opening stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting process: %w", err)
	}
	return c, nil
}

// wait reaps the process. Call it after the stdout reader has finished.
func (c *child) wait() error {
	if err := c.cmd.Wait(); err != nil {
		return fmt.Errorf("waiting for process: %w", err)
	}
	return nil
}
