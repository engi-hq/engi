// Package debug serves the opt-in diagnostics endpoint (pprof and memory
// statistics) on its own listener, never on the public API mux.
package debug

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"runtime"
	"time"
)

// readHeaderTimeout guards the diagnostics listener against slow clients.
const readHeaderTimeout = 10 * time.Second

// MemStats is the JSON document served at /debug/memstats.
type MemStats struct {
	HeapInuse    uint64 `json:"heapInuse"`
	HeapIdle     uint64 `json:"heapIdle"`
	HeapReleased uint64 `json:"heapReleased"`
	HeapAlloc    uint64 `json:"heapAlloc"`
	StackInuse   uint64 `json:"stackInuse"`
	Sys          uint64 `json:"sys"`
	NumGC        uint32 `json:"numGc"`
	Goroutines   int    `json:"goroutines"`
}

// Server is the diagnostics HTTP server.
type Server struct {
	addr string
	log  *slog.Logger
	http *http.Server
}

// New builds a diagnostics server for addr.
func New(addr string, log *slog.Logger) *Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	mux.HandleFunc("/debug/memstats", memstats)
	return &Server{addr: addr, log: log, http: &http.Server{Handler: mux, ReadHeaderTimeout: readHeaderTimeout}}
}

// Start listens and serves in the background. It returns the bound address.
func (s *Server) Start() (string, error) {
	lis, err := net.Listen("tcp", s.addr)
	if err != nil {
		return "", fmt.Errorf("listening for diagnostics on %s: %w", s.addr, err)
	}
	go func() {
		if err := s.http.Serve(lis); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.log.Error("diagnostics server stopped", "error", err)
		}
	}()
	s.log.Warn("diagnostics (pprof) enabled; keep this address private", "addr", lis.Addr().String())
	return lis.Addr().String(), nil
}

// Shutdown stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	if err := s.http.Shutdown(ctx); err != nil {
		return fmt.Errorf("stopping diagnostics server: %w", err)
	}
	return nil
}

// Read returns the current runtime memory statistics.
func Read() MemStats {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return MemStats{
		HeapInuse: m.HeapInuse, HeapIdle: m.HeapIdle, HeapReleased: m.HeapReleased, HeapAlloc: m.HeapAlloc,
		StackInuse: m.StackInuse, Sys: m.Sys, NumGC: m.NumGC, Goroutines: runtime.NumGoroutine(),
	}
}

func memstats(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Has("gc") {
		runtime.GC()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(Read())
}
