package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/engi-hq/engi/internal/adapter/credential/noop"
	"github.com/engi-hq/engi/internal/adapter/crypto/aesgcm"
	pglock "github.com/engi-hq/engi/internal/adapter/lock/postgres"
	"github.com/engi-hq/engi/internal/adapter/oauth2/httptoken"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/credtype/builtin"
	"github.com/engi-hq/engi/internal/credtype/registry"
	"github.com/engi-hq/engi/internal/id"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/service/credential"
	"github.com/engi-hq/engi/internal/service/variable"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// credentialStack is what New builds for credentials.
type credentialStack struct {
	resolver port.CredentialResolver
	service  *credential.Service
	types    *registry.Registry
	// db is the database the service reads from, opened here only when the
	// caller did not supply a store. The App shares it with the runtime.
	db *database
}

// buildCredentials wires the credential service when a keyring is configured
// and falls back to the noop resolver (no credentials) otherwise, which keeps
// the in-memory `engi run` path free of key material.
func buildCredentials(ctx context.Context, cfg config.Config, opts Options, clock port.Clock, httpc nodesdk.HTTPClient, log *slog.Logger) (credentialStack, error) {
	types, err := builtin.Registry()
	if err != nil {
		return credentialStack{}, fmt.Errorf("building credential types: %w", err)
	}
	stack := credentialStack{resolver: noop.Resolver{}, types: types}
	if opts.Credentials != nil {
		stack.resolver = opts.Credentials
		return stack, nil
	}
	if !cfg.Credentials.Enabled() {
		return stack, nil
	}
	cipher, err := aesgcm.New(cfg.Credentials)
	if err != nil {
		return credentialStack{}, fmt.Errorf("building credential cipher: %w", err)
	}
	store, tx := opts.CredentialStore, opts.Tx
	// Token refreshes are serialised across replicas when this process opens
	// a Postgres database itself. A caller-supplied store gets no cross-process
	// lock, because the process does not know which database backs it.
	var refreshLock port.KeyLocker
	if store == nil {
		db, err := openDatabase(ctx, cfg, opts.ReadFile)
		if err != nil {
			return credentialStack{}, fmt.Errorf("opening credential store: %w", err)
		}
		if err := prepareSchema(ctx, db, cfg.Server.AutoMigrate); err != nil {
			_ = db.Close()
			return credentialStack{}, err
		}
		s := db.stores(cfg.Store, clock)
		store, tx = s.Credentials, s.Tx
		stack.db = db
		if db.driver == config.StorePostgres {
			refreshLock = pglock.New(db.DB, cfg.Credentials.OAuth2RefreshLockTimeout)
		}
	}
	svc, err := credential.New(cfg.Credentials, credential.Deps{
		Store: store, Cipher: cipher, Types: types, Clock: clock, Tx: tx, HTTP: httpc,
		OAuth2:      httptoken.New(httpc, cfg.Credentials.OAuth2RequestTimeout),
		RefreshLock: refreshLock,
		IDs:         id.NewGenerator(clock.Now, cryptoRand()),
		Logger:      log.With("component", "credentials"),
	})
	if err != nil {
		if stack.db != nil {
			_ = stack.db.Close()
		}
		return credentialStack{}, fmt.Errorf("building credential service: %w", err)
	}
	stack.resolver, stack.service = svc, svc
	return stack, nil
}

// buildVariables wires the variable service. Secret variables need the
// credential keyring; without one the service only accepts plain variables.
func (a *App) buildVariables(store storeSet) (*variable.Service, error) {
	var cipher port.Cipher
	if a.Config.Credentials.Enabled() {
		c, err := aesgcm.New(a.Config.Credentials)
		if err != nil {
			return nil, fmt.Errorf("building variable cipher: %w", err)
		}
		cipher = c
	}
	svc, err := variable.New(variable.Deps{Store: store.Variables, Cipher: cipher, IDs: a.ids})
	if err != nil {
		return nil, fmt.Errorf("building variable service: %w", err)
	}
	return svc, nil
}
