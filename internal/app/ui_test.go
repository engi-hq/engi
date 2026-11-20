package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWithUIRoutesAPIPathsToTheAPI(t *testing.T) {
	api := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("api")) })
	ui := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ui")) })
	h := withUI(api, ui)
	for path, want := range map[string]string{
		"/api/v1/me": "api", "/api/nope": "api", "/healthz": "api", "/readyz": "api", "/oauth2/callback": "api",
		"/": "ui", "/workflows/wf_1": "ui", "/assets/app.js": "ui", "/login": "ui",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, http.NoBody))
		if got := rec.Body.String(); got != want {
			t.Errorf("%s served by %q, want %q", path, got, want)
		}
	}
}
