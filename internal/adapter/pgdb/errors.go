package pgdb

import (
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// SQLSTATE codes the adapters translate into domain errors.
const (
	codeUniqueViolation     = "23505"
	codeForeignKeyViolation = "23503"
	pkeySuffix              = "_pkey"
)

func pgErr(err error) *pgconn.PgError {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe
	}
	return nil
}

// IsPrimaryKeyViolation reports a duplicate primary key.
func IsPrimaryKeyViolation(err error) bool {
	pe := pgErr(err)
	return pe != nil && pe.Code == codeUniqueViolation && strings.HasSuffix(pe.ConstraintName, pkeySuffix)
}

// IsUniqueViolation reports a duplicate in a unique index other than the
// primary key (SQLite's SQLITE_CONSTRAINT_UNIQUE).
func IsUniqueViolation(err error) bool {
	pe := pgErr(err)
	return pe != nil && pe.Code == codeUniqueViolation && !strings.HasSuffix(pe.ConstraintName, pkeySuffix)
}

// IsForeignKeyViolation reports a reference to a missing row.
func IsForeignKeyViolation(err error) bool {
	pe := pgErr(err)
	return pe != nil && pe.Code == codeForeignKeyViolation
}
