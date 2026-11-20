package errorflow_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/service/errorflow"
	"github.com/engi-hq/engi/internal/service/execution"
	"github.com/engi-hq/engi/internal/service/servicetest"
)

const triggerType = "test.trigger.error"

type starter struct {
	reqs []execution.StartRequest
	err  error
}

func (s *starter) Start(_ context.Context, r execution.StartRequest) (domain.ExecutionID, error) {
	s.reqs = append(s.reqs, r)
	return "ex_new", s.err
}

func wf(id, errorWF domain.WorkflowID, nodeTypes ...string) domain.Workflow {
	w := domain.Workflow{ID: id, Name: "name-" + string(id), Settings: domain.WorkflowSettings{ErrorWorkflowID: errorWF}}
	for _, t := range nodeTypes {
		w.Nodes = append(w.Nodes, domain.Node{Name: "N-" + t, Type: t})
	}
	return w
}

func TestExecutionFailed(t *testing.T) {
	failure := &domain.ErrorInfo{Kind: domain.ErrorKindNode, Node: "Boom", Message: "nope", Description: "why"}
	handler := wf("handler", "", triggerType)
	disabledHandler := wf("disabled", "", triggerType)
	disabledHandler.Nodes[0].Disabled = true
	noTrigger := wf("plain", "", "test.noop")
	errorParent := domain.ExecutionID("ex_errparent")
	userParent := domain.ExecutionID("ex_userparent")

	tests := []struct {
		name      string
		target    domain.WorkflowID // error workflow of the failed one
		ex        domain.Execution
		wantStart bool
		wantLog   string
	}{
		{name: "error in trigger mode", target: "handler", ex: domain.Execution{Mode: domain.ModeTrigger, Status: domain.StatusError}, wantStart: true},
		{name: "crashed", target: "handler", ex: domain.Execution{Mode: domain.ModeWebhook, Status: domain.StatusCrashed}, wantStart: true},
		{name: "retry mode", target: "handler", ex: domain.Execution{Mode: domain.ModeRetry, Status: domain.StatusError}, wantStart: true},
		{name: "subworkflow mode", target: "handler", ex: domain.Execution{Mode: domain.ModeSubworkflow, Status: domain.StatusError}, wantStart: true},
		{name: "success", target: "handler", ex: domain.Execution{Mode: domain.ModeTrigger, Status: domain.StatusSuccess}},
		{name: "canceled", target: "handler", ex: domain.Execution{Mode: domain.ModeTrigger, Status: domain.StatusCanceled}},
		{name: "manual mode", target: "handler", ex: domain.Execution{Mode: domain.ModeManual, Status: domain.StatusError}},
		{name: "error mode never chains", target: "handler", ex: domain.Execution{Mode: domain.ModeError, Status: domain.StatusError}},
		{name: "no error workflow set", target: "", ex: domain.Execution{Mode: domain.ModeTrigger, Status: domain.StatusError}},
		{name: "own error workflow", target: "failing", ex: domain.Execution{Mode: domain.ModeTrigger, Status: domain.StatusError}, wantStart: true},
		{
			name: "child of an error execution", target: "handler", wantLog: "started by an error workflow",
			ex: domain.Execution{Mode: domain.ModeSubworkflow, Status: domain.StatusError, ParentExecutionID: &errorParent},
		},
		{
			name: "child of an ordinary execution", target: "handler", wantStart: true,
			ex: domain.Execution{Mode: domain.ModeSubworkflow, Status: domain.StatusError, ParentExecutionID: &userParent},
		},
		{name: "error workflow missing", target: "gone", ex: domain.Execution{Mode: domain.ModeTrigger, Status: domain.StatusError}, wantLog: "cannot be loaded"},
		{name: "error workflow without trigger", target: "plain", ex: domain.Execution{Mode: domain.ModeTrigger, Status: domain.StatusError}, wantLog: "no enabled Error Trigger"},
		{name: "error workflow with disabled trigger", target: "disabled", ex: domain.Execution{Mode: domain.ModeTrigger, Status: domain.StatusError}, wantLog: "no enabled Error Trigger"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := servicetest.NewStore(servicetest.NewClock())
			failing := wf("failing", tc.target, triggerType)
			for _, w := range []domain.Workflow{failing, handler, disabledHandler, noTrigger} {
				if _, err := store.Save(ctx, w); err != nil {
					t.Fatal(err)
				}
			}
			execs := store.Executions()
			for _, e := range []domain.Execution{
				{ID: "ex_errparent", WorkflowID: "handler", Mode: domain.ModeError, Status: domain.StatusRunning},
				{ID: "ex_userparent", WorkflowID: "failing", Mode: domain.ModeTrigger, Status: domain.StatusRunning},
			} {
				if err := execs.Create(ctx, e); err != nil {
					t.Fatal(err)
				}
			}
			var logs bytes.Buffer
			st := &starter{}
			n, err := errorflow.New(errorflow.Deps{
				Workflows: store, Versions: store, Executions: execs, Starter: st, TriggerType: triggerType,
				Logger: slog.New(slog.NewTextHandler(&logs, nil)),
			})
			if err != nil {
				t.Fatal(err)
			}
			tc.ex.ID, tc.ex.WorkflowID, tc.ex.WorkflowVersion, tc.ex.Error = "ex_failed", "failing", 1, failure

			n.ExecutionFailed(ctx, tc.ex)

			if got := len(st.reqs) == 1; got != tc.wantStart || len(st.reqs) > 1 {
				t.Fatalf("starts = %d, want start=%v (logs: %s)", len(st.reqs), tc.wantStart, logs.String())
			}
			if tc.wantLog != "" && !strings.Contains(logs.String(), tc.wantLog) {
				t.Fatalf("log %q lacks %q", logs.String(), tc.wantLog)
			}
			if !tc.wantStart {
				return
			}
			r := st.reqs[0]
			target := tc.target
			if r.WorkflowID != target || r.Mode != domain.ModeError || r.StartNode != "N-"+triggerType || r.DedupKey != errorflow.DedupPrefix+"ex_failed" {
				t.Fatalf("request = %+v", r)
			}
			got := r.Items[0].JSON
			e := got["execution"].(map[string]any)
			info := e["error"].(map[string]any)
			w := got["workflow"].(map[string]any)
			if e["id"] != "ex_failed" || e["mode"] != string(tc.ex.Mode) || e["status"] != string(tc.ex.Status) || e["lastNodeExecuted"] != "Boom" ||
				info["message"] != "nope" || info["description"] != "why" || info["node"] != "Boom" || info["kind"] != string(domain.ErrorKindNode) ||
				w["id"] != "failing" || w["name"] != "name-failing" {
				t.Fatalf("item = %v", got)
			}
		})
	}
}

