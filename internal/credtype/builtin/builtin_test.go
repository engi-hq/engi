package builtin_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/engi-hq/engi/internal/credtype/builtin"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

func TestRegistryHoldsAllTypes(t *testing.T) {
	r, err := builtin.Registry()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(r.List()), len(builtin.Types()); got != want {
		t.Fatalf("registered %d, want %d", got, want)
	}
	if err := r.Register(builtin.Types()[0]); err == nil {
		t.Fatal("duplicate registration must fail")
	}
}

func TestAuthenticate(t *testing.T) {
	tests := []struct {
		name  string
		typ   string
		data  map[string]any
		check func(t *testing.T, r *http.Request)
	}{
		{"basic", "engi.http.basic", map[string]any{"user": "ann", "password": "pw"}, func(t *testing.T, r *http.Request) {
			if u, p, ok := r.BasicAuth(); !ok || u != "ann" || p != "pw" {
				t.Fatalf("basic auth = %q %q %v", u, p, ok)
			}
		}},
		{"header", "engi.http.header", map[string]any{"name": "X-Key", "value": "abc"}, func(t *testing.T, r *http.Request) {
			if r.Header.Get("X-Key") != "abc" {
				t.Fatalf("headers = %v", r.Header)
			}
		}},
		{"bearer", "engi.http.bearer", map[string]any{"token": "tkn"}, func(t *testing.T, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer tkn" {
				t.Fatalf("headers = %v", r.Header)
			}
		}},
		{"query", "engi.http.queryKey", map[string]any{"name": "key", "value": "v&1"}, func(t *testing.T, r *http.Request) {
			if r.URL.Query().Get("key") != "v&1" || r.URL.Query().Get("keep") != "1" {
				t.Fatalf("query = %v", r.URL.Query())
			}
		}},
		{"oauth2 access token", "engi.oauth2", map[string]any{"accessToken": "at"}, func(t *testing.T, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer at" {
				t.Fatalf("headers = %v", r.Header)
			}
		}},
	}
	reg, _ := builtin.Registry()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			typ, ok := reg.Get(tt.typ)
			if !ok {
				t.Fatal("type missing")
			}
			req := httptest.NewRequest(http.MethodGet, "http://example.test/x?keep=1", http.NoBody)
			cred := nodesdk.Credential{Type: typ.Name, Data: tt.data, Auth: typ.Authenticate}
			if err := cred.Authenticate(req); err != nil {
				t.Fatal(err)
			}
			tt.check(t, req)
		})
	}
}

func TestAuthenticateErrors(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.test/", http.NoBody)
	if err := (nodesdk.Credential{Type: "engi.postgres"}).Authenticate(req); err == nil {
		t.Fatal("type without authenticate must error")
	}
	reg, _ := builtin.Registry()
	typ, _ := reg.Get("engi.http.bearer")
	if err := (nodesdk.Credential{Data: map[string]any{}, Auth: typ.Authenticate}).Authenticate(req); err == nil {
		t.Fatal("missing token must error")
	}
}

func TestCustomAuthenticate(t *testing.T) {
	spec := &nodesdk.AuthSpec{Kind: nodesdk.AuthCustom, Custom: func(r *http.Request, d map[string]any) error {
		r.Header.Set("X-Sig", d["secret"].(string)+"-signed")
		return nil
	}}
	req := httptest.NewRequest(http.MethodGet, "http://example.test/", http.NoBody)
	if err := (nodesdk.Credential{Data: map[string]any{"secret": "s"}, Auth: spec}).Authenticate(req); err != nil {
		t.Fatal(err)
	}
	if req.Header.Get("X-Sig") != "s-signed" {
		t.Fatal("custom function not applied")
	}
}

func TestChatCredentialTypesAuthenticate(t *testing.T) {
	r, err := builtin.Registry()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		data map[string]any
		url  string
		want func(*http.Request) bool
	}{
		{"engi.slack", map[string]any{"token": "xoxb-1"}, "https://slack.test/api/x", func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer xoxb-1" }},
		{"engi.telegram", map[string]any{"token": "1:abc"}, "https://tg.test/bot-token/getMe", func(r *http.Request) bool { return r.URL.Path == "/bot1:abc/getMe" }},
		{"engi.teamsWebhook", map[string]any{"url": "https://hooks.test/h/1"}, "http://placeholder.invalid/", func(r *http.Request) bool {
			return r.URL.String() == "https://hooks.test/h/1" && r.Host == "hooks.test"
		}},
	} {
		ct, ok := r.Get(tc.name)
		if !ok {
			t.Fatalf("%s is not registered", tc.name)
		}
		req, _ := http.NewRequest(http.MethodPost, tc.url, http.NoBody)
		if err := ct.Authenticate.Authenticate(req, tc.data); err != nil || !tc.want(req) {
			t.Errorf("%s: err=%v url=%s", tc.name, err, req.URL)
		}
		if err := ct.Authenticate.Authenticate(req, map[string]any{}); err == nil {
			t.Errorf("%s: missing secret must fail", tc.name)
		}
	}
}
