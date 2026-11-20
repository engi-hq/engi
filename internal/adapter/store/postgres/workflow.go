package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/engi-hq/engi/internal/adapter/jsoncodec"
	"github.com/engi-hq/engi/internal/adapter/pgdb"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

// Save implements port.WorkflowStore.
func (s *Workflows) Save(ctx context.Context, w domain.Workflow) (domain.Workflow, error) {
	if err := w.ID.Validate(); err != nil {
		return domain.Workflow{}, fmt.Errorf("saving workflow: %w", err)
	}
	now := pgdb.Stamp(s.clk.Now())
	var saved domain.Workflow
	err := s.run.Do(ctx, func(q pgdb.DBTX) error {
		var current int
		err := q.QueryRowContext(ctx, `SELECT version FROM workflows WHERE id = ? FOR UPDATE`, string(w.ID)).Scan(&current)
		exists := err == nil
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("reading workflow version: %w", err)
		}
		w.Version = current + 1
		def, err := jsoncodec.EncodeWorkflow(w)
		if err != nil {
			return fmt.Errorf("encoding workflow: %w", err)
		}
		if exists {
			_, err = q.ExecContext(ctx,
				`UPDATE workflows SET name = ?, active = ?, version = ?, definition = ?, updated_at = ? WHERE id = ?`,
				w.Name, w.Active, w.Version, string(def), now, string(w.ID))
		} else {
			_, err = q.ExecContext(ctx,
				`INSERT INTO workflows (id, name, active, version, definition, created_at, updated_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?)`,
				string(w.ID), w.Name, w.Active, w.Version, string(def), now, now)
		}
		if err != nil {
			return fmt.Errorf("writing workflow: %w", err)
		}
		if _, err := q.ExecContext(ctx,
			`INSERT INTO workflow_versions (workflow_id, version, definition, created_at) VALUES (?, ?, ?, ?)`,
			string(w.ID), w.Version, string(def), now); err != nil {
			return fmt.Errorf("writing workflow version: %w", err)
		}
		if _, err := q.ExecContext(ctx, `DELETE FROM workflow_tags WHERE workflow_id = ?`, string(w.ID)); err != nil {
			return fmt.Errorf("clearing workflow tags: %w", err)
		}
		for _, tag := range w.Tags {
			if _, err := q.ExecContext(ctx,
				`INSERT INTO workflow_tags (workflow_id, tag) VALUES (?, ?) ON CONFLICT DO NOTHING`, string(w.ID), tag); err != nil {
				return fmt.Errorf("writing workflow tag: %w", err)
			}
		}
		saved = w
		return nil
	})
	if err != nil {
		return domain.Workflow{}, fmt.Errorf("saving workflow %s: %w", w.ID, err)
	}
	return saved, nil
}

// Get implements port.WorkflowStore.
func (s *Workflows) Get(ctx context.Context, id domain.WorkflowID) (domain.Workflow, error) {
	var def string
	var active bool
	var version int
	err := s.run.Q(ctx).QueryRowContext(ctx,
		`SELECT definition, active, version FROM workflows WHERE id = ?`, string(id)).Scan(&def, &active, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Workflow{}, notFound("workflow", string(id))
	}
	if err != nil {
		return domain.Workflow{}, fmt.Errorf("reading workflow %s: %w", id, err)
	}
	w, err := decodeWorkflow(def, active, version)
	if err != nil {
		return domain.Workflow{}, err
	}
	if w.Tags, err = s.tagsOf(ctx, id); err != nil {
		return domain.Workflow{}, err
	}
	return w, nil
}

// tagsOf reads the workflow's current tags. The tag tables are authoritative:
// renaming or deleting a tag does not rewrite stored definitions.
func (s *Workflows) tagsOf(ctx context.Context, id domain.WorkflowID) ([]string, error) {
	rows, err := s.run.Q(ctx).QueryContext(ctx, `SELECT tag FROM workflow_tags WHERE workflow_id = ? ORDER BY tag`, string(id))
	if err != nil {
		return nil, fmt.Errorf("reading tags of workflow %s: %w", id, err)
	}
	defer rows.Close()
	var tags []string
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, fmt.Errorf("scanning tag: %w", err)
		}
		tags = append(tags, tag)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating tags: %w", err)
	}
	return tags, nil
}

func decodeWorkflow(def string, active bool, version int) (domain.Workflow, error) {
	w, err := jsoncodec.DecodeWorkflow([]byte(def))
	if err != nil {
		return domain.Workflow{}, fmt.Errorf("decoding stored workflow: %w", err)
	}
	// The columns are authoritative: activation changes without a new version.
	w.Active, w.Version = active, version
	return w, nil
}

// SetActive implements port.WorkflowStore.
func (s *Workflows) SetActive(ctx context.Context, id domain.WorkflowID, active bool) error {
	res, err := s.run.Q(ctx).ExecContext(ctx,
		`UPDATE workflows SET active = ?, updated_at = ? WHERE id = ?`,
		active, pgdb.Stamp(s.clk.Now()), string(id))
	return affectedOrNotFound(res, err, "workflow", string(id))
}

// Delete implements port.WorkflowStore.
func (s *Workflows) Delete(ctx context.Context, id domain.WorkflowID) error {
	res, err := s.run.Q(ctx).ExecContext(ctx, `DELETE FROM workflows WHERE id = ?`, string(id))
	return affectedOrNotFound(res, err, "workflow", string(id))
}

