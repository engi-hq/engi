package trigger

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// webhookNode is a webhook trigger of a workflow with its parsed settings.
type webhookNode struct {
	node domain.Node
	spec nodesdk.WebhookSpec
	// path is the final route pattern (default applied, normalized).
	path string
}

// scheduleNode is a schedule trigger of a workflow with its parsed settings.
type scheduleNode struct {
	node domain.Node
	spec nodesdk.ScheduleSpec
}

// plan is what activating a workflow registers.
type plan struct {
	webhooks  []webhookNode
	schedules []scheduleNode
	listeners []listenNode
	polls     []pollNode
}

var (
	paramName    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	nonSlugChars = regexp.MustCompile(`[^a-z0-9]+`)
)

// inspect checks every enabled trigger node of wf and returns what to register.
// All problems are reported together as a *domain.ValidationError.
func (s *Service) inspect(ctx context.Context, wf domain.Workflow) (plan, error) {
	var p plan
	var issues []domain.Issue
	add := func(i int, msg string) {
		issues = append(issues, domain.Issue{Path: fmt.Sprintf("nodes[%d]", i), Message: msg})
	}
	seen := map[string]string{}
	for i, n := range wf.Nodes {
		if n.Disabled {
			continue
		}
		impl, err := s.d.Nodes.Lookup(n.Type, n.TypeVersion)
		if err != nil {
			add(i, fmt.Sprintf("node %q: %v", n.Name, err))
			continue
		}
		switch t := impl.(type) {
		case nodesdk.WebhookTrigger:
			w, err := s.webhookOf(ctx, wf.ID, n, t)
			if err != nil {
				add(i, fmt.Sprintf("node %q: %v", n.Name, err))
				continue
			}
			if w.spec.ResponseMode == nodesdk.ResponseNode && !s.respondsAfter(wf, n.Name) {
				add(i, fmt.Sprintf("%s %q responds with a Respond to Webhook node, but the workflow has none connected after it", t.Describe().DisplayName, n.Name))
				continue
			}
			key := w.spec.Method + " " + shapeOf(w.path)
			if other, dup := seen[key]; dup {
				add(i, fmt.Sprintf("node %q: %s /%s is already used by node %q", n.Name, w.spec.Method, w.path, other))
				continue
			}
			seen[key] = n.Name
			p.webhooks = append(p.webhooks, w)
		case nodesdk.ScheduleTrigger:
			spec, err := t.Schedule(n.Parameters)
			if err != nil {
				add(i, fmt.Sprintf("node %q: %v", n.Name, err))
				continue
			}
			p.schedules = append(p.schedules, scheduleNode{node: n, spec: spec})
		case nodesdk.PollTrigger:
			pn, err := s.pollOf(ctx, n, t)
			if err != nil {
				add(i, fmt.Sprintf("node %q: %v", n.Name, err))
				continue
			}
			p.polls = append(p.polls, pn)
		case nodesdk.ListenTrigger:
			if err := s.checkListener(ctx, n, t); err != nil {
				add(i, fmt.Sprintf("node %q: %v", n.Name, err))
				continue
			}
			p.listeners = append(p.listeners, listenNode{node: n, impl: t})
		}
	}
	if len(issues) > 0 {
		return plan{}, &domain.ValidationError{Issues: issues}
	}
	return p, nil
}

// respondsAfter reports whether an enabled node that answers the webhook call
// (nodesdk.WebhookResponder) is reachable from the node named start over main
// connections. A disabled node passes its input on, so the search continues past
// it, as the engine does.
func (s *Service) respondsAfter(wf domain.Workflow, start string) bool {
	next := map[string][]string{}
	for _, c := range wf.Connections {
		if c.From.Port == domain.PortMain {
			next[c.From.Node] = append(next[c.From.Node], c.To.Node)
		}
	}
	seen := map[string]bool{start: true}
	queue := []string{start}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, name := range next[cur] {
			if seen[name] {
				continue
			}
			seen[name] = true
			n, ok := wf.NodeByName(name)
			if !ok {
				continue
			}
			if !n.Disabled && s.isResponder(*n) {
				return true
			}
			queue = append(queue, name)
		}
	}
	return false
}

