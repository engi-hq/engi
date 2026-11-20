// Package mcpservertrigger implements the MCP Server trigger node
// (engi.trigger.mcpServer). Unlike other triggers it starts no execution of its
// own workflow: while the workflow is active, the host serves a Model Context
// Protocol endpoint at /mcp/<path> whose tools are the workflows listed in the
// node. Each tools/call runs one of those workflows (which must begin with an
// Execute Workflow Trigger) and returns its last node's output. The route is
// registered, authenticated and served by the host; this package only
// describes and validates the node.
package mcpservertrigger

import (
	"context"
	"fmt"
	"strings"

	"github.com/engi-hq/engi/internal/mcp"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.trigger.mcpServer"

// Credential slots, one per authentication kind.
const (
	credBearer = "bearerAuth"
	credHeader = "headerAuth"
)

// Node is the MCP Server trigger.
type Node struct{}

var _ nodesdk.WebhookTrigger = Node{}

// New returns an MCP Server trigger node.
func New() nodesdk.Node { return Node{} }

// Describe implements nodesdk.Node.
func (Node) Describe() nodesdk.Descriptor {
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "MCP Server Trigger", Group: nodesdk.GroupTrigger, Icon: "plug",
		Description: "Exposes workflows as tools of a Model Context Protocol server while this workflow is active.",
		Credentials: []nodesdk.CredentialRef{
			{Name: credBearer, Type: "engi.http.bearer"},
			{Name: credHeader, Type: "engi.http.header"},
		},
		Properties: []nodesdk.Property{
			{Name: "path", DisplayName: "Path", Type: nodesdk.PropString, Placeholder: "my-tools",
				Description: "The server answers at <mcp base path>/<path>. Empty uses <workflow id>/<node name>."},
			{Name: "authentication", DisplayName: "Authentication", Type: nodesdk.PropOptions, Default: mcp.ServerAuthNone, Options: []nodesdk.Option{
				{Name: "None", Value: mcp.ServerAuthNone}, {Name: "Bearer token", Value: mcp.ServerAuthBearer},
				{Name: "Header token", Value: mcp.ServerAuthHeader}}},
			{Name: mcp.ParamTools, DisplayName: "Tools", Type: nodesdk.PropFixedCollection, Required: true,
				Description: "One tool per workflow. The workflow must start with an Execute Workflow Trigger node; its input item is the tool's arguments and its last node's output is the result.",
				Children: []nodesdk.Property{
					{Name: "name", DisplayName: "Tool name", Type: nodesdk.PropString, Required: true, Description: "Letters, digits, _ and - (at most 64)."},
					{Name: "description", DisplayName: "Description", Type: nodesdk.PropString, Description: "Tells the client's model when to use the tool."},
					{Name: "workflowId", DisplayName: "Workflow ID", Type: nodesdk.PropString, Required: true},
					{Name: "inputSchema", DisplayName: "Input schema", Type: nodesdk.PropJSON,
						Description: "JSON Schema (type object) of the arguments. Empty accepts any object."},
				}},
		},
	}
}

// Execute implements nodesdk.Node. The workflow of an MCP server trigger is
// never started from the trigger itself.
func (Node) Execute(context.Context, nodesdk.Input) (nodesdk.Output, error) {
	return nodesdk.Output{}, fmt.Errorf("MCP server trigger: %w", nodesdk.ErrUnavailable)
}

// Webhook implements nodesdk.WebhookTrigger. The host registers the route under
// the pseudo method mcp.RouteMethod and serves it itself; params are the node
// parameters as saved, so expressions are not evaluated.
func (Node) Webhook(params map[string]any) (nodesdk.WebhookSpec, error) {
	spec := nodesdk.WebhookSpec{
		Method: mcp.RouteMethod,
		Path:   strings.Trim(str(params, "path"), "/"),
		Auth:   str(params, "authentication"),
	}
	for _, seg := range strings.Split(spec.Path, "/") {
		if strings.HasPrefix(seg, ":") {
			return nodesdk.WebhookSpec{}, fmt.Errorf("path %q: MCP paths cannot have parameters (%s)", spec.Path, seg)
		}
	}
	switch spec.Auth {
	case "", mcp.ServerAuthNone:
		spec.Auth = mcp.ServerAuthNone
	case mcp.ServerAuthBearer:
		spec.AuthCredential = credBearer
	case mcp.ServerAuthHeader:
		spec.AuthCredential = credHeader
	default:
		return nodesdk.WebhookSpec{}, fmt.Errorf("authentication %q is not supported", spec.Auth)
	}
	tools, err := mcp.ParseWorkflowTools(params[mcp.ParamTools])
	if err != nil {
		return nodesdk.WebhookSpec{}, fmt.Errorf("tools: %w", err)
	}
	if len(tools) == 0 {
		return nodesdk.WebhookSpec{}, fmt.Errorf("tools: add at least one workflow to expose")
	}
	return spec, nil
}

func str(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return strings.TrimSpace(s)
}
