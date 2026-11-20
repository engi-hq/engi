// Package aikit holds what the agent node and its sub-nodes (model, memory and
// the tool nodes) share: the tool name/description/schema parameters, a
// provider that wraps one tool, argument checks and the provider credential
// map. It depends on the node SDK and ports only.
package aikit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	credanthropic "github.com/engi-hq/engi/internal/credtype/anthropic"
	credazure "github.com/engi-hq/engi/internal/credtype/azureopenai"
	credollama "github.com/engi-hq/engi/internal/credtype/ollama"
	credopenai "github.com/engi-hq/engi/internal/credtype/openai"
	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// CredentialTypes maps a model provider id to its credential type (the
// binding name equals the type name).
var CredentialTypes = map[string]string{
	port.LLMProviderAnthropic:   credanthropic.Name,
	port.LLMProviderOpenAI:      credopenai.Name,
	port.LLMProviderAzureOpenAI: credazure.Name,
	port.LLMProviderOllama:      credollama.Name,
}

// ProviderCredentials lists the four provider credential slots.
func ProviderCredentials() []nodesdk.CredentialRef {
	return []nodesdk.CredentialRef{
		{Name: credanthropic.Name, Type: credanthropic.Name},
		{Name: credopenai.Name, Type: credopenai.Name},
		{Name: credazure.Name, Type: credazure.Name},
		{Name: credollama.Name, Type: credollama.Name},
	}
}

// ProviderProperty is the "provider" options parameter.
func ProviderProperty() nodesdk.Property {
	return nodesdk.Property{Name: "provider", DisplayName: "Provider", Type: nodesdk.PropOptions, Default: port.LLMProviderAnthropic,
		Description: "Bind the credential of the matching type.",
		Options: []nodesdk.Option{
			nodekit.Opt(port.LLMProviderAnthropic, "Anthropic"), nodekit.Opt(port.LLMProviderOpenAI, "OpenAI"),
			nodekit.Opt(port.LLMProviderAzureOpenAI, "Azure OpenAI"), nodekit.Opt(port.LLMProviderOllama, "Ollama"),
		}}
}

// toolNamePattern is the common provider limit for tool names.
var toolNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// ValidToolName reports whether name is usable as a tool name.
func ValidToolName(name string) bool { return toolNamePattern.MatchString(name) }

// DefaultSchema is the argument schema of a tool that takes one free-text input.
const DefaultSchema = `{"type":"object","properties":{"input":{"type":"string","description":"The input for the tool."}},"required":["input"]}`

// ToolProperties are the parameters every tool sub-node starts with.
func ToolProperties(defaultName, defaultDescription string) []nodesdk.Property {
	return []nodesdk.Property{
		{Name: "name", DisplayName: "Tool name", Type: nodesdk.PropString, Required: true, Default: defaultName,
			Description: "What the model calls the tool: letters, digits, underscores and hyphens (at most 64). Must be unique within an agent."},
		{Name: "description", DisplayName: "Description", Type: nodesdk.PropString, Required: true, Default: defaultDescription,
			Description: "Tells the model what the tool does and when to use it."},
	}
}

// SchemaProperty is the "parameters" JSON Schema parameter.
func SchemaProperty(desc string) nodesdk.Property {
	return nodesdk.Property{Name: "parameters", DisplayName: "Arguments schema", Type: nodesdk.PropJSON, Default: DefaultSchema,
		Description: desc}
}

// ToolMeta is a tool's name, description and argument schema.
type ToolMeta struct {
	Def    port.ToolDefinition
	Schema Schema
}

// ReadToolMeta reads and validates the name, description and "parameters"
// parameters (resolved against the parent's item 0).
func ReadToolMeta(ctx context.Context, in nodesdk.Input) (ToolMeta, error) {
	name, err := nodesdk.ParamString(ctx, in, "name", 0)
	if err != nil {
		return ToolMeta{}, err //nolint:wrapcheck // SDK errors are descriptive
	}
	if name = strings.TrimSpace(name); !ValidToolName(name) {
		return ToolMeta{}, fmt.Errorf("tool name %q must be 1-64 letters, digits, underscores or hyphens", name)
	}
	desc, err := nodesdk.ParamString(ctx, in, "description", 0)
	if err != nil {
		return ToolMeta{}, err //nolint:wrapcheck // SDK errors are descriptive
	}
	raw, err := schemaParam(ctx, in)
	if err != nil {
		return ToolMeta{}, err
	}
	sch, err := ParseSchema(raw)
	if err != nil {
		return ToolMeta{}, err
	}
	return ToolMeta{Def: port.ToolDefinition{Name: name, Description: strings.TrimSpace(desc), Parameters: raw}, Schema: sch}, nil
}

func schemaParam(ctx context.Context, in nodesdk.Input) (json.RawMessage, error) {
	v, err := in.Param(ctx, "parameters", 0)
	if err != nil {
		return nil, fmt.Errorf("reading parameter %q: %w", "parameters", err)
	}
	var raw []byte
	switch t := v.(type) {
	case nil:
		return json.RawMessage(DefaultSchema), nil
	case string:
		if strings.TrimSpace(t) == "" {
			return json.RawMessage(DefaultSchema), nil
		}
		raw = []byte(t)
	default:
		if raw, err = json.Marshal(t); err != nil {
			return nil, fmt.Errorf("encoding arguments schema: %w", err)
		}
	}
	return json.RawMessage(raw), nil
}

// Schema is the part of a JSON Schema object the tools need: which arguments
// exist and which are required.
type Schema struct {
	Properties map[string]struct{}
	Required   []string
}

// ParseSchema reads a JSON Schema object.
func ParseSchema(raw json.RawMessage) (Schema, error) {
	var s struct {
		Type       string                     `json:"type"`
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return Schema{}, fmt.Errorf("arguments schema must be a JSON object: %w", err)
	}
	if s.Type != "" && s.Type != "object" {
		return Schema{}, fmt.Errorf("arguments schema type must be \"object\", got %q", s.Type)
	}
	out := Schema{Properties: make(map[string]struct{}, len(s.Properties)), Required: s.Required}
	for k := range s.Properties {
		out.Properties[k] = struct{}{}
	}
	for _, r := range s.Required {
		if _, ok := out.Properties[r]; !ok {
			return Schema{}, fmt.Errorf("arguments schema requires %q but does not define it", r)
		}
	}
	return out, nil
}

// ParseArgs decodes the model's arguments into an object and checks that the
// required ones are present. The error text is meant for the model.
func (s Schema) ParseArgs(raw json.RawMessage) (map[string]any, error) {
	args := map[string]any{}
	if strings.TrimSpace(string(raw)) != "" {
		if err := json.Unmarshal(raw, &args); err != nil || args == nil {
			return nil, errors.New("arguments must be a JSON object")
		}
	}
	for _, r := range s.Required {
		if v, ok := args[r]; !ok || v == nil {
			return nil, fmt.Errorf("missing required argument %q", r)
		}
	}
	return args, nil
}

// Tools is a port.ToolProvider over a fixed set of tools.
type Tools []port.Tool

var _ port.ToolProvider = Tools(nil)

// Tools implements port.ToolProvider.
func (t Tools) Tools(context.Context) ([]port.Tool, error) { return t, nil }

// ErrorResult is a tool result the model can react to.
func ErrorResult(format string, a ...any) port.ToolResult {
	return port.ToolResult{Content: fmt.Sprintf(format, a...), IsError: true}
}
