package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/engi-hq/engi/internal/adapter/pgdb"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

// Tags implements port.TagStore.
type Tags struct{ s *Store }

var _ port.TagStore = Tags{}

// Tags returns the tag store.
func (s *Store) Tags() Tags { return Tags{s} }

// Create inserts a tag.
func (t Tags) Create(ctx context.Context, tag domain.Tag) error {
	_, err := t.s.run.Q(ctx).ExecContext(ctx,
		`INSERT INTO tags (id, name, created_at) VALUES (?, ?, ?)`,
		string(tag.ID), tag.Name, pgdb.Stamp(t.s.clk.Now()))
	if isUniqueViolation(err) || isPrimaryKeyViolation(err) {
		return fmt.Errorf("tag %q: %w", tag.Name, domain.ErrAlreadyExists)
	}
	if err != nil {
		return fmt.Errorf("creating tag %s: %w", tag.ID, err)
	}
	return nil
}

// Get returns one tag.
func (t Tags) Get(ctx context.Context, id domain.TagID) (domain.Tag, error) {
	var tag domain.Tag
	var created time.Time
	err := t.s.run.Q(ctx).QueryRowContext(ctx,
		`SELECT id, name, created_at FROM tags WHERE id = ?`, string(id)).Scan(&tag.ID, &tag.Name, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Tag{}, notFound("tag", string(id))
	}
	if err != nil {
		return domain.Tag{}, fmt.Errorf("reading tag %s: %w", id, err)
	}
	tag.CreatedAt = pgdb.FromDB(created)
	return tag, nil
}

// Rename renames the tag and every workflow's use of it.
func (t Tags) Rename(ctx context.Context, id domain.TagID, name string) error {
	return t.s.WithinTx(ctx, func(ctx context.Context) error {
		q := t.s.run.Q(ctx)
		old, err := t.Get(ctx, id)
		if err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `UPDATE tags SET name = ? WHERE id = ?`, name, string(id)); err != nil {
			if isUniqueViolation(err) {
				return fmt.Errorf("tag %q: %w", name, domain.ErrAlreadyExists)
			}
			return fmt.Errorf("renaming tag %s: %w", id, err)
		}
		// A workflow that already carries the new name keeps just that one.
		if _, err := q.ExecContext(ctx, `UPDATE workflow_tags w SET tag = ? WHERE tag = ?
			 AND NOT EXISTS (SELECT 1 FROM workflow_tags x WHERE x.workflow_id = w.workflow_id AND x.tag = ?)`, name, old.Name, name); err != nil {
			return fmt.Errorf("renaming tag on workflows: %w", err)
		}
		if _, err := q.ExecContext(ctx, `DELETE FROM workflow_tags WHERE tag = ?`, old.Name); err != nil {
			return fmt.Errorf("clearing old tag from workflows: %w", err)
		}
		return nil
	})
}

// Delete removes the tag and detaches it from every workflow.
func (t Tags) Delete(ctx context.Context, id domain.TagID) error {
	return t.s.WithinTx(ctx, func(ctx context.Context) error {
		q := t.s.run.Q(ctx)
		old, err := t.Get(ctx, id)
		if err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `DELETE FROM workflow_tags WHERE tag = ?`, old.Name); err != nil {
			return fmt.Errorf("detaching tag from workflows: %w", err)
		}
		_, err = q.ExecContext(ctx, `DELETE FROM tags WHERE id = ?`, string(id))
		if err != nil {
			return fmt.Errorf("deleting tag %s: %w", id, err)
		}
		return nil
	})
}

// List returns tags ordered by name.
func (t Tags) List(ctx context.Context, p port.Page) (port.PageResult[domain.Tag], error) {
	after, err := decodeCursor(p.Cursor)
	if err != nil {
		return port.PageResult[domain.Tag]{}, err
	}
	var where []string
	var args []any
	if after != "" {
		where, args = append(where, "name > ?"), append(args, after)
	}
	n := t.s.limit(p)
	rows, err := t.s.run.Q(ctx).QueryContext(ctx,
		`SELECT id, name, created_at FROM tags`+whereClause(where)+` ORDER BY name LIMIT `+strconv.Itoa(n+1), args...)
	if err != nil {
		return port.PageResult[domain.Tag]{}, fmt.Errorf("listing tags: %w", err)
	}
	defer rows.Close()
	var items []domain.Tag
	var keys []string
	for rows.Next() {
		var tag domain.Tag
		var created time.Time
		if err := rows.Scan(&tag.ID, &tag.Name, &created); err != nil {
			return port.PageResult[domain.Tag]{}, fmt.Errorf("scanning tag: %w", err)
		}
		tag.CreatedAt = pgdb.FromDB(created)
		items, keys = append(items, tag), append(keys, tag.Name)
	}
	if err := rows.Err(); err != nil {
		return port.PageResult[domain.Tag]{}, fmt.Errorf("iterating tags: %w", err)
	}
	return page(items, keys, n), nil
}
