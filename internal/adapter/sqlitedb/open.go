// Package sqlitedb opens the SQLite database shared by the store and queue
// adapters, applies migrations, and provides transaction plumbing.
package sqlitedb

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strconv"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, registered as "sqlite"

	"github.com/engi-hq/engi/internal/config"
)

// timeLayout is a fixed-width UTC layout, so text comparison orders like time.
const timeLayout = "2006-01-02T15:04:05.000000Z"

// FormatTime renders t in the storage layout (UTC, microseconds).
func FormatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

// ParseTime is the inverse of FormatTime.
func ParseTime(s string) (time.Time, error) {
	t, err := time.Parse(timeLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing stored time %q: %w", s, err)
	}
	return t, nil
}

// Open opens the database file named by cfg, applies the connection pragmas to
// every pooled connection and verifies connectivity.
func Open(ctx context.Context, cfg config.StoreConfig) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn(cfg))
	if err != nil {
		return nil, fmt.Errorf("opening sqlite %q: %w", cfg.Path, err)
	}
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connecting to sqlite %q: %w", cfg.Path, err)
	}
	return db, nil
}

func dsn(cfg config.StoreConfig) string {
	q := url.Values{}
	// _pragma runs on each new connection; _txlock makes BEGIN take the write
	// lock up front, so a transaction never fails halfway on lock upgrade.
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout("+strconv.FormatInt(cfg.BusyTimeout.Milliseconds(), 10)+")")
	q.Add("_pragma", "journal_mode("+cfg.JournalMode+")")
	q.Add("_pragma", "synchronous("+cfg.Synchronous+")")
	q.Set("_txlock", "immediate")
	u := url.URL{Scheme: "file", Opaque: (&url.URL{Path: cfg.Path}).EscapedPath(), RawQuery: q.Encode()}
	return u.String()
}