func affectedOrNotFound(res sql.Result, err error, what, id string) error {
	if err != nil {
		return fmt.Errorf("writing %s %s: %w", what, id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("counting affected %s rows: %w", what, err)
	}
	if n == 0 {
		return notFound(what, id)
	}
	return nil
}

// List implements port.WorkflowStore.
func (s *Workflows) List(ctx context.Context, f port.WorkflowFilter, p port.Page) (port.PageResult[domain.Workflow], error) {
	after, err := decodeCursor(p.Cursor)
	if err != nil {
		return port.PageResult[domain.Workflow]{}, err
	}
	var where []string
	var args []any
	if after != "" {
		where = append(where, "id < ?")
		args = append(args, after)
	}
	if f.Active != nil {
		where = append(where, "active = ?")
		args = append(args, *f.Active)
	}
	if f.Tag != "" {
		where = append(where, "id IN (SELECT workflow_id FROM workflow_tags WHERE tag = ?)")
		args = append(args, f.Tag)
	}
	if f.NameContains != "" {
		where = append(where, `lower(name) LIKE ? ESCAPE '\'`)
		args = append(args, "%"+likeEscape(strings.ToLower(f.NameContains))+"%")
	}
	n := s.limit(p)
	query := `SELECT id, definition, active, version FROM workflows` + whereClause(where) +
		` ORDER BY id DESC LIMIT ` + strconv.Itoa(n+1)
	rows, err := s.run.Q(ctx).QueryContext(ctx, query, args...)
	if err != nil {
		return port.PageResult[domain.Workflow]{}, fmt.Errorf("listing workflows: %w", err)
	}
	defer rows.Close()
	var items []domain.Workflow
	var ids []string
	for rows.Next() {
		var id, def string
		var active bool
		var version int
		if err := rows.Scan(&id, &def, &active, &version); err != nil {
			return port.PageResult[domain.Workflow]{}, fmt.Errorf("scanning workflow: %w", err)
		}
		w, err := decodeWorkflow(def, active, version)
		if err != nil {
			return port.PageResult[domain.Workflow]{}, err
		}
		items, ids = append(items, w), append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return port.PageResult[domain.Workflow]{}, fmt.Errorf("iterating workflows: %w", err)
	}
	_ = rows.Close() // free the connection before the per-workflow tag reads
	for i := range items {
		if items[i].Tags, err = s.tagsOf(ctx, items[i].ID); err != nil {
			return port.PageResult[domain.Workflow]{}, err
		}
	}
	return page(items, ids, n), nil
}

// page trims the n+1 probe row and derives the next cursor from the last kept key.
func page[T any](items []T, keys []string, n int) port.PageResult[T] {
	if len(items) <= n {
		return port.PageResult[T]{Items: items}
	}
	return port.PageResult[T]{Items: items[:n], NextCursor: encodeCursor(keys[n-1])}
}

func whereClause(where []string) string {
	if len(where) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(where, " AND ")
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// GetVersion implements port.WorkflowVersionStore.
func (s *Workflows) GetVersion(ctx context.Context, id domain.WorkflowID, version int) (domain.WorkflowVersion, error) {
	var def string
	var created time.Time
	err := s.run.Q(ctx).QueryRowContext(ctx,
		`SELECT definition, created_at FROM workflow_versions WHERE workflow_id = ? AND version = ?`,
		string(id), version).Scan(&def, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.WorkflowVersion{}, notFound("workflow version", fmt.Sprintf("%s@%d", id, version))
	}
	if err != nil {
		return domain.WorkflowVersion{}, fmt.Errorf("reading workflow version: %w", err)
	}
	return decodeVersion(id, version, def, created)
}

func decodeVersion(id domain.WorkflowID, version int, def string, created time.Time) (domain.WorkflowVersion, error) {
	w, err := jsoncodec.DecodeWorkflow([]byte(def))
	if err != nil {
		return domain.WorkflowVersion{}, fmt.Errorf("decoding stored workflow version: %w", err)
	}
	return domain.WorkflowVersion{WorkflowID: id, Version: version, CreatedAt: pgdb.FromDB(created), Workflow: w}, nil
}

// ListVersions implements port.WorkflowVersionStore.
func (s *Workflows) ListVersions(ctx context.Context, id domain.WorkflowID, p port.Page) (port.PageResult[domain.WorkflowVersion], error) {
	after, err := decodeCursor(p.Cursor)
	if err != nil {
		return port.PageResult[domain.WorkflowVersion]{}, err
	}
	where := []string{"workflow_id = ?"}
	args := []any{string(id)}
	if after != "" {
		v, convErr := strconv.Atoi(after)
		if convErr != nil {
			return port.PageResult[domain.WorkflowVersion]{}, fmt.Errorf("%w: %w", domain.ErrInvalidCursor, convErr)
		}
		where, args = append(where, "version < ?"), append(args, v)
	}
	n := s.limit(p)
	rows, err := s.run.Q(ctx).QueryContext(ctx,
		`SELECT version, definition, created_at FROM workflow_versions`+whereClause(where)+
			` ORDER BY version DESC LIMIT `+strconv.Itoa(n+1), args...)
	if err != nil {
		return port.PageResult[domain.WorkflowVersion]{}, fmt.Errorf("listing workflow versions: %w", err)
	}
	defer rows.Close()
	var items []domain.WorkflowVersion
	var keys []string
	for rows.Next() {
		var v int
		var def string
		var created time.Time
		if err := rows.Scan(&v, &def, &created); err != nil {
			return port.PageResult[domain.WorkflowVersion]{}, fmt.Errorf("scanning workflow version: %w", err)
		}
		wv, err := decodeVersion(id, v, def, created)
		if err != nil {
			return port.PageResult[domain.WorkflowVersion]{}, err
		}
		items, keys = append(items, wv), append(keys, strconv.Itoa(v))
	}
	if err := rows.Err(); err != nil {
		return port.PageResult[domain.WorkflowVersion]{}, fmt.Errorf("iterating workflow versions: %w", err)
	}
	return page(items, keys, n), nil
}
