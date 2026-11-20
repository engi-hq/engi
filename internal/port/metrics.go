package port

import (
	"context"
	"time"

	"github.com/engi-hq/engi/internal/domain"
)

// Metrics records operational measurements. Services depend on this port and
// never on a metrics library; NopMetrics is used when metrics are disabled.
// Label values are low-cardinality enums (statuses, modes, node types, route
// patterns), never ids or raw paths. Implementations must be safe for
// concurrent use and must not block.
type Metrics interface {
	// ExecutionStarted counts an execution that began running (not a resume).
	ExecutionStarted(mode domain.ExecutionMode)
	// ExecutionFinished counts an execution that reached a terminal status and
	// observes how long the run took.
	ExecutionFinished(mode domain.ExecutionMode, status domain.ExecutionStatus, d time.Duration)
	// NodeFinished observes how long one node run took.
	NodeFinished(nodeType string, d time.Duration)
	// JobClaimed observes the time a job waited between becoming available and
	// being claimed by a worker.
	JobClaimed(wait time.Duration)
	// JobReclaimed counts a job taken back from a worker whose lease expired;
	// outcome is ReclaimRequeued or ReclaimFailed.
	JobReclaimed(outcome string)
	// WorkerBusy sets the number of worker slots currently running a job.
	WorkerBusy(slots int)
	// WatchQueueDepth exposes the job queue depth, sampled when metrics are read.
	WatchQueueDepth(stats QueueStats)
	// WebhookRequest counts a webhook request by HTTP status.
	WebhookRequest(status int)
	// HTTPRequest records an API request. route is the registered route
	// pattern ("GET /workflows/{id}"), or a fixed placeholder for no match.
	HTTPRequest(route string, status int, d time.Duration)
	// TriggerError counts a trigger failure; kind is "listener" or "poll".
	TriggerError(kind string)
	// KafkaPaused reports whether Kafka consumers are paused for backpressure.
	KafkaPaused(paused bool)
	// LLMCall records one finished model call. provider is an LLMProvider* id,
	// model the requested model name, outcome one of the LLMOutcome* values.
	// Token counts are the provider-reported usage (0 when none was returned).
	LLMCall(provider, model, outcome string, d time.Duration, usage LLMUsage)
}

// Outcomes of reclaiming a job whose worker stopped responding.
const (
	// ReclaimRequeued means the job went back to the queue for another try.
	ReclaimRequeued = "requeued"
	// ReclaimFailed means the job ran out of attempts and was parked as failed.
	ReclaimFailed = "failed"
)

// NopMetrics discards every measurement.
type NopMetrics struct{}

// ExecutionStarted implements Metrics.
func (NopMetrics) ExecutionStarted(domain.ExecutionMode) {}

// ExecutionFinished implements Metrics.
func (NopMetrics) ExecutionFinished(domain.ExecutionMode, domain.ExecutionStatus, time.Duration) {}

// NodeFinished implements Metrics.
func (NopMetrics) NodeFinished(string, time.Duration) {}

// JobClaimed implements Metrics.
func (NopMetrics) JobClaimed(time.Duration) {}

// JobReclaimed implements Metrics.
func (NopMetrics) JobReclaimed(string) {}

// WorkerBusy implements Metrics.
func (NopMetrics) WorkerBusy(int) {}

// WatchQueueDepth implements Metrics.
func (NopMetrics) WatchQueueDepth(QueueStats) {}

// WebhookRequest implements Metrics.
func (NopMetrics) WebhookRequest(int) {}

// HTTPRequest implements Metrics.
func (NopMetrics) HTTPRequest(string, int, time.Duration) {}

// TriggerError implements Metrics.
func (NopMetrics) TriggerError(string) {}

// KafkaPaused implements Metrics.
func (NopMetrics) KafkaPaused(bool) {}

// LLMCall implements Metrics.
func (NopMetrics) LLMCall(string, string, string, time.Duration, LLMUsage) {}

// Tracer starts spans and carries trace context between processes. Services
// depend on this port, never on a tracing library. Span attributes must never
// hold item data or credentials.
type Tracer interface {
	// Start begins a span named name as a child of any span in ctx.
	Start(ctx context.Context, name string, attrs ...SpanAttr) (context.Context, Span)
	// Inject returns the W3C traceparent of the span in ctx, or "".
	Inject(ctx context.Context) string
	// Extract returns ctx carrying the remote trace context in traceparent
	// (as returned by Inject); an empty or invalid value returns ctx unchanged.
	Extract(ctx context.Context, traceparent string) context.Context
}

// SpanAttr is a string-keyed span attribute with a string or int value.
type SpanAttr struct {
	Key   string
	Str   string
	Int   int
	IsInt bool
}

// StrAttr builds a string attribute.
func StrAttr(key, v string) SpanAttr { return SpanAttr{Key: key, Str: v} }

// IntAttr builds an integer attribute.
func IntAttr(key string, v int) SpanAttr { return SpanAttr{Key: key, Int: v, IsInt: true} }

// Span is one unit of traced work.
type Span interface {
	// SetAttrs adds attributes.
	SetAttrs(attrs ...SpanAttr)
	// Fail marks the span failed; description must not contain item data.
	Fail(description string)
	// End finishes the span.
	End()
}

// NopTracer starts spans that record nothing.
type NopTracer struct{}

type nopSpan struct{}

func (nopSpan) SetAttrs(...SpanAttr) {}
func (nopSpan) Fail(string)          {}
func (nopSpan) End()                 {}

// Start implements Tracer.
func (NopTracer) Start(ctx context.Context, _ string, _ ...SpanAttr) (context.Context, Span) {
	return ctx, nopSpan{}
}

// Inject implements Tracer.
func (NopTracer) Inject(context.Context) string { return "" }

// Extract implements Tracer.
func (NopTracer) Extract(ctx context.Context, _ string) context.Context { return ctx }
