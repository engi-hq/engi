package app

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	oteladapter "github.com/engi-hq/engi/internal/adapter/telemetry/otel"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

func metricsConfig() config.Config {
	cfg := config.Default()
	cfg.Observability.Metrics.Enabled = true
	return cfg
}

func scrape(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", http.NoBody))
	body, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func runPipeline(t *testing.T, a *App) {
	t.Helper()
	state, err := a.Run(context.Background(), RunRequest{Workflow: workflow(t, pipelineDoc), Items: []domain.Item{
		{JSON: map[string]any{"n": 1.0}}, {JSON: map[string]any{"n": 5.0, "secret": "s3cr3t-value"}},
	}})
	if err != nil || state.Status != domain.StatusSuccess {
		t.Fatalf("run: %v, %+v", err, state)
	}
}

func TestMetricsRecordedForAWorkflowRun(t *testing.T) {
	a, err := New(t.Context(), metricsConfig(), Options{LogOutput: &bytes.Buffer{}, LookupEnv: func(string) (string, bool) { return "", false }})
	if err != nil {
		t.Fatal(err)
	}
	runPipeline(t, a)
	out := scrape(t, a.MetricsHandler())
	for _, want := range []string{
		`engi_executions_started_total{mode="manual"} 1`,
		`engi_executions_finished_total{mode="manual",status="success"} 1`,
		`engi_execution_duration_seconds_count{mode="manual",status="success"} 1`,
		`engi_node_duration_seconds_count{node_type="engi.transform.set"}`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("metrics missing %q", want)
		}
	}
}

func TestMetricsDisabledIsNoOp(t *testing.T) {
	a := newApp(t)
	if a.MetricsHandler() != nil {
		t.Fatal("metrics handler present while disabled")
	}
	runPipeline(t, a)
	if _, ok := a.Metrics().(port.NopMetrics); !ok {
		t.Fatalf("metrics = %T, want NopMetrics", a.Metrics())
	}
	if _, ok := a.Tracer().(port.NopTracer); !ok {
		t.Fatalf("tracer = %T, want NopTracer", a.Tracer())
	}
}

func TestHTTPMetricsUseRoutePatterns(t *testing.T) {
	f := newServerFixture(t, func(c *config.Config) { c.Observability.Metrics.Enabled = true })
	for _, p := range []string{"/api/v1/workflows/aaa", "/api/v1/workflows/bbb", "/no/such/thing"} {
		f.do(req{method: "GET", path: p})
	}
	out := scrape(t, f.app.MetricsHandler())
	if !strings.Contains(out, `route="GET /api/v1/workflows/{id}"`) {
		t.Errorf("route pattern label missing:\n%s", grep(out, "engi_http_requests_total"))
	}
	if !strings.Contains(out, `route="unmatched"`) {
		t.Errorf("unmatched label missing:\n%s", grep(out, "engi_http_requests_total"))
	}
	for _, raw := range []string{"aaa", "bbb", "no/such"} {
		if strings.Contains(grep(out, "engi_http_"), raw) {
			t.Errorf("raw path %q leaked into labels", raw)
		}
	}
}

func TestMetricsEndpointOnMainServerBypassesAuth(t *testing.T) {
	f := newServerFixture(t, func(c *config.Config) { c.Observability.Metrics.Enabled = true })
	ts := httptest.NewServer(f.srv.http.Handler)
	defer ts.Close()
	res, err := http.Get(ts.URL + "/metrics") //nolint:noctx // test
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), "engi_") {
		t.Fatalf("status %d, body %.200s", res.StatusCode, body)
	}
}

func grep(text, substr string) string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		if strings.Contains(l, substr) {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

func TestTracingSpanTreeWithoutDataOrSecrets(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tracer := oteladapter.NewFromProvider(sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp)))
	a, err := New(t.Context(), config.Default(), Options{LogOutput: &bytes.Buffer{}, Tracer: tracer, LookupEnv: func(string) (string, bool) { return "", false }})
	if err != nil {
		t.Fatal(err)
	}
	runPipeline(t, a)

	spans := exp.GetSpans()
	var root tracetest.SpanStub
	nodes := 0
	for _, s := range spans {
		if s.Name == "engi.execution" {
			root = s
		}
	}
	if !root.SpanContext.IsValid() {
		t.Fatalf("no execution span in %d spans", len(spans))
	}
	for _, s := range spans {
		if s.Name != "engi.node" {
			continue
		}
		nodes++
		if s.Parent.SpanID() != root.SpanContext.SpanID() {
			t.Errorf("node span %v is not a child of the execution span", s.Attributes)
		}
	}
	if nodes == 0 {
		t.Fatal("no node spans")
	}
	for _, s := range spans {
		for _, kv := range s.Attributes {
			if strings.Contains(kv.Value.String(), "s3cr3t-value") || strings.Contains(string(kv.Key), "item") {
				t.Errorf("span %s carries data attribute %s=%s", s.Name, kv.Key, kv.Value.String())
			}
		}
	}
}

func TestTraceContextPropagatesThroughTraceparent(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tracer := oteladapter.NewFromProvider(sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp)))
	ctx, span := tracer.Start(context.Background(), "incoming")
	tp := tracer.Inject(ctx)
	span.End()
	if tp == "" {
		t.Fatal("empty traceparent")
	}
	ctx2, child := tracer.Start(tracer.Extract(context.Background(), tp), "child")
	child.End()
	_ = ctx2
	var in, ch tracetest.SpanStub
	for _, s := range exp.GetSpans() {
		if s.Name == "incoming" {
			in = s
		} else {
			ch = s
		}
	}
	if ch.SpanContext.TraceID() != in.SpanContext.TraceID() || ch.Parent.SpanID() != in.SpanContext.SpanID() {
		t.Fatal("child span did not continue the incoming trace")
	}
}
