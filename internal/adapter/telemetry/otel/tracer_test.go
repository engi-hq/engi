package otel_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/engi-hq/engi/internal/adapter/telemetry/otel"
	"github.com/engi-hq/engi/internal/port"
)

const incoming = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

func TestMiddlewareContinuesIncomingTraceWithoutLeakingURL(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tr := otel.NewFromProvider(sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp)))
	var inner string
	h := tr.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner = tr.Inject(r.Context())
		w.WriteHeader(http.StatusTeapot)
	}))
	r := httptest.NewRequest(http.MethodPost, "/hook/secret-token?code=abc", http.NoBody)
	r.Header.Set("traceparent", incoming)
	h.ServeHTTP(httptest.NewRecorder(), r)

	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("spans = %d", len(spans))
	}
	if got := spans[0].SpanContext.TraceID().String(); got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("trace id = %s", got)
	}
	if !strings.Contains(inner, "4bf92f3577b34da6a3ce929d0e0e4736") {
		t.Errorf("handler context lost the trace: %q", inner)
	}
	for _, kv := range spans[0].Attributes {
		if v := kv.Value.String(); strings.Contains(v, "secret-token") || strings.Contains(v, "abc") {
			t.Errorf("attribute %s leaks the URL: %s", kv.Key, v)
		}
	}
}

func TestSpanAttributesAndFailure(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tr := otel.NewFromProvider(sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp)))
	_, s := tr.Start(t.Context(), "n", port.StrAttr("a", "b"), port.IntAttr("i", 3))
	s.Fail("boom")
	s.End()
	got := exp.GetSpans()[0]
	if len(got.Attributes) != 2 || got.Status.Description != "boom" {
		t.Fatalf("span = %+v", got)
	}
}
