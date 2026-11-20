// Package workflow holds the use cases around workflow definitions: create,
// update (every save is a new immutable version), read, list, delete, version
// history, restore and import. Activation (registering triggers) belongs to the
// trigger service; this package only stores definitions.
package workflow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

// Validator checks a workflow against the registered node types (the engine).
type Validator interface {
	Validate(wf domain.Workflow) error
}

// IDGenerator creates workflow IDs.
type IDGenerator interface {
	WorkflowID() (domain.WorkflowID, error)
}

// Deps are the collaborators of Service. All but Logger are required.
type Deps struct {
	Workflows port.WorkflowStore
	Versions  port.WorkflowVersionStore
	Validator Validator
	IDs       IDGenerator
	// NoteLimits bound the sticky notes of a workflow; zero fields mean no limit.
	NoteLimits domain.NoteLimits
	Logger     *slog.Logger
}

// Service implements the workflow use cases. It is safe for concurrent use.
type Service struct {
	d Deps
}

// New builds a Service.
func New(d Deps) (*Service, error) {
	if d.Workflows == nil || d.Versions == nil || d.Validator == nil || d.IDs == nil {
		return nil, errors.New("workflow service requires workflow and version stores, validator and id generator")
	}
	if d.Logger == nil {
		d.Logger = slog.New(slog.DiscardHandler)
	}
	return &Service{d: d}, nil
}

// validate runs the structural checks, then the node-type checks. Both report
// as a *domain.ValidationError so callers see one error shape.
func (s *Service) validate(wf domain.Workflow) error {
	if err := wf.Validate(); err != nil {
		return err //nolint:wrapcheck // already a *domain.ValidationError
	}
	if err := wf.ValidateNoteLimits(s.d.NoteLimits); err != nil {
		return err //nolint:wrapcheck // already a *domain.ValidationError
	}
	if err := s.d.Validator.Validate(wf); err != nil {
		var ve *domain.ValidationError
		if errors.As(err, &ve) {
			return ve
		}
		path := "nodes"
		var we *domain.WorkflowError
		if errors.As(err, &we) && we.Node != "" {
			path = "nodes[" + we.Node + "]"
		}
		return &domain.ValidationError{Issues: []domain.Issue{{Path: path, Message: err.Error()}}}
	}
	return nil
}

// Check validates a workflow document exactly as a save would, without storing
// it. Failures are a *domain.ValidationError.
func (s *Service) Check(wf domain.Workflow) error { return s.validate(wf) }

// Create validates and stores a new workflow with a fresh ID. The workflow
// starts inactive whatever the input says.
func (s *Service) Create(ctx context.Context, wf domain.Workflow) (domain.Workflow, error) {
	id, err := s.d.IDs.WorkflowID()
	if err != nil {
		return domain.Workflow{}, fmt.Errorf("creating workflow id: %w", err)
	}
	wf.ID, wf.Active, wf.Version = id, false, 0
	if err := s.validate(wf); err != nil {
		return domain.Workflow{}, err
	}
	saved, err := s.d.Workflows.Save(ctx, wf)
	if err != nil {
		return domain.Workflow{}, fmt.Errorf("creating workflow: %w", err)
	}
	s.d.Logger.InfoContext(ctx, "workflow created", "workflow", saved.ID)
	return saved, nil
}

// Import stores a workflow read from an export file: like Create, the ID of the
// file is ignored, so importing never overwrites an existing workflow.
func (s *Service) Import(ctx context.Context, wf domain.Workflow) (domain.Workflow, error) {
	return s.Create(ctx, wf)
}

// Update replaces the definition of a workflow and records a new version. A
// positive expectedVersion must equal the current version, otherwise the call
// fails with an error wrapping domain.ErrConflict (optimistic concurrency).
// Activation state is kept; it only changes through the trigger service.
func (s *Service) Update(ctx context.Context, id domain.WorkflowID, wf domain.Workflow, expectedVersion int) (domain.Workflow, error) {
	cur, err := s.d.Workflows.Get(ctx, id)
	if err != nil {
		return domain.Workflow{}, fmt.Errorf("updating workflow %s: %w", id, err)
	}
	if expectedVersion > 0 && cur.Version != expectedVersion {
		return domain.Workflow{}, fmt.Errorf("workflow %s is at version %d, not %d: %w", id, cur.Version, expectedVersion, domain.ErrConflict)
	}
	wf.ID, wf.Active = id, cur.Active
	if err := s.validate(wf); err != nil {
		return domain.Workflow{}, err
	}
	if err := s.checkErrorWorkflow(ctx, cur, wf); err != nil {
		return domain.Workflow{}, err
	}
	saved, err := s.d.Workflows.Save(ctx, wf)
	if err != nil {
		return domain.Workflow{}, fmt.Errorf("updating workflow %s: %w", id, err)
	}
	s.d.Logger.InfoContext(ctx, "workflow updated", "workflow", id, "version", saved.Version)
	return saved, nil
}

