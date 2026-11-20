//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/adapter/lock/postgres"
	"github.com/engi-hq/engi/internal/adapter/pgdb/pgtest"
)

const (
	waitTimeout = 10 * time.Second
	settle      = 150 * time.Millisecond
)

// replica is a second pool on the same database, as another pod would hold.
func replica(t *testing.T, env *pgtest.Env, wait time.Duration) *postgres.Locker {
	t.Helper()
	db, err := env.Target.Open(context.Background(), env.Store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return postgres.New(db, wait)
}

// TestLockSerializesHoldersAcrossPools: while one holder is inside the
// section, a holder on another pool does not enter it, and enters once the
// first one commits.
func TestLockSerializesHoldersAcrossPools(t *testing.T) {
	ctx := context.Background()
	env := pgtest.New(t)
	a, b := replica(t, env, waitTimeout), replica(t, env, waitTimeout)
	const key = "engi:oauth2:cred_serial"

	inside := make(chan struct{})
	release := make(chan struct{})
	aDone := make(chan error, 1)
	go func() {
		aDone <- a.WithLock(ctx, key, func(ctx context.Context) error {
			close(inside)
			<-release
			return nil
		})
	}()
	<-inside

	var bEntered atomic.Bool
	bDone := make(chan error, 1)
	go func() {
		bDone <- b.WithLock(ctx, key, func(ctx context.Context) error {
			bEntered.Store(true)
			return nil
		})
	}()
	time.Sleep(settle)
	if bEntered.Load() {
		t.Fatal("second holder entered while the first still holds the lock")
	}
	close(release)
	if err := <-aDone; err != nil {
		t.Fatal(err)
	}
	if err := <-bDone; err != nil {
		t.Fatal(err)
	}
	if !bEntered.Load() {
		t.Fatal("second holder never entered")
	}
}

// TestLockIsReleasedOnFailure: a failing section rolls back and frees the
// lock, so the next holder is not stuck behind it.
func TestLockIsReleasedOnFailure(t *testing.T) {
	ctx := context.Background()
	env := pgtest.New(t)
	a, b := replica(t, env, waitTimeout), replica(t, env, waitTimeout)
	const key = "engi:oauth2:cred_fail"

	boom := errors.New("token endpoint down")
	if err := a.WithLock(ctx, key, func(context.Context) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	ran := false
	if err := b.WithLock(ctx, key, func(context.Context) error { ran = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Fatal("lock was not released after a failed section")
	}
}

// TestLockDifferentKeysDoNotBlock: the lock is per key.
func TestLockDifferentKeysDoNotBlock(t *testing.T) {
	ctx := context.Background()
	env := pgtest.New(t)
	a, b := replica(t, env, waitTimeout), replica(t, env, waitTimeout)

	inside := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_ = a.WithLock(ctx, "engi:oauth2:cred_one", func(context.Context) error {
			close(inside)
			<-release
			return nil
		})
	}()
	<-inside
	defer close(release)

	done := make(chan error, 1)
	go func() {
		done <- b.WithLock(ctx, "engi:oauth2:cred_two", func(context.Context) error { return nil })
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(waitTimeout):
		t.Fatal("a different key waited for the first lock")
	}
}

// TestLockWaitIsBounded: a waiter gives up after the configured wait, and
// fn never runs.
func TestLockWaitIsBounded(t *testing.T) {
	ctx := context.Background()
	env := pgtest.New(t)
	holder := replica(t, env, waitTimeout)
	waiter := replica(t, env, 200*time.Millisecond)
	const key = "engi:oauth2:cred_bounded"

	inside := make(chan struct{})
	release := make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- holder.WithLock(ctx, key, func(context.Context) error {
			close(inside)
			<-release
			return nil
		})
	}()
	<-inside

	ran := false
	start := time.Now()
	err := waiter.WithLock(ctx, key, func(context.Context) error { ran = true; return nil })
	if err == nil || ran {
		t.Fatalf("waiter got lock or no error: err=%v ran=%v", err, ran)
	}
	if elapsed := time.Since(start); elapsed > waitTimeout {
		t.Fatalf("waiter took %v, lock_timeout not applied", elapsed)
	}
	close(release)
	if err := <-holderDone; err != nil {
		t.Fatal(err)
	}
}
