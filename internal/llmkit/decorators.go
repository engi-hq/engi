package llmkit

import (
	"context"
	"errors"
	"time"

	"github.com/engi-hq/engi/internal/port"
)

type timeoutLLM struct {
	inner   port.LLM
	timeout time.Duration
}

// WithTimeout bounds each call (streaming included) to d. A call whose
// context already has an earlier deadline keeps it.
func WithTimeout(inner port.LLM, d time.Duration) port.LLM {
	return &timeoutLLM{inner: inner, timeout: d}
}

func (t *timeoutLLM) Chat(ctx context.Context, req port.LLMRequest) (port.LLMResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()
	return t.inner.Chat(ctx, req)
}

func (t *timeoutLLM) Stream(ctx context.Context, req port.LLMRequest, emit func(port.StreamEvent) error) (port.LLMResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()
	return t.inner.Stream(ctx, req, emit)
}

type meteredLLM struct {
	inner    port.LLM
	provider string
	metrics  port.Metrics
	now      func() time.Time
}

// WithMetrics records every call of inner (token usage, duration, outcome)
// under provider and the requested model. now is the clock (time.Now in
// production). Model names come from node settings, so they are as
// low-cardinality as the workflows using them.
func WithMetrics(inner port.LLM, provider string, m port.Metrics, now func() time.Time) port.LLM {
	return &meteredLLM{inner: inner, provider: provider, metrics: m, now: now}
}

func (m *meteredLLM) Chat(ctx context.Context, req port.LLMRequest) (port.LLMResponse, error) {
	start := m.now()
	resp, err := m.inner.Chat(ctx, req)
	m.record(req, resp, err, start)
	return resp, err
}

func (m *meteredLLM) Stream(ctx context.Context, req port.LLMRequest, emit func(port.StreamEvent) error) (port.LLMResponse, error) {
	start := m.now()
	resp, err := m.inner.Stream(ctx, req, emit)
	m.record(req, resp, err, start)
	return resp, err
}

func (m *meteredLLM) record(req port.LLMRequest, resp port.LLMResponse, err error, start time.Time) {
	outcome := port.LLMOutcomeOK
	if err != nil {
		outcome = port.LLMOutcomeError
		var le *port.LLMError
		if errors.As(err, &le) && le.Status == 429 {
			outcome = port.LLMOutcomeRateLimited
		}
	}
	m.metrics.LLMCall(m.provider, req.Model, outcome, m.now().Sub(start), resp.Usage)
}