// checkErrorWorkflow rejects a newly chosen error workflow that does not exist.
// A value that is unchanged is left alone, so a workflow whose target was
// deleted later can still be saved. Create and Import skip the check on purpose:
// an exported file may name a workflow that only exists on another instance.
func (s *Service) checkErrorWorkflow(ctx context.Context, cur, next domain.Workflow) error {
	target := next.Settings.ErrorWorkflowID
	if target == "" || target == next.ID || target == cur.Settings.ErrorWorkflowID {
		return nil
	}
	if _, err := s.d.Workflows.Get(ctx, target); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return &domain.ValidationError{Issues: []domain.Issue{{
				Path: "settings.errorWorkflowId", Message: fmt.Sprintf("workflow %q does not exist", target),
			}}}
		}
		return fmt.Errorf("checking error workflow %s: %w", target, err)
	}
	return nil
}

// Get returns the current workflow.
func (s *Service) Get(ctx context.Context, id domain.WorkflowID) (domain.Workflow, error) {
	wf, err := s.d.Workflows.Get(ctx, id)
	if err != nil {
		return domain.Workflow{}, fmt.Errorf("getting workflow %s: %w", id, err)
	}
	return wf, nil
}

// List returns a page of workflows, newest first.
func (s *Service) List(ctx context.Context, f port.WorkflowFilter, p port.Page) (port.PageResult[domain.Workflow], error) {
	res, err := s.d.Workflows.List(ctx, f, p)
	if err != nil {
		return port.PageResult[domain.Workflow]{}, fmt.Errorf("listing workflows: %w", err)
	}
	return res, nil
}

// Delete removes a workflow with its versions and executions. Deactivate it
// first; deleting an active workflow is refused so no trigger is left behind.
func (s *Service) Delete(ctx context.Context, id domain.WorkflowID) error {
	cur, err := s.d.Workflows.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("deleting workflow %s: %w", id, err)
	}
	if cur.Active {
		return fmt.Errorf("workflow %s is active, deactivate it first: %w", id, domain.ErrConflict)
	}
	if err := s.d.Workflows.Delete(ctx, id); err != nil {
		return fmt.Errorf("deleting workflow %s: %w", id, err)
	}
	s.d.Logger.InfoContext(ctx, "workflow deleted", "workflow", id)
	return nil
}

// ListVersions returns the version history, newest first. The workflow must exist.
func (s *Service) ListVersions(ctx context.Context, id domain.WorkflowID, p port.Page) (port.PageResult[domain.WorkflowVersion], error) {
	if _, err := s.d.Workflows.Get(ctx, id); err != nil {
		return port.PageResult[domain.WorkflowVersion]{}, fmt.Errorf("listing versions of %s: %w", id, err)
	}
	res, err := s.d.Versions.ListVersions(ctx, id, p)
	if err != nil {
		return port.PageResult[domain.WorkflowVersion]{}, fmt.Errorf("listing versions of %s: %w", id, err)
	}
	return res, nil
}

// GetVersion returns one historical version.
func (s *Service) GetVersion(ctx context.Context, id domain.WorkflowID, version int) (domain.WorkflowVersion, error) {
	v, err := s.d.Versions.GetVersion(ctx, id, version)
	if err != nil {
		return domain.WorkflowVersion{}, fmt.Errorf("getting version %d of %s: %w", version, id, err)
	}
	return v, nil
}

// Restore makes an old version current again by saving its definition as a new
// version; history is never rewritten.
func (s *Service) Restore(ctx context.Context, id domain.WorkflowID, version int) (domain.Workflow, error) {
	v, err := s.d.Versions.GetVersion(ctx, id, version)
	if err != nil {
		return domain.Workflow{}, fmt.Errorf("restoring version %d of %s: %w", version, id, err)
	}
	return s.Update(ctx, id, v.Workflow, 0)
}
