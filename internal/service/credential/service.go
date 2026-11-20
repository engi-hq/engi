package credential

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// TypeRegistry looks up credential types.
type TypeRegistry interface {
	Get(name string) (nodesdk.CredentialType, bool)
}

// IDGenerator creates credential IDs.
type IDGenerator interface {
	CredentialID() (domain.CredentialID, error)
}

// Deps are the collaborators of Service.
type Deps struct {
	Store  port.CredentialStore
	Cipher port.Cipher
	Types  TypeRegistry
	// OAuth2 fetches and refreshes tokens; required only for OAuth2 types.
	OAuth2 port.OAuth2Client
	// HTTP sends credential test requests.
	HTTP  nodesdk.HTTPClient
	Clock port.Clock
	IDs   IDGenerator
	// Tx, when set, makes each re-encryption batch atomic.
	Tx port.TxManager
	// RefreshLock serialises token refreshes of one credential across
	// processes (cluster mode). Nil when the process is the only writer, as in
	// standalone mode, where the in-process lock is enough.
	RefreshLock port.KeyLocker
	// Random is the entropy source for PKCE verifiers; defaults to crypto/rand.
	Random io.Reader
	Logger *slog.Logger
}

// Service implements credential management and resolution.
type Service struct {
	cfg   config.CredentialsConfig
	d     Deps
	locks *keyLock
}

var _ port.CredentialResolver = (*Service)(nil)

// New builds the service.
func New(cfg config.CredentialsConfig, d Deps) (*Service, error) {
	switch {
	case d.Store == nil, d.Cipher == nil, d.Types == nil, d.Clock == nil, d.IDs == nil, d.Logger == nil:
		return nil, errors.New("credential service: store, cipher, types, clock, ids and logger are required")
	}
	if d.Random == nil {
		d.Random = rand.Reader
	}
	return &Service{cfg: cfg, d: d, locks: newKeyLock()}, nil
}

// Input is the data to create or update a credential.
type Input struct {
	Name string
	// Type is required on create and, if given on update, must not change.
	Type string
	Data map[string]any
}

// View is a credential as returned to callers: secrets are replaced by Sentinel.
type View struct {
	ID          domain.CredentialID
	Name        string
	Type        string
	Data        map[string]any
	KeyID       string
	NeedsReauth bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (s *Service) typeOf(name string) (nodesdk.CredentialType, error) {
	t, ok := s.d.Types.Get(name)
	if !ok {
		return nodesdk.CredentialType{}, &domain.ValidationError{Issues: []domain.Issue{{Path: "type", Message: fmt.Sprintf("unknown credential type %q", name)}}}
	}
	return t, nil
}

func nameIssue(name string) error {
	if strings.TrimSpace(name) == "" {
		return &domain.ValidationError{Issues: []domain.Issue{{Path: "name", Message: "must not be empty"}}}
	}
	return nil
}

func aad(id domain.CredentialID) []byte { return []byte(id) }

func (s *Service) seal(ctx context.Context, id domain.CredentialID, data map[string]any) (keyID string, ct []byte, err error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return "", nil, fmt.Errorf("encoding credential data: %w", err)
	}
	keyID, ct, err = s.d.Cipher.Encrypt(ctx, raw, aad(id))
	if err != nil {
		return "", nil, fmt.Errorf("encrypting credential %s: %w", id, err)
	}
	return keyID, ct, nil
}

