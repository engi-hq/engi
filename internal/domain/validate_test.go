package domain_test

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/engi-hq/engi/internal/domain"
)

func node(name string) domain.Node {
	return domain.Node{Name: name, Type: "test.node", TypeVersion: 1}
}

func conn(from, to string) domain.Connection {
	return domain.Connection{
		From: domain.Endpoint{Node: from, Port: domain.PortMain},
		To:   domain.Endpoint{Node: to, Port: domain.PortMain},
	}
}

func validWorkflow() domain.Workflow {
	return domain.Workflow{
		Name:        "wf",
		Nodes:       []domain.Node{node("A"), node("B")},
		Connections: []domain.Connection{conn("A", "B")},
	}
}

func TestWorkflowValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*domain.Workflow)
		wantErr []string // substrings, all must appear; empty means valid
	}{
		{"valid", func(*domain.Workflow) {}, nil},
		{"self loop is allowed", func(w *domain.Workflow) { w.Connections = append(w.Connections, conn("B", "B")) }, nil},
		{"empty name", func(w *domain.Workflow) { w.Name = " " }, []string{"name: must not be empty"}},
		{"bad workflow id", func(w *domain.Workflow) { w.ID = "x_123" }, []string{"id:"}},
		{"duplicate node names", func(w *domain.Workflow) { w.Nodes = append(w.Nodes, node("A")) }, []string{`duplicate node name "A"`}},
		{"empty node name", func(w *domain.Workflow) { w.Nodes[0].Name = "" }, []string{"nodes[0].name"}},
		{"empty node type", func(w *domain.Workflow) { w.Nodes[0].Type = "" }, []string{"nodes[0].type"}},
		{"duplicate node ids", func(w *domain.Workflow) {
			w.Nodes[0].ID = "nd_01J00000000000000000000000"
			w.Nodes[1].ID = "nd_01J00000000000000000000000"
		}, []string{"duplicate node id"}},
		{"bad node id", func(w *domain.Workflow) { w.Nodes[0].ID = "wf_01J00000000000000000000000" }, []string{"nodes[0].id"}},
		{"unknown source node", func(w *domain.Workflow) { w.Connections[0].From.Node = "Z" }, []string{`unknown node "Z"`}},
		{"unknown target node", func(w *domain.Workflow) { w.Connections[0].To.Node = "Z" }, []string{`connections[0].to.node`}},
		{"empty port", func(w *domain.Workflow) { w.Connections[0].From.Port = "" }, []string{"from.port"}},
		{"negative index", func(w *domain.Workflow) { w.Connections[0].To.Index = -1 }, []string{"to.index"}},
		{"port kind mismatch", func(w *domain.Workflow) { w.Connections[0].To.Port = "ai.tool" }, []string{"port kind mismatch"}},
		{"duplicate connection", func(w *domain.Workflow) { w.Connections = append(w.Connections, conn("A", "B")) }, []string{"duplicate connection"}},
		{"pin data unknown node", func(w *domain.Workflow) { w.PinData = map[string][]domain.Item{"Z": nil} }, []string{`unknown node "Z"`}},
		{"negative retries", func(w *domain.Workflow) { w.Nodes[0].Settings.Retry.MaxAttempts = -1 }, []string{"maxAttempts"}},
		{"bad backoff", func(w *domain.Workflow) { w.Nodes[0].Settings.Retry.Backoff = "wild" }, []string{"backoff"}},
		{"bad onError", func(w *domain.Workflow) { w.Nodes[0].Settings.OnError = "explode" }, []string{"onError"}},
		{"negative timeout", func(w *domain.Workflow) { w.Nodes[0].Settings.Timeout = -1 }, []string{"settings.timeout"}},
		{"valid note", func(w *domain.Workflow) { w.Notes = []domain.Note{validNote("n1")} }, nil},
		{"note without colour is valid", func(w *domain.Workflow) {
			n := validNote("n1")
			n.Color = ""
			w.Notes = []domain.Note{n}
		}, nil},
		{"note empty id", func(w *domain.Workflow) { w.Notes = []domain.Note{validNote("")} }, []string{"notes[0].id"}},
		{"note bad id", func(w *domain.Workflow) { w.Notes = []domain.Note{validNote("a b")} }, []string{"notes[0].id"}},
		{"duplicate note ids", func(w *domain.Workflow) { w.Notes = []domain.Note{validNote("n1"), validNote("n1")} }, []string{"duplicate note id"}},
		{"note unknown colour", func(w *domain.Workflow) {
			n := validNote("n1")
			n.Color = "chartreuse"
			w.Notes = []domain.Note{n}
		}, []string{"notes[0].color"}},
		{"note zero size", func(w *domain.Workflow) {
			n := validNote("n1")
			n.Size = domain.Size{}
			w.Notes = []domain.Note{n}
		}, []string{"notes[0].size"}},
		{"note infinite position", func(w *domain.Workflow) {
			n := validNote("n1")
			n.Position.X = math.Inf(1)
			w.Notes = []domain.Note{n}
		}, []string{"notes[0].position"}},
		{"reports all problems", func(w *domain.Workflow) {
			w.Name = ""
			w.Connections[0].From.Node = "Z"
		}, []string{"name:", "unknown node"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := validWorkflow()
			tt.mutate(&w)
			err := w.Validate()
			if len(tt.wantErr) == 0 {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			var ve *domain.ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("want *ValidationError, got %T (%v)", err, err)
			}
			for _, want := range tt.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}

func validNote(id string) domain.Note {
	return domain.Note{ID: domain.NoteID(id), Text: "# hi", Size: domain.Size{Width: 240, Height: 160}, Color: domain.NoteColorBlue}
}

func TestValidateNoteLimits(t *testing.T) {
	w := validWorkflow()
	w.Notes = []domain.Note{validNote("a"), validNote("b")}
	w.Notes[1].Text = "12345"
	if err := w.ValidateNoteLimits(domain.NoteLimits{}); err != nil {
		t.Fatalf("zero limits mean no limit: %v", err)
	}
	if err := w.ValidateNoteLimits(domain.NoteLimits{MaxNotes: 2, MaxTextBytes: 5}); err != nil {
		t.Fatalf("at the limit is allowed: %v", err)
	}
	err := w.ValidateNoteLimits(domain.NoteLimits{MaxNotes: 1, MaxTextBytes: 4})
	var ve *domain.ValidationError
	if !errors.As(err, &ve) || len(ve.Issues) != 2 {
		t.Fatalf("want two issues (count, text size), got %v", err)
	}
}

func TestNodesInCanvasOrder(t *testing.T) {
	w := domain.Workflow{Nodes: []domain.Node{
		{Name: "C", Position: domain.Position{X: 0, Y: 200}},
		{Name: "B", Position: domain.Position{X: 300, Y: 100}},
		{Name: "A", Position: domain.Position{X: 100, Y: 100}},
		{Name: "D", Position: domain.Position{X: 100, Y: 100}},
	}}
	got := strings.Join(w.NodesInCanvasOrder(), ",")
	if got != "A,D,B,C" {
		t.Fatalf("order = %s, want A,D,B,C", got)
	}
}

func TestIDValidate(t *testing.T) {
	const body = "01J00000000000000000000000"
	tests := []struct {
		name string
		err  error
		ok   bool
	}{
		{"workflow ok", domain.WorkflowID("wf_" + body).Validate(), true},
		{"execution ok", domain.ExecutionID("ex_" + body).Validate(), true},
		{"credential ok", domain.CredentialID("cr_" + body).Validate(), true},
		{"wrong prefix", domain.WorkflowID("ex_" + body).Validate(), false},
		{"short body", domain.NodeID("nd_123").Validate(), false},
		{"empty", domain.NodeID("").Validate(), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if (tt.err == nil) != tt.ok {
				t.Fatalf("err = %v, ok want %v", tt.err, tt.ok)
			}
		})
	}
}

func TestErrors(t *testing.T) {
	cause := errors.New("boom")
	ne := &domain.NodeError{Node: "N", Err: cause}
	if !errors.Is(ne, cause) {
		t.Fatal("NodeError must unwrap")
	}
	if info := ne.Info(); info.Message != "boom" || info.Kind != domain.ErrorKindNode {
		t.Fatalf("info = %+v", info)
	}
	we := &domain.WorkflowError{Node: "N", Message: "bad", Err: cause}
	if !errors.Is(we, cause) || !strings.Contains(we.Error(), `"N"`) {
		t.Fatalf("WorkflowError = %v", we)
	}
	if !errors.Is(errors.Join(domain.ErrNotFound), domain.ErrNotFound) {
		t.Fatal("ErrNotFound must be matchable")
	}
}

func TestEnums(t *testing.T) {
	if !domain.ModeManual.Valid() || domain.ExecutionMode("x").Valid() {
		t.Fatal("mode validity")
	}
	if domain.StatusRunning.Terminal() || !domain.StatusError.Terminal() || domain.StatusWaiting.Terminal() {
		t.Fatal("terminal statuses")
	}
}
