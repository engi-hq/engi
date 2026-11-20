package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/engi-hq/engi/internal/adapter/pgdb"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

// Users implements port.UserStore.
type Users struct{ s *Store }

// Sessions implements port.SessionStore.
type Sessions struct{ s *Store }

// APIKeys implements port.APIKeyStore.
type APIKeys struct{ s *Store }

var (
	_ port.UserStore    = Users{}
	_ port.SessionStore = Sessions{}
	_ port.APIKeyStore  = APIKeys{}
)

// Users returns the user store.
func (s *Store) Users() Users { return Users{s} }

// Sessions returns the session store.
func (s *Store) Sessions() Sessions { return Sessions{s} }

// APIKeys returns the API key store.
func (s *Store) APIKeys() APIKeys { return APIKeys{s} }

const userColumns = `id, email, name, password_hash, role, disabled, created_at, updated_at`

func scanUser(r rowScanner) (domain.User, error) {
	var u domain.User
	var created, updated time.Time
	if err := r.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Role, &u.Disabled, &created, &updated); err != nil {
		return domain.User{}, err //nolint:wrapcheck // callers wrap with context
	}
	u.CreatedAt = pgdb.FromDB(created)
	u.UpdatedAt = pgdb.FromDB(updated)
	return u, nil
}

// Create implements port.UserStore.
func (u Users) Create(ctx context.Context, usr domain.User) error {
	now := pgdb.Stamp(u.s.clk.Now())
	_, err := u.s.run.Q(ctx).ExecContext(ctx,
		`INSERT INTO users (`+userColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		string(usr.ID), usr.Email, usr.Name, usr.PasswordHash, string(usr.Role), usr.Disabled, now, now)
	if isUniqueViolation(err) || isPrimaryKeyViolation(err) {
		return fmt.Errorf("user %q: %w", usr.Email, domain.ErrAlreadyExists)
	}
	if err != nil {
		return fmt.Errorf("creating user %s: %w", usr.ID, err)
	}
	return nil
}

// Get implements port.UserStore.
func (u Users) Get(ctx context.Context, id domain.UserID) (domain.User, error) {
	return u.one(ctx, `WHERE id = ?`, string(id))
}

// GetByEmail implements port.UserStore.
func (u Users) GetByEmail(ctx context.Context, email string) (domain.User, error) {
	return u.one(ctx, `WHERE email = ?`, email)
}

func (u Users) one(ctx context.Context, where string, arg any) (domain.User, error) {
	usr, err := scanUser(u.s.run.Q(ctx).QueryRowContext(ctx, `SELECT `+userColumns+` FROM users `+where, arg))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.User{}, notFound("user", fmt.Sprint(arg))
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("reading user: %w", err)
	}
	return usr, nil
}

// Update implements port.UserStore.
func (u Users) Update(ctx context.Context, usr domain.User) error {
	res, err := u.s.run.Q(ctx).ExecContext(ctx,
		`UPDATE users SET email = ?, name = ?, password_hash = ?, role = ?, disabled = ?, updated_at = ? WHERE id = ?`,
		usr.Email, usr.Name, usr.PasswordHash, string(usr.Role), usr.Disabled, pgdb.Stamp(u.s.clk.Now()), string(usr.ID))
	if isUniqueViolation(err) {
		return fmt.Errorf("user %q: %w", usr.Email, domain.ErrAlreadyExists)
	}
	return affectedOrNotFound(res, err, "user", string(usr.ID))
}

// Delete implements port.UserStore.
func (u Users) Delete(ctx context.Context, id domain.UserID) error {
	res, err := u.s.run.Q(ctx).ExecContext(ctx, `DELETE FROM users WHERE id = ?`, string(id))
	return affectedOrNotFound(res, err, "user", string(id))
}

// List implements port.UserStore.
func (u Users) List(ctx context.Context, p port.Page) (port.PageResult[domain.User], error) {
	after, err := decodeCursor(p.Cursor)
	if err != nil {
		return port.PageResult[domain.User]{}, err
	}
	var where []string
	var args []any
	if after != "" {
		where, args = append(where, "id < ?"), append(args, after)
	}
	n := u.s.limit(p)
	rows, err := u.s.run.Q(ctx).QueryContext(ctx,
		`SELECT `+userColumns+` FROM users`+whereClause(where)+` ORDER BY id DESC LIMIT `+strconv.Itoa(n+1), args...)
	if err != nil {
		return port.PageResult[domain.User]{}, fmt.Errorf("listing users: %w", err)
	}
	defer rows.Close()
	var items []domain.User
	var keys []string
	for rows.Next() {
		usr, err := scanUser(rows)
		if err != nil {
			return port.PageResult[domain.User]{}, fmt.Errorf("scanning user: %w", err)
		}
		items, keys = append(items, usr), append(keys, string(usr.ID))
	}
	if err := rows.Err(); err != nil {
		return port.PageResult[domain.User]{}, fmt.Errorf("iterating users: %w", err)
	}
	return page(items, keys, n), nil
}

