package system_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/adapter/clock/system"
)

func TestSleepReturnsAfterDuration(t *testing.T) {
	if err := (system.Clock{}).Sleep(context.Background(), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := (system.Clock{}).Sleep(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
}

func TestSleepHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (system.Clock{}).Sleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
