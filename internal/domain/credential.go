package domain

import "errors"

// ErrNeedsReauth is returned (wrapped) when an OAuth2 credential can no longer
// obtain tokens without the user connecting it again.
var ErrNeedsReauth = errors.New("credential needs re-authorization")

// ErrNoTest is returned (wrapped) when a credential type has no test request.
var ErrNoTest = errors.New("credential type has no test")
