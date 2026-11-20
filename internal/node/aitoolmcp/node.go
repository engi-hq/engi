// Package aitoolmcp implements the MCP Client tool sub-node (engi.ai.tool.mcp).
// It connects to a remote Model Context Protocol server and offers the server's
// tools to the agent it is connected to. The protocol itself lives in
// internal/mcp.
//
// Connections are short-lived on purpose: the tool contract has no close hook,
// so each Tools and each Call opens a session, does its work and closes it
// before returning. That costs a handshake per call but leaks nothing, works
// with servers that expire sessions, and makes cancellation trivial.
package aitoolmcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	credmcp "github.com/engi-hq/engi/internal/credtype/mcp"
	"github.com/engi-hq/engi/internal/mcp"
	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.ai.tool.mcp"

// Tool selection modes (the toolSelection parameter).
const (
	selectAll    = "all"
	selectOnly   = "selected"
	selectExcept = "allExcept"
)

// defaultClientName identifies Engi to MCP servers when Config.Info names none.
const defaultClientName = "engi"

// Authentication modes (the authentication parameter).
const (
	authNone       = "none"
	authCredential = "credential"
)

// truncationNote ends a tool result that was cut at Limits.MaxResultBytes.
const truncationNote = "\n[output truncated]"

var (
	headerToken = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")
	badToolChar = regexp.MustCompile(`[^a-zA-Z0-9_-]`)
)

// maxToolNameLength is the common provider limit port.Tool names obey (OpenAI
// and Anthropic both accept [a-zA-Z0-9_-]{1,64}).
const maxToolNameLength = 64

// maxToolPrefixLength bounds the sub-node name prefix put in front of MCP tool
// names, so that the server's own tool name keeps room within maxToolNameLength.
const maxToolPrefixLength = 24

// fallbackToolName stands in for a name that sanitizes to nothing.
const fallbackToolName = "tool"

// toolNameSeparator joins the sub-node prefix and the server's tool name.
const toolNameSeparator = "__"

// Limits are the host-configured bounds of the client (nodes.mcp.*).
type Limits struct {
	RequestTimeout   time.Duration
	MaxResponseBytes int
	MaxListPages     int
	MaxTools         int
	MaxResultBytes   int
}

// Config is what the node needs from the host.
type Config struct {
	Limits Limits
	// Info identifies Engi to MCP servers.
	Info mcp.Implementation
}

// Node is the MCP Client tool.
type Node struct{ cfg Config }

var _ nodesdk.Supplier = (*Node)(nil)

// New returns an MCP Client tool node.
func New(cfg Config) *Node {
	if cfg.Info.Name == "" {
		cfg.Info.Name = defaultClientName
	}
	return &Node{cfg: cfg}
}

// Describe implements nodesdk.Node.
func (n *Node) Describe() nodesdk.Descriptor {
	picked := nodekit.Show("toolSelection", selectOnly, selectExcept)
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "MCP Client Tool", Group: nodesdk.GroupAI, Icon: "plug",
		Description: "Gives an agent the tools of a remote MCP (Model Context Protocol) server.",
		Outputs:     []nodesdk.PortSpec{{Name: "tool", Kind: nodesdk.PortAITool}},
		Credentials: []nodesdk.CredentialRef{{Name: credmcp.Name, Type: credmcp.Name}},
		Properties: []nodesdk.Property{
			{Name: "endpoint", DisplayName: "Endpoint URL", Type: nodesdk.PropString, Required: true, Placeholder: "https://example.com/mcp",
				Description: "The server's MCP URL (for the SSE transport, its event stream URL)."},
			{Name: "transport", DisplayName: "Transport", Type: nodesdk.PropOptions, Default: mcp.TransportStreamableHTTP, Options: []nodesdk.Option{
				{Name: "Streamable HTTP", Value: mcp.TransportStreamableHTTP, Description: "Current transport. Falls back to SSE when the server answers the first request with a client error (400, 404, 405...) other than 401, 403, 408 or 429."},
				{Name: "SSE (legacy)", Value: mcp.TransportSSE, Description: "The older HTTP+SSE transport (protocol 2024-11-05)."}}},
			{Name: "authentication", DisplayName: "Authentication", Type: nodesdk.PropOptions, Default: authNone, Options: []nodesdk.Option{
				{Name: "None", Value: authNone},
				{Name: "MCP credential", Value: authCredential, Description: "Send the bearer token or custom header of the bound MCP Server credential."}}},
			{Name: "toolSelection", DisplayName: "Tools to include", Type: nodesdk.PropOptions, Default: selectAll, Options: []nodesdk.Option{
				{Name: "All", Value: selectAll}, {Name: "Selected", Value: selectOnly}, {Name: "All except", Value: selectExcept}}},
			{Name: "tools", DisplayName: "Tool names", Type: nodesdk.PropString, DisplayOptions: picked,
				Description: "Comma-separated MCP tool names, as the server lists them."},
		},
	}
}

