// Package otel implements port.Tracer with OpenTelemetry and exports spans over
// OTLP/HTTP. It also provides the HTTP server middleware that opens a span per
// request and continues an incoming W3C trace context.
package otel

import (
	"context"
	"fmt"
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/port"
)

const instrumentationName = "github.com/engi-hq/engi"

// Tracer is a port.Tracer backed by an OpenTelemetry tracer provider. It uses
// no global provider or propagator.
type Tracer struct {
	tracer trace.Tracer
	prop   propagation.TextMapPropagator
}

var _ port.Tracer = (*Tracer)(nil)

// New builds a tracer exporting to the OTLP/HTTP collector in cfg. The returned
// function flushes and stops the exporter.
func New(ctx context.Context, cfg config.TracingConfig) (*Tracer, func(context.Context) error, error) {
	opts := []otlptracehttp.Option{otlptracehttp.WithEndpoint(cfg.Endpoint)}
	if cfg.Insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}
	exp, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("creating OTLP trace exporter: %w", err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SamplingRatio))),
		sdktrace.WithResource(resource.NewSchemaless(semconv.ServiceName(cfg.ServiceName))),
	)
	return NewFromProvider(tp), tp.Shutdown, nil
}

// NewFromProvider wraps an existing provider (tests use an in-memory exporter).
func NewFromProvider(tp trace.TracerProvider) *Tracer {
	return &Tracer{tracer: tp.Tracer(instrumentationName), prop: propagation.TraceContext{}}
}

// Start implements port.Tracer.
func (t *Tracer) Start(ctx context.Context, name string, attrs ...port.SpanAttr) (context.Context, port.Span) {
	ctx, s := t.tracer.Start(ctx, name, trace.WithAttributes(kv(attrs)...))
	return ctx, span{s}
}

// Inject implements port.Tracer.
func (t *Tracer) Inject(ctx context.Context) string {
	c := propagation.MapCarrier{}
	t.prop.Inject(ctx, c)
	return c.Get("traceparent")
}

// Extract implements port.Tracer.
func (t *Tracer) Extract(ctx context.Context, traceparent string) context.Context {
	if traceparent == "" {
		return ctx
	}
	return t.prop.Extract(ctx, propagation.MapCarrier{"traceparent": traceparent})
}

type span struct{ s trace.Span }

func (s span) SetAttrs(attrs ...port.SpanAttr) { s.s.SetAttributes(kv(attrs)...) }
func (s span) Fail(description string)         { s.s.SetStatus(codes.Error, description) }
func (s span) End()                            { s.s.End() }

func kv(attrs []port.SpanAttr) []attribute.KeyValue {
	out := make([]attribute.KeyValue, len(attrs))
	for i, a := range attrs {
		if a.IsInt {
			out[i] = attribute.Int(a.Key, a.Int)
		} else {
			out[i] = attribute.String(a.Key, a.Str)
		}
	}
	return out
}

// Middleware opens a server span per request, continuing any incoming
// traceparent, so executions started by a webhook join the caller's trace. The
// span records the method, matched pattern and status; never the path, query,
// headers or body, which can hold secrets.
func (t *Tracer) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := t.prop.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		ctx, s := t.tracer.Start(ctx, "HTTP "+r.Method, trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(attribute.String("http.request.method", r.Method)))
		defer s.End()
		rec := &statusWriter{ResponseWriter: w}
		r = r.WithContext(ctx)
		next.ServeHTTP(rec, r)
		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		s.SetAttributes(attribute.Int("http.response.status_code", status))
		if r.Pattern != "" {
			s.SetAttributes(attribute.String("http.route", r.Pattern))
		}
		if status >= http.StatusInternalServerError {
			s.SetStatus(codes.Error, "server error")
		}
	})
}

// statusWriter captures the status code while keeping Flush for event streams.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
