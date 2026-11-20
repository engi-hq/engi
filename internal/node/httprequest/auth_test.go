package httprequest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/engi-hq/engi/internal/credtype/builtin"
	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

func credential(t *testing.T, typ string, data map[string]any) nodesdk.Credential {
	t.Helper()
	for _, ct := range builtin.Types() {
		if ct.Name == typ {
			return nodesdk.Credential{Type: typ, Data: data, Auth: ct.Authenticate}
		}
	}
	t.Fatalf("unknown credential type %s", typ)
	return nodesdk.Credential{}
}

func TestAuthentication(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
		binds  map[string]nodesdk.Credential
		check  func(t *testing.T, r recorded)
	}{
		{"none sends no credential", map[string]any{}, nil, func(t *testing.T, r recorded) {
			if r.header.Get("Authorization") != "" {
				t.Fatal("unexpected Authorization header")
			}
		}},
		{"generic basic", map[string]any{"authentication": "generic", "genericAuthType": "engi.http.basic"},
			map[string]nodesdk.Credential{"engi.http.basic": credential(t, "engi.http.basic", map[string]any{"user": "ann", "password": "pw"})},
			func(t *testing.T, r recorded) {
				if r.header.Get("Authorization") != nodesdk.BasicAuthValue("ann", "pw") {
					t.Fatalf("auth = %q", r.header.Get("Authorization"))
				}
			}},
		{"generic header", map[string]any{"authentication": "generic", "genericAuthType": "engi.http.header"},
			map[string]nodesdk.Credential{"engi.http.header": credential(t, "engi.http.header", map[string]any{"name": "X-Key", "value": "k1"})},
			func(t *testing.T, r recorded) {
				if r.header.Get("X-Key") != "k1" {
					t.Fatalf("headers = %v", r.header)
				}
			}},
		{"generic bearer", map[string]any{"authentication": "generic", "genericAuthType": "engi.http.bearer"},
			map[string]nodesdk.Credential{"engi.http.bearer": credential(t, "engi.http.bearer", map[string]any{"token": "tk"})},
			func(t *testing.T, r recorded) {
				if r.header.Get("Authorization") != "Bearer tk" {
					t.Fatalf("auth = %q", r.header.Get("Authorization"))
				}
			}},
		{"generic query keeps existing query", map[string]any{"authentication": "generic", "genericAuthType": "engi.http.queryKey",
			"query": []any{map[string]any{"name": "a", "value": "1"}}},
			map[string]nodesdk.Credential{"engi.http.queryKey": credential(t, "engi.http.queryKey", map[string]any{"name": "key", "value": "s3"})},
			func(t *testing.T, r recorded) {
				if r.query != "a=1&key=s3" {
					t.Fatalf("query = %q", r.query)
				}
			}},
		{"generic oauth2 uses the access token", map[string]any{"authentication": "generic", "genericAuthType": "engi.oauth2"},
			map[string]nodesdk.Credential{"engi.oauth2": credential(t, "engi.oauth2", map[string]any{"accessToken": "at"})},
			func(t *testing.T, r recorded) {
				if r.header.Get("Authorization") != "Bearer at" {
					t.Fatalf("auth = %q", r.header.Get("Authorization"))
				}
			}},
		{"predefined any type with authenticate", map[string]any{"authentication": "predefined"},
			map[string]nodesdk.Credential{"predefined": credential(t, "engi.http.bearer", map[string]any{"token": "pt"})},
			func(t *testing.T, r recorded) {
				if r.header.Get("Authorization") != "Bearer pt" {
					t.Fatalf("auth = %q", r.header.Get("Authorization"))
				}
			}},
		{"credential overrides a user header", map[string]any{"authentication": "predefined",
			"headers": []any{map[string]any{"name": "Authorization", "value": "Bearer user"}}},
			map[string]nodesdk.Credential{"predefined": credential(t, "engi.http.bearer", map[string]any{"token": "cred"})},
			func(t *testing.T, r recorded) {
				if r.header.Get("Authorization") != "Bearer cred" {
					t.Fatalf("auth = %q", r.header.Get("Authorization"))
				}
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, seen := server(t, jsonReply(`{}`))
			in := input(srv, 2, tt.params)
			in.Credentials = tt.binds
			if _, err := New().Execute(context.Background(), in); err != nil {
				t.Fatal(err)
			}
			if len(*seen) != 2 {
				t.Fatalf("requests = %d", len(*seen))
			}
			for _, r := range *seen {
				tt.check(t, r)
			}
		})
	}
}

func TestAuthenticationErrors(t *testing.T) {
	srv, seen := server(t, jsonReply(`{}`))
	t.Run("missing credential binding", func(t *testing.T) {
		in := input(srv, 1, map[string]any{"authentication": "predefined"})
		_, err := New().Execute(context.Background(), in)
		if !errors.Is(err, nodesdk.ErrUnavailable) || len(*seen) != 0 {
			t.Fatalf("err = %v, requests = %d", err, len(*seen))
		}
	})
	t.Run("type without authenticate", func(t *testing.T) {
		in := input(srv, 1, map[string]any{"authentication": "predefined"})
		in.Credentials = map[string]nodesdk.Credential{"predefined": {Type: "engi.postgres"}}
		_, err := New().Execute(context.Background(), in)
		if !errors.Is(err, nodesdk.ErrNoAuthentication) || len(*seen) != 0 {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("unknown mode", func(t *testing.T) {
		in := input(srv, 1, map[string]any{"authentication": "magic"})
		if _, err := New().Execute(context.Background(), in); err == nil || !strings.Contains(err.Error(), "magic") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestAuthCredentialResolvedOncePerRun(t *testing.T) {
	srv, _ := server(t, jsonReply(`{}`))
	in := input(srv, 3, map[string]any{"authentication": "predefined"})
	cred := credential(t, "engi.http.bearer", map[string]any{"token": "t"})
	counter := &countingInput{Input: in, cred: cred}
	if _, err := New().Execute(context.Background(), counter); err != nil {
		t.Fatal(err)
	}
	if counter.calls != 1 {
		t.Fatalf("Credential called %d times, want 1", counter.calls)
	}
}

type countingInput struct {
	*nodetest.Input
	cred  nodesdk.Credential
	calls int
}

func (c *countingInput) Credential(context.Context, string) (nodesdk.Credential, error) {
	c.calls++
	return c.cred, nil
}
