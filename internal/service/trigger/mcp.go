package trigger

import (
	"context"
	"fmt"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/mcp"
)

// ResolveMCP finds the MCP server trigger of an active workflow that serves
// path (relative to the MCP base path). It wraps domain.ErrNotFound when none
// does. MCP routes live in the webhook route table under the pseudo method
// mcp.RouteMethod; they are served by this resolver only, so a /hook/... call
// can never reach an MCP trigger.
func (s *Service) ResolveMCP(ctx context.Context, path string) (Route, error) {
	path = domain.NormalizeWebhookPath(path)
	reg, err := s.d.Webhooks.Find(ctx, mcp.RouteMethod, path)
	if err != nil {
		return Route{}, fmt.Errorf("finding MCP trigger for /%s: %w", path, err)
	}
	r, err := s.route(ctx, reg.WorkflowID, reg.Node, map[string]string{})
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