func (s *Service) open(ctx context.Context, row domain.Credential) (map[string]any, error) {
	raw, err := s.d.Cipher.Decrypt(ctx, row.KeyID, row.Data, aad(row.ID))
	if err != nil {
		return nil, fmt.Errorf("decrypting credential %s: %w", row.ID, err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("decoding credential %s: %w", row.ID, err)
	}
	return data, nil
}

// Create validates and stores a new credential.
func (s *Service) Create(ctx context.Context, in Input) (View, error) {
	if err := nameIssue(in.Name); err != nil {
		return View{}, err
	}
	typ, err := s.typeOf(in.Type)
	if err != nil {
		return View{}, err
	}
	data, err := normalize(typ, in.Data)
	if err != nil {
		return View{}, err
	}
	id, err := s.d.IDs.CredentialID()
	if err != nil {
		return View{}, fmt.Errorf("creating credential id: %w", err)
	}
	keyID, ct, err := s.seal(ctx, id, data)
	if err != nil {
		return View{}, err
	}
	row := domain.Credential{ID: id, Name: in.Name, Type: typ.Name, KeyID: keyID, Data: ct}
	if err := s.d.Store.Create(ctx, row); err != nil {
		return View{}, fmt.Errorf("storing credential: %w", err)
	}
	s.d.Logger.InfoContext(ctx, "credential created", "credential", id, "type", typ.Name)
	return s.Get(ctx, id)
}

// Update replaces the name and data of a credential. Secret fields that are
// missing or equal to Sentinel keep their stored value. The type cannot change.
func (s *Service) Update(ctx context.Context, id domain.CredentialID, in Input) (View, error) {
	if err := nameIssue(in.Name); err != nil {
		return View{}, err
	}
	unlock, err := s.locks.lock(ctx, string(id))
	if err != nil {
		return View{}, err
	}
	defer unlock()
	row, err := s.d.Store.Get(ctx, id)
	if err != nil {
		return View{}, fmt.Errorf("loading credential: %w", err)
	}
	if in.Type != "" && in.Type != row.Type {
		return View{}, &domain.ValidationError{Issues: []domain.Issue{{Path: "type", Message: "cannot be changed"}}}
	}
	typ, err := s.typeOf(row.Type)
	if err != nil {
		return View{}, err
	}
	old, err := s.open(ctx, row)
	if err != nil {
		return View{}, err
	}
	merged := make(map[string]any, len(in.Data))
	for k, v := range in.Data {
		merged[k] = v
	}
	for _, f := range typ.Fields {
		if !f.Secret || f.Managed {
			continue
		}
		if v, given := merged[f.Name]; (!given || v == Sentinel) && old[f.Name] != nil {
			merged[f.Name] = old[f.Name]
		}
	}
	data, err := normalize(typ, merged)
	if err != nil {
		return View{}, err
	}
	for _, f := range typ.Fields {
		if f.Managed && old[f.Name] != nil && !oauthSettingsChanged(typ, old, data) {
			data[f.Name] = old[f.Name]
		}
	}
	keyID, ct, err := s.seal(ctx, id, data)
	if err != nil {
		return View{}, err
	}
	row.Name, row.KeyID, row.Data = in.Name, keyID, ct
	if err := s.d.Store.Update(ctx, row); err != nil {
		return View{}, fmt.Errorf("updating credential: %w", err)
	}
	s.d.Logger.InfoContext(ctx, "credential updated", "credential", id, "type", typ.Name)
	updated, err := s.d.Store.Get(ctx, id)
	if err != nil {
		return View{}, fmt.Errorf("reloading credential: %w", err)
	}
	return s.viewOf(typ, updated, data)
}

// oauthSettingsChanged reports whether an edit invalidates a stored token.
func oauthSettingsChanged(t nodesdk.CredentialType, old, next map[string]any) bool {
	o := t.OAuth2
	if o == nil {
		return false
	}
	for _, f := range []string{o.GrantField, o.TokenURLField, o.ClientIDField, o.ClientSecretField, o.ScopeField} {
		if fmt.Sprint(old[f]) != fmt.Sprint(next[f]) {
			return true
		}
	}
	return false
}

// Get returns the credential with secrets redacted.
func (s *Service) Get(ctx context.Context, id domain.CredentialID) (View, error) {
	row, err := s.d.Store.Get(ctx, id)
	if err != nil {
		return View{}, fmt.Errorf("loading credential: %w", err)
	}
	typ, err := s.typeOf(row.Type)
	if err != nil {
		return View{}, err
	}
	data, err := s.open(ctx, row)
	if err != nil {
		return View{}, err
	}
	return s.viewOf(typ, row, data)
}

func (s *Service) viewOf(typ nodesdk.CredentialType, row domain.Credential, data map[string]any) (View, error) {
	v := View{ID: row.ID, Name: row.Name, Type: row.Type, KeyID: row.KeyID, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		Data: make(map[string]any, len(data))}
	for _, f := range typ.Fields {
		val, ok := data[f.Name]
		switch {
		case f.Managed:
			if typ.OAuth2 != nil && f.Name == typ.OAuth2.TokenField {
				v.NeedsReauth = parseToken(val).NeedsReauth
			}
		case !ok || val == nil:
		case f.Secret:
			v.Data[f.Name] = Sentinel
		default:
			v.Data[f.Name] = val
		}
	}
	return v, nil
}

// List returns one page of credentials without their data.
func (s *Service) List(ctx context.Context, p port.Page) (port.PageResult[domain.Credential], error) {
	res, err := s.d.Store.List(ctx, p)
	if err != nil {
		return port.PageResult[domain.Credential]{}, fmt.Errorf("listing credentials: %w", err)
	}
	return res, nil
}

// Delete removes a credential.
func (s *Service) Delete(ctx context.Context, id domain.CredentialID) error {
	unlock, err := s.locks.lock(ctx, string(id))
	if err != nil {
		return err
	}
	defer unlock()
	if err := s.d.Store.Delete(ctx, id); err != nil {
		return fmt.Errorf("deleting credential: %w", err)
	}
	s.d.Logger.InfoContext(ctx, "credential deleted", "credential", id)
	return nil
}

// Test checks that the credential works. OAuth2 credentials fetch a fresh
// token; other types send their declared test request. Types without a test
// return an error wrapping domain.ErrNoTest. It is bounded by the configured
// test timeout and never runs implicitly.
func (s *Service) Test(ctx context.Context, id domain.CredentialID) error {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.TestTimeout)
	defer cancel()
	row, err := s.d.Store.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("loading credential: %w", err)
	}
	typ, err := s.typeOf(row.Type)
	if err != nil {
		return err
	}
	data, err := s.open(ctx, row)
	if err != nil {
		return err
	}
	if typ.OAuth2 != nil {
		if _, err := s.oauthData(ctx, row, typ, data, true); err != nil {
			return err
		}
		return nil
	}
	if typ.Test == nil {
		return fmt.Errorf("credential type %q: %w", typ.Name, domain.ErrNoTest)
	}
	if s.d.HTTP == nil {
		return errors.New("credential test: no HTTP client configured")
	}
	if err := typ.RunTest(ctx, s.d.HTTP, data); err != nil {
		return fmt.Errorf("testing credential %s: %w", id, err)
	}
	return nil
}

