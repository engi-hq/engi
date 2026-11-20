package config

import (
	"errors"
	"strconv"
	"strings"
)

// RuntimeConfig tunes the Go runtime and the opt-in diagnostics endpoint.
type RuntimeConfig struct {
	// MemoryLimitMiB is a soft limit for the Go runtime's total memory
	// (debug.SetMemoryLimit). The garbage collector works harder as the process
	// nears it, and returns freed memory to the OS sooner. Zero leaves the
	// runtime default (no limit) unless the GOMEMLIMIT environment variable is
	// set, which then stays in force. In a container set it to about 80-90% of
	// the container memory limit.
	MemoryLimitMiB int64 `yaml:"memoryLimitMib"`
	// PprofAddr, when not empty, serves net/http/pprof and expvar-style memory
	// statistics on that address, separate from the public listener. Bind it to
	// a loopback address. Empty (the default) disables it.
	PprofAddr string `yaml:"pprofAddr"`
}

func defaultRuntime() RuntimeConfig { return RuntimeConfig{} }

// Validate reports every problem with the runtime settings.
func (c RuntimeConfig) Validate() error {
	var errs []error
	if c.MemoryLimitMiB < 0 {
		errs = append(errs, errors.New("runtime.memoryLimitMib must not be negative"))
	}
	if c.PprofAddr != "" && strings.TrimSpace(c.PprofAddr) == "" {
		errs = append(errs, errors.New("runtime.pprofAddr must not be blank"))
	}
	return errors.Join(errs...)
}

func runtimeSettings() []setting {
	return []setting{
		{"ENGI_RUNTIME_MEMORY_LIMIT_MIB", "runtime-memory-limit-mib", "soft Go memory limit in MiB; 0 = none",
			func(c *Config, v string) (err error) {
				c.Runtime.MemoryLimitMiB, err = strconv.ParseInt(v, 10, 64)
				return
			}},
		{"ENGI_RUNTIME_PPROF_ADDR", "runtime-pprof-addr", "serve pprof on this address (empty = off)",
			stringSetting(func(c *Config) *string { return &c.Runtime.PprofAddr })},
	}
}
