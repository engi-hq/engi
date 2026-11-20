package app

import (
	"context"
	"log/slog"
	"os"
	"runtime/debug"

	diag "github.com/engi-hq/engi/internal/adapter/http/debug"
	"github.com/engi-hq/engi/internal/config"
)

const bytesPerMiB = 1 << 20

// ApplyRuntime applies the runtime section: the soft memory limit (unless the
// GOMEMLIMIT environment variable already set one) and the opt-in diagnostics
// listener. The returned function stops the listener; it is never nil.
func ApplyRuntime(cfg config.RuntimeConfig, log *slog.Logger) (stop func(context.Context) error, err error) {
	if _, fromEnv := os.LookupEnv("GOMEMLIMIT"); cfg.MemoryLimitMiB > 0 && !fromEnv {
		debug.SetMemoryLimit(cfg.MemoryLimitMiB * bytesPerMiB)
		log.Info("go memory limit set", "mib", cfg.MemoryLimitMiB)
	}
	if cfg.PprofAddr == "" {
		return func(context.Context) error { return nil }, nil
	}
	srv := diag.New(cfg.PprofAddr, log.With("component", "diagnostics"))
	if _, err := srv.Start(); err != nil {
		return nil, err //nolint:wrapcheck // already contextual
	}
	return srv.Shutdown, nil
}
