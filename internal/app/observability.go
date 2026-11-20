package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	promadapter "github.com/engi-hq/engi/internal/adapter/metrics/prometheus"
	oteladapter "github.com/engi-hq/engi/internal/adapter/telemetry/otel"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/port"
)

// metricsReadHeaderTimeout guards the separate metrics listener against slow clients.
const metricsReadHeaderTimeout = 10 * time.Second

// observability is the metrics and tracing stack: real adapters when enabled
// in configuration, no-ops otherwise.
type observability struct {
	metrics port.Metrics
	tracer  port.Tracer
	// metricsHandler serves /metrics; nil when metrics are disabled or the
	// metrics implementation was injected.
	metricsHandler http.Handler
	// traceMiddleware wraps the HTTP server with server spans; nil when tracing is off.
	traceMiddleware func(http.Handler) http.Handler
	stopTracing     func(context.Context) error
}

// buildObservability applies the observability section. Injected metrics or
// tracer (tests) take precedence over configuration.
func buildObservability(ctx context.Context, cfg config.ObservabilityConfig, opts Options) (*observability, error) {
	o := &observability{metrics: port.NopMetrics{}, tracer: port.NopTracer{}, stopTracing: func(context.Context) error { return nil }}
	switch {
	case opts.Metrics != nil:
		o.metrics = opts.Metrics
	case cfg.Metrics.Enabled:
		m := promadapter.New(cfg.Metrics)
		o.metrics, o.metricsHandler = m, m.Handler()
	}
	switch {
	case opts.Tracer != nil:
		o.tracer = opts.Tracer
	case cfg.Tracing.Enabled:
		t, stop, err := oteladapter.New(ctx, cfg.Tracing)
		if err != nil {
			return nil, fmt.Errorf("starting tracing: %w", err)
		}
		o.tracer, o.stopTracing = t, stop
	}
	if mw, ok := o.tracer.(interface {
		Middleware(http.Handler) http.Handler
	}); ok {
		o.traceMiddleware = mw.Middleware
	}
	return o, nil
}

// Metrics returns the metrics recorder; a no-op when metrics are disabled.
func (a *App) Metrics() port.Metrics { return a.obs.metrics }

// Tracer returns the tracer; a no-op when tracing is disabled.
func (a *App) Tracer() port.Tracer { return a.obs.tracer }

// MetricsHandler serves the Prometheus text format; nil when metrics are disabled.
func (a *App) MetricsHandler() http.Handler { return a.obs.metricsHandler }

// CloseTelemetry flushes pending spans and stops the trace exporter.
func (a *App) CloseTelemetry(ctx context.Context) error {
	if err := a.obs.stopTracing(ctx); err != nil {
		return fmt.Errorf("stopping tracing: %w", err)
	}
	return nil
}

// instrument wraps the server handler in tracing. The metrics endpoint is
// mounted on the mux by mountMetrics, outside API authentication.
func (a *App) instrument(h http.Handler) http.Handler {
	if a.obs.traceMiddleware == nil {
		return h
	}
	return a.obs.traceMiddleware(h)
}

// mountMetrics serves the metrics endpoint on mux when metrics are enabled and
// no separate listener is configured.
func (a *App) mountMetrics(mux *http.ServeMux) {
	m := a.Config.Observability.Metrics
	if a.obs.metricsHandler != nil && m.Listen == "" {
		mux.Handle("GET "+m.Path, a.obs.metricsHandler)
	}
}

// metricsServer serves metrics on their own listener (observability.metrics.listen).
type metricsServer struct {
	http *http.Server
	lis  net.Listener
}

func (a *App) startMetricsServer() (*metricsServer, error) {
	m := a.Config.Observability.Metrics
	if a.obs.metricsHandler == nil || m.Listen == "" {
		return nil, nil //nolint:nilnil // no separate listener configured
	}
	lis, err := net.Listen("tcp", m.Listen)
	if err != nil {
		return nil, fmt.Errorf("listening for metrics on %s: %w", m.Listen, err)
	}
	mux := http.NewServeMux()
	mux.Handle("GET "+m.Path, a.obs.metricsHandler)
	s := &metricsServer{lis: lis, http: &http.Server{Handler: mux, ReadHeaderTimeout: metricsReadHeaderTimeout}}
	log := a.Logger.With("component", "metrics")
	go func() {
		if err := s.http.Serve(lis); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("metrics server stopped", slog.Any("error", err))
		}
	}()
	log.Info("metrics listening", "addr", lis.Addr().String(), "path", m.Path)
	return s, nil
}

func (s *metricsServer) shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if err := s.http.Shutdown(ctx); err != nil {
		return fmt.Errorf("stopping metrics server: %w", err)
	}
	return nil
}

// countWebhooks counts every request the webhook handler answers, by status.
func countWebhooks(h http.Handler, m port.Metrics) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w}
		h.ServeHTTP(rec, r)
		m.WebhookRequest(rec.status)
	})
}

// statusRecorder remembers the first status written; zero means the implicit 200.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