// isResponder reports whether the node's type answers webhook calls.
func (s *Service) isResponder(n domain.Node) bool {
	impl, err := s.d.Nodes.Lookup(n.Type, n.TypeVersion)
	if err != nil {
		return false
	}
	_, ok := impl.(nodesdk.WebhookResponder)
	return ok
}

// checkListener validates a listener's parameters and that its required
// credentials are bound and exist.
func (s *Service) checkListener(ctx context.Context, n domain.Node, t nodesdk.ListenTrigger) error {
	if err := t.CheckListen(n.Parameters); err != nil {
		return err //nolint:wrapcheck // the node's message is already specific
	}
	return s.checkCredentials(ctx, n, t)
}

// pollOf validates a polling trigger: its parameters, the interval against
// poll.minInterval, and its credentials.
func (s *Service) pollOf(ctx context.Context, n domain.Node, t nodesdk.PollTrigger) (pollNode, error) {
	spec, err := t.CheckPoll(n.Parameters)
	if err != nil {
		return pollNode{}, err //nolint:wrapcheck // the node's message is already specific
	}
	if spec.Interval < s.cfg.Poll.MinInterval {
		return pollNode{}, fmt.Errorf("poll interval %s is shorter than the minimum %s (poll.minInterval)", spec.Interval, s.cfg.Poll.MinInterval)
	}
	if err := s.checkCredentials(ctx, n, t); err != nil {
		return pollNode{}, err
	}
	return pollNode{node: n, impl: t, spec: spec}, nil
}

// checkCredentials verifies that a trigger's required credentials are bound
// and exist.
func (s *Service) checkCredentials(ctx context.Context, n domain.Node, t nodesdk.Node) error {
	for _, ref := range t.Describe().Credentials {
		cid := n.Credentials[ref.Name]
		if cid == "" {
			if ref.Required {
				return fmt.Errorf("the %q credential is required", ref.Name)
			}
			continue
		}
		if _, err := s.d.Credentials.Resolve(ctx, cid); err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return fmt.Errorf("credential %s does not exist", cid)
			}
			return fmt.Errorf("checking credential %s: %w", cid, err)
		}
	}
	return nil
}

func (s *Service) webhookOf(ctx context.Context, id domain.WorkflowID, n domain.Node, t nodesdk.WebhookTrigger) (webhookNode, error) {
	spec, err := t.Webhook(n.Parameters)
	if err != nil {
		return webhookNode{}, err //nolint:wrapcheck // the node's message is already specific
	}
	path := domain.NormalizeWebhookPath(spec.Path)
	if path == "" {
		path = defaultPath(id, n.Name)
	}
	if err := checkPath(path); err != nil {
		return webhookNode{}, err
	}
	if spec.Auth != nodesdk.WebhookAuthNone {
		cid := n.Credentials[spec.AuthCredential]
		if cid == "" {
			return webhookNode{}, fmt.Errorf("authentication %q needs the %q credential", spec.Auth, spec.AuthCredential)
		}
		if _, err := s.d.Credentials.Resolve(ctx, cid); err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return webhookNode{}, fmt.Errorf("credential %s does not exist", cid)
			}
			return webhookNode{}, fmt.Errorf("checking credential %s: %w", cid, err)
		}
	}
	return webhookNode{node: n, spec: spec, path: path}, nil
}

// defaultPath is "<workflow id>/<node name as a slug>".
func defaultPath(id domain.WorkflowID, node string) string {
	slug := strings.Trim(nonSlugChars.ReplaceAllString(strings.ToLower(node), "-"), "-")
	if slug == "" {
		slug = "webhook"
	}
	return string(id) + "/" + slug
}

// checkPath validates a normalized route pattern.
func checkPath(path string) error {
	for _, seg := range strings.Split(path, "/") {
		switch {
		case seg == "":
			return fmt.Errorf("path %q has an empty segment", path)
		case strings.HasPrefix(seg, ":") && !paramName.MatchString(seg[1:]):
			return fmt.Errorf("path %q: parameter %q must be a name of letters, digits and underscores", path, seg)
		}
	}
	return nil
}

func shapeOf(path string) string { return domain.WebhookRegistration{Path: path}.Shape() }
