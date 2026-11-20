package trigger

import (
	"context"
	"fmt"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// ChatRoute is a chat call matched to the chat trigger node that serves it.
type ChatRoute struct {
	// Route carries the workflow, node, credential and the response mode and
	// authentication in Route.Spec.
	Route
	// Chat holds the chat-specific settings (reply field, origins, widget texts).
	Chat nodesdk.ChatSpec
}

// ResolveChat finds the chat trigger of an active workflow that serves path. It
// wraps domain.ErrNotFound when none does.
func (s *Service) ResolveChat(ctx context.Context, path string) (ChatRoute, error) {
	path = domain.NormalizeWebhookPath(path)
	reg, err := s.d.Webhooks.Find(ctx, nodesdk.WebhookMethodChat, path)
	if err != nil {
		return ChatRoute{}, fmt.Errorf("finding chat trigger for /%s: %w", path, err)
	}
	wf, err := s.d.Workflows.Get(ctx, reg.WorkflowID)
	if err != nil {
		return ChatRoute{}, fmt.Errorf("loading workflow %s: %w", reg.WorkflowID, err)
	}
	if !wf.Active {
		return ChatRoute{}, fmt.Errorf("workflow %s is not active: %w", wf.ID, domain.ErrNotFound)
	}
	n, ok := wf.NodeByName(reg.Node)
	if !ok || n.Disabled {
		return ChatRoute{}, fmt.Errorf("workflow %s has no enabled node %q: %w", wf.ID, reg.Node, domain.ErrNotFound)
	}
	impl, err := s.d.Nodes.Lookup(n.Type, n.TypeVersion)
	if err != nil {
		return ChatRoute{}, fmt.Errorf("node %q: %w", n.Name, err)
	}
	t, ok := impl.(nodesdk.ChatTrigger)
	if !ok {
		return ChatRoute{}, fmt.Errorf("node %q is not a chat trigger: %w", n.Name, domain.ErrNotFound)
	}
	web, err := t.Webhook(n.Parameters)
	if err != nil {
		return ChatRoute{}, fmt.Errorf("node %q: %w", n.Name, err)
	}
	spec, err := t.Chat(n.Parameters)
	if err != nil {
		return ChatRoute{}, fmt.Errorf("node %q: %w", n.Name, err)
	}
	return ChatRoute{
		Route: Route{
			WorkflowID: wf.ID, Node: n.Name, Spec: web, Params: map[string]string{},
			Credential: n.Credentials[web.AuthCredential], Mode: domain.ModeWebhook,
		},
		Chat: spec,
	}, nil
}
