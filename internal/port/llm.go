package port

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/engi-hq/engi/pkg/nodesdk"
)

// LLM is a provider-neutral chat model. Adapters translate to and from each
// vendor's HTTP API; callers (the chat node, the agent loop) never see vendor
// types. Implementations must be safe for concurrent use.
//
// Failures are returned as *LLMError (use errors.As) so callers can decide
// whether to retry; context cancellation is returned as the context error.
type LLM interface {
	// Chat sends the conversation and returns the complete reply.
	Chat(ctx context.Context, req LLMRequest) (LLMResponse, error)
	// Stream behaves like Chat but calls emit for each text delta as it
	// arrives. It returns the aggregated response, tool calls included. An
	// error from emit aborts the call and is returned.
	Stream(ctx context.Context, req LLMRequest, emit func(StreamEvent) error) (LLMResponse, error)
}

// Provider ids understood by LLMFactory.
const (
	LLMProviderAnthropic   = "anthropic"
	LLMProviderOpenAI      = "openai"
	LLMProviderAzureOpenAI = "azureopenai"
	LLMProviderOllama      = "ollama"
)

// LLMFactory builds an LLM for one provider from a decrypted credential. The
// HTTP client is the shared outbound client. Building is cheap and does no
// I/O, so callers build per call and never cache across credential edits.
type LLMFactory interface {
	// New returns the LLM for provider (one of the LLMProvider* ids). It
	// fails with *LLMConfigError when the provider is unknown or the
	// credential is incomplete.
	New(provider string, cred nodesdk.Credential, httpc nodesdk.HTTPClient) (LLM, error)
}

// Outcomes reported to Metrics.LLMCall.
const (
	LLMOutcomeOK          = "ok"
	LLMOutcomeRateLimited = "rate_limited"
	LLMOutcomeError       = "error"
)

// LLMConfigError reports an unknown provider or an unusable credential.
type LLMConfigError struct {
	Provider string
	Message  string
}

func (e *LLMConfigError) Error() string {
	return fmt.Sprintf("llm provider %q: %s", e.Provider, e.Message)
}

// LLMRole is the author of a message.
type LLMRole string

// Message roles.
const (
	RoleSystem    LLMRole = "system"
	RoleUser      LLMRole = "user"
	RoleAssistant LLMRole = "assistant"
	RoleTool      LLMRole = "tool"
)

// LLMMessage is one conversation turn. Assistant messages may carry ToolCalls
// (Content may then be empty). A tool message answers the call named by
// ToolCallID with Content, and sets IsError when the tool failed.
type LLMMessage struct {
	Role       LLMRole
	Content    string
	ToolCalls  []LLMToolCall
	ToolCallID string
	IsError    bool
}

// LLMToolCall is a tool invocation requested by the model.
type LLMToolCall struct {
	ID   string
	Name string
	// Arguments is the call's JSON object (never empty: "{}" when none).
	Arguments json.RawMessage
}

// ToolDefinition describes a tool to the model.
type ToolDefinition struct {
	Name        string
	Description string
	// Parameters is a JSON Schema object. Empty means no parameters.
	Parameters json.RawMessage
}

// ToolChoiceMode says whether and how the model may call tools.
type ToolChoiceMode string

// Tool choice modes. The zero value behaves like ToolChoiceAuto.
const (
	ToolChoiceAuto ToolChoiceMode = "auto"
	ToolChoiceNone ToolChoiceMode = "none"
	// ToolChoiceRequired forces some tool call.
	ToolChoiceRequired ToolChoiceMode = "required"
	// ToolChoiceTool forces the tool named in ToolChoice.Name.
	ToolChoiceTool ToolChoiceMode = "tool"
)

// ToolChoice constrains tool use.
type ToolChoice struct {
	Mode ToolChoiceMode
	// Name is the forced tool for ToolChoiceTool.
	Name string
}

// ResponseFormatKind selects the shape of the model's reply.
type ResponseFormatKind string

// Response formats. The zero value behaves like ResponseText.
const (
	ResponseText ResponseFormatKind = "text"
	// ResponseJSON asks for any valid JSON object.
	ResponseJSON ResponseFormatKind = "json"
	// ResponseJSONSchema asks for JSON conforming to Schema.
	ResponseJSONSchema ResponseFormatKind = "jsonSchema"
)

// ResponseFormat constrains the reply. Name and Schema apply to
// ResponseJSONSchema only.
type ResponseFormat struct {
	Kind   ResponseFormatKind
	Name   string
	Schema json.RawMessage
}

// LLMRequest is one chat call. Pointer fields are optional: nil leaves the
// provider default.
type LLMRequest struct {
	Model    string
	System   string
	Messages []LLMMessage
	Tools    []ToolDefinition
	// ToolChoice applies when Tools is not empty.
	ToolChoice  ToolChoice
	Temperature *float64
	TopP        *float64
	// MaxTokens caps the reply; 0 leaves the provider (or adapter) default.
	MaxTokens int
	Stop      []string
	Format    ResponseFormat
}

// StopReason is why the model stopped, normalized across providers.
type StopReason string

// Stop reasons.
const (
	StopEnd          StopReason = "end"
	StopMaxTokens    StopReason = "maxTokens"
	StopToolUse      StopReason = "toolUse"
	StopStopSequence StopReason = "stopSequence"
	StopOther        StopReason = "other"
)

// LLMUsage counts tokens as the provider reports them.
type LLMUsage struct {
	InputTokens  int
	OutputTokens int
}

// LLMResponse is the model's reply.
type LLMResponse struct {
	Content    string
	ToolCalls  []LLMToolCall
	StopReason StopReason
	Usage      LLMUsage
	// Model is the model that answered, as the provider names it.
	Model string
}

// StreamEvent is one incremental piece of a streamed reply.
type StreamEvent struct {
	// TextDelta is the next chunk of reply text.
	TextDelta string
}

// LLMError is a failed provider call.
type LLMError struct {
	// Provider is the LLMProvider* id.
	Provider string
	// Status is the HTTP status, 0 when none was received.
	Status int
	// Message is the provider's error text (never the request).
	Message string
	// Retryable is true for rate limits (429), server errors (5xx) and
	// transport failures.
	Retryable bool
	// RetryAfter is the provider's requested wait, 0 when not given.
	RetryAfter time.Duration
	Err        error
}

func (e *LLMError) Error() string {
	s := e.Provider
	if e.Status != 0 {
		s += fmt.Sprintf(" HTTP %d", e.Status)
	}
	if e.Message != "" {
		s += ": " + e.Message
	}
	if e.Err != nil {
		s += ": " + e.Err.Error()
	}
	return s
}

// Unwrap returns the underlying transport error, if any.
func (e *LLMError) Unwrap() error { return e.Err }

// IsRetryableLLMError reports whether err is an *LLMError worth retrying.
func IsRetryableLLMError(err error) bool {
	var le *LLMError
	return errors.As(err, &le) && le.Retryable
}
