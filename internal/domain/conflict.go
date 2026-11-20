package domain

import "errors"

// ErrConflict is returned (wrapped) when a change is based on a stale view, for
// example saving a workflow on top of a newer version than the caller read.
var ErrConflict = errors.New("conflict")
