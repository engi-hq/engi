package prometheus_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/engi-hq/engi/internal/adapter/metrics/prometheus"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

type depth int

func (d depth) Depth(context.Context) (int, error) { return int(d), nil }

func TestRecordsMetricsInAPrivateRegistry(t *testing.T) {
	m := prometheus.New(config.Default().Observability.Metrics)
	m.ExecutionStarted(domain.ModeWebhook)
	m.ExecutionFinished(domain.ModeWebhook, domain.StatusError, 2*time.Second)
	m.NodeFinished("engi.flow.if", 3*time.Millisecond)
	m.JobClaimed(40 * time.Millisecond)
	m.WorkerBusy(3)
	m.WebhookRequest(404)
	m.HTTPRequest("GET /workflows/{id}", 0, time.Millisecond)
	m.TriggerError("poll")
	m.KafkaPaused(true)
	m.JobReclaimed("requeued")
	m.JobReclaimed("requeued")
	m.JobReclaimed("failed")
	m.WatchQueueDepth(depth(7))
	m.WatchQueueDepth(depth(8)) // a second registration is ignored

	want := `
# HELP engi_executions_finished_total Executions that reached a terminal status, by mode and status.
# TYPE engi_executions_finished_total counter
engi_executions_finished_total{mode="webhook",status="error"} 1
# HELP engi_http_requests_total API requests, by route pattern and status.
# TYPE engi_http_requests_total counter
engi_http_requests_total{route="GET /workflows/{id}",status="200"} 1
# HELP engi_kafka_consumers_paused 1 while Kafka consumers are paused for queue backpressure.
# TYPE engi_kafka_consumers_paused gauge
engi_kafka_consumers_paused 1
# HELP engi_queue_jobs_reclaimed_total Jobs taken back from a worker whose lease expired, by outcome (requeued, failed).
# TYPE engi_queue_jobs_reclaimed_total counter
engi_queue_jobs_reclaimed_total{outcome="failed"} 1
engi_queue_jobs_reclaimed_total{outcome="requeued"} 2
# HELP engi_queue_depth Jobs waiting to be claimed.
# TYPE engi_queue_depth gauge
engi_queue_depth 7
# HELP engi_trigger_errors_total Trigger failures, by kind (listener, poll).
# TYPE engi_trigger_errors_total counter
engi_trigger_errors_total{kind="poll"} 1
# HELP engi_webhook_requests_total Webhook requests, by HTTP status.
# TYPE engi_webhook_requests_total counter
engi_webhook_requests_total{status="404"} 1
# HELP engi_worker_busy_slots Worker slots currently running a job.
# TYPE engi_worker_busy_slots gauge
engi_worker_busy_slots 3
`
	if err := testutil.GatherAndCompare(m.Registry(), strings.NewReader(want),
		"engi_executions_finished_total", "engi_http_requests_total", "engi_kafka_consumers_paused",
		"engi_queue_depth", "engi_queue_jobs_reclaimed_total", "engi_trigger_errors_total", "engi_webhook_requests_total", "engi_worker_busy_slots"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"engi_execution_duration_seconds", "engi_node_duration_seconds", "engi_queue_claim_latency_seconds", "engi_http_request_duration_seconds"} {
		if n, err := testutil.GatherAndCount(m.Registry(), name); err != nil || n == 0 {
			t.Errorf("%s: count %d, err %v", name, n, err)
		}
	}
}

func TestHistogramBucketsComeFromConfig(t *testing.T) {
	cfg := config.Default().Observability.Metrics
	cfg.Buckets.Node = []float64{0.5, 2}
	m := prometheus.New(cfg)
	m.NodeFinished("x", time.Second)
	mfs, err := m.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() == "engi_node_duration_seconds" {
			if n := len(mf.GetMetric()[0].GetHistogram().GetBucket()); n != 2 {
				t.Fatalf("buckets = %d, want 2", n)
			}
			return
		}
	}
	t.Fatal("histogram not found")
}

func TestLLMCall(t *testing.T) {
	m := prometheus.New(config.Default().Observability.Metrics)
	m.LLMCall("anthropic", "claude-sonnet-5-5", "ok", time.Second, port.LLMUsage{InputTokens: 10, OutputTokens: 4})
	m.LLMCall("anthropic", "claude-sonnet-5-5", "rate_limited", time.Second, port.LLMUsage{})
	want := `# HELP engi_llm_requests_total Model calls, by provider, model and outcome (ok, rate_limited, error).
# TYPE engi_llm_requests_total counter
engi_llm_requests_total{model="claude-sonnet-5-5",outcome="ok",provider="anthropic"} 1
engi_llm_requests_total{model="claude-sonnet-5-5",outcome="rate_limited",provider="anthropic"} 1
# HELP engi_llm_tokens_total Tokens used by model calls, by provider, model and kind (input, output).
# TYPE engi_llm_tokens_total counter
engi_llm_tokens_total{kind="input",model="claude-sonnet-5-5",provider="anthropic"} 10
engi_llm_tokens_total{kind="output",model="claude-sonnet-5-5",provider="anthropic"} 4
`
	if err := testutil.GatherAndCompare(m.Registry(), strings.NewReader(want), "engi_llm_requests_total", "engi_llm_tokens_total"); err != nil {
		t.Fatal(err)
	}
	if n, err := testutil.GatherAndCount(m.Registry(), "engi_llm_request_duration_seconds"); err != nil || n == 0 {
		t.Errorf("duration: count %d, err %v", n, err)
	}
}
