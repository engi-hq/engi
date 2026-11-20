package trigger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/service/execution"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// listenNode is a listener trigger of a workflow.
type listenNode struct {
	node domain.Node
	impl nodesdk.ListenTrigger
}

// listener is a running listenNode. Listeners are not leader-gated: group-based
// sources balance their consumers themselves, so every instance runs them.
type listener struct {
	workflow    domain.WorkflowID
	node        string
	fingerprint string
	cancel      context.CancelFunc
	done        chan struct{}
}

// fingerprint identifies the saved configuration of a listener node, so that an
// activation event for an unchanged node leaves the running consumer alone.
func (l listenNode) fingerprint() string {
	b, _ := json.Marshal(struct {
		Type    string
		Version int
		Params  map[string]any
		Creds   map[string]domain.CredentialID
	}{l.node.Type, l.node.TypeVersion, l.node.Parameters, l.node.Credentials})
	return string(b)
}

// setListeners makes the running listeners of a workflow match nodes. With
// force every listener restarts (an explicit activation re-reads credentials);
// otherwise unchanged ones keep running. Nothing starts before Start.
func (s *Service) setListeners(id domain.WorkflowID, nodes []listenNode, force bool) {
	s.lmu.Lock()
	running := s.listeners[id]
	delete(s.listeners, id)
	var keep, stop []*listener
	want := map[string]listenNode{}
	for _, n := range nodes {
		want[n.node.Name] = n
	}
	for _, l := range running {
		if n, ok := want[l.node]; ok && !force && n.fingerprint() == l.fingerprint {
			keep = append(keep, l)
			delete(want, l.node)
			continue
		}
		stop = append(stop, l)
	}
	s.lmu.Unlock()

	for _, l := range stop {
		stopListener(l)
	}
	s.lmu.Lock()
	defer s.lmu.Unlock()
	list := keep
	if s.listenCtx != nil {
		for _, n := range nodes {
			if _, fresh := want[n.node.Name]; fresh {
				list = append(list, s.startListener(id, n))
			}
		}
	}
	if len(list) > 0 {
		s.listeners[id] = list
	}
}

// stopListeners ends every listener of a workflow and waits for them to leave
// their source.
func (s *Service) stopListeners(id domain.WorkflowID) {
	s.lmu.Lock()
	running := s.listeners[id]
	delete(s.listeners, id)
	s.lmu.Unlock()
	for _, l := range running {
		stopListener(l)
	}
}

func stopListener(l *listener) {
	l.cancel()
	<-l.done
}

// startListener runs n until it is stopped. s.lmu must be held and s.listenCtx set.
func (s *Service) startListener(id domain.WorkflowID, n listenNode) *listener {
	ctx, cancel := context.WithCancel(s.listenCtx)
	l := &listener{workflow: id, node: n.node.Name, fingerprint: n.fingerprint(), cancel: cancel, done: make(chan struct{})}
	s.listenWG.Add(1)
	go func() {
		defer s.listenWG.Done()
		defer close(l.done)
		s.runListener(ctx, id, n)
	}()
	return l
}

// runListener keeps the listener running: after a failure it waits
// listen.retryDelay and starts it again.
func (s *Service) runListener(ctx context.Context, id domain.WorkflowID, n listenNode) {
	log := s.d.Logger.With("workflow", id, "node", n.node.Name)
	for ctx.Err() == nil {
		err := s.listenOnce(ctx, id, n)
		if ctx.Err() != nil {
			return
		}
		s.d.Metrics.TriggerError("listener")
		log.Error("listener stopped; restarting", "error", err, "delay", s.cfg.Listen.RetryDelay)
		if err := s.d.Sleeper.Sleep(ctx, s.cfg.Listen.RetryDelay); err != nil {
			return
		}
	}
}

func (s *Service) listenOnce(ctx context.Context, id domain.WorkflowID, n listenNode) error {
	creds := make(map[string]nodesdk.Credential, len(n.node.Credentials))
	for slot, cid := range n.node.Credentials {
		c, err := s.d.Credentials.Resolve(ctx, cid)
		if err != nil {
			return fmt.Errorf("loading credential %s: %w", cid, err)
		}
		creds[slot] = nodesdk.Credential{Type: c.Type, ID: c.ID, Version: c.Version, Data: c.Data, Auth: c.Auth}
	}
	in := nodesdk.ListenInput{
		WorkflowID: string(id), NodeName: n.node.Name, Params: n.node.Parameters, Credentials: creds,
		Logger: s.d.Logger.With("workflow", id, "node", n.node.Name), Overloaded: s.Overloaded,
	}
	emit := func(ctx context.Context, e nodesdk.ListenEvent) error { return s.emit(ctx, id, n.node.Name, e) }
	if err := n.impl.Listen(ctx, in, emit); err != nil {
		return fmt.Errorf("listening: %w", err)
	}
	return nil
}

// emit starts the execution of one event. The dedup key is scoped to the node,
// and a repeated key is a success: the first delivery already started it.
func (s *Service) emit(ctx context.Context, id domain.WorkflowID, node string, e nodesdk.ListenEvent) error {
	items := make([]domain.Item, len(e.Items))
	for i, it := range e.Items {
		items[i] = domain.Item{JSON: it.JSON, Binary: binaryRefs(it.Binary)}
	}
	execID, err := s.d.Executions.Start(ctx, execution.StartRequest{
		WorkflowID: id, Mode: domain.ModeTrigger, StartNode: node, Items: items, DedupKey: node + ":" + e.DedupKey,
	})
	if err != nil && !errors.Is(err, domain.ErrDuplicate) {
		return fmt.Errorf("starting execution: %w", err)
	}
	if e.AwaitCompletion && execID != "" {
		if _, err := s.d.Executions.Await(ctx, execID); err != nil {
			return fmt.Errorf("waiting for execution: %w", err)
		}
	}
	return nil
}

// followListeners applies activation events from other instances: every
// instance, not only the leader, keeps its listeners in step with the store.
func (s *Service) followListeners(ctx context.Context, wg *sync.WaitGroup) {
	if s.d.Bus == nil {
		return
	}
	sub, err := s.d.Bus.Subscribe(ctx, port.EventFilter{Kinds: []port.EventKind{port.EventWorkflowActivated, port.EventWorkflowDeactivated}})
	if err != nil {
		s.d.Logger.Error("subscribing to activation events for listeners failed", "error", err)
		return
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer sub.Close()
		for {
			select {
			case <-ctx.Done():
				return
			case e, ok := <-sub.Events():
				if !ok {
					return
				}
				s.syncListeners(ctx, e.WorkflowID)
			}
		}
	}()
}

// syncListeners makes the running listeners of a workflow match the store.
func (s *Service) syncListeners(ctx context.Context, id domain.WorkflowID) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	wf, err := s.d.Workflows.Get(ctx, id)
	if err != nil || !wf.Active {
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			s.d.Logger.Error("loading workflow for listeners failed", "workflow", id, "error", err)
			return
		}
		s.stopListeners(id)
		return
	}
	p, err := s.inspect(ctx, wf)
	if err != nil {
		s.d.Logger.Error("workflow listeners are invalid", "workflow", id, "error", err)
		return
	}
	s.setListeners(id, p.listeners, false) //nolint:contextcheck // listeners outlive the event that changed them
}

// Overloaded reports whether listeners should stop taking new work because the
// job queue is deep. It applies hysteresis (kafka.pauseAboveDepth and
// kafka.resumeBelowDepth) and samples the queue at most once per
// kafka.depthCheckInterval.
func (s *Service) Overloaded(ctx context.Context) bool { return s.gate.isOverloaded(ctx) }
