package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"strings"
	"time"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// errorKey is the json field that holds an item error message.
const errorKey = "error"

var errExecutionTimeout = errors.New("execution timeout")

// runner holds the mutable state of one Run call.
type runner struct {
	e       *Engine
	req     Request
	g       *graph
	st      *State
	pins    map[string][]domain.Item
	needed  map[string]bool // nil unless a partial run
	resumed bool
	dest    string
	obs     Observer
}

func (e *Engine) newRunner(req Request) (*runner, error) {
	if req.Depth > e.cfg.MaxSubworkflowDepth {
		return nil, &domain.WorkflowError{Message: fmt.Sprintf("sub-workflow depth %d exceeds the limit of %d", req.Depth, e.cfg.MaxSubworkflowDepth)}
	}
	g, err := buildGraph(&req.Workflow, e.d.Registry)
	if err != nil {
		return nil, err
	}
	r := &runner{e: e, req: req, g: g, pins: map[string][]domain.Item{}, obs: e.d.Observer}
	if req.Observer != nil {
		r.obs = req.Observer
	}
	if req.Mode == domain.ModeManual {
		for k, v := range req.Workflow.PinData {
			r.pins[k] = v
		}
	}
	for k, v := range req.PinData {
		r.pins[k] = v
	}

	if req.Destination != nil {
		r.dest = req.Destination.Node
		if _, ok := g.nodes[r.dest]; !ok {
			return nil, &domain.WorkflowError{Node: r.dest, Message: "destination node does not exist"}
		}
		if g.nodes[r.dest].isSub() {
			return nil, &domain.WorkflowError{Node: r.dest, Message: "a sub-node cannot be a destination; run the node it supplies instead"}
		}
		r.needed = g.ancestors(r.dest)
		if req.Destination.Exclusive {
			delete(r.needed, r.dest)
		}
	}

	if req.Resume != nil {
		if req.Resume.Status != domain.StatusWaiting || req.Resume.Pending == nil {
			return nil, &domain.WorkflowError{Message: "only a waiting execution can be resumed"}
		}
		if _, ok := g.nodes[req.Resume.Pending.Node]; !ok {
			return nil, &domain.WorkflowError{Node: req.Resume.Pending.Node, Message: "waiting node no longer exists"}
		}
		if req.Resume.ResumeSecret == "" {
			secret, err := backfillResumeSecret(req.Resume, e.d.Random)
			if err != nil {
				return nil, err
			}
			req.Resume.ResumeSecret = secret
		}
		r.st = req.Resume
		r.resumed = true
		return r, nil
	}

	if _, ok := g.nodes[req.StartNode]; !ok {
		return nil, &domain.WorkflowError{Node: req.StartNode, Message: "start node does not exist"}
	}
	if g.nodes[req.StartNode].isSub() {
		return nil, &domain.WorkflowError{Node: req.StartNode, Message: "a sub-node cannot be a start node"}
	}
	if r.dest != "" && !g.reaches(req.StartNode, r.dest) {
		return nil, &domain.WorkflowError{Node: r.dest, Message: "destination is not reachable from the start node"}
	}
	items := req.StartItems
	if len(items) == 0 {
		items = []domain.Item{{JSON: map[string]any{}}}
	}
	start := g.nodes[req.StartNode]
	inputs := make([][]domain.Item, start.inputs())
	inputs[0] = items
	secret, err := nodesdk.NewResumeSecret(e.d.Random)
	if err != nil {
		return nil, err
	}
	r.st = &State{
		ExecutionID:  req.ExecutionID,
		Status:       domain.StatusRunning,
		ResumeSecret: secret,
		Waiting:      map[string]WaitingInputs{},
		RunData:      map[string][]domain.NodeRun{},
		Queue:        []Task{{Node: req.StartNode, Inputs: inputs, Sources: make([]domain.Source, start.inputs())}},
	}
	return r, nil
}

func (r *runner) timeout() time.Duration {
	if t := r.req.Workflow.Settings.Timeout; t > 0 {
		return t
	}
	return r.e.cfg.ExecutionTimeout
}

