package port

import "context"

// KeyLocker runs work under a lock on a key that every process sharing the
// store observes. WithLock calls fn while it holds the lock for key and
// releases it when fn returns. Store calls made with the context passed to fn
// belong to the same critical section, so a reader that waits for the lock
// sees what the previous holder committed.
//
// An implementation returns an error without calling fn when the lock cannot
// be acquired, for example because ctx ends first.
type KeyLocker interface {
	WithLock(ctx context.Context, key string, fn func(ctx context.Context) error) error
}