// Resolve returns the decrypted credential for one node call. OAuth2 access
// tokens are refreshed first when they expire within the configured skew.
// The result must not be logged or stored.
func (s *Service) Resolve(ctx context.Context, id domain.CredentialID) (port.CredentialData, error) {
	row, err := s.d.Store.Get(ctx, id)
	if err != nil {
		return port.CredentialData{}, fmt.Errorf("loading credential: %w", err)
	}
	typ, err := s.typeOf(row.Type)
	if err != nil {
		return port.CredentialData{}, err
	}
	data, err := s.open(ctx, row)
	if err != nil {
		return port.CredentialData{}, err
	}
	if typ.OAuth2 != nil {
		if data, err = s.oauthData(ctx, row, typ, data, false); err != nil {
			return port.CredentialData{}, err
		}
	}
	return port.CredentialData{
		Type: typ.Name, ID: string(row.ID), Version: row.UpdatedAt.UnixNano(), Data: data, Auth: typ.Authenticate,
	}, nil
}

// RotationResult reports a RotateKeys run.
type RotationResult struct {
	// Rotated credentials were re-encrypted with the active key.
	Rotated int
	// Failed credentials could not be re-encrypted (see the returned error).
	Failed int
}

// RotateKeys re-encrypts every credential that is not on the active key, in
// batches. It is idempotent and resumable: run it again after a failure.
func (s *Service) RotateKeys(ctx context.Context) (RotationResult, error) {
	var res RotationResult
	var errs []error
	active := s.d.Cipher.ActiveKeyID()
	page := port.Page{Limit: s.cfg.RotationBatchSize}
	for {
		if err := ctx.Err(); err != nil {
			return res, fmt.Errorf("rotating keys: %w", err)
		}
		batch, err := s.d.Store.List(ctx, page)
		if err != nil {
			return res, fmt.Errorf("listing credentials: %w", err)
		}
		var stale []domain.CredentialID
		for _, c := range batch.Items {
			if c.KeyID != active {
				stale = append(stale, c.ID)
			}
		}
		rotate := func(ctx context.Context) error {
			for _, id := range stale {
				if err := s.rotateOne(ctx, id, active); err != nil {
					res.Failed++
					errs = append(errs, err)
					continue
				}
				res.Rotated++
			}
			return nil
		}
		if s.d.Tx != nil && len(stale) > 0 {
			err = s.d.Tx.WithinTx(ctx, rotate)
		} else {
			err = rotate(ctx)
		}
		if err != nil {
			return res, fmt.Errorf("rotating batch: %w", err)
		}
		if batch.NextCursor == "" {
			break
		}
		page.Cursor = batch.NextCursor
	}
	s.d.Logger.InfoContext(ctx, "credential keys rotated", "rotated", res.Rotated, "failed", res.Failed, "keyId", active)
	return res, errors.Join(errs...)
}

func (s *Service) rotateOne(ctx context.Context, id domain.CredentialID, active string) error {
	unlock, err := s.locks.lock(ctx, string(id))
	if err != nil {
		return err
	}
	defer unlock()
	row, err := s.d.Store.Get(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return nil // deleted meanwhile
	}
	if err != nil {
		return fmt.Errorf("loading credential %s: %w", id, err)
	}
	if row.KeyID == active {
		return nil
	}
	data, err := s.open(ctx, row)
	if err != nil {
		return err
	}
	keyID, ct, err := s.seal(ctx, id, data)
	if err != nil {
		return err
	}
	row.KeyID, row.Data = keyID, ct
	if err := s.d.Store.Update(ctx, row); err != nil {
		return fmt.Errorf("updating credential %s: %w", id, err)
	}
	return nil
}