func (r *runner) run(ctx context.Context) *State {
	ctx = r.e.d.Tracer.Extract(ctx, r.req.TraceParent)
	ctx, span := r.e.d.Tracer.Start(ctx, "engi.execution",
		port.StrAttr("engi.workflow.id", string(r.req.Workflow.ID)),
		port.StrAttr("engi.execution.id", string(r.st.ExecutionID)),
		port.StrAttr("engi.execution.mode", string(r.req.Mode)))
	defer span.End()
	obs := r.obs
	now := r.e.d.Clock.Now()
	if !r.resumed {
		r.st.StartedAt = now
	}
	r.st.Status = domain.StatusRunning
	if !r.resumed {
		r.e.d.Metrics.ExecutionStarted(r.req.Mode)
	}
	obs.ExecutionStarted(ctx, ExecutionInfo{
		ExecutionID: r.st.ExecutionID, WorkflowID: r.req.Workflow.ID, Mode: r.req.Mode,
		StartedAt: r.st.StartedAt, Resumed: r.resumed,
	})

	execCtx, cancel := context.WithTimeoutCause(ctx, r.timeout(), errExecutionTimeout)
	defer cancel()

	if r.resumed {
		if err := r.completeWait(execCtx); err != nil {
			r.fail(err)
		}
	}
	for r.st.Status == domain.StatusRunning {
		if err := execCtx.Err(); err != nil {
			r.interrupted(execCtx)
			break
		}
		task, ok := r.pop()
		if !ok {
			if r.flush() {
				continue
			}
			r.st.Status = domain.StatusSuccess
			break
		}
		if err := r.step(execCtx, task); err != nil {
			if execCtx.Err() != nil {
				r.interrupted(execCtx)
			} else {
				r.fail(err)
			}
		}
	}

	if r.st.Status != domain.StatusWaiting {
		r.st.Queue, r.st.Waiting = nil, map[string]WaitingInputs{}
		r.st.FinishedAt = r.e.d.Clock.Now()
	}
	span.SetAttrs(port.StrAttr("engi.execution.status", string(r.st.Status)))
	if r.st.Status == domain.StatusError || r.st.Status == domain.StatusCrashed {
		span.Fail(string(r.st.Status))
	}
	if r.st.Status != domain.StatusWaiting {
		r.e.d.Metrics.ExecutionFinished(r.req.Mode, r.st.Status, r.st.FinishedAt.Sub(r.st.StartedAt))
	}
	obs.ExecutionFinished(ctx, Result{
		ExecutionID: r.st.ExecutionID, Status: r.st.Status, Error: r.st.Error,
		StartedAt: r.st.StartedAt, FinishedAt: r.st.FinishedAt, Pending: r.st.Pending,
	})
	return r.st
}

// fail records err as the execution's failure.
func (r *runner) fail(err error) {
	r.st.Status = domain.StatusError
	var ne *domain.NodeError
	var we *domain.WorkflowError
	switch {
	case errors.As(err, &ne):
		info := ne.Info()
		r.st.Error = &info
	case errors.As(err, &we):
		info := we.Info()
		r.st.Error = &info
	default:
		r.st.Error = &domain.ErrorInfo{Kind: domain.ErrorKindWorkflow, Message: err.Error()}
	}
}

// interrupted records a cancellation or execution timeout.
func (r *runner) interrupted(ctx context.Context) {
	if errors.Is(context.Cause(ctx), errExecutionTimeout) {
		r.st.Status = domain.StatusError
		r.st.Error = &domain.ErrorInfo{Kind: domain.ErrorKindTimeout, Message: fmt.Sprintf("execution exceeded %s", r.timeout())}
		return
	}
	r.st.Status = domain.StatusCanceled
	r.st.Error = &domain.ErrorInfo{Kind: domain.ErrorKindCanceled, Message: "execution canceled"}
}

func (r *runner) pop() (Task, bool) {
	n := len(r.st.Queue)
	if n == 0 {
		return Task{}, false
	}
	t := r.st.Queue[n-1]
	r.st.Queue = r.st.Queue[:n-1]
	return t, true
}

