package config

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// MCPClientConfig tunes the MCP client tool (nodes.mcp).
type MCPClientConfig struct {
	// RequestTimeout bounds one request/response exchange with an MCP server,
	// and the legacy SSE transport's connection handshake.
	RequestTimeout time.Duration `yaml:"requestTimeout"`
	// MaxResponseBytes caps one server response, event or stream line.
	MaxResponseBytes int `yaml:"maxResponseBytes"`
	// MaxListPages and MaxTools bound tools/list pagination; a server that
	// exceeds them makes the tool node fail rather than truncate its list.
	MaxListPages int `yaml:"maxListPages"`
	MaxTools     int `yaml:"maxTools"`
	// MaxResultBytes caps the text one tool call returns to the model.
	MaxResultBytes int `yaml:"maxResultBytes"`
}

func defaultMCPClient() MCPClientConfig {
	return MCPClientConfig{RequestTimeout: 30 * time.Second, MaxResponseBytes: 4 << 20, MaxListPages: 20, MaxTools: 200, MaxResultBytes: 1 << 20}
}

func (c MCPClientConfig) validate(errs []error) []error {
	errs = appendPositive(errs, "nodes.mcp.requestTimeout", int64(c.RequestTimeout))
	errs = appendPositive(errs, "nodes.mcp.maxResponseBytes", int64(c.MaxResponseBytes))
	errs = appendPositive(errs, "nodes.mcp.maxListPages", int64(c.MaxListPages))
	errs = appendPositive(errs, "nodes.mcp.maxTools", int64(c.MaxTools))
	return appendPositive(errs, "nodes.mcp.maxResultBytes", int64(c.MaxResultBytes))
}

// MCPServerConfig tunes the public MCP endpoints of MCP server triggers.
type MCPServerConfig struct {
	// BasePath is the URL prefix of MCP endpoints (/mcp/<path>).
	BasePath string `yaml:"basePath"`
	// MaxBodyBytes caps a request body; larger requests get 413.
	MaxBodyBytes int64 `yaml:"maxBodyBytes"`
	// CallTimeout bounds how long one tools/call waits for its workflow. It
	// should stay below server.writeTimeout.
	CallTimeout time.Duration `yaml:"callTimeout"`
	// MaxConcurrentCalls caps tools/call requests running at once across all
	// MCP endpoints of this process; more are refused with a JSON-RPC error.
	MaxConcurrentCalls int `yaml:"maxConcurrentCalls"`
	// AllowedOrigins lists the browser origins (the Origin request header, for
	// example https://app.example.com) that may call MCP endpoints, as the spec
	// requires against DNS rebinding. Requests without an Origin header (every
	// non-browser MCP client) are always allowed; a request with an Origin not
	// in this list gets 403. Empty refuses all browser-originated requests.
	AllowedOrigins []string `yaml:"allowedOrigins"`
	// ServerName is the name sent in serverInfo of the initialize result.
	ServerName string `yaml:"serverName"`
}

func defaultMCPServer() MCPServerConfig {
	return MCPServerConfig{BasePath: "/mcp", MaxBodyBytes: 1 << 20, CallTimeout: 50 * time.Second, MaxConcurrentCalls: 32, ServerName: "engi"}
}

func (c MCPServerConfig) validate(errs []error, w WebhookConfig, chat ChatTriggerConfig) []error {
	if len(c.BasePath) < 2 || c.BasePath[0] != '/' || c.BasePath[len(c.BasePath)-1] == '/' {
		errs = append(errs, fmt.Errorf("mcpServer.basePath %q must start with / and not end with /", c.BasePath))
	}
	if slices.Contains([]string{w.BasePath, w.TestBasePath, w.WaitBasePath, chat.BasePath}, c.BasePath) {
		errs = append(errs, errors.New("mcpServer.basePath must differ from the webhook and chat base paths"))
	}
	errs = appendPositive(errs, "mcpServer.maxBodyBytes", c.MaxBodyBytes)
	errs = appendPositive(errs, "mcpServer.callTimeout", int64(c.CallTimeout))
	errs = appendPositive(errs, "mcpServer.maxConcurrentCalls", int64(c.MaxConcurrentCalls))
	if strings.TrimSpace(c.ServerName) == "" {
		errs = append(errs, errors.New("mcpServer.serverName must not be empty"))
	}
	return errs
}

func mcpSettings() []setting {
	return []setting{
		{"ENGI_NODES_MCP_REQUEST_TIMEOUT", "nodes-mcp-request-timeout", "time limit of one MCP client request",
			durationSetting(func(c *Config) *time.Duration { return &c.Nodes.MCP.RequestTimeout })},
		{"ENGI_NODES_MCP_MAX_RESPONSE_BYTES", "nodes-mcp-max-response-bytes", "max bytes of one MCP server response",
			intSetting(func(c *Config) *int { return &c.Nodes.MCP.MaxResponseBytes })},
		{"ENGI_NODES_MCP_MAX_LIST_PAGES", "nodes-mcp-max-list-pages", "max tools/list pages the MCP client follows",
			intSetting(func(c *Config) *int { return &c.Nodes.MCP.MaxListPages })},
		{"ENGI_NODES_MCP_MAX_TOOLS", "nodes-mcp-max-tools", "max tools one MCP server may offer",
			intSetting(func(c *Config) *int { return &c.Nodes.MCP.MaxTools })},
		{"ENGI_NODES_MCP_MAX_RESULT_BYTES", "nodes-mcp-max-result-bytes", "max text bytes one MCP tool call returns",
			intSetting(func(c *Config) *int { return &c.Nodes.MCP.MaxResultBytes })},
		{"ENGI_MCP_SERVER_BASE_PATH", "mcp-server-base-path", "URL prefix of MCP server endpoints",
			stringSetting(func(c *Config) *string { return &c.Trigger.MCPServer.BasePath })},
		{"ENGI_MCP_SERVER_MAX_BODY_BYTES", "mcp-server-max-body-bytes", "max MCP server request body bytes",
			func(c *Config, v string) (err error) {
				c.Trigger.MCPServer.MaxBodyBytes, err = strconv.ParseInt(v, 10, 64)
				return
			}},
		{"ENGI_MCP_SERVER_CALL_TIMEOUT", "mcp-server-call-timeout", "how long one MCP tools/call waits for its workflow",
			durationSetting(func(c *Config) *time.Duration { return &c.Trigger.MCPServer.CallTimeout })},
		{"ENGI_MCP_SERVER_MAX_CONCURRENT_CALLS", "mcp-server-max-concurrent-calls", "MCP tools/call requests running at once",
			intSetting(func(c *Config) *int { return &c.Trigger.MCPServer.MaxConcurrentCalls })},
		{"ENGI_MCP_SERVER_ALLOWED_ORIGINS", "mcp-server-allowed-origins", "comma-separated browser origins allowed to call MCP endpoints",
			func(c *Config, v string) error {
				c.Trigger.MCPServer.AllowedOrigins = nil
				for _, o := range strings.Split(v, ",") {
					if o = strings.TrimSpace(o); o != "" {
						c.Trigger.MCPServer.AllowedOrigins = append(c.Trigger.MCPServer.AllowedOrigins, o)
					}
				}
				return nil
			}},
		{"ENGI_MCP_SERVER_NAME", "mcp-server-name", "server name in the MCP initialize result",
			stringSetting(func(c *Config) *string { return &c.Trigger.MCPServer.ServerName })},
	}
}