// Execute implements nodesdk.Node. Sub-nodes never run as steps.
func (n *Node) Execute(context.Context, nodesdk.Input) (nodesdk.Output, error) {
	return nodesdk.Output{}, fmt.Errorf("MCP client tool: %w", nodesdk.ErrUnavailable)
}

// Supply implements nodesdk.Supplier and returns a port.ToolProvider. It reads
// parameters and the credential but does not touch the network; the provider
// connects when the agent asks for tools.
func (n *Node) Supply(ctx context.Context, in nodesdk.Input) (any, error) {
	client := in.HTTP()
	if client == nil {
		return nil, fmt.Errorf("HTTP client: %w", nodesdk.ErrUnavailable)
	}
	endpoint, err := nodesdk.ParamString(ctx, in, "endpoint", 0)
	if err != nil {
		return nil, fmt.Errorf("reading endpoint: %w", err)
	}
	transport, err := nodesdk.ParamString(ctx, in, "transport", 0)
	if err != nil {
		return nil, fmt.Errorf("reading transport: %w", err)
	}
	mode, err := nodesdk.ParamString(ctx, in, "toolSelection", 0)
	if err != nil {
		return nil, fmt.Errorf("reading toolSelection: %w", err)
	}
	if !slices.Contains([]string{selectAll, selectOnly, selectExcept}, mode) {
		return nil, fmt.Errorf("toolSelection %q is not one of %s, %s, %s", mode, selectAll, selectOnly, selectExcept)
	}
	var names []string
	if mode != selectAll {
		if names, err = nodekit.ParamStringList(ctx, in, "tools", 0); err != nil {
			return nil, err
		}
		if mode == selectOnly && len(names) == 0 {
			return nil, errors.New("toolSelection is \"selected\" but no tool names are given")
		}
	}
	header, err := n.authHeader(ctx, in)
	if err != nil {
		return nil, err
	}
	return &provider{
		client: client,
		cfg: mcp.ClientConfig{
			Endpoint: strings.TrimSpace(endpoint), Transport: transport, Header: header,
			Timeout: n.cfg.Limits.RequestTimeout, MaxResponseBytes: n.cfg.Limits.MaxResponseBytes,
			MaxListPages: n.cfg.Limits.MaxListPages, MaxTools: n.cfg.Limits.MaxTools, Info: n.cfg.Info,
		},
		maxResult: n.cfg.Limits.MaxResultBytes, mode: mode, names: names,
	}, nil
}

// authHeader builds the request headers; the credential is read only when the
// node is set to use it.
func (n *Node) authHeader(ctx context.Context, in nodesdk.Input) (http.Header, error) {
	mode, err := nodesdk.ParamString(ctx, in, "authentication", 0)
	if err != nil {
		return nil, fmt.Errorf("reading authentication: %w", err)
	}
	switch mode {
	case "", authNone:
		return http.Header{}, nil
	case authCredential:
	default:
		return nil, fmt.Errorf("authentication %q is not one of %s, %s", mode, authNone, authCredential)
	}
	cred, err := in.Credential(ctx, credmcp.Name)
	if err != nil {
		return nil, fmt.Errorf("loading credential %q: %w", credmcp.Name, err)
	}
	return HeadersFor(cred.Data)
}

// HeadersFor turns the fields of an engi.mcp credential into request headers.
func HeadersFor(data map[string]any) (http.Header, error) {
	str := func(k string) string { s, _ := data[k].(string); return strings.TrimSpace(s) }
	h := http.Header{}
	switch kind := str(credmcp.FieldAuthentication); kind {
	case "", credmcp.AuthNone:
	case credmcp.AuthBearer:
		token := str(credmcp.FieldToken)
		if token == "" {
			return nil, errors.New("the MCP credential uses bearer authentication but has no token")
		}
		if strings.ContainsAny(token, "\r\n") {
			return nil, errors.New("the MCP credential token must not contain line breaks")
		}
		h.Set("Authorization", "Bearer "+token)
	case credmcp.AuthHeader:
		name, value := str(credmcp.FieldHeaderName), str(credmcp.FieldHeaderValue)
		if !headerToken.MatchString(name) {
			return nil, fmt.Errorf("the MCP credential header name %q is not a valid HTTP header name", name)
		}
		if value == "" || strings.ContainsAny(value, "\r\n") {
			return nil, errors.New("the MCP credential header value must be set and must not contain line breaks")
		}
		h.Set(name, value)
	default:
		return nil, fmt.Errorf("the MCP credential has unknown authentication %q", kind)
	}
	return h, nil
}

// provider implements port.ToolProvider for one MCP server.
type provider struct {
	client    mcp.Doer
	cfg       mcp.ClientConfig
	maxResult int
	mode      string
	names     []string
	// prefix is the sanitized sub-node name put in front of tool names; empty
	// means the server's names are used as they are.
	prefix string
}

// WithToolPrefix returns a copy of the provider whose tool names start with
// the sanitized name of its sub-node, for example "MCP_Client__search". The
// agent calls it for every MCP sub-node, so two servers that both offer
// "search" stay apart. The original name is still what goes to the server.
func (p *provider) WithToolPrefix(node string) port.ToolProvider {
	q := *p
	q.prefix = prefixOf(node)
	return &q
}

