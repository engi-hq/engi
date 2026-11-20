package trigger

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/mcp"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Route is a webhook call matched to the trigger node that serves it.
type Route struct {
	WorkflowID domain.WorkflowID
	// Node is the name of the webhook trigger node, which is also the node the
	// execution starts at.
	Node string
	Spec nodesdk.WebhookSpec
	// Params holds the values of the dynamic path segments.
	Params map[string]string
	// Credential is the credential bound to the node's authentication slot;
	// empty when the node needs no authentication.
	Credential domain.CredentialID
	// Mode is the execution mode: webhook for production calls, manual for tests.
	Mode domain.ExecutionMode
	// Test marks a call to an armed test webhook.
	Test bool
}

// registerWebhooks replaces the workflow's routes with the planned ones.
func (s *Service) registerWebhooks(ctx context.Context, id domain.WorkflowID, hooks []webhookNode) error {
	if err := s.d.Webhooks.DeleteByWorkflow(ctx, id); err != nil {
		return fmt.Errorf("removing old webhooks: %w", err)
	}
	for _, h := range hooks {
		err := s.d.Webhooks.Register(ctx, domain.WebhookRegistration{
			Method: h.spec.Method, Path: h.path, WorkflowID: id, Node: h.node.Name, CreatedAt: s.d.Clock.Now(),
		})
		if err == nil {
			continue
		}
		if cleanup := s.d.Webhooks.DeleteByWorkflow(ctx, id); cleanup != nil {
			s.d.Logger.Error("removing partly registered webhooks", "workflow", id, "error", cleanup)
		}
		if errors.Is(err, domain.ErrAlreadyExists) {
			return fmt.Errorf("webhook %s /%s is used by another workflow: %w", h.spec.Method, h.path, domain.ErrConflict)
		}
		return fmt.Errorf("registering webhook %s /%s: %w", h.spec.Method, h.path, err)
	}
	return nil
}

// ResolveLive finds the production route for a call. It wraps
// domain.ErrNotFound when no active workflow serves method and path. Static
// routes win over routes with path parameters.
func (s *Service) ResolveLive(ctx context.Context, method, path string) (Route, error) {
	method, path = strings.ToUpper(method), domain.NormalizeWebhookPath(path)
	if method == nodesdk.WebhookMethodChat || method == mcp.RouteMethod {
		// Chat routes are served by ResolveChat and MCP routes by ResolveMCP only.
		return Route{}, fmt.Errorf("no webhook for %s /%s: %w", method, path, domain.ErrNotFound)
	}
	reg, params, err := s.match(ctx, method, path)
	if err != nil {
		return Route{}, err
	}
	r, err := s.route(ctx, reg.WorkflowID, reg.Node, params)
	if err != nil {
		return Route{}, err
	}
	wf, err := s.d.Workflows.Get(ctx, reg.WorkflowID)
	if err != nil {
		return Route{}, fmt.Errorf("loading workflow %s: %w", reg.WorkflowID, err)
	}
	if !wf.Active {
		return Route{}, fmt.Errorf("workflow %s is not active: %w", wf.ID, domain.ErrNotFound)
	}
	r.Mode = domain.ModeWebhook
	return r, nil
}

func (s *Service) match(ctx context.Context, method, path string) (domain.WebhookRegistration, map[string]string, error) {
	reg, err := s.d.Webhooks.Find(ctx, method, path)
	if err == nil && !reg.Dynamic() {
		return reg, map[string]string{}, nil
	}
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return domain.WebhookRegistration{}, nil, fmt.Errorf("finding webhook: %w", err)
	}
	cands, err := s.d.Webhooks.ListDynamic(ctx, method, strings.Count(path, "/")+1)
	if err != nil {
		return domain.WebhookRegistration{}, nil, fmt.Errorf("finding webhook: %w", err)
	}
	// Prefer the pattern with the most literal segments.
	sort.SliceStable(cands, func(i, j int) bool { return literals(cands[i]) > literals(cands[j]) })
	for _, c := range cands {
		if params, ok := c.Match(path); ok {
			return c, params, nil
		}
	}
	return domain.WebhookRegistration{}, nil, fmt.Errorf("no webhook for %s /%s: %w", method, path, domain.ErrNotFound)
}

func literals(r domain.WebhookRegistration) int {
	return r.Segments() - strings.Count(r.Shape(), ":")
}

// route loads the node of a workflow and reads its webhook settings.
func (s *Service) route(ctx context.Context, id domain.WorkflowID, node string, params map[string]string) (Route, error) {
	wf, err := s.d.Workflows.Get(ctx, id)
	if err != nil {
		return Route{}, fmt.Errorf("loading workflow %s: %w", id, err)
	}
	n, ok := wf.NodeByName(node)
	if !ok || n.Disabled {
		return Route{}, fmt.Errorf("workflow %s has no enabled node %q: %w", id, node, domain.ErrNotFound)
	}
	impl, err := s.d.Nodes.Lookup(n.Type, n.TypeVersion)
	if err != nil {
		return Route{}, fmt.Errorf("node %q: %w", node, err)
	}
	t, ok := impl.(nodesdk.WebhookTrigger)
	if !ok {
		return Route{}, fmt.Errorf("node %q is not a webhook trigger: %w", node, domain.ErrNotFound)
	}
	spec, err := t.Webhook(n.Parameters)
	if err != nil {
		return Route{}, fmt.Errorf("node %q: %w", node, err)
	}
	return Route{
		WorkflowID: id, Node: node, Spec: spec, Params: params, Credential: n.Credentials[spec.AuthCredential],
		Mode: domain.ModeWebhook,
	}, nil
}

