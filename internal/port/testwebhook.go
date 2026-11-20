package port

import (
	"context"
	"time"

	"github.com/engi-hq/engi/internal/domain"
)

// TestWebhookStore holds the armed test webhooks. It is shared by every process
// of a cluster, so the webhook pod that receives a test call can serve a test
// armed by the api pod. A test answers exactly one call. Implementations must
// make Claim atomic across processes.
type TestWebhookStore interface {
	// Arm stores the test, replacing an armed test with the same method and
	// shape, and deletes the tests that expired at or before now.
	Arm(ctx context.Context, t domain.ArmedTestWebhook, now time.Time) error
	// Find returns the unexpired tests (at now) whose method and segment count
	// equal the call's. The caller matches the path itself.
	Find(ctx context.Context, method string, segments int, now time.Time) ([]domain.ArmedTestWebhook, error)
	// Claim removes and returns the unexpired test with the method and shape.
	// Among concurrent claims for one test exactly one succeeds; the others
	// wrap domain.ErrNotFound.
	Claim(ctx context.Context, method, shape string, now time.Time) (domain.ArmedTestWebhook, error)
	// DeleteByWorkflow disarms every test of the workflow.
	DeleteByWorkflow(ctx context.Context, id domain.WorkflowID) error
}