// prefixOf turns a sub-node name into a tool-name prefix within the limits.
func prefixOf(node string) string {
	p := badToolChar.ReplaceAllString(strings.TrimSpace(node), "_")
	if len(p) > maxToolPrefixLength {
		p = p[:maxToolPrefixLength]
	}
	if p == "" {
		p = fallbackToolName
	}
	return p
}

// session opens a connection and returns it with its closer.
func (p *provider) session(ctx context.Context) (*mcp.Client, func(), error) {
	c, err := mcp.Connect(ctx, p.client, p.cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("connecting to MCP server: %w", err)
	}
	return c, func() { _ = c.Close(context.WithoutCancel(ctx)) }, nil
}

// Tools implements port.ToolProvider.
func (p *provider) Tools(ctx context.Context) ([]port.Tool, error) {
	c, closeSession, err := p.session(ctx)
	if err != nil {
		return nil, err
	}
	defer closeSession()
	infos, err := c.ListTools(ctx)
	if err != nil {
		return nil, err
	}
	chosen, err := p.filter(infos)
	if err != nil {
		return nil, err
	}
	tools := make([]port.Tool, 0, len(chosen))
	used := map[string]bool{}
	for _, info := range chosen {
		name := uniqueName(sanitize(p.qualify(info.Name)), used)
		used[name] = true
		tools = append(tools, &tool{
			p: p, remote: info.Name,
			def: port.ToolDefinition{Name: name, Description: describe(info), Parameters: schemaOf(info)},
		})
	}
	return tools, nil
}

// qualify adds the sub-node prefix to a server tool name.
func (p *provider) qualify(remote string) string {
	if p.prefix == "" {
		return remote
	}
	return p.prefix + toolNameSeparator + remote
}

func (p *provider) filter(infos []mcp.ToolInfo) ([]mcp.ToolInfo, error) {
	if p.mode == selectAll {
		return infos, nil
	}
	offered := map[string]bool{}
	for _, i := range infos {
		offered[i.Name] = true
	}
	if p.mode == selectOnly {
		for _, want := range p.names {
			if !offered[want] {
				return nil, fmt.Errorf("the MCP server does not offer a tool named %q", want)
			}
		}
	}
	var out []mcp.ToolInfo
	for _, i := range infos {
		if listed := slices.Contains(p.names, i.Name); listed == (p.mode == selectOnly) {
			out = append(out, i)
		}
	}
	return out, nil
}

// sanitize makes a name satisfy port.Tool's rule [a-zA-Z0-9_-]{1,64}.
func sanitize(name string) string {
	name = badToolChar.ReplaceAllString(name, "_")
	if len(name) > maxToolNameLength {
		name = name[:maxToolNameLength]
	}
	if name == "" {
		name = fallbackToolName
	}
	return name
}

// uniqueName appends _2, _3... until name is unused, keeping the length limit.
func uniqueName(name string, used map[string]bool) string {
	if !used[name] {
		return name
	}
	for i := 2; ; i++ {
		suffix := fmt.Sprintf("_%d", i)
		base := name
		if len(base)+len(suffix) > maxToolNameLength {
			base = base[:maxToolNameLength-len(suffix)]
		}
		if cand := base + suffix; !used[cand] {
			return cand
		}
	}
}

func describe(i mcp.ToolInfo) string {
	if i.Description != "" {
		return i.Description
	}
	return i.Title
}

// schemaOf returns the tool's input schema, or an empty object schema when the
// server sent none (some providers reject a missing schema).
func schemaOf(i mcp.ToolInfo) json.RawMessage {
	if len(i.InputSchema) == 0 || string(i.InputSchema) == "null" {
		return json.RawMessage(`{"type":"object"}`)
	}
	return i.InputSchema
}

// tool implements port.Tool for one remote tool.
type tool struct {
	p      *provider
	remote string
	def    port.ToolDefinition
}

// Definition implements port.Tool.
func (t *tool) Definition() port.ToolDefinition { return t.def }

// Call implements port.Tool. A tool failure and a JSON-RPC error (bad
// arguments, say) are results the model can react to; connection failures are
// errors.
func (t *tool) Call(ctx context.Context, args json.RawMessage) (port.ToolResult, error) {
	c, closeSession, err := t.p.session(ctx)
	if err != nil {
		return port.ToolResult{}, err
	}
	defer closeSession()
	res, err := c.CallTool(ctx, t.remote, args)
	var rpc *mcp.RPCError
	if errors.As(err, &rpc) {
		return port.ToolResult{Content: rpc.Message, IsError: true}, nil
	}
	if err != nil {
		return port.ToolResult{}, err
	}
	return port.ToolResult{Content: truncate(res.Text(), t.p.maxResult), IsError: res.IsError}, nil
}

// truncate cuts s to at most limit bytes on a rune boundary and marks the cut.
func truncate(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + truncationNote
}
