package app

import (
	"context"
	"fmt"
	"testing"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/service/execution"
	"github.com/engi-hq/engi/internal/service/servicetest"
)

const failingDoc = `{
  "name": "Orders",
  "settings": {"errorWorkflowId": %q},
  "nodes": [
    {"name": "Start", "type": "engi.trigger.manual", "position": [0, 0]},
    {"name": "Boom", "type": "engi.code.javascript", "position": [200, 0], "parameters": {"jsCode": "throw new Error('nope')"}}
  ],
  "connections": [{"from": {"node": "Start"}, "to": {"node": "Boom"}}]
}`

const handlerDoc = `{
  "name": "Alert",
  "nodes": [
    {"name": "On error", "type": "engi.trigger.error", "position": [0, 0]},
    {"name": "Keep", "type": "engi.transform.set", "position": [200, 0], "parameters": {"fields": [{"name": "seen", "type": "boolean", "value": true}]}}
  ],
  "connections": [{"from": {"node": "On error"}, "to": {"node": "Keep"}}]
}`

func (f *runtimeFixture) startMode(t *testing.T, wf domain.Workflow, mode domain.ExecutionMode) domain.ExecutionID {
	t.Helper()
	id, err := f.rt.Executions.Start(context.Background(), execution.StartRequest{
		WorkflowID: wf.ID, Mode: mode, StartNode: "Start", Items: items(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *runtimeFixture) executionsOf(t *testing.T, wf domain.WorkflowID) []domain.Execution {
	t.Helper()
	list, err := f.rt.Executions.List(context.Background(), port.ExecutionFilter{WorkflowID: wf}, port.Page{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	return list.Items
}

func TestRuntimeFailureStartsTheErrorWorkflow(t *testing.T) {
	f := newRuntime(t)
	f.start(t)
	handler := f.save(t, handlerDoc)
	failing := f.save(t, fmt.Sprintf(failingDoc, string(handler.ID)))
	failed := f.startMode(t, failing, domain.ModeTrigger)
	f.status(t, failed, domain.StatusError)

	f.until(t, failed, "the error workflow execution", func(domain.Execution) bool {
		l := f.executionsOf(t, handler.ID)
		return len(l) == 1 && l[0].Status == domain.StatusSuccess
	})
	ex, err := f.rt.Executions.Get(context.Background(), f.executionsOf(t, handler.ID)[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if ex.Mode != domain.ModeError {
		t.Fatalf("mode = %s", ex.Mode)
	}
	out := outputOf(ex, "Keep")
	if len(out) != 1 || out[0]["seen"] != true {
		t.Fatalf("handler output = %v", out)
	}
	trig := outputOf(ex, "On error")
	if len(trig) != 1 {
		t.Fatalf("trigger output = %v", trig)
	}
	e, _ := trig[0]["execution"].(map[string]any)
	w, _ := trig[0]["workflow"].(map[string]any)
	errInfo, _ := e["error"].(map[string]any)
	if e["id"] != string(failed) || e["mode"] != "trigger" || e["lastNodeExecuted"] != "Boom" ||
		errInfo["node"] != "Boom" || w["id"] != string(failing.ID) || w["name"] != "Orders" {
		t.Fatalf("failure item = %v", trig[0])
	}
}

func TestRuntimeManualFailureDoesNotStartTheErrorWorkflow(t *testing.T) {
	f := newRuntime(t)
	f.start(t)
	handler := f.save(t, handlerDoc)
	failing := f.save(t, fmt.Sprintf(failingDoc, string(handler.ID)))
	f.status(t, f.startMode(t, failing, domain.ModeManual), domain.StatusError)
	// A trigger run afterwards proves the hook had time to act on the first.
	f.status(t, f.startMode(t, failing, domain.ModeTrigger), domain.StatusError)
	servicetest.Eventually(t, "the single error execution", func() bool { return len(f.executionsOf(t, handler.ID)) == 1 })
}

func TestRuntimeSelfErrorWorkflowDoesNotLoop(t *testing.T) {
	f := newRuntime(t)
	f.start(t)
	// A workflow that is its own error workflow and always fails.
	doc := `{"name": "Self", "settings": {"errorWorkflowId": %q}, "nodes": [
	  {"name": "Start", "type": "engi.trigger.manual", "position": [0, 0]},
	  {"name": "On error", "type": "engi.trigger.error", "position": [0, 100]},
	  {"name": "Boom", "type": "engi.code.javascript", "position": [200, 0], "parameters": {"jsCode": "throw new Error('nope')"}}],
	  "connections": [{"from": {"node": "Start"}, "to": {"node": "Boom"}}, {"from": {"node": "On error"}, "to": {"node": "Boom"}}]}`
	id, err := f.app.ids.WorkflowID()
	if err != nil {
		t.Fatal(err)
	}
	wf := workflow(t, fmt.Sprintf(doc, string(id)))
	wf.ID = id
	wf, err = f.rt.Workflows.Save(context.Background(), wf)
	if err != nil {
		t.Fatal(err)
	}
	f.status(t, f.startMode(t, wf, domain.ModeTrigger), domain.StatusError)
	servicetest.Eventually(t, "the error run to fail", func() bool {
		for _, e := range f.executionsOf(t, wf.ID) {
			if e.Mode == domain.ModeError && e.Status == domain.StatusError {
				return true
			}
		}
		return false
	})
	// Give a loop the chance to show itself: a second wave would add executions.
	f.status(t, f.startMode(t, wf, domain.ModeManual), domain.StatusError)
	if n := len(f.executionsOf(t, wf.ID)); n != 3 {
		t.Fatalf("executions = %d, want 3 (trigger, error, manual)", n)
	}
}
