// Package variable is the variable use case: instance-wide key/value pairs
// read as $vars.KEY, with optional encryption at rest for secret values.
//
// Plaintext of a secret lives only inside this package and in the map returned
// by Vars. Nothing here logs values.
package variable

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

// Sentinel replaces secret values in anything returned to callers.
const Sentinel = "__ENGI_SECRET__"

// IDGenerator creates variable IDs.
type IDGenerator interface {
	VariableID() (domain.VariableID, error)
}

// Deps are the collaborators of Service.
type Deps struct {
	Store port.VariableStore
	// Cipher seals secret values; nil rejects secret variables.
	Cipher port.Cipher
	IDs    IDGenerator
}

// Service manages variables and resolves them for executions.
type Service struct{ d Deps }

var _ port.VarsResolver = (*Service)(nil)

// New builds the service.
func New(d Deps) (*Service, error) {
	if d.Store == nil || d.IDs == nil {
		return nil, errors.New("variable service: store and ids are required")
	}
	return &Service{d: d}, nil
}

// Input creates a variable.
type Input struct {
	Key    string
	Value  string
	Secret bool
}

// Patch changes a variable; nil fields stay as they are. Turning Secret off
// needs a new Value, so a secret is never exposed by a flag flip.
type Patch struct {
	Key    *string
	Value  *string
	Secret *bool
}

// View is a variable as returned to callers: a secret's value is Sentinel.
type View struct {
	ID        domain.VariableID
	Key       string
	Value     string
	Secret    bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

func issue(path, msg string) error {
	return &domain.ValidationError{Issues: []domain.Issue{{Path: path, Message: msg}}}
}

func aad(id domain.VariableID) []byte { return []byte(id) }

func toView(v domain.Variable) View {
	out := View{ID: v.ID, Key: v.Key, Secret: v.Secret, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
	if v.Secret {
		out.Value = Sentinel
	} else {
		out.Value = string(v.Value)
	}
	return out
}

// seal prepares the stored form of value.
func (s *Service) seal(ctx context.Context, v *domain.Variable, value string) error {
	if !v.Secret {
		v.KeyID, v.Value = "", []byte(value)
		return nil
	}
	if s.d.Cipher == nil {
		return issue("secret", "secret variables need an encryption key to be configured")
	}
	keyID, ct, err := s.d.Cipher.Encrypt(ctx, []byte(value), aad(v.ID))
	if err != nil {
		return fmt.Errorf("encrypting variable %s: %w", v.ID, err)
	}
	v.KeyID, v.Value = keyID, ct
	return nil
}

func (s *Service) open(ctx context.Context, v domain.Variable) (string, error) {
	if !v.Secret {
		return string(v.Value), nil
	}
	if s.d.Cipher == nil {
		return "", fmt.Errorf("variable %s is secret but no encryption key is configured", v.ID)
	}
	raw, err := s.d.Cipher.Decrypt(ctx, v.KeyID, v.Value, aad(v.ID))
	if err != nil {
		return "", fmt.Errorf("decrypting variable %s: %w", v.ID, err)
	}
	return string(raw), nil
}

// Create validates and stores a variable.
func (s *Service) Create(ctx context.Context, in Input) (View, error) {
	if err := domain.ValidateVariableKey(in.Key); err != nil {
		return View{}, err
	}
	id, err := s.d.IDs.VariableID()
	if err != nil {
		return View{}, fmt.Errorf("generating variable id: %w", err)
	}
	v := domain.Variable{ID: id, Key: in.Key, Secret: in.Secret}
	if err := s.seal(ctx, &v, in.Value); err != nil {
		return View{}, err
	}
	if err := s.d.Store.Create(ctx, v); err != nil {
		return View{}, err
	}
	return s.Get(ctx, id)
}

// Get returns a variable with secrets redacted.
func (s *Service) Get(ctx context.Context, id domain.VariableID) (View, error) {
	v, err := s.d.Store.Get(ctx, id)
	if err != nil {
		return View{}, err
	}
	return toView(v), nil
}

// Update applies a patch.
func (s *Service) Update(ctx context.Context, id domain.VariableID, p Patch) (View, error) {
	v, err := s.d.Store.Get(ctx, id)
	if err != nil {
		return View{}, err
	}
	if p.Key != nil {
		if err := domain.ValidateVariableKey(*p.Key); err != nil {
			return View{}, err
		}
		v.Key = *p.Key
	}
	wasSecret := v.Secret
	if p.Secret != nil {
		v.Secret = *p.Secret
	}
	if p.Value != nil || v.Secret != wasSecret {
		var value string
		switch {
		case p.Value != nil:
			value = *p.Value
		case wasSecret:
			return View{}, issue("value", "is required to make a secret variable plain")
		default:
			value = string(v.Value) // plain becomes secret: seal what is stored
		}
		if err := s.seal(ctx, &v, value); err != nil {
			return View{}, err
		}
	}
	if err := s.d.Store.Update(ctx, v); err != nil {
		return View{}, err
	}
	return s.Get(ctx, id)
}

// Delete removes a variable.
func (s *Service) Delete(ctx context.Context, id domain.VariableID) error {
	return s.d.Store.Delete(ctx, id)
}

// List returns one page of variables with secrets redacted.
func (s *Service) List(ctx context.Context, p port.Page) (port.PageResult[View], error) {
	res, err := s.d.Store.List(ctx, p)
	if err != nil {
		return port.PageResult[View]{}, err
	}
	out := port.PageResult[View]{NextCursor: res.NextCursor}
	for _, v := range res.Items {
		out.Items = append(out.Items, toView(v))
	}
	return out, nil
}

// Vars returns every variable decrypted, for $vars.
func (s *Service) Vars(ctx context.Context) (map[string]any, error) {
	vars := map[string]any{}
	p := port.Page{}
	for {
		res, err := s.d.Store.List(ctx, p)
		if err != nil {
			return nil, fmt.Errorf("listing variables: %w", err)
		}
		for _, v := range res.Items {
			val, err := s.open(ctx, v)
			if err != nil {
				return nil, err
			}
			vars[v.Key] = val
		}
		if res.NextCursor == "" {
			return vars, nil
		}
		p.Cursor = res.NextCursor
	}
}