// Count implements port.UserStore.
func (u Users) Count(ctx context.Context) (int, error) {
	var n int
	if err := u.s.run.Q(ctx).QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("counting users: %w", err)
	}
	return n, nil
}

// Create implements port.SessionStore.
func (s Sessions) Create(ctx context.Context, sess domain.Session) error {
	_, err := s.s.run.Q(ctx).ExecContext(ctx,
		`INSERT INTO sessions (token_hash, user_id, csrf_token, created_at, last_seen_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)`,
		sess.TokenHash, string(sess.UserID), sess.CSRFToken, pgdb.Stamp(sess.CreatedAt),
		pgdb.Stamp(sess.LastSeenAt), pgdb.Stamp(sess.ExpiresAt))
	if err != nil {
		return fmt.Errorf("creating session: %w", err)
	}
	return nil
}

// Get implements port.SessionStore.
func (s Sessions) Get(ctx context.Context, tokenHash string) (domain.Session, error) {
	var sess domain.Session
	var created, seen, expires time.Time
	err := s.s.run.Q(ctx).QueryRowContext(ctx,
		`SELECT token_hash, user_id, csrf_token, created_at, last_seen_at, expires_at FROM sessions WHERE token_hash = ?`, tokenHash).
		Scan(&sess.TokenHash, &sess.UserID, &sess.CSRFToken, &created, &seen, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Session{}, notFound("session", "")
	}
	if err != nil {
		return domain.Session{}, fmt.Errorf("reading session: %w", err)
	}
	sess.CreatedAt, sess.LastSeenAt, sess.ExpiresAt = pgdb.FromDB(created), pgdb.FromDB(seen), pgdb.FromDB(expires)
	return sess, nil
}

// Touch implements port.SessionStore.
func (s Sessions) Touch(ctx context.Context, tokenHash string, seen time.Time) error {
	res, err := s.s.run.Q(ctx).ExecContext(ctx, `UPDATE sessions SET last_seen_at = ? WHERE token_hash = ?`, pgdb.Stamp(seen), tokenHash)
	return affectedOrNotFound(res, err, "session", "")
}

// Delete implements port.SessionStore. Deleting an unknown session is not an error.
func (s Sessions) Delete(ctx context.Context, tokenHash string) error {
	if _, err := s.s.run.Q(ctx).ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash); err != nil {
		return fmt.Errorf("deleting session: %w", err)
	}
	return nil
}

// DeleteByUser implements port.SessionStore.
func (s Sessions) DeleteByUser(ctx context.Context, id domain.UserID) error {
	if _, err := s.s.run.Q(ctx).ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, string(id)); err != nil {
		return fmt.Errorf("deleting sessions of %s: %w", id, err)
	}
	return nil
}

