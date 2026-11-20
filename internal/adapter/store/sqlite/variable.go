package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/engi-hq/engi/internal/adapter/sqlitedb"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

// Variables implements port.VariableStore.
type Variables struct{ s *Store }

var _ port.VariableStore = Variables{}

// Variables returns the variable store.
func (s *Store) Variables() Variables { return Variables{s} }

const variableColumns = `id, key, secret, key_id, value, created_at, updated_at`

func scanVariable(r rowScanner) (domain.Variable, error) {
	var v domain.Variable
	var created, updated string
	if err := r.Scan(&v.ID, &v.Key, &v.Secret, &v.KeyID, &v.Value, &created, &updated); err != nil {
		return domain.Variable{}, err
	}
	var err error
	if v.CreatedAt, err = sqlitedb.ParseTime(created); err != nil {
		return domain.Variable{}, err
	}
	if v.UpdatedAt, err = sqlitedb.ParseTime(updated); err != nil {
		return domain.Variable{}, err
	}
	return v, nil
}

func variableConflict(v domain.Variable, err error) error {
	if isUniqueViolation(err) || isPrimaryKeyViolation(err) {
		return fmt.Errorf("variable %q: %w", v.Key, domain.ErrAlreadyExists)
	}
	return nil
}

// Create inserts a variable.
func (c Variables) Create(ctx context.Context, v domain.Variable) error {
	now := sqlitedb.FormatTime(c.s.clk.Now())
	_, err := c.s.run.Q(ctx).ExecContext(ctx,
		`INSERT INTO variables (`+variableColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		string(v.ID), v.Key, v.Secret, v.KeyID, v.Value, now, now)
	if err != nil {
		if cerr := variableConflict(v, err); cerr != nil {
			return cerr
		}
		return fmt.Errorf("creating variable %s: %w", v.ID, err)
	}
	return nil
}

// Update replaces key, secret flag, key ID and value.
func (c Variables) Update(ctx context.Context, v domain.Variable) error {
	res, err := c.s.run.Q(ctx).ExecContext(ctx,
		`UPDATE variables SET key = ?, secret = ?, key_id = ?, value = ?, updated_at = ? WHERE id = ?`,
		v.Key, v.Secret, v.KeyID, v.Value, sqlitedb.FormatTime(c.s.clk.Now()), string(v.ID))
	if cerr := variableConflict(v, err); cerr != nil {
		return cerr
	}
	return affectedOrNotFound(res, err, "variable", string(v.ID))
}

// Get returns a variable including its stored value.
func (c Variables) Get(ctx context.Context, id domain.VariableID) (domain.Variable, error) {
	v, err := scanVariable(c.s.run.Q(ctx).QueryRowContext(ctx,
		`SELECT `+variableColumns+` FROM variables WHERE id = ?`, string(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Variable{}, notFound("variable", string(id))
	}
	if err != nil {
		return domain.Variable{}, fmt.Errorf("reading variable %s: %w", id, err)
	}
	return v, nil
}

// Delete removes a variable.
func (c Variables) Delete(ctx context.Context, id domain.VariableID) error {
	res, err := c.s.run.Q(ctx).ExecContext(ctx, `DELETE FROM variables WHERE id = ?`, string(id))
	return affectedOrNotFound(res, err, "variable", string(id))
}

// List returns variables ordered by key.
func (c Variables) List(ctx context.Context, p port.Page) (port.PageResult[domain.Variable], error) {
	after, err := decodeCursor(p.Cursor)
	if err != nil {
		return port.PageResult[domain.Variable]{}, err
	}
	var where []string
	var args []any
	if after != "" {
		where, args = append(where, "key > ?"), append(args, after)
	}
	n := c.s.limit(p)
	rows, err := c.s.run.Q(ctx).QueryContext(ctx,
		`SELECT `+variableColumns+` FROM variables`+whereClause(where)+` ORDER BY key LIMIT `+strconv.Itoa(n+1), args...)
	if err != nil {
		return port.PageResult[domain.Variable]{}, fmt.Errorf("listing variables: %w", err)
	}
	defer rows.Close()
	var items []domain.Variable
	var keys []string
	for rows.Next() {
		v, err := scanVariable(rows)
		if err != nil {
			return port.PageResult[domain.Variable]{}, fmt.Errorf("scanning variable: %w", err)
		}
		items, keys = append(items, v), append(keys, v.Key)
	}
	if err := rows.Err(); err != nil {
		return port.PageResult[domain.Variable]{}, fmt.Errorf("iterating variables: %w", err)
	}
	return page(items, keys, n), nil
}
