package trigger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/service/execution"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// pollNode is a polling trigger of a workflow with its parsed settings.
type pollNode struct {
	node domain.Node
	impl nodesdk.PollTrigger
	spec nodesdk.PollSpec
}

func (p pollNode) fingerprint() string {
	b, _ := json.Marshal(struct {
		Type    string
		Version int
		Params  map[string]any
		Creds   map[string]domain.CredentialID
	}{p.node.Type, p.node.TypeVersion, p.node.Parameters, p.node.Credentials})
	return string(b)
}

// poller is a running pollNode. It lives while this process leads.
type poller struct {
	workflow domain.WorkflowID
	pollNode
	fp     string
	cancel context.CancelFunc
	done   chan struct{}
}

// setPollers makes the running pollers of a workflow match nodes. It does
// nothing unless this process leads; a new leader loads pollers itself.
func (s *Service) setPollers(id domain.WorkflowID, nodes []pollNode) {
	s.mu.Lock()
	if !s.leading || s.leadCtx == nil {
		s.mu.Unlock()
		return
	}
	running := s.pollers[id]
	delete(s.pollers, id)
	want := map[string]pollNode{}
	for _, n := range nodes {
		want[n.node.Name] = n
	}
	var keep, stop []*poller
	for _, p := range running {
		if n, ok := want[p.node.Name]; ok && n.fingerprint() == p.fp {
			keep = append(keep, p)
			delete(want, p.node.Name)
			continue
		}
		stop = append(stop, p)
	}
	s.mu.Unlock()
	for _, p := range stop {
		stopPoller(p, true)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.leading || s.leadCtx == nil {
		return
	}
	list := keep
	for _, n := range nodes {
		if _, fresh := want[n.node.Name]; fresh {
			list = append(list, s.startPoller(id, n))
		}
	}
	if len(list) > 0 {
		s.pollers[id] = list
	}
}

// stopPollers ends the pollers of one workflow and waits for them.
func (s *Service) stopPollers(id domain.WorkflowID) {
	s.mu.Lock()
	running := s.pollers[id]
	delete(s.pollers, id)
	s.mu.Unlock()
	for _, p := range running {
		stopPoller(p, true)
	}
}

// stopAllPollers ends every poller, for the end of leadership.
func (s *Service) stopAllPollers() {
	s.mu.Lock()
	all := s.pollers
	s.pollers = map[domain.WorkflowID][]*poller{}
	s.mu.Unlock()
	for _, list := range all {
		for _, p := range list {
			stopPoller(p, true)
		}
	}
}

func stopPoller(p *poller, wait bool) {
	p.cancel()
	if wait {
		<-p.done
	}
}

// startPoller runs n until it is stopped. s.mu must be held and s.leadCtx set.
func (s *Service) startPoller(id domain.WorkflowID, n pollNode) *poller {
	ctx, cancel := context.WithCancel(s.leadCtx)
	p := &poller{workflow: id, pollNode: n, fp: n.fingerprint(), cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(p.done)
		s.runPoller(ctx, p)
	}()
	return p
}

// runPoller polls at the node's interval. After a failure it waits twice as
// long as before (capped by poll.maxBackoff); poll.maxFailures failures in a
// row deactivate the workflow with the last error recorded.
func (s *Service) runPoller(ctx context.Context, p *poller) {
	log := s.d.Logger.With("workflow", p.workflow, "node", p.node.Name)
	failures := 0
	for ctx.Err() == nil {
		delay := p.spec.Interval
		if err := s.pollOnce(ctx, p); err != nil {
			if ctx.Err() != nil {
				return
			}
			failures++
			s.d.Metrics.TriggerError("poll")
			log.Error("poll failed", "error", err, "failures", failures)
			if failures >= s.cfg.Poll.MaxFailures {
				s.deactivateFailedPoller(ctx, p, err)
				return
			}
			delay = backoff(p.spec.Interval, failures, s.cfg.Poll.MaxBackoff)
		} else {
			failures = 0
		}
		if err := s.d.Sleeper.Sleep(ctx, delay); err != nil {
			return
		}
	}
}

// backoff doubles base per failure, up to limit.
func backoff(base time.Duration, failures int, limit time.Duration) time.Duration {
	d := base
	for i := 1; i < failures && d < limit; i++ {
		d *= 2
	}
	return min(d, limit)
}

// deactivateFailedPoller marks the workflow inactive and records why. It runs
// on the poller's own goroutine, so it never waits for the pollers to stop.
func (s *Service) deactivateFailedPoller(ctx context.Context, p *poller, cause error) {
	log := s.d.Logger.With("workflow", p.workflow, "node", p.node.Name)
	log.Error("deactivating the workflow after repeated poll failures", "error", cause)
	msg := fmt.Sprintf("trigger %q failed %d polls in a row: %v", p.node.Name, s.cfg.Poll.MaxFailures, cause)
	// The leader context may end first; the record must still be written.
	wctx := context.WithoutCancel(ctx)
	if err := s.d.Workflows.SetActive(wctx, p.workflow, false); err != nil && !errors.Is(err, domain.ErrNotFound) {
		log.Error("marking the workflow inactive", "error", err)
	}
	if err := s.d.Activations.SetActivationError(wctx, p.workflow, msg); err != nil && !errors.Is(err, domain.ErrNotFound) {
		log.Error("recording the activation error", "error", err)
	}
	s.publish(wctx, port.EventWorkflowDeactivated, p.workflow)
}

// pollOnce runs one poll: load the cursor, poll, start an execution per item,
// store the cursor, then let the node finish (Done).
func (s *Service) pollOnce(ctx context.Context, p *poller) error {
	cursor, err := s.d.States.LoadTriggerState(ctx, p.workflow, p.node.Name)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("loading poll cursor: %w", err)
	}
	creds := make(map[string]nodesdk.Credential, len(p.node.Credentials))
	for slot, cid := range p.node.Credentials {
		c, err := s.d.Credentials.Resolve(ctx, cid)
		if err != nil {
			return fmt.Errorf("loading credential %s: %w", cid, err)
		}
		creds[slot] = nodesdk.Credential{Type: c.Type, ID: c.ID, Version: c.Version, Data: c.Data, Auth: c.Auth}
	}
	in := nodesdk.PollInput{
		WorkflowID: string(p.workflow), NodeName: p.node.Name, Params: p.node.Parameters, Credentials: creds,
		Logger: s.d.Logger.With("workflow", p.workflow, "node", p.node.Name),
	}
	if s.d.Blobs != nil && s.d.IDs != nil {
		in.Blobs = blobAllocator{ids: s.d.IDs, blobs: s.d.Blobs}
	}
	res, err := p.impl.Poll(ctx, in, cursor)
	if err != nil {
		return fmt.Errorf("polling: %w", err)
	}
	for _, it := range res.Items {
		id, err := s.d.Executions.Start(ctx, execution.StartRequest{
			WorkflowID: p.workflow, Mode: domain.ModeTrigger, StartNode: p.node.Name, ExecutionID: domain.ExecutionID(it.ExecutionID),
			Items: []domain.Item{{JSON: it.Item.JSON, Binary: binaryRefs(it.Item.Binary)}}, DedupKey: p.node.Name + ":" + it.DedupKey,
		})
		if it.ExecutionID != "" && (err != nil || id != domain.ExecutionID(it.ExecutionID)) {
			s.dropBlobs(ctx, domain.ExecutionID(it.ExecutionID))
		}
		if err != nil && !errors.Is(err, domain.ErrDuplicate) {
			return fmt.Errorf("starting execution: %w", err)
		}
	}
	if res.Next != nil {
		if err := s.d.States.SaveTriggerState(ctx, p.workflow, p.node.Name, res.Next); err != nil {
			return fmt.Errorf("saving poll cursor: %w", err)
		}
	}
	if res.Done != nil {
		if err := res.Done(ctx); err != nil {
			in.Logger.Warn("finishing a poll failed; the items are not repeated", "error", err)
		}
	}
	return nil
}
