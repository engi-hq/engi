package workflow_test

import (
	"context"
	"errors"
	"testing"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/service/servicetest"
	"github.com/engi-hq/engi/internal/service/workflow"
)

type validator struct{ err error }

func (v validator) Validate(domain.Workflow) error { return v.err }

func newService(t *testing.T, v validator) (*workflow.Service, *servicetest.Store) {
	t.Helper()
	clk := servicetest.NewClock()
	st := servicetest.NewStore(clk)
	svc, err := workflow.New(workflow.Deps{Workflows: st, Versions: st, Validator: v, IDs: servicetest.NewIDs(clk)})
	if err != nil {
		t.Fatal(err)
	}
	return svc, st
}

func sample(name string) domain.Workflow {
	return domain.Workflow{
		Name:  name,
		Nodes: []domain.Node{{Name: "Start", Type: "engi.manual", TypeVersion: 1}, {Name: "End", Type: "engi.noop", TypeVersion: 1}},
		Connections: []domain.Connection{{
			From: domain.Endpoint{Node: "Start", Port: domain.PortMain}, To: domain.Endpoint{Node: "End", Port: domain.PortMain},
		}},
	}
}

func TestCreateAndVersions(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t, validator{})
	in := sample("one")
	in.Active, in.ID = true, "wf_ignored"
	wf, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if wf.ID == "wf_ignored" || wf.Active || wf.Version != 1 {
		t.Fatalf("created = id %q active %v version %d; the id must be new and the workflow inactive", wf.ID, wf.Active, wf.Version)
	}
	upd := sample("two")
	got, err := svc.Update(ctx, wf.ID, upd, 1)
	if err != nil || got.Version != 2 || got.Name != "two" {
		t.Fatalf("Update = %+v, %v", got, err)
	}
	if _, err := svc.Update(ctx, wf.ID, upd, 1); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale update = %v, want ErrConflict", err)
	}
	if got, err = svc.Update(ctx, wf.ID, sample("three"), 0); err != nil || got.Version != 3 {
		t.Fatalf("update without If-Match = %+v, %v", got, err)
	}
	vs, err := svc.ListVersions(ctx, wf.ID, port.Page{})
	if err != nil || len(vs.Items) != 3 || vs.Items[0].Version != 3 {
		t.Fatalf("versions = %d, %v", len(vs.Items), err)
	}
	restored, err := svc.Restore(ctx, wf.ID, 1)
	if err != nil || restored.Version != 4 || restored.Name != "one" {
		t.Fatalf("Restore = %+v, %v", restored, err)
	}
	v1, err := svc.GetVersion(ctx, wf.ID, 1)
	if err != nil || v1.Workflow.Name != "one" {
		t.Fatalf("history was rewritten: %+v, %v", v1, err)
	}
	if _, err := svc.GetVersion(ctx, wf.ID, 99); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown version = %v", err)
	}
}

func TestValidationErrors(t *testing.T) {
	ctx := context.Background()
	t.Run("structural", func(t *testing.T) {
		svc, _ := newService(t, validator{})
		bad := sample("")
		var ve *domain.ValidationError
		if _, err := svc.Create(ctx, bad); !errors.As(err, &ve) || ve.Issues[0].Path != "name" {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("node types", func(t *testing.T) {
		svc, _ := newService(t, validator{err: &domain.WorkflowError{Node: "End", Message: "unknown node type"}})
		var ve *domain.ValidationError
		_, err := svc.Create(ctx, sample("x"))
		if !errors.As(err, &ve) || ve.Issues[0].Path != "nodes[End]" {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("update validates too", func(t *testing.T) {
		svc, _ := newService(t, validator{})
		wf, _ := svc.Create(ctx, sample("ok"))
		var ve *domain.ValidationError
		if _, err := svc.Update(ctx, wf.ID, sample(""), 0); !errors.As(err, &ve) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestUpdateKeepsActivationAndDeleteNeedsInactive(t *testing.T) {
	ctx := context.Background()
	svc, st := newService(t, validator{})
	wf, _ := svc.Create(ctx, sample("a"))
	if err := st.SetActive(ctx, wf.ID, true); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Update(ctx, wf.ID, sample("b"), 0)
	if err != nil || !got.Active {
		t.Fatalf("update dropped activation: %+v, %v", got, err)
	}
	if err := svc.Delete(ctx, wf.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("delete active = %v, want ErrConflict", err)
	}
	if err := st.SetActive(ctx, wf.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(ctx, wf.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx, wf.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get deleted = %v", err)
	}
	if err := svc.Delete(ctx, wf.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("delete twice = %v", err)
	}
}

func TestUpdateChecksErrorWorkflow(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t, validator{})
	wf, _ := svc.Create(ctx, sample("main"))
	handler, _ := svc.Create(ctx, sample("handler"))
	with := func(target domain.WorkflowID) domain.Workflow {
		w := sample("main")
		w.Settings.ErrorWorkflowID = target
		return w
	}

	var ve *domain.ValidationError
	if _, err := svc.Update(ctx, wf.ID, with("missing"), 0); !errors.As(err, &ve) || ve.Issues[0].Path != "settings.errorWorkflowId" {
		t.Fatalf("unknown target = %v, want a validation error at settings.errorWorkflowId", err)
	}
	for name, target := range map[string]domain.WorkflowID{"existing": handler.ID, "itself": wf.ID, "none": ""} {
		if _, err := svc.Update(ctx, wf.ID, with(target), 0); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}

	// A target deleted after it was chosen does not block later saves.
	if _, err := svc.Update(ctx, wf.ID, with(handler.ID), 0); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(ctx, handler.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Update(ctx, wf.ID, with(handler.ID), 0); err != nil {
		t.Fatalf("unchanged dangling target = %v", err)
	}
}

func TestNewRequiresDependencies(t *testing.T) {
	if _, err := workflow.New(workflow.Deps{}); err == nil {
		t.Fatal("expected an error")
	}
}

func TestNoteLimitsAreEnforcedOnSave(t *testing.T) {
	ctx := context.Background()
	clk := servicetest.NewClock()
	st := servicetest.NewStore(clk)
	svc, err := workflow.New(workflow.Deps{
		Workflows: st, Versions: st, Validator: validator{}, IDs: servicetest.NewIDs(clk),
		NoteLimits: domain.NoteLimits{MaxNotes: 1, MaxTextBytes: 4},
	})
	if err != nil {
		t.Fatal(err)
	}
	note := func(id, text string) domain.Note {
		return domain.Note{ID: domain.NoteID(id), Text: text, Size: domain.Size{Width: 240, Height: 160}}
	}
	ok := sample("ok")
	ok.Notes = []domain.Note{note("a", "1234")}
	wf, err := svc.Create(ctx, ok)
	if err != nil || len(wf.Notes) != 1 {
		t.Fatalf("Create within limits = %+v, %v", wf.Notes, err)
	}
	tooMany := sample("many")
	tooMany.Notes = []domain.Note{note("a", ""), note("b", "")}
	tooLong := sample("long")
	tooLong.Notes = []domain.Note{note("a", "12345")}
	for name, in := range map[string]domain.Workflow{"too many": tooMany, "too long": tooLong} {
		var ve *domain.ValidationError
		if _, err := svc.Update(ctx, wf.ID, in, 0); !errors.As(err, &ve) {
			t.Errorf("%s: Update = %v, want a *ValidationError", name, err)
		}
		if _, err := svc.Create(ctx, in); !errors.As(err, &ve) {
			t.Errorf("%s: Create = %v, want a *ValidationError", name, err)
		}
	}
}