// DeleteExpired implements port.SessionStore.
func (s Sessions) DeleteExpired(ctx context.Context, now, idleCutoff time.Time) (int, error) {
	res, err := s.s.run.Q(ctx).ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ? OR last_seen_at < ?`,
		pgdb.Stamp(now), pgdb.Stamp(idleCutoff))
	if err != nil {
		return 0, fmt.Errorf("deleting expired sessions: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counting deleted sessions: %w", err)
	}
	return int(n), nil
}

const apiKeyColumns = `id, user_id, name, prefix, secret_hash, scopes, expires_at, last_used_at, revoked_at, created_at` //nolint:gosec // G101 false positive: a SQL column list, not a credential

func scanAPIKey(r rowScanner) (domain.APIKey, error) {
	var k domain.APIKey
	var scopes string
	var created time.Time
	var expires, used, revoked sql.NullTime
	if err := r.Scan(&k.ID, &k.UserID, &k.Name, &k.Prefix, &k.SecretHash, &scopes, &expires, &used, &revoked, &created); err != nil {
		return domain.APIKey{}, err //nolint:wrapcheck // callers wrap with context
	}
	for _, sc := range strings.Split(scopes, ",") {
		if sc != "" {
			k.Scopes = append(k.Scopes, domain.Scope(sc))
		}
	}
	k.CreatedAt = pgdb.FromDB(created)
	k.ExpiresAt, k.LastUsedAt, k.RevokedAt = optTime(expires), optTime(used), optTime(revoked)
	return k, nil
}

// Create implements port.APIKeyStore.
func (a APIKeys) Create(ctx context.Context, k domain.APIKey) error {
	scopes := make([]string, len(k.Scopes))
	for i, s := range k.Scopes {
		scopes[i] = string(s)
	}
	_, err := a.s.run.Q(ctx).ExecContext(ctx,
		`INSERT INTO api_keys (`+apiKeyColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(k.ID), string(k.UserID), k.Name, k.Prefix, k.SecretHash, strings.Join(scopes, ","),
		timeArg(k.ExpiresAt), timeArg(k.LastUsedAt), timeArg(k.RevokedAt), pgdb.Stamp(k.CreatedAt))
	if isUniqueViolation(err) || isPrimaryKeyViolation(err) {
		return fmt.Errorf("api key %s: %w", k.ID, domain.ErrAlreadyExists)
	}
	if err != nil {
		return fmt.Errorf("creating api key %s: %w", k.ID, err)
	}
	return nil
}

// GetByPrefix implements port.APIKeyStore.
func (a APIKeys) GetByPrefix(ctx context.Context, prefix string) (domain.APIKey, error) {
	return a.one(ctx, `WHERE prefix = ?`, prefix)
}

// Get implements port.APIKeyStore.
func (a APIKeys) Get(ctx context.Context, id domain.APIKeyID) (domain.APIKey, error) {
	return a.one(ctx, `WHERE id = ?`, string(id))
}

func (a APIKeys) one(ctx context.Context, where, arg string) (domain.APIKey, error) {
	k, err := scanAPIKey(a.s.run.Q(ctx).QueryRowContext(ctx, `SELECT `+apiKeyColumns+` FROM api_keys `+where, arg))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.APIKey{}, notFound("api key", arg)
	}
	if err != nil {
		return domain.APIKey{}, fmt.Errorf("reading api key: %w", err)
	}
	return k, nil
}

// ListByUser implements port.APIKeyStore.
func (a APIKeys) ListByUser(ctx context.Context, id domain.UserID, p port.Page) (port.PageResult[domain.APIKey], error) {
	after, err := decodeCursor(p.Cursor)
	if err != nil {
		return port.PageResult[domain.APIKey]{}, err
	}
	where, args := []string{"user_id = ?"}, []any{string(id)}
	if after != "" {
		where, args = append(where, "id < ?"), append(args, after)
	}
	n := a.s.limit(p)
	rows, err := a.s.run.Q(ctx).QueryContext(ctx,
		`SELECT `+apiKeyColumns+` FROM api_keys`+whereClause(where)+` ORDER BY id DESC LIMIT `+strconv.Itoa(n+1), args...)
	if err != nil {
		return port.PageResult[domain.APIKey]{}, fmt.Errorf("listing api keys: %w", err)
	}
	defer rows.Close()
	var items []domain.APIKey
	var keys []string
	for rows.Next() {
		k, err := scanAPIKey(rows)
		if err != nil {
			return port.PageResult[domain.APIKey]{}, fmt.Errorf("scanning api key: %w", err)
		}
		items, keys = append(items, k), append(keys, string(k.ID))
	}
	if err := rows.Err(); err != nil {
		return port.PageResult[domain.APIKey]{}, fmt.Errorf("iterating api keys: %w", err)
	}
	return page(items, keys, n), nil
}

// CountActive implements port.APIKeyStore.
func (a APIKeys) CountActive(ctx context.Context, id domain.UserID, now time.Time) (int, error) {
	var n int
	err := a.s.run.Q(ctx).QueryRowContext(ctx,
		`SELECT COUNT(*) FROM api_keys WHERE user_id = ? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)`,
		string(id), pgdb.Stamp(now)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("counting api keys: %w", err)
	}
	return n, nil
}

// Revoke implements port.APIKeyStore.
func (a APIKeys) Revoke(ctx context.Context, id domain.APIKeyID, at time.Time) error {
	res, err := a.s.run.Q(ctx).ExecContext(ctx,
		`UPDATE api_keys SET revoked_at = COALESCE(revoked_at, ?) WHERE id = ?`, pgdb.Stamp(at), string(id))
	return affectedOrNotFound(res, err, "api key", string(id))
}

// Touch implements port.APIKeyStore.
func (a APIKeys) Touch(ctx context.Context, id domain.APIKeyID, at time.Time) error {
	res, err := a.s.run.Q(ctx).ExecContext(ctx, `UPDATE api_keys SET last_used_at = ? WHERE id = ?`, pgdb.Stamp(at), string(id))
	return affectedOrNotFound(res, err, "api key", string(id))
}