// flush releases the first waiting node (in canvas order) that accepts partial
// input. It reports whether a task was queued.
func (r *runner) flush() bool {
	for _, name := range r.g.canvas {
		w, ok := r.st.Waiting[name]
		if !ok || r.g.nodes[name].desc.MergePolicy != nodesdk.MergePartialOnFlush {
			continue
		}
		delete(r.st.Waiting, name)
		r.st.Queue = append(r.st.Queue, Task{Node: name, Inputs: w.Inputs, Sources: w.Sources})
		return true
	}
	return false
}

// completeWait finishes the node a resumed execution was paused on and routes
// its output.
func (r *runner) completeWait(ctx context.Context) error {
	p := r.st.Pending
	runs := r.st.RunData[p.Node]
	if p.RunIndex >= len(runs) {
		return &domain.WorkflowError{Node: p.Node, Message: "waiting run is missing from the saved state"}
	}
	out := p.Output
	resumeItems, replace := r.req.ResumeItems, r.req.ResumeItems != nil
	if p.Resumed {
		resumeItems, replace = p.ResumeItems, true
	}
	if replace {
		if len(out) == 0 {
			out = make([][]domain.Item, 1)
		}
		out[0] = append([]domain.Item(nil), resumeItems...)
	}
	run := &runs[p.RunIndex]
	if replace {
		// Items from the resume call are new data: pair them to the node's input.
		for i := range out[0] {
			out[0][i].PairedItem = nil
		}
		assignPairing(out[0], run.Input[domain.PortMain], len(out) == 1)
	}
	run.Status = domain.NodeRunSuccess
	run.Output = domain.PortData{domain.PortMain: out}
	r.st.Pending = nil
	r.obs.NodeFinished(ctx, p.Node, *run)
	r.route(p.Node, p.RunIndex, out)
	return nil
}

// step runs one task: it executes (or skips) the node, records the run and
// routes the output.
func (r *runner) step(ctx context.Context, task Task) error {
	info := r.g.nodes[task.Node]
	node := info.node
	runIndex := len(r.st.RunData[task.Node])
	if runIndex >= r.e.cfg.MaxNodeRuns {
		return &domain.WorkflowError{Node: task.Node, Message: fmt.Sprintf("node ran %d times, the limit is %d (possible infinite loop)", runIndex, r.e.cfg.MaxNodeRuns)}
	}
	started := r.e.d.Clock.Now()
	run := domain.NodeRun{
		RunIndex: runIndex, StartedAt: started, Sources: task.Sources,
		Input: domain.PortData{domain.PortMain: task.Inputs},
	}
	ctx, span := r.e.d.Tracer.Start(ctx, "engi.node",
		port.StrAttr("engi.workflow.id", string(r.req.Workflow.ID)),
		port.StrAttr("engi.execution.id", string(r.st.ExecutionID)),
		port.StrAttr("engi.node.name", task.Node),
		port.StrAttr("engi.node.type", node.Type),
		port.IntAttr("engi.node.run_index", runIndex))
	defer func() {
		span.SetAttrs(port.StrAttr("engi.node.status", string(run.Status)))
		if run.Status == domain.NodeRunError {
			span.Fail("node failed")
		}
		span.End()
		if run.Status != domain.NodeRunWaiting {
			r.e.d.Metrics.NodeFinished(node.Type, run.Duration)
		}
	}()
	r.obs.NodeStarted(ctx, task.Node, runIndex)

	var outputs [][]domain.Item
	var stepErr error
	switch pin, pinned := r.pins[task.Node]; {
	case node.Disabled:
		run.Status = domain.NodeRunDisabled
		outputs = [][]domain.Item{stampLineage(task.Inputs[0], 0)}
	case pinned:
		run.Status = domain.NodeRunPinned
		outputs = [][]domain.Item{pinnedItems(pin, task.Inputs)}
	default:
		outputs, stepErr = r.execute(ctx, info, task, &run)
	}

	var failure *domain.ErrorInfo
	if stepErr != nil {
		if ctx.Err() != nil {
			run.Status, run.Error = domain.NodeRunError, &domain.ErrorInfo{Kind: domain.ErrorKindCanceled, Node: task.Node, Message: stepErr.Error()}
			r.record(ctx, task.Node, &run, started)
			return stepErr
		}
		var we *domain.WorkflowError
		if errors.As(stepErr, &we) {
			run.Status, run.Error = domain.NodeRunError, &domain.ErrorInfo{Kind: domain.ErrorKindWorkflow, Node: task.Node, Message: we.Message}
			r.record(ctx, task.Node, &run, started)
			return we
		}
		ne := r.toNodeError(task.Node, stepErr)
		fi := ne.Info()
		failure = &fi
		run.Status, run.Error = domain.NodeRunError, failure
		switch node.Settings.OnError {
		case domain.OnErrorContinue:
			outputs = [][]domain.Item{errorItems(fi, task.Inputs[0], false)}
		case domain.OnErrorErrorOutput:
			outputs = make([][]domain.Item, info.errorPort()+1)
			outputs[info.errorPort()] = errorItems(fi, task.Inputs[0], true)
		default:
			r.record(ctx, task.Node, &run, started)
			return ne
		}
	} else if run.Status == "" {
		run.Status = domain.NodeRunSuccess
		if node.Settings.OnError == domain.OnErrorErrorOutput {
			outputs = routeItemErrors(outputs, info.errorPort(), task.Inputs[0])
		}
	}

	// Only the first output decides: an empty output 0 gets one
	// empty item even when another output has data.
	if node.Settings.AlwaysOutputData && (len(outputs) == 0 || len(outputs[0]) == 0) {
		if len(outputs) == 0 {
			outputs = make([][]domain.Item, 1)
		}
		outputs[0] = []domain.Item{{JSON: map[string]any{}, PairedItem: pairToAll(task.Inputs)}}
	}
	run.Output = domain.PortData{domain.PortMain: outputs}

	if run.Status == domain.NodeRunWaiting {
		r.record(ctx, task.Node, &run, started)
		r.st.Status = domain.StatusWaiting
		return nil
	}
	r.record(ctx, task.Node, &run, started)
	if task.Node == r.dest {
		return nil // partial run: the destination's children do not run
	}
	r.route(task.Node, runIndex, outputs)
	return nil
}

