package api

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/service/execution"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"not found", fmt.Errorf("x: %w", domain.ErrNotFound), http.StatusNotFound},
		{"validation", &domain.ValidationError{Issues: []domain.Issue{{Path: "a", Message: "b"}}}, http.StatusUnprocessableEntity},
		{"conflict", fmt.Errorf("x: %w", domain.ErrConflict), http.StatusConflict},
		{"exists", domain.ErrAlreadyExists, http.StatusConflict},
		{"not cancelable", execution.ErrNotCancelable, http.StatusConflict},
		{"unauthenticated", domain.ErrUnauthenticated, http.StatusUnauthorized},
		{"forbidden", domain.ErrForbidden, http.StatusForbidden},
		{"rate", &domain.RateLimitedError{RetryAfter: time.Second}, http.StatusTooManyRequests},
		{"cursor", domain.ErrInvalidCursor, http.StatusBadRequest},
		{"not implemented", ErrNotImplemented, http.StatusNotImplemented},
		{"unknown", errors.New("db exploded: secret detail"), http.StatusInternalServerError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := classify(tc.err)
			if v.status != tc.want {
				t.Fatalf("status = %d, want %d", v.status, tc.want)
			}
			if tc.want == http.StatusInternalServerError && v.detail != "internal error" {
				t.Fatalf("internal detail leaks: %q", v.detail)
			}
		})
	}
}

func TestPointerOf(t *testing.T) {
	for in, want := range map[string]string{
		"name":                  "/name",
		"nodes[2].name":         "/nodes/2/name",
		"nodes[0].parameters.x": "/nodes/0/parameters/x",
		"nodes[Fetch]":          "/nodes/Fetch",
	} {
		if got := pointerOf(in); got != want {
			t.Errorf("pointerOf(%q) = %q, want %q", in, got, want)
		}
	}
}

type counterIDs struct{ n int }

func (c *counterIDs) New(prefix string) (string, error) {
	c.n++
	return fmt.Sprintf("%s_%d", prefix, c.n), nil
}

func testAPI() *API {
	return &API{d: Deps{Server: config.Default().Server, IDs: &counterIDs{}, Logger: slog.New(slog.DiscardHandler)}}
}

func TestRecovererTurnsPanicIntoProblem(t *testing.T) {
	a := testAPI()
	h := a.requestID(a.accessLog(a.recoverer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", http.NoBody))
	if rec.Code != http.StatusInternalServerError || rec.Header().Get("Content-Type") != problemContentType {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if body := rec.Body.String(); body == "" || strings.Contains(body, "boom") {
		t.Fatalf("body = %s", body)
	}
}

func TestClientAddr(t *testing.T) {
	a := testAPI()
	r := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	r.RemoteAddr = "10.0.0.1:5555"
	r.Header.Set("X-Forwarded-For", "6.6.6.6, 9.9.9.9")
	if got := a.clientAddr(r); got != "10.0.0.1" {
		t.Errorf("untrusted proxy: %q", got)
	}
	a.d.Server.TrustForwardedFor = true
	if got := a.clientAddr(r); got != "9.9.9.9" {
		t.Errorf("trusted proxy takes the last entry: %q", got)
	}
}

func TestPageOf(t *testing.T) {
	a := testAPI()
	p, err := a.pageOf(map[string][]string{"limit": {"100000"}})
	if err != nil || p.Limit != a.d.Server.Pagination.MaxLimit {
		t.Fatalf("limit = %d, %v", p.Limit, err)
	}
	if p, _ := a.pageOf(nil); p.Limit != a.d.Server.Pagination.DefaultLimit {
		t.Fatalf("default limit = %d", p.Limit)
	}
	if _, err := a.pageOf(map[string][]string{"limit": {"-1"}}); err == nil {
		t.Fatal("negative limit accepted")
	}
}
