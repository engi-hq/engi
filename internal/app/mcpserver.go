package app

import (
	"fmt"

	mcphttp "github.com/engi-hq/engi/internal/adapter/http/mcpserver"
	"github.com/engi-hq/engi/internal/node/executeworkflowtrigger"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/service/execution"
	"github.com/engi-hq/engi/internal/service/mcpserver"
	"github.com/engi-hq/engi/internal/service/trigger"
)

// buildMCPServer wires the MCP server endpoints (/mcp/<path>): the trigger
// service resolves which node serves a path, the use-case layer runs the listed
// workflows, and the handler speaks the protocol over HTTP.
func (a *App) buildMCPServer(triggers *trigger.Service, wfs port.WorkflowStore, execs *execution.Service) (*mcphttp.Handler, error) {
	svc, err := mcpserver.New(a.Config.Trigger.MCPServer, mcpserver.Deps{
		Router: triggers, Workflows: wfs, Executions: execs, SubworkflowTrigger: executeworkflowtrigger.Type,
		Logger: a.Logger.With("component", "mcp-server"),
	})
	if err != nil {
		return nil, fmt.Errorf("building MCP server service: %w", err)
	}
	h, err := mcphttp.New(a.Config.Trigger.MCPServer, mcphttp.Deps{
		Endpoints: svc, Credentials: a.credResolver, Logger: a.Logger.With("component", "mcp-server"),
	})
	if err != nil {
		return nil, fmt.Errorf("building MCP handler: %w", err)
	}
	return h, nil
}