func (r *runner) record(ctx context.Context, node string, run *domain.NodeRun, started time.Time) {
	run.Duration = r.e.d.Clock.Now().Sub(started)
	r.st.RunData[node] = append(r.st.RunData[node], *run)
	r.st.Order = append(r.st.Order, RunRef{Node: node, RunIndex: run.RunIndex})
	r.obs.NodeFinished(ctx, node, *run)
}

func (r *runner) toNodeError(node string, err error) *domain.NodeError {
	var ne *domain.NodeError
	if errors.As(err, &ne) {
		return ne
	}
	var sdk *nodesdk.NodeError
	if errors.As(err, &sdk) {
		return &domain.NodeError{Node: node, Message: sdk.Error(), Description: sdk.Description, HTTPStatus: sdk.HTTPStatus, ItemIndex: sdk.ItemIndex, Err: err}
	}
	var xe *domain.ExpressionError
	if errors.As(err, &xe) {
		idx := xe.ItemIndex
		return &domain.NodeError{Node: node, Message: xe.Error(), Description: xe.Stack, ItemIndex: &idx, Err: err}
	}
	return &domain.NodeError{Node: node, Message: err.Error(), Err: err}
}

// errorItems builds one error item per failing input item, or a single one.
// The item's json holds the message under "error". With keepInput (the error
// output) the failing input item's json is kept next to it.
func errorItems(info domain.ErrorInfo, inputs []domain.Item, keepInput bool) []domain.Item {
	build := func(in *domain.Item) domain.Item {
		e := info
		js := map[string]any{}
		if keepInput && in != nil {
			js = cloneMap(in.JSON)
		}
		js[errorKey] = e.Message
		return domain.Item{JSON: js, Error: &e}
	}
	if len(inputs) == 0 {
		return []domain.Item{build(nil)}
	}
	out := make([]domain.Item, len(inputs))
	for i := range inputs {
		out[i] = build(&inputs[i])
		out[i].PairedItem = []domain.ItemRef{{Input: 0, Item: i}}
	}
	return out
}

