package credential_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/service/credential"
)

func TestCreateGetRedactsSecretsAndEncryptsAtRest(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	v, err := e.svc.Create(ctx, credential.Input{Name: "api", Type: "engi.http.basic", Data: map[string]any{"user": "ann", "password": "hunter2"}})
	if err != nil {
		t.Fatal(err)
	}
	if v.Data["user"] != "ann" || v.Data["password"] != credential.Sentinel {
		t.Fatalf("view data = %v", v.Data)
	}
	got, err := e.svc.Get(ctx, v.ID)
	if err != nil || got.Data["password"] != credential.Sentinel {
		t.Fatalf("get = %v %v", got.Data, err)
	}
	row, err := e.store.Credentials().Get(ctx, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(row.Data, []byte("hunter2")) || bytes.Contains(row.Data, []byte("ann")) || row.KeyID != "k1" {
		t.Fatalf("row not encrypted: key=%s", row.KeyID)
	}
	res, err := e.svc.Resolve(ctx, v.ID)
	if err != nil || res.Data["password"] != "hunter2" || res.Type != "engi.http.basic" || res.Auth == nil {
		t.Fatalf("resolve = %+v %v", res, err)
	}
}

func TestResolveUnknownIsNotFound(t *testing.T) {
	e := newEnv(t)
	_, err := e.svc.Resolve(context.Background(), "cr_01J00000000000000000000000")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	var _ port.CredentialResolver = e.svc
}

func TestRowsCannotBeSwapped(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	a, _ := e.svc.Create(ctx, credential.Input{Name: "a", Type: "engi.http.bearer", Data: map[string]any{"token": "ta"}})
	b, _ := e.svc.Create(ctx, credential.Input{Name: "b", Type: "engi.http.bearer", Data: map[string]any{"token": "tb"}})
	rowA, _ := e.store.Credentials().Get(ctx, a.ID)
	rowB, _ := e.store.Credentials().Get(ctx, b.ID)
	rowB.Data = rowA.Data // attacker copies A's ciphertext onto B
	if err := e.store.Credentials().Update(ctx, rowB); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Resolve(ctx, b.ID); err == nil {
		t.Fatal("swapped ciphertext must not decrypt")
	}
}

func TestUpdateSentinelSemantics(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	v, _ := e.svc.Create(ctx, credential.Input{Name: "api", Type: "engi.http.basic", Data: map[string]any{"user": "ann", "password": "hunter2"}})

	// Sentinel keeps the secret, other fields change.
	if _, err := e.svc.Update(ctx, v.ID, credential.Input{Name: "api2", Data: map[string]any{"user": "bob", "password": credential.Sentinel}}); err != nil {
		t.Fatal(err)
	}
	res, _ := e.svc.Resolve(ctx, v.ID)
	if res.Data["user"] != "bob" || res.Data["password"] != "hunter2" {
		t.Fatalf("after sentinel update: %v", res.Data)
	}
	// Omitting the secret keeps it too.
	if _, err := e.svc.Update(ctx, v.ID, credential.Input{Name: "api2", Data: map[string]any{"user": "cy"}}); err != nil {
		t.Fatal(err)
	}
	if res, _ = e.svc.Resolve(ctx, v.ID); res.Data["password"] != "hunter2" || res.Data["user"] != "cy" {
		t.Fatalf("after omit update: %v", res.Data)
	}
	// A new value replaces it.
	if _, err := e.svc.Update(ctx, v.ID, credential.Input{Name: "api2", Data: map[string]any{"user": "cy", "password": "new"}}); err != nil {
		t.Fatal(err)
	}
	if res, _ = e.svc.Resolve(ctx, v.ID); res.Data["password"] != "new" {
		t.Fatalf("after replace: %v", res.Data)
	}
	// Type cannot change; unknown ids fail.
	if _, err := e.svc.Update(ctx, v.ID, credential.Input{Name: "x", Type: "engi.http.bearer", Data: map[string]any{"token": "t"}}); err == nil {
		t.Fatal("type change must fail")
	}
	if _, err := e.svc.Update(ctx, "cr_01J00000000000000000000000", credential.Input{Name: "x", Data: map[string]any{}}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestValidation(t *testing.T) {
	e := newEnv(t)
	tests := []struct {
		name string
		in   credential.Input
		want string
	}{
		{"unknown type", credential.Input{Name: "x", Type: "nope", Data: nil}, "unknown credential type"},
		{"empty name", credential.Input{Name: " ", Type: "engi.http.basic"}, "name"},
		{"missing required", credential.Input{Name: "x", Type: "engi.http.basic", Data: map[string]any{}}, "data.user: is required"},
		{"unknown field", credential.Input{Name: "x", Type: "engi.http.basic", Data: map[string]any{"user": "a", "extra": 1}}, "data.extra: unknown field"},
		{"wrong type", credential.Input{Name: "x", Type: "engi.http.basic", Data: map[string]any{"user": 5}}, "must be a string"},
		{"sentinel on create", credential.Input{Name: "x", Type: "engi.http.basic", Data: map[string]any{"user": "a", "password": credential.Sentinel}}, "no stored value"},
		{"bad option", credential.Input{Name: "x", Type: "engi.postgres", Data: map[string]any{"host": "h", "database": "d", "user": "u", "sslMode": "bogus"}}, "must be one of"},
		{"managed field", credential.Input{Name: "x", Type: "engi.oauth2", Data: map[string]any{"accessTokenUrl": "http://t", "clientId": "c", "oauthTokenData": map[string]any{}}}, "managed by the system"},
		{"display options hide required", credential.Input{Name: "x", Type: "engi.oauth2", Data: map[string]any{"grantType": "authorizationCode", "accessTokenUrl": "http://t", "clientId": "c"}}, "data.authUrl: is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := e.svc.Create(context.Background(), tt.in)
			var ve *domain.ValidationError
			if !errors.As(err, &ve) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want validation error containing %q", err, tt.want)
			}
		})
	}
	// Defaults and hidden fields: client credentials does not need authUrl; port defaults.
	if _, err := e.svc.Create(context.Background(), credential.Input{Name: "ok", Type: "engi.oauth2",
		Data: map[string]any{"accessTokenUrl": "http://t", "clientId": "c"}}); err != nil {
		t.Fatalf("hidden authUrl must not be required: %v", err)
	}
	v, err := e.svc.Create(context.Background(), credential.Input{Name: "pg", Type: "engi.postgres", Data: map[string]any{"host": "h", "database": "d", "user": "u"}})
	if err != nil || v.Data["port"] != float64(5432) || v.Data["sslMode"] != "prefer" {
		t.Fatalf("defaults: %v %v", v.Data, err)
	}
}

func TestListAndDelete(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	v, _ := e.svc.Create(ctx, credential.Input{Name: "a", Type: "engi.http.bearer", Data: map[string]any{"token": "t"}})
	page, err := e.svc.List(ctx, port.Page{})
	if err != nil || len(page.Items) != 1 || len(page.Items[0].Data) != 0 {
		t.Fatalf("list = %+v %v", page, err)
	}
	if err := e.svc.Delete(ctx, v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Get(ctx, v.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestRotateKeys(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.cfg.RotationBatchSize = 2
	old := e.service(e.cfg)
	var ids []domain.CredentialID
	for i := 0; i < 5; i++ {
		v, err := old.Create(ctx, credential.Input{Name: "c", Type: "engi.http.bearer", Data: map[string]any{"token": "tok-secret"}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, v.ID)
	}
	rot := e.cfg
	rot.Keys = map[string]config.Secret{"k1": e.cfg.Keys["k1"], "k2": newKey()}
	rot.ActiveKeyID = "k2"
	svc := e.service(rot)
	res, err := svc.RotateKeys(ctx)
	if err != nil || res.Rotated != 5 || res.Failed != 0 {
		t.Fatalf("rotate = %+v %v", res, err)
	}
	for _, id := range ids {
		row, _ := e.store.Credentials().Get(ctx, id)
		if row.KeyID != "k2" {
			t.Fatalf("row %s on key %s", id, row.KeyID)
		}
		if r, err := svc.Resolve(ctx, id); err != nil || r.Data["token"] != "tok-secret" {
			t.Fatalf("resolve after rotate: %v %v", r.Data, err)
		}
	}
	if res, err = svc.RotateKeys(ctx); err != nil || res.Rotated != 0 {
		t.Fatalf("second run = %+v %v", res, err)
	}
	// The retired key is no longer needed: a keyring with only k2 still works.
	only := e.cfg
	only.Keys = map[string]config.Secret{"k2": rot.Keys["k2"]}
	only.ActiveKeyID = "k2"
	if _, err := e.service(only).Resolve(ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
}

func TestRotateKeysReportsUndecryptableRows(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	good, _ := e.svc.Create(ctx, credential.Input{Name: "g", Type: "engi.http.bearer", Data: map[string]any{"token": "t"}})
	rot := e.cfg
	rot.Keys = map[string]config.Secret{"k2": newKey()} // k1 dropped too early
	rot.ActiveKeyID = "k2"
	res, err := e.service(rot).RotateKeys(ctx)
	if err == nil || res.Failed != 1 || res.Rotated != 0 {
		t.Fatalf("rotate = %+v %v", res, err)
	}
	if _, err := e.svc.Resolve(ctx, good.ID); err != nil {
		t.Fatalf("row must be untouched: %v", err)
	}
}
