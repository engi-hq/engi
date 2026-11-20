// Package tag is the tag management use case: create, rename and delete the
// labels workflows carry.
package tag

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

// IDGenerator creates tag IDs.
type IDGenerator interface {
	TagID() (domain.TagID, error)
}

// Deps are the collaborators of Service.
type Deps struct {
	Store port.TagStore
	IDs   IDGenerator
}

// Service manages tags.
type Service struct{ d Deps }

// New builds the service.
func New(d Deps) (*Service, error) {
	if d.Store == nil || d.IDs == nil {
		return nil, errors.New("tag service: store and ids are required")
	}
	return &Service{d: d}, nil
}

// Create adds a tag; the name is trimmed.
func (s *Service) Create(ctx context.Context, name string) (domain.Tag, error) {
	name = strings.TrimSpace(name)
	if err := domain.ValidateTagName(name); err != nil {
		return domain.Tag{}, err
	}
	id, err := s.d.IDs.TagID()
	if err != nil {
		return domain.Tag{}, fmt.Errorf("generating tag id: %w", err)
	}
	if err := s.d.Store.Create(ctx, domain.Tag{ID: id, Name: name}); err != nil {
		return domain.Tag{}, err
	}
	return s.d.Store.Get(ctx, id)
}

// Rename changes a tag's name everywhere it is used.
func (s *Service) Rename(ctx context.Context, id domain.TagID, name string) (domain.Tag, error) {
	name = strings.TrimSpace(name)
	if err := domain.ValidateTagName(name); err != nil {
		return domain.Tag{}, err
	}
	if err := s.d.Store.Rename(ctx, id, name); err != nil {
		return domain.Tag{}, err
	}
	return s.d.Store.Get(ctx, id)
}

// Delete removes a tag from the store and from all workflows.
func (s *Service) Delete(ctx context.Context, id domain.TagID) error {
	return s.d.Store.Delete(ctx, id)
}

// List returns tags ordered by name.
func (s *Service) List(ctx context.Context, p port.Page) (port.PageResult[domain.Tag], error) {
	return s.d.Store.List(ctx, p)
}
