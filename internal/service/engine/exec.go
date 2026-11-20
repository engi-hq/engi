package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// execute runs a node with its retry policy and per-attempt timeout. On
// success it returns the output ports; if the node asks to wait it sets the
// run status to waiting and records the pending state.
func (r *runner) execute(ctx context.Context, info *nodeInfo, task Task, run *domain.NodeRun) ([][]domain.Item, error) {
	node := info.node
	inst, err := r.e.d.Registry.Lookup(node.Type, node.TypeVersion)
	if err != nil {
		return nil, &domain.WorkflowError{Node: node.Name, Message: "unknown node type", Err: err}
	}
	inputs := task.Inputs
	if node.Settings.ExecuteOnce {
		inputs = firstItemOnly(inputs)
	}
	timeout := r.e.cfg.DefaultNodeTimeout
	if node.Settings.Timeout > 0 {
		timeout = node.Settings.Timeout
	}
	attempts := max(1, node.Settings.Retry.MaxAttempts)
	if err := r.checkRequiredSubPorts(info); err != nil {
		return nil, err
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		run.Attempts = attempt
		out, err := r.attempt(ctx, inst, info, task, inputs, run.RunIndex, attempt, timeout)
		if err == nil {
			return r.finishOutput(info, inputs, out, run)
		}
		lastErr = err
		if ctx.Err() != nil || attempt == attempts {
			break
		}
		delay := retryDelay(node.Settings.Retry, attempt, r.e.cfg.MaxRetryDelay)
		if serr := r.e.d.Sleeper.Sleep(ctx, delay); serr != nil {
			return nil, fmt.Errorf("waiting to retry: %w", serr)
		}
	}
	return nil, lastErr
}

// attempt performs one call of Execute under its own deadline and converts
// panics and timeouts into errors.
func (r *runner) attempt(ctx context.Context, inst nodesdk.Node, info *nodeInfo, task Task, inputs [][]domain.Item,
	runIndex, attempt int, timeout time.Duration,
) (out nodesdk.Output, err error) {
	nctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("node panicked: %v", p)
		}
		if err != nil && ctx.Err() == nil && errors.Is(nctx.Err(), context.DeadlineExceeded) {
			err = fmt.Errorf("node timed out after %s: %w", timeout, context.DeadlineExceeded)
		}
	}()
	in := newNodeInput(r, info, task, inputs, runIndex, attempt)
	return inst.Execute(nctx, in) //nolint:wrapcheck // node errors are classified by the caller
}

// finishOutput validates the node's output and converts it to domain items.
func (r *runner) finishOutput(info *nodeInfo, inputs [][]domain.Item, out nodesdk.Output, run *domain.NodeRun) ([][]domain.Item, error) {
	name := info.node.Name
	if declared := info.errorPort(); len(out.Ports) > declared {
		return nil, &domain.WorkflowError{Node: name, Message: fmt.Sprintf("node returned %d output ports but declares %d", len(out.Ports), declared)}
	}
	ports := make([][]domain.Item, len(out.Ports))
	for i, p := range out.Ports {
		ports[i] = fromSDKItems(p, name)
		dropStalePairing(ports[i], inputs)
		assignPairing(ports[i], inputs, len(out.Ports) == 1)
	}
	if out.Response != nil {
		run.Response = &domain.WebhookResponse{Status: out.Response.Status, Headers: out.Response.Headers, Body: out.Response.Body}
	}
	if out.Wait != nil {
		if out.Wait.Until.IsZero() && out.Wait.ResumeToken == "" {
			return nil, &domain.WorkflowError{Node: name, Message: "node asked to wait without a time or resume token"}
		}
		run.Status = domain.NodeRunWaiting
		r.st.Pending = &PendingWait{Node: name, RunIndex: run.RunIndex, Until: out.Wait.Until, ResumeToken: out.Wait.ResumeToken, Output: ports}
	}
	return ports, nil
}

func firstItemOnly(inputs [][]domain.Item) [][]domain.Item {
	out := make([][]domain.Item, len(inputs))
	for i, p := range inputs {
		if len(p) > 1 {
			p = p[:1]
		}
		out[i] = p
	}
	return out
}

// retryDelay is the pause after a failed attempt (1-based), capped at limit.
func retryDelay(p domain.RetryPolicy, failedAttempt int, limit time.Duration) time.Duration {
	d := p.InitialDelay
	if p.Backoff == domain.BackoffExponential {
		for i := 1; i < failedAttempt && d < limit; i++ {
			d *= 2
		}
	}
	return min(d, limit)
}
