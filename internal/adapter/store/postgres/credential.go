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

// Credentials implements port.CredentialStore.
type Credentials struct{ s *Store }

var _ port.CredentialStore = Credentials{}

// Credentials returns the credential store.
func (s *Store) Credentials() Credentials { return Credentials{s} }

// Create inserts a credential.
func (c Credentials) Create(ctx context.Context, cr domain.Credential) error {
	now := pgdb.Stamp(c.s.clk.Now())
	_, err := c.s.run.Q(ctx).ExecContext(ctx,
		`INSERT INTO credentials (id, name, type, key_id, data, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		string(cr.ID), cr.Name, cr.Type, cr.KeyID, cr.Data, now, now)
	if isPrimaryKeyViolation(err) {
		return fmt.Errorf("credential %s: %w", cr.ID, domain.ErrAlreadyExists)
	}
	if err != nil {
		return fmt.Errorf("creating credential %s: %w", cr.ID, err)
	}
	return nil
}

// Update replaces name, type, key and ciphertext.
func (c Credentials) Update(ctx context.Context, cr domain.Credential) error {
	res, err := c.s.run.Q(ctx).ExecContext(ctx,
		`UPDATE credentials SET name = ?, type = ?, key_id = ?, data = ?, updated_at = ? WHERE id = ?`,
		cr.Name, cr.Type, cr.KeyID, cr.Data, pgdb.Stamp(c.s.clk.Now()), string(cr.ID))
	return affectedOrNotFound(res, err, "credential", string(cr.ID))
}

// Get returns a credential including its ciphertext.
func (c Credentials) Get(ctx context.Context, id domain.CredentialID) (domain.Credential, error) {
	var cr domain.Credential
	var created, updated time.Time
	err := c.s.run.Q(ctx).QueryRowContext(ctx,
		`SELECT id, name, type, key_id, data, created_at, updated_at FROM credentials WHERE id = ?`, string(id)).
		Scan(&cr.ID, &cr.Name, &cr.Type, &cr.KeyID, &cr.Data, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Credential{}, notFound("credential", string(id))
	}
	if err != nil {
		return domain.Credential{}, fmt.Errorf("reading credential %s: %w", id, err)
	}
	cr.CreatedAt = pgdb.FromDB(created)
	cr.UpdatedAt = pgdb.FromDB(updated)
	return cr, nil
}

// Delete removes a credential.
func (c Credentials) Delete(ctx context.Context, id domain.CredentialID) error {
	res, err := c.s.run.Q(ctx).ExecContext(ctx, `DELETE FROM credentials WHERE id = ?`, string(id))
	return affectedOrNotFound(res, err, "credential", string(id))
}

// List returns credentials newest first, without ciphertext.
func (c Credentials) List(ctx context.Context, p port.Page) (port.PageResult[domain.Credential], error) {
	after, err := decodeCursor(p.Cursor)
	if err != nil {
		return port.PageResult[domain.Credential]{}, err
	}
	var where []string
	var args []any
	if after != "" {
		where, args = append(where, "id < ?"), append(args, after)
	}
	n := c.s.limit(p)
	rows, err := c.s.run.Q(ctx).QueryContext(ctx,
		`SELECT id, name, type, key_id, created_at, updated_at FROM credentials`+whereClause(where)+
			` ORDER BY id DESC LIMIT `+strconv.Itoa(n+1), args...)
	if err != nil {
		return port.PageResult[domain.Credential]{}, fmt.Errorf("listing credentials: %w", err)
	}
	defer rows.Close()
	var items []domain.Credential
	var keys []string
	for rows.Next() {
		var cr domain.Credential
		var created, updated time.Time
		if err := rows.Scan(&cr.ID, &cr.Name, &cr.Type, &cr.KeyID, &created, &updated); err != nil {
			return port.PageResult[domain.Credential]{}, fmt.Errorf("scanning credential: %w", err)
		}
		cr.CreatedAt = pgdb.FromDB(created)
		cr.UpdatedAt = pgdb.FromDB(updated)
		items, keys = append(items, cr), append(keys, string(cr.ID))
	}
	if err := rows.Err(); err != nil {
		return port.PageResult[domain.Credential]{}, fmt.Errorf("iterating credentials: %w", err)
	}
	return page(items, keys, n), nil
}
