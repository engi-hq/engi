package servicetest

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

// TestWebhooks is an in-memory port.TestWebhookStore. Several services can share
// one instance to play the pods of a cluster.
type TestWebhooks struct {
	mu    sync.Mutex
	tests map[string]domain.ArmedTestWebhook // by method and shape
}

var _ port.TestWebhookStore = (*TestWebhooks)(nil)

// NewTestWebhooks returns an empty store.
func NewTestWebhooks() *TestWebhooks {
	return &TestWebhooks{tests: map[string]domain.ArmedTestWebhook{}}
}

func testKey(method, shape string) string { return method + " " + shape }

// Arm implements port.TestWebhookStore.
func (w *TestWebhooks) Arm(_ context.Context, a domain.ArmedTestWebhook, now time.Time) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	for k, t := range w.tests {
		if !now.Before(t.ExpiresAt) {
			delete(w.tests, k)
		}
	}
	w.tests[testKey(a.Method, a.Shape())] = a
	return nil
}

// Find implements port.TestWebhookStore.
func (w *TestWebhooks) Find(_ context.Context, method string, segments int, now time.Time) ([]domain.ArmedTestWebhook, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []domain.ArmedTestWebhook
	for _, t := range w.tests {
		if t.Method == method && t.Route().Segments() == segments && now.Before(t.ExpiresAt) {
			out = append(out, t)
		}
	}
	return out, nil
}

// Claim implements port.TestWebhookStore.
func (w *TestWebhooks) Claim(_ context.Context, method, shape string, now time.Time) (domain.ArmedTestWebhook, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	k := testKey(method, shape)
	t, ok := w.tests[k]
	if !ok || !now.Before(t.ExpiresAt) {
		return domain.ArmedTestWebhook{}, fmt.Errorf("test webhook %s: %w", k, domain.ErrNotFound)
	}
	delete(w.tests, k)
	return t, nil
}

// DeleteByWorkflow implements port.TestWebhookStore.
func (w *TestWebhooks) DeleteByWorkflow(_ context.Context, id domain.WorkflowID) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	for k, t := range w.tests {
		if t.WorkflowID == id {
			delete(w.tests, k)
		}
	}
	return nil
}
