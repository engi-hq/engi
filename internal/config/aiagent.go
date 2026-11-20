package config

import (
	"errors"
	"time"
)

// AIAgentConfig bounds the AI Agent node. A node may lower or raise the
// iteration, token and time limits for itself; zero in the node means "use
// these".
type AIAgentConfig struct {
	// MaxIterations caps model calls per item (one call per loop turn).
	MaxIterations int `yaml:"maxIterations"`
	// MaxTotalTokens stops the loop once input plus output tokens of all calls
	// for one item reach this total.
	MaxTotalTokens int `yaml:"maxTotalTokens"`
	// Timeout bounds the whole loop for one item, tool runs included.
	Timeout time.Duration `yaml:"timeout"`
	// ToolConcurrency caps tool calls running at once within one turn.
	ToolConcurrency int `yaml:"toolConcurrency"`
	// MaxToolResultBytes truncates a tool result fed back to the model.
	MaxToolResultBytes int `yaml:"maxToolResultBytes"`
}

// AIMemoryConfig bounds chat memory.
type AIMemoryConfig struct {
	// DefaultWindow is the number of recent messages a memory node loads when
	// it sets none.
	DefaultWindow int `yaml:"defaultWindow"`
	// MaxWindow caps the window a node may ask for.
	MaxWindow int `yaml:"maxWindow"`
	// MaxMessages caps the messages stored per session; the oldest are dropped
	// when a session grows past it.
	MaxMessages int `yaml:"maxMessages"`
	// Retention deletes messages older than this (checked on every write).
	Retention time.Duration `yaml:"retention"`
}

// AIToolConfig bounds the tool sub-nodes. The code tool uses nodes.code limits.
type AIToolConfig struct {
	// HTTPTimeout bounds one request of the HTTP tool.
	HTTPTimeout time.Duration `yaml:"httpTimeout"`
	// HTTPMaxResponseBytes truncates the response body the HTTP tool returns.
	HTTPMaxResponseBytes int `yaml:"httpMaxResponseBytes"`
	// WorkflowTimeout bounds one run of the workflow tool.
	WorkflowTimeout time.Duration `yaml:"workflowTimeout"`
}

func defaultAIAgent() AIAgentConfig {
	return AIAgentConfig{MaxIterations: 10, MaxTotalTokens: 200000, Timeout: 5 * time.Minute, ToolConcurrency: 4, MaxToolResultBytes: 32 << 10}
}

func defaultAIMemory() AIMemoryConfig {
	return AIMemoryConfig{DefaultWindow: 10, MaxWindow: 200, MaxMessages: 1000, Retention: 30 * 24 * time.Hour}
}

func defaultAITool() AIToolConfig {
	return AIToolConfig{HTTPTimeout: 30 * time.Second, HTTPMaxResponseBytes: 1 << 20, WorkflowTimeout: 2 * time.Minute}
}

func (c AIAgentConfig) validate(errs []error) []error {
	errs = appendPositive(errs, "nodes.ai.agent.maxIterations", int64(c.MaxIterations))
	errs = appendPositive(errs, "nodes.ai.agent.maxTotalTokens", int64(c.MaxTotalTokens))
	errs = appendPositive(errs, "nodes.ai.agent.timeout", int64(c.Timeout))
	errs = appendPositive(errs, "nodes.ai.agent.toolConcurrency", int64(c.ToolConcurrency))
	return appendPositive(errs, "nodes.ai.agent.maxToolResultBytes", int64(c.MaxToolResultBytes))
}

func (c AIMemoryConfig) validate(errs []error) []error {
	errs = appendPositive(errs, "nodes.ai.memory.defaultWindow", int64(c.DefaultWindow))
	errs = appendPositive(errs, "nodes.ai.memory.maxWindow", int64(c.MaxWindow))
	errs = appendPositive(errs, "nodes.ai.memory.maxMessages", int64(c.MaxMessages))
	errs = appendPositive(errs, "nodes.ai.memory.retention", int64(c.Retention))
	if c.DefaultWindow > c.MaxWindow {
		errs = append(errs, errors.New("nodes.ai.memory.defaultWindow must not exceed nodes.ai.memory.maxWindow"))
	}
	if c.MaxWindow > c.MaxMessages {
		errs = append(errs, errors.New("nodes.ai.memory.maxWindow must not exceed nodes.ai.memory.maxMessages"))
	}
	return errs
}

