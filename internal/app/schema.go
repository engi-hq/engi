package app

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/engi-hq/engi/internal/config"
)

// ErrSchemaOutdated is returned when auto-migration is off and the database
// lacks migrations the binary needs.
var ErrSchemaOutdated = errors.New("database schema is out of date; run `engi migrate up`")

// prepareSchema applies pending migrations, or, when auto is false, only checks
// that none are pending (docs/11 section 11.8).
func prepareSchema(ctx context.Context, db *database, auto bool) error {
	if auto {
		if err := db.migrate(ctx); err != nil {
			return fmt.Errorf("migrating store: %w", err)
		}
		return nil
	}
	n, err := db.pending(ctx)
	if err != nil {
		return fmt.Errorf("checking store schema: %w", err)
	}
	if n > 0 {
		return fmt.Errorf("%d pending migrations: %w", n, ErrSchemaOutdated)
	}
	return nil
}

// MigrateUp opens the configured database and applies every pending
// migration. It backs `engi migrate up`.
func MigrateUp(ctx context.Context, cfg config.Config) error {
	db, err := openDatabase(ctx, cfg, os.ReadFile)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.migrate(ctx); err != nil {
		return fmt.Errorf("migrating store: %w", err)
	}
	return nil
}

// MigrateDown reverts the most recently applied migration and returns its
// version (zero when nothing was applied). It backs `engi migrate down`.
func MigrateDown(ctx context.Context, cfg config.Config) (int, error) {
	db, err := openDatabase(ctx, cfg, os.ReadFile)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	v, err := db.migrateDown(ctx)
	if err != nil {
		return 0, fmt.Errorf("reverting migration: %w", err)
	}
	return v, nil
}
