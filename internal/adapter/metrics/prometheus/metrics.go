// Package prometheus implements port.Metrics with the Prometheus client. It
// owns a private registry (no global default registry) and serves it over HTTP.
package prometheus

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"time"

	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

const namespace = "engi"

// Metrics records engine, worker, webhook, API and trigger measurements.
type Metrics struct {
	reg *prom.Registry

	execStarted   *prom.CounterVec
	execFinished  *prom.CounterVec
	execDuration  *prom.HistogramVec
	nodeDuration  *prom.HistogramVec
	claimLatency  prom.Histogram
	jobsReclaimed *prom.CounterVec
	workerBusy    prom.Gauge
	webhooks      *prom.CounterVec
	httpRequests  *prom.CounterVec
	httpDuration  *prom.HistogramVec
	triggerErrs   *prom.CounterVec
	kafkaPaused   prom.Gauge
	llmCalls      *prom.CounterVec
	llmDuration   *prom.HistogramVec
	llmTokens     *prom.CounterVec
	embedCalls    *prom.CounterVec
	embedDur      *prom.HistogramVec
	embedTokens   *prom.CounterVec
}

var _ port.Metrics = (*Metrics)(nil)
var _ port.EmbeddingMetrics = (*Metrics)(nil)

// New builds the metrics with histogram buckets from cfg, plus the Go runtime
// and process collectors, in a fresh registry.
func New(cfg config.MetricsConfig) *Metrics {
	reg := prom.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	m := &Metrics{
		reg: reg,
		execStarted: prom.NewCounterVec(prom.CounterOpts{Namespace: namespace, Name: "executions_started_total",
			Help: "Executions that began running, by mode."}, []string{"mode"}),
		execFinished: prom.NewCounterVec(prom.CounterOpts{Namespace: namespace, Name: "executions_finished_total",
			Help: "Executions that reached a terminal status, by mode and status."}, []string{"mode", "status"}),
		execDuration: prom.NewHistogramVec(prom.HistogramOpts{Namespace: namespace, Name: "execution_duration_seconds",
			Help: "Execution run time.", Buckets: cfg.Buckets.Execution}, []string{"mode", "status"}),
		nodeDuration: prom.NewHistogramVec(prom.HistogramOpts{Namespace: namespace, Name: "node_duration_seconds",
			Help: "Node run time, by node type.", Buckets: cfg.Buckets.Node}, []string{"node_type"}),
		claimLatency: prom.NewHistogram(prom.HistogramOpts{Namespace: namespace, Name: "queue_claim_latency_seconds",
			Help: "Time a job waited between becoming available and being claimed.", Buckets: cfg.Buckets.Claim}),
		jobsReclaimed: prom.NewCounterVec(prom.CounterOpts{Namespace: namespace, Name: "queue_jobs_reclaimed_total",
			Help: "Jobs taken back from a worker whose lease expired, by outcome (requeued, failed)."}, []string{"outcome"}),
		workerBusy: prom.NewGauge(prom.GaugeOpts{Namespace: namespace, Name: "worker_busy_slots",
			Help: "Worker slots currently running a job."}),
		webhooks: prom.NewCounterVec(prom.CounterOpts{Namespace: namespace, Name: "webhook_requests_total",
			Help: "Webhook requests, by HTTP status."}, []string{"status"}),
		httpRequests: prom.NewCounterVec(prom.CounterOpts{Namespace: namespace, Name: "http_requests_total",
			Help: "API requests, by route pattern and status."}, []string{"route", "status"}),
		httpDuration: prom.NewHistogramVec(prom.HistogramOpts{Namespace: namespace, Name: "http_request_duration_seconds",
			Help: "API request duration, by route pattern.", Buckets: cfg.Buckets.HTTP}, []string{"route"}),
		triggerErrs: prom.NewCounterVec(prom.CounterOpts{Namespace: namespace, Name: "trigger_errors_total",
			Help: "Trigger failures, by kind (listener, poll)."}, []string{"kind"}),
		kafkaPaused: prom.NewGauge(prom.GaugeOpts{Namespace: namespace, Name: "kafka_consumers_paused",
			Help: "1 while Kafka consumers are paused for queue backpressure."}),
		llmCalls: prom.NewCounterVec(prom.CounterOpts{Namespace: namespace, Name: "llm_requests_total",
			Help: "Model calls, by provider, model and outcome (ok, rate_limited, error)."}, []string{"provider", "model", "outcome"}),
		llmDuration: prom.NewHistogramVec(prom.HistogramOpts{Namespace: namespace, Name: "llm_request_duration_seconds",
			Help: "Model call duration, by provider and model.", Buckets: cfg.Buckets.LLM}, []string{"provider", "model"}),
		llmTokens: prom.NewCounterVec(prom.CounterOpts{Namespace: namespace, Name: "llm_tokens_total",
			Help: "Tokens used by model calls, by provider, model and kind (input, output)."}, []string{"provider", "model", "kind"}),
		embedCalls: prom.NewCounterVec(prom.CounterOpts{Namespace: namespace, Name: "embedding_requests_total",
			Help: "Embedding calls, by provider, model and outcome (ok, rate_limited, error)."}, []string{"provider", "model", "outcome"}),
		embedDur: prom.NewHistogramVec(prom.HistogramOpts{Namespace: namespace, Name: "embedding_request_duration_seconds",
			Help: "Embedding call duration, by provider and model.", Buckets: cfg.Buckets.LLM}, []string{"provider", "model"}),
		embedTokens: prom.NewCounterVec(prom.CounterOpts{Namespace: namespace, Name: "embedding_tokens_total",
			Help: "Input tokens used by embedding calls, by provider and model."}, []string{"provider", "model"}),
	}
	reg.MustRegister(m.execStarted, m.execFinished, m.execDuration, m.nodeDuration, m.claimLatency,
		m.jobsReclaimed, m.workerBusy, m.webhooks, m.httpRequests, m.httpDuration, m.triggerErrs, m.kafkaPaused,
		m.llmCalls, m.llmDuration, m.llmTokens, m.embedCalls, m.embedDur, m.embedTokens)
	return m
}