// routeItemErrors moves items that carry an error out of the regular outputs
// onto the error port, merging in the input item they came from.
func routeItemErrors(outputs [][]domain.Item, errPort int, inputs []domain.Item) [][]domain.Item {
	res := make([][]domain.Item, errPort+1)
	for p, items := range outputs {
		for _, it := range items {
			if it.Error == nil {
				res[p] = append(res[p], it)
				continue
			}
			js := map[string]any{}
			if len(it.PairedItem) > 0 {
				if ref := it.PairedItem[0]; ref.Input == 0 && ref.Item >= 0 && ref.Item < len(inputs) {
					js = cloneMap(inputs[ref.Item].JSON)
				}
			}
			maps.Copy(js, it.JSON)
			it.JSON = js
			res[errPort] = append(res[errPort], it)
		}
	}
	return res
}

// route delivers a node's output to its children and queues the resulting tasks
// so that the first child (in canvas order) runs first, depth-first.
func (r *runner) route(node string, runIndex int, outputs [][]domain.Item) {
	info := r.g.nodes[node]
	var ready []Task
	for _, d := range r.g.deliveries(info, outputs) {
		if r.needed != nil && !r.needed[d.to] {
			continue
		}
		src := domain.Source{Node: node, OutputIndex: d.fromPort, RunIndex: runIndex}
		if t, ok := r.deliver(d, src); ok {
			ready = append(ready, t)
		}
	}
	for i := len(ready) - 1; i >= 0; i-- {
		r.st.Queue = append(r.st.Queue, ready[i])
	}
}

// deliver hands items to a target input and returns a task if it is ready.
func (r *runner) deliver(d delivery, src domain.Source) (Task, bool) {
	target := r.g.nodes[d.to]
	n := target.inputs()
	if !target.multiInput() {
		return Task{Node: d.to, Inputs: [][]domain.Item{d.items}, Sources: []domain.Source{src}}, true
	}
	if target.desc.MergePolicy == nodesdk.MergeAny {
		inputs, sources := make([][]domain.Item, n), make([]domain.Source, n)
		inputs[d.toIndex], sources[d.toIndex] = d.items, src
		return Task{Node: d.to, Inputs: inputs, Sources: sources}, true
	}
	w, ok := r.st.Waiting[d.to]
	if !ok {
		w = WaitingInputs{Inputs: make([][]domain.Item, n), Sources: make([]domain.Source, n), Filled: make([]bool, n)}
	}
	if w.Filled[d.toIndex] {
		w.Inputs[d.toIndex] = append(append([]domain.Item(nil), w.Inputs[d.toIndex]...), d.items...)
	} else {
		w.Inputs[d.toIndex], w.Sources[d.toIndex], w.Filled[d.toIndex] = d.items, src, true
	}
	for idx := range target.connected {
		if !w.Filled[idx] {
			r.st.Waiting[d.to] = w
			return Task{}, false
		}
	}
	delete(r.st.Waiting, d.to)
	return Task{Node: d.to, Inputs: w.Inputs, Sources: w.Sources}, true
}

// backfillResumeSecret gives a state saved before resume secrets existed the
// secret of its pending token, so a resume URL that was already handed out keeps
// working. A state without a token draws a new secret. The caller stores it with
// the next save of the state.
func backfillResumeSecret(st *State, random io.Reader) (string, error) {
	if st.Pending != nil && st.Pending.ResumeToken != "" {
		if _, secret, ok := strings.Cut(st.Pending.ResumeToken, nodesdk.ResumeTokenSeparator); ok && secret != "" {
			return secret, nil
		}
	}
	return nodesdk.NewResumeSecret(random)
}

// resumeURL is the absolute URL that resumes the execution's waiting Wait node,
// or "" when no base is configured.
func resumeURL(base string, st *State) string {
	if base == "" || st.ResumeSecret == "" {
		return ""
	}
	return base + "/" + nodesdk.NewResumeToken(string(st.ExecutionID), st.ResumeSecret)
}
