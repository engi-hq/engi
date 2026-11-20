package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/engi-hq/engi/internal/adapter/pgdb"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

// TestWebhooks implements port.TestWebhookStore.
type TestWebhooks struct{ s *Store }

var _ port.TestWebhookStore = TestWebhooks{}

// TestWebhooks returns the armed test webhook store.
func (s *Store) TestWebhooks() TestWebhooks { return TestWebhooks{s} }

// Arm implements port.TestWebhookStore.
func (w TestWebhooks) Arm(ctx context.Context, a domain.ArmedTestWebhook, now time.Time) error {
	return w.s.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := w.s.run.Q(ctx).ExecContext(ctx,
			`DELETE FROM test_webhooks WHERE expires_at <= ?`, pgdb.Stamp(now)); err != nil {
			return fmt.Errorf("deleting expired test webhooks: %w", err)
		}
		_, err := w.s.run.Q(ctx).ExecContext(ctx,
			`INSERT INTO test_webhooks (method, shape, path, segments, workflow_id, node, expires_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT (method, shape) DO UPDATE SET path = excluded.path, segments = excluded.segments,
			     workflow_id = excluded.workflow_id, node = excluded.node, expires_at = excluded.expires_at`,
			a.Method, a.Shape(), a.Path, a.Route().Segments(), string(a.WorkflowID), a.Node, pgdb.Stamp(a.ExpiresAt))
		if err != nil {
			return fmt.Errorf("arming test webhook %s /%s: %w", a.Method, a.Path, err)
		}
		return nil
	})
}

// Find implements port.TestWebhookStore.
func (w TestWebhooks) Find(ctx context.Context, method string, segments int, now time.Time) ([]domain.ArmedTestWebhook, error) {
	rows, err := w.s.run.Q(ctx).QueryContext(ctx,
		`SELECT method, path, workflow_id, node, expires_at FROM test_webhooks
		 WHERE method = ? AND segments = ? AND expires_at > ? ORDER BY shape`,
		method, segments, pgdb.Stamp(now))
	if err != nil {
		return nil, fmt.Errorf("finding test webhooks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []domain.ArmedTestWebhook
	for rows.Next() {
		a, err := scanTestWebhook(rows)
		if err != nil {
			return nil, fmt.Errorf("reading test webhook: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("finding test webhooks: %w", err)
	}
	return out, nil
}

// Claim implements port.TestWebhookStore. The delete and the return are one
// statement, so a test is handed out once even when calls race.
func (w TestWebhooks) Claim(ctx context.Context, method, shape string, now time.Time) (domain.ArmedTestWebhook, error) {
	row := w.s.run.Q(ctx).QueryRowContext(ctx,
		`DELETE FROM test_webhooks WHERE method = ? AND shape = ? AND expires_at > ?
		 RETURNING method, path, workflow_id, node, expires_at`,
		method, shape, pgdb.Stamp(now))
	a, err := scanTestWebhook(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ArmedTestWebhook{}, notFound("test webhook", method+" /"+shape)
	}
	if err != nil {
		return domain.ArmedTestWebhook{}, fmt.Errorf("claiming test webhook %s /%s: %w", method, shape, err)
	}
	return a, nil
}

// DeleteByWorkflow implements port.TestWebhookStore.
func (w TestWebhooks) DeleteByWorkflow(ctx context.Context, id domain.WorkflowID) error {
	if _, err := w.s.run.Q(ctx).ExecContext(ctx, `DELETE FROM test_webhooks WHERE workflow_id = ?`, string(id)); err != nil {
		return fmt.Errorf("disarming test webhooks of workflow %s: %w", id, err)
	}
	return nil
}

func scanTestWebhook(row rowScanner) (domain.ArmedTestWebhook, error) {
	var a domain.ArmedTestWebhook
	var workflow string
	var expires time.Time
	if err := row.Scan(&a.Method, &a.Path, &workflow, &a.Node, &expires); err != nil {
		return domain.ArmedTestWebhook{}, err //nolint:wrapcheck // wrapped by callers
	}
	a.WorkflowID = domain.WorkflowID(workflow)
	a.ExpiresAt = pgdb.FromDB(expires)
	return a, nil
}