// Registry returns the private registry, for tests and extra collectors.
func (m *Metrics) Registry() *prom.Registry { return m.reg }

// Handler serves the registry in the Prometheus text format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}

// ExecutionStarted implements port.Metrics.
func (m *Metrics) ExecutionStarted(mode domain.ExecutionMode) {
	m.execStarted.WithLabelValues(string(mode)).Inc()
}

// ExecutionFinished implements port.Metrics.
func (m *Metrics) ExecutionFinished(mode domain.ExecutionMode, status domain.ExecutionStatus, d time.Duration) {
	m.execFinished.WithLabelValues(string(mode), string(status)).Inc()
	m.execDuration.WithLabelValues(string(mode), string(status)).Observe(d.Seconds())
}

// NodeFinished implements port.Metrics.
func (m *Metrics) NodeFinished(nodeType string, d time.Duration) {
	m.nodeDuration.WithLabelValues(nodeType).Observe(d.Seconds())
}

// JobClaimed implements port.Metrics.
func (m *Metrics) JobClaimed(wait time.Duration) { m.claimLatency.Observe(wait.Seconds()) }

// JobReclaimed implements port.Metrics.
func (m *Metrics) JobReclaimed(outcome string) { m.jobsReclaimed.WithLabelValues(outcome).Inc() }

// WorkerBusy implements port.Metrics.
func (m *Metrics) WorkerBusy(slots int) { m.workerBusy.Set(float64(slots)) }

// WatchQueueDepth implements port.Metrics: the depth is read at scrape time.
func (m *Metrics) WatchQueueDepth(stats port.QueueStats) {
	if stats == nil {
		return
	}
	// Registering twice (a second runtime on the same app) keeps the first.
	_ = m.reg.Register(prom.NewGaugeFunc(prom.GaugeOpts{Namespace: namespace, Name: "queue_depth",
		Help: "Jobs waiting to be claimed."}, func() float64 {
		n, err := stats.Depth(context.Background())
		if err != nil {
			return math.NaN()
		}
		return float64(n)
	}))
}

// WebhookRequest implements port.Metrics.
func (m *Metrics) WebhookRequest(status int) { m.webhooks.WithLabelValues(statusLabel(status)).Inc() }

// HTTPRequest implements port.Metrics.
func (m *Metrics) HTTPRequest(route string, status int, d time.Duration) {
	m.httpRequests.WithLabelValues(route, statusLabel(status)).Inc()
	m.httpDuration.WithLabelValues(route).Observe(d.Seconds())
}

// TriggerError implements port.Metrics.
func (m *Metrics) TriggerError(kind string) { m.triggerErrs.WithLabelValues(kind).Inc() }

// KafkaPaused implements port.Metrics.
func (m *Metrics) KafkaPaused(paused bool) {
	if paused {
		m.kafkaPaused.Set(1)
		return
	}
	m.kafkaPaused.Set(0)
}

// statusLabel renders a status code; 0 (nothing written) is the implicit 200.
func statusLabel(status int) string {
	if status == 0 {
		status = http.StatusOK
	}
	return strconv.Itoa(status)
}

// LLMCall implements port.Metrics.
func (m *Metrics) LLMCall(provider, model, outcome string, d time.Duration, usage port.LLMUsage) {
	m.llmCalls.WithLabelValues(provider, model, outcome).Inc()
	m.llmDuration.WithLabelValues(provider, model).Observe(d.Seconds())
	if usage.InputTokens > 0 {
		m.llmTokens.WithLabelValues(provider, model, "input").Add(float64(usage.InputTokens))
	}
	if usage.OutputTokens > 0 {
		m.llmTokens.WithLabelValues(provider, model, "output").Add(float64(usage.OutputTokens))
	}
}

// EmbeddingCall implements port.EmbeddingMetrics.
func (m *Metrics) EmbeddingCall(provider, model, outcome string, d time.Duration, usage port.EmbedUsage) {
	m.embedCalls.WithLabelValues(provider, model, outcome).Inc()
	m.embedDur.WithLabelValues(provider, model).Observe(d.Seconds())
	if usage.InputTokens > 0 {
		m.embedTokens.WithLabelValues(provider, model).Add(float64(usage.InputTokens))
	}
}
