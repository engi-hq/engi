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

// Triggers implements port.WebhookStore, port.TriggerStateStore and
// port.ActivationStore.
type Triggers struct{ s *Store }

var (
	_ port.WebhookStore      = Triggers{}
	_ port.TriggerStateStore = Triggers{}
	_ port.ActivationStore   = Triggers{}
)

// Triggers returns the trigger registration, state and activation stores.
func (s *Store) Triggers() Triggers { return Triggers{s} }

const webhookColumns = `method, path, workflow_id, node, created_at`

// Register implements port.WebhookStore.
func (t Triggers) Register(ctx context.Context, r domain.WebhookRegistration) error {
	_, err := t.s.run.Q(ctx).ExecContext(ctx,
		`INSERT INTO webhook_registrations (method, path, shape, segments, dynamic, workflow_id, node, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		r.Method, r.Path, r.Shape(), r.Segments(), r.Dynamic(), string(r.WorkflowID), r.Node, pgdb.Stamp(t.s.clk.Now()))
	if isUniqueViolation(err) {
		return fmt.Errorf("webhook %s /%s: %w", r.Method, r.Path, domain.ErrAlreadyExists)
	}
	if err != nil {
		return fmt.Errorf("registering webhook %s /%s: %w", r.Method, r.Path, err)
	}
	return nil
}

// DeleteByWorkflow implements port.WebhookStore.
func (t Triggers) DeleteByWorkflow(ctx context.Context, id domain.WorkflowID) error {
	if _, err := t.s.run.Q(ctx).ExecContext(ctx, `DELETE FROM webhook_registrations WHERE workflow_id = ?`, string(id)); err != nil {
		return fmt.Errorf("deleting webhooks of workflow %s: %w", id, err)
	}
	return nil
}

// Find implements port.WebhookStore.
func (t Triggers) Find(ctx context.Context, method, path string) (domain.WebhookRegistration, error) {
	row := t.s.run.Q(ctx).QueryRowContext(ctx,
		`SELECT `+webhookColumns+` FROM webhook_registrations WHERE method = ? AND shape = ?`, method, path)
	r, err := scanWebhook(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.WebhookRegistration{}, notFound("webhook", method+" /"+path)
	}
	if err != nil {
		return domain.WebhookRegistration{}, fmt.Errorf("finding webhook %s /%s: %w", method, path, err)
	}
	return r, nil
}

// ListDynamic implements port.WebhookStore.
func (t Triggers) ListDynamic(ctx context.Context, method string, segments int) ([]domain.WebhookRegistration, error) {
	return t.list(ctx, `WHERE dynamic = 1 AND method = ? AND segments = ?`, method, segments)
}

// ListByWorkflow implements port.WebhookStore.
func (t Triggers) ListByWorkflow(ctx context.Context, id domain.WorkflowID) ([]domain.WebhookRegistration, error) {
	return t.list(ctx, `WHERE workflow_id = ?`, string(id))
}

func (t Triggers) list(ctx context.Context, where string, args ...any) ([]domain.WebhookRegistration, error) {
	rows, err := t.s.run.Q(ctx).QueryContext(ctx,
		`SELECT `+webhookColumns+` FROM webhook_registrations `+where+` ORDER BY shape, method`, args...)
	if err != nil {
		return nil, fmt.Errorf("listing webhooks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []domain.WebhookRegistration
	for rows.Next() {
		r, err := scanWebhook(rows)
		if err != nil {
			return nil, fmt.Errorf("reading webhook: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing webhooks: %w", err)
	}
	return out, nil
}

func scanWebhook(row rowScanner) (domain.WebhookRegistration, error) {
	var r domain.WebhookRegistration
	var created time.Time
	if err := row.Scan(&r.Method, &r.Path, &r.WorkflowID, &r.Node, &created); err != nil {
		return domain.WebhookRegistration{}, err //nolint:wrapcheck // wrapped by callers
	}
	r.CreatedAt = pgdb.FromDB(created)
	return r, nil
}

// LoadTriggerState implements port.TriggerStateStore.
func (t Triggers) LoadTriggerState(ctx context.Context, id domain.WorkflowID, node string) ([]byte, error) {
	var state string
	err := t.s.run.Q(ctx).QueryRowContext(ctx,
		`SELECT state FROM trigger_state WHERE workflow_id = ? AND node = ?`, string(id), node).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound("trigger state", string(id)+"/"+node)
	}
	if err != nil {
		return nil, fmt.Errorf("loading trigger state of %s/%s: %w", id, node, err)
	}
	return []byte(state), nil
}

// SaveTriggerState implements port.TriggerStateStore.
func (t Triggers) SaveTriggerState(ctx context.Context, id domain.WorkflowID, node string, state []byte) error {
	_, err := t.s.run.Q(ctx).ExecContext(ctx,
		`INSERT INTO trigger_state (workflow_id, node, state, updated_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT (workflow_id, node) DO UPDATE SET state = excluded.state, updated_at = excluded.updated_at`,
		string(id), node, string(state), pgdb.Stamp(t.s.clk.Now()))
	if err != nil {
		return fmt.Errorf("saving trigger state of %s/%s: %w", id, node, err)
	}
	return nil
}

// DeleteTriggerState implements port.TriggerStateStore.
func (t Triggers) DeleteTriggerState(ctx context.Context, id domain.WorkflowID) error {
	if _, err := t.s.run.Q(ctx).ExecContext(ctx, `DELETE FROM trigger_state WHERE workflow_id = ?`, string(id)); err != nil {
		return fmt.Errorf("deleting trigger state of workflow %s: %w", id, err)
	}
	return nil
}

// SetActivationError implements port.ActivationStore.
func (t Triggers) SetActivationError(ctx context.Context, id domain.WorkflowID, message string) error {
	_, err := t.s.run.Q(ctx).ExecContext(ctx,
		`INSERT INTO workflow_activation_errors (workflow_id, message, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT (workflow_id) DO UPDATE SET message = excluded.message, updated_at = excluded.updated_at`,
		string(id), message, pgdb.Stamp(t.s.clk.Now()))
	if err != nil {
		return fmt.Errorf("recording activation error of workflow %s: %w", id, err)
	}
	return nil
}

// ActivationError implements port.ActivationStore.
func (t Triggers) ActivationError(ctx context.Context, id domain.WorkflowID) (string, error) {
	var msg string
	err := t.s.run.Q(ctx).QueryRowContext(ctx,
		`SELECT message FROM workflow_activation_errors WHERE workflow_id = ?`, string(id)).Scan(&msg)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading activation error of workflow %s: %w", id, err)
	}
	return msg, nil
}

// ClearActivationError implements port.ActivationStore.
func (t Triggers) ClearActivationError(ctx context.Context, id domain.WorkflowID) error {
	if _, err := t.s.run.Q(ctx).ExecContext(ctx, `DELETE FROM workflow_activation_errors WHERE workflow_id = ?`, string(id)); err != nil {
		return fmt.Errorf("clearing activation error of workflow %s: %w", id, err)
	}
	return nil
}