func (c AIToolConfig) validate(errs []error) []error {
	errs = appendPositive(errs, "nodes.ai.tool.httpTimeout", int64(c.HTTPTimeout))
	errs = appendPositive(errs, "nodes.ai.tool.httpMaxResponseBytes", int64(c.HTTPMaxResponseBytes))
	return appendPositive(errs, "nodes.ai.tool.workflowTimeout", int64(c.WorkflowTimeout))
}

func aiAgentSettings() []setting {
	return []setting{
		{"ENGI_NODES_AI_AGENT_MAX_ITERATIONS", "nodes-ai-agent-max-iterations", "AI Agent model calls per item",
			intSetting(func(c *Config) *int { return &c.Nodes.AI.Agent.MaxIterations })},
		{"ENGI_NODES_AI_AGENT_MAX_TOTAL_TOKENS", "nodes-ai-agent-max-total-tokens", "AI Agent token budget per item",
			intSetting(func(c *Config) *int { return &c.Nodes.AI.Agent.MaxTotalTokens })},
		{"ENGI_NODES_AI_AGENT_TIMEOUT", "nodes-ai-agent-timeout", "AI Agent time limit per item",
			durationSetting(func(c *Config) *time.Duration { return &c.Nodes.AI.Agent.Timeout })},
		{"ENGI_NODES_AI_AGENT_TOOL_CONCURRENCY", "nodes-ai-agent-tool-concurrency", "AI Agent parallel tool calls per turn",
			intSetting(func(c *Config) *int { return &c.Nodes.AI.Agent.ToolConcurrency })},
		{"ENGI_NODES_AI_AGENT_MAX_TOOL_RESULT_BYTES", "nodes-ai-agent-max-tool-result-bytes", "AI Agent tool result size fed back to the model",
			intSetting(func(c *Config) *int { return &c.Nodes.AI.Agent.MaxToolResultBytes })},
		{"ENGI_NODES_AI_MEMORY_DEFAULT_WINDOW", "nodes-ai-memory-default-window", "messages a memory node loads by default",
			intSetting(func(c *Config) *int { return &c.Nodes.AI.Memory.DefaultWindow })},
		{"ENGI_NODES_AI_MEMORY_MAX_WINDOW", "nodes-ai-memory-max-window", "largest window a memory node may ask for",
			intSetting(func(c *Config) *int { return &c.Nodes.AI.Memory.MaxWindow })},
		{"ENGI_NODES_AI_MEMORY_MAX_MESSAGES", "nodes-ai-memory-max-messages", "messages stored per chat session",
			intSetting(func(c *Config) *int { return &c.Nodes.AI.Memory.MaxMessages })},
		{"ENGI_NODES_AI_MEMORY_RETENTION", "nodes-ai-memory-retention", "age after which chat messages are deleted",
			durationSetting(func(c *Config) *time.Duration { return &c.Nodes.AI.Memory.Retention })},
		{"ENGI_NODES_AI_TOOL_HTTP_TIMEOUT", "nodes-ai-tool-http-timeout", "time limit of one HTTP tool request",
			durationSetting(func(c *Config) *time.Duration { return &c.Nodes.AI.Tool.HTTPTimeout })},
		{"ENGI_NODES_AI_TOOL_HTTP_MAX_RESPONSE_BYTES", "nodes-ai-tool-http-max-response-bytes", "HTTP tool response bytes returned to the model",
			intSetting(func(c *Config) *int { return &c.Nodes.AI.Tool.HTTPMaxResponseBytes })},
		{"ENGI_NODES_AI_TOOL_WORKFLOW_TIMEOUT", "nodes-ai-tool-workflow-timeout", "time limit of one workflow tool run",
			durationSetting(func(c *Config) *time.Duration { return &c.Nodes.AI.Tool.WorkflowTimeout })},
	}
}