// TestWebhook describes an armed test webhook.
type TestWebhook struct {
	Method string
	// Path is the route pattern relative to the test base path.
	Path string
	// URLPath is the path to call, including webhook.testBasePath.
	URLPath   string
	ExpiresAt time.Time
}

// ArmTest makes the workflow's webhook node answer one call on the test URL for
// webhook.testTimeout, whether or not the workflow is active; the execution runs
// in manual mode. node may be empty when the workflow has exactly one webhook
// trigger. Arming again replaces the earlier test of that route. The test lives
// in the shared store, so any webhook pod serves its call.
func (s *Service) ArmTest(ctx context.Context, id domain.WorkflowID, node string) (TestWebhook, error) {
	wf, err := s.d.Workflows.Get(ctx, id)
	if err != nil {
		return TestWebhook{}, fmt.Errorf("arming test webhook: %w", err)
	}
	p, err := s.inspect(ctx, wf)
	if err != nil {
		return TestWebhook{}, fmt.Errorf("arming test webhook: %w", err)
	}
	var pick *webhookNode
	for i := range p.webhooks {
		if m := p.webhooks[i].spec.Method; m == nodesdk.WebhookMethodChat || m == mcp.RouteMethod {
			continue // chat and MCP triggers have no test URL
		}
		if node == "" || p.webhooks[i].node.Name == node {
			if pick != nil {
				return TestWebhook{}, &domain.ValidationError{Issues: []domain.Issue{{Path: "node", Message: "the workflow has several webhook triggers; name one"}}}
			}
			pick = &p.webhooks[i]
		}
	}
	if pick == nil {
		return TestWebhook{}, &domain.ValidationError{Issues: []domain.Issue{{Path: "node", Message: "the workflow has no such webhook trigger node"}}}
	}
	now := s.d.Clock.Now()
	a := domain.ArmedTestWebhook{
		WorkflowID: id, Node: pick.node.Name, Method: pick.spec.Method, Path: pick.path,
		ExpiresAt: now.Add(s.cfg.Webhook.TestTimeout),
	}
	if err := s.d.TestWebhooks.Arm(ctx, a, now); err != nil {
		return TestWebhook{}, fmt.Errorf("arming test webhook: %w", err)
	}
	return TestWebhook{
		Method: a.Method, Path: a.Path, URLPath: s.cfg.Webhook.TestBasePath + "/" + a.Path, ExpiresAt: a.ExpiresAt,
	}, nil
}

// ResolveTest finds and consumes the armed test webhook for a call. It wraps
// domain.ErrNotFound when none is armed or it has expired. When several armed
// tests match, the one with the most literal segments is tried first; a test
// that another call claims in the meantime is skipped.
func (s *Service) ResolveTest(ctx context.Context, method, path string) (Route, error) {
	method, path = strings.ToUpper(method), domain.NormalizeWebhookPath(path)
	now := s.d.Clock.Now()
	cands, err := s.d.TestWebhooks.Find(ctx, method, domain.WebhookRegistration{Path: path}.Segments(), now)
	if err != nil {
		return Route{}, fmt.Errorf("finding test webhook: %w", err)
	}
	for _, m := range matchTests(cands, path) {
		a, err := s.d.TestWebhooks.Claim(ctx, method, m.test.Shape(), now)
		if errors.Is(err, domain.ErrNotFound) {
			continue // claimed by a racing call, or expired since it was found
		}
		if err != nil {
			return Route{}, fmt.Errorf("claiming test webhook: %w", err)
		}
		r, err := s.route(ctx, a.WorkflowID, a.Node, m.params)
		if err != nil {
			return Route{}, err
		}
		r.Mode, r.Test = domain.ModeManual, true
		return r, nil
	}
	return Route{}, fmt.Errorf("no test webhook armed for %s /%s: %w", method, path, domain.ErrNotFound)
}

// testMatch is an armed test whose pattern serves a call, with its parameters.
type testMatch struct {
	test   domain.ArmedTestWebhook
	params map[string]string
}

// matchTests keeps the tests that serve path, most literal segments first.
func matchTests(cands []domain.ArmedTestWebhook, path string) []testMatch {
	var out []testMatch
	for _, c := range cands {
		if params, ok := c.Route().Match(path); ok {
			out = append(out, testMatch{test: c, params: params})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return literals(out[i].test.Route()) > literals(out[j].test.Route()) })
	return out
}