func TestExecutionFailedStartProblemsAreLoggedNotReturned(t *testing.T) {
	for name, err := range map[string]error{
		"duplicate (hook ran twice)": &domain.DuplicateError{ExistingID: "ex_old"},
		"queue failure":              errors.New("queue down"),
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			store := servicetest.NewStore(servicetest.NewClock())
			for _, w := range []domain.Workflow{wf("failing", "handler", triggerType), wf("handler", "", triggerType)} {
				if _, e := store.Save(ctx, w); e != nil {
					t.Fatal(e)
				}
			}
			var logs bytes.Buffer
			n, e := errorflow.New(errorflow.Deps{
				Workflows: store, Versions: store, Executions: store.Executions(), Starter: &starter{err: err}, TriggerType: triggerType,
				Logger: slog.New(slog.NewTextHandler(&logs, nil)),
			})
			if e != nil {
				t.Fatal(e)
			}
			n.ExecutionFailed(ctx, domain.Execution{ID: "ex_f", WorkflowID: "failing", WorkflowVersion: 1, Mode: domain.ModeTrigger, Status: domain.StatusError})
			if strings.Contains(name, "queue") && !strings.Contains(logs.String(), "starting the error workflow failed") {
				t.Fatalf("queue failure not logged: %s", logs.String())
			}
			if strings.Contains(name, "duplicate") && strings.Contains(logs.String(), "level=ERROR") {
				t.Fatalf("duplicate must not be an error: %s", logs.String())
			}
		})
	}
}

func TestNewRequiresDependencies(t *testing.T) {
	if _, err := errorflow.New(errorflow.Deps{}); err == nil {
		t.Fatal("want an error")
	}
}
