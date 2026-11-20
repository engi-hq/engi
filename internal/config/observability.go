package config

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// ObservabilityConfig turns on metrics and tracing. Both are off by default.
type ObservabilityConfig struct {
	Metrics MetricsConfig `yaml:"metrics"`
	Tracing TracingConfig `yaml:"tracing"`
}

// MetricsConfig configures the Prometheus endpoint.
type MetricsConfig struct {
	// Enabled serves the metrics endpoint.
	Enabled bool `yaml:"enabled"`
	// Path is the URL path of the endpoint.
	Path string `yaml:"path"`
	// Listen, when not empty, serves the endpoint on its own listener at this
	// address instead of the main server. Either way the endpoint is outside
	// API authentication: bind it privately or protect it at the network.
	Listen string `yaml:"listen"`
	// Buckets are histogram upper bounds in seconds, ascending.
	Buckets MetricBuckets `yaml:"buckets"`
}

// MetricBuckets holds the histogram bucket boundaries, in seconds.
type MetricBuckets struct {
	Execution []float64 `yaml:"execution"`
	Node      []float64 `yaml:"node"`
	Claim     []float64 `yaml:"claim"`
	HTTP      []float64 `yaml:"http"`
	// LLM buckets model call durations.
	LLM []float64 `yaml:"llm"`
}

// TracingConfig configures OpenTelemetry tracing over OTLP/HTTP.
type TracingConfig struct {
	Enabled bool `yaml:"enabled"`
	// Endpoint is the OTLP/HTTP collector as host:port.
	Endpoint string `yaml:"endpoint"`
	// Insecure sends plain HTTP instead of HTTPS.
	Insecure bool `yaml:"insecure"`
	// SamplingRatio is the fraction of new traces recorded, 0 to 1. A trace
	// continued from an incoming traceparent follows its parent's decision.
	SamplingRatio float64 `yaml:"samplingRatio"`
	// ServiceName is the service.name resource attribute.
	ServiceName string `yaml:"serviceName"`
}

func defaultObservability() ObservabilityConfig {
	return ObservabilityConfig{
		Metrics: MetricsConfig{
			Path: "/metrics",
			Buckets: MetricBuckets{
				Execution: []float64{0.01, 0.05, 0.1, 0.5, 1, 5, 10, 30, 60, 300, 900},
				Node:      []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5, 10, 30, 60},
				Claim:     []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5, 30, 60},
				HTTP:      []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
				LLM:       []float64{0.25, 0.5, 1, 2, 5, 10, 20, 40, 80, 160},
			},
		},
		Tracing: TracingConfig{Endpoint: "localhost:4318", SamplingRatio: 1, ServiceName: "engi"},
	}
}

// Validate reports every problem with the observability settings.
func (c ObservabilityConfig) Validate() error {
	var errs []error
	m := c.Metrics
	if m.Enabled {
		if !strings.HasPrefix(m.Path, "/") || strings.ContainsAny(m.Path, " {}") {
			errs = append(errs, fmt.Errorf("observability.metrics.path %q must start with / and have no wildcards", m.Path))
		}
		for name, b := range map[string][]float64{"execution": m.Buckets.Execution, "node": m.Buckets.Node, "claim": m.Buckets.Claim, "http": m.Buckets.HTTP, "llm": m.Buckets.LLM} {
			if len(b) == 0 || !slices.IsSorted(b) || b[0] <= 0 || len(slices.Compact(slices.Clone(b))) != len(b) {
				errs = append(errs, fmt.Errorf("observability.metrics.buckets.%s must be a non-empty, strictly ascending list of positive seconds", name))
			}
		}
	}
	t := c.Tracing
	if t.Enabled {
		if strings.TrimSpace(t.Endpoint) == "" {
			errs = append(errs, errors.New("observability.tracing.endpoint is required when tracing is enabled"))
		}
		if strings.TrimSpace(t.ServiceName) == "" {
			errs = append(errs, errors.New("observability.tracing.serviceName is required when tracing is enabled"))
		}
		if t.SamplingRatio < 0 || t.SamplingRatio > 1 {
			errs = append(errs, errors.New("observability.tracing.samplingRatio must be between 0 and 1"))
		}
	}
	return errors.Join(errs...)
}

func observabilitySettings() []setting {
	return []setting{
		{"ENGI_METRICS_ENABLED", "metrics-enabled", "serve Prometheus metrics",
			boolSetting(func(c *Config) *bool { return &c.Observability.Metrics.Enabled })},
		{"ENGI_METRICS_PATH", "metrics-path", "metrics URL path",
			stringSetting(func(c *Config) *string { return &c.Observability.Metrics.Path })},
		{"ENGI_METRICS_LISTEN", "metrics-listen", "serve metrics on this separate address (empty = main server)",
			stringSetting(func(c *Config) *string { return &c.Observability.Metrics.Listen })},
		{"ENGI_TRACING_ENABLED", "tracing-enabled", "export OpenTelemetry traces",
			boolSetting(func(c *Config) *bool { return &c.Observability.Tracing.Enabled })},
		{"ENGI_TRACING_ENDPOINT", "tracing-endpoint", "OTLP/HTTP collector host:port",
			stringSetting(func(c *Config) *string { return &c.Observability.Tracing.Endpoint })},
		{"ENGI_TRACING_INSECURE", "tracing-insecure", "use plain HTTP to the collector",
			boolSetting(func(c *Config) *bool { return &c.Observability.Tracing.Insecure })},
		{"ENGI_TRACING_SAMPLING_RATIO", "tracing-sampling-ratio", "fraction of traces recorded, 0 to 1",
			func(c *Config, v string) (err error) {
				c.Observability.Tracing.SamplingRatio, err = strconv.ParseFloat(v, 64)
				return
			}},
		{"ENGI_TRACING_SERVICE_NAME", "tracing-service-name", "service.name resource attribute",
			stringSetting(func(c *Config) *string { return &c.Observability.Tracing.ServiceName })},
	}
}
