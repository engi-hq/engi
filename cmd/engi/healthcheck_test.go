package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthcheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer srv.Close()
	env := []string{"ENGI_SERVER_ADDR=" + strings.TrimPrefix(srv.URL, "http://")}
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"healthz is up", []string{"/healthz"}, exitOK},
		{"readyz default is down", nil, exitFailure},
	}
	for _, tc := range cases {
		var stderr bytes.Buffer
		if got := healthcheckCommand(context.Background(), tc.args, &stderr, env); got != tc.want {
			t.Errorf("%s: exit %d, want %d (%s)", tc.name, got, tc.want, stderr.String())
		}
	}
	srv.Close()
	if got := healthcheckCommand(context.Background(), nil, &bytes.Buffer{}, env); got != exitFailure {
		t.Errorf("closed server: exit %d, want %d", got, exitFailure)
	}
}
