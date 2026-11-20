package jsoncodec_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/adapter/jsoncodec"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/service/engine"
)

const sampleWorkflow = `{
  "id": "wf_01J00000000000000000000000",
  "name": "Order sync",
  "active": true,
  "version": 7,
  "settings": {"timeoutSeconds": 300, "saveSuccessData": true},
  "nodes": [
    {"name": "A", "type": "t.a", "typeVersion": 1, "position": [10, 20],
     "parameters": {"url": "={{ $vars.base }}"}, "credentials": {"auth": "cr_01J00000000000000000000000"},
     "settings": {"retry": {"maxAttempts": 3, "backoff": "exponential", "initialDelayMs": 1000},
                  "onError": "continue", "alwaysOutputData": true, "executeOnce": true, "timeoutMs": 500}},
    {"name": "B", "type": "t.b", "position": [30, 40], "disabled": true}
  ],
  "connections": [{"from": {"node": "A", "index": 1}, "to": {"node": "B"}}],
  "pinData": {"A": [{"json": {"id": 1}}]},
  "tags": ["sales"]
}`

func TestWorkflowDecode(t *testing.T) {
	w, err := jsoncodec.DecodeWorkflow([]byte(sampleWorkflow))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Validate(); err != nil {
		t.Fatalf("decoded workflow should validate: %v", err)
	}
	a := w.Nodes[0]
	if a.Position != (domain.Position{X: 10, Y: 20}) || a.Settings.Timeout != 500*time.Millisecond ||
		a.Settings.Retry.InitialDelay != time.Second || a.Settings.OnError != domain.OnErrorContinue ||
		a.Credentials["auth"] != "cr_01J00000000000000000000000" {
		t.Fatalf("node A decoded wrong: %+v", a)
	}
	if w.Settings.Timeout != 300*time.Second || w.Connections[0].From.Index != 1 || w.Connections[0].To.Port != domain.PortMain {
		t.Fatalf("workflow decoded wrong: %+v", w)
	}
	if !w.Nodes[1].Disabled || w.PinData["A"][0].JSON["id"] != float64(1) {
		t.Fatal("disabled/pinData decoded wrong")
	}
}

func TestWorkflowRoundTrip(t *testing.T) {
	w, err := jsoncodec.DecodeWorkflow([]byte(sampleWorkflow))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := jsoncodec.EncodeWorkflow(w)
	if err != nil {
		t.Fatal(err)
	}
	again, err := jsoncodec.DecodeWorkflow(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(w, again) {
		t.Fatalf("round trip changed the workflow:\n%+v\n%+v", w, again)
	}
}

func TestDecodeRejectsBadDocuments(t *testing.T) {
	for name, doc := range map[string]string{
		"unknown field": `{"name":"x","nodes":[],"connections":[],"bogus":1}`,
		"trailing data": `{"name":"x","nodes":[],"connections":[]} {}`,
		"not json":      `nope`,
		"wrong type":    `{"name": 5}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := jsoncodec.DecodeWorkflow([]byte(doc)); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestDecodeItems(t *testing.T) {
	items, err := jsoncodec.DecodeItems([]byte(`[{"json":{"a":1},"binary":{"f":{"ref":"blob://1","mime":"x/y","size":3}},
		"pairedItem":[{"input":0,"item":2}],"error":{"message":"bad","httpStatus":500}},{}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Binary["f"].Size != 3 || items[0].PairedItem[0].Item != 2 || items[0].Error.HTTPStatus != 500 {
		t.Fatalf("items = %+v", items)
	}
	if items[1].JSON == nil {
		t.Fatal("missing json must decode to an empty object")
	}
	if _, err := jsoncodec.DecodeItems([]byte(`{}`)); err == nil {
		t.Fatal("non-array must fail")
	}
}

func TestStateRoundTrip(t *testing.T) {
	idx := 2
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	item := domain.Item{
		JSON:       map[string]any{"n": float64(1), "nested": map[string]any{"k": "v"}},
		Binary:     map[string]domain.BinaryRef{"f": {Ref: "blob://1", MIME: "a/b", Name: "n", Size: 9}},
		PairedItem: []domain.ItemRef{{Input: 0, Item: 1}},
		Error:      &domain.ErrorInfo{Kind: domain.ErrorKindNode, Node: "X", Message: "m", ItemIndex: &idx},
	}
	want := &engine.State{
		ExecutionID: "ex_1", Status: domain.StatusWaiting, StartedAt: now,
		Queue:   []engine.Task{{Node: "Q", Inputs: [][]domain.Item{{item}, {}}, Sources: []domain.Source{{Node: "S", OutputIndex: 1, RunIndex: 2}, {}}}},
		Waiting: map[string]engine.WaitingInputs{"M": {Inputs: [][]domain.Item{{item}, {}}, Sources: []domain.Source{{Node: "S"}, {}}, Filled: []bool{true, false}}},
		RunData: map[string][]domain.NodeRun{"A": {{
			RunIndex: 0, StartedAt: now, Duration: 1500 * time.Millisecond, Status: domain.NodeRunWaiting, Attempts: 2,
			Sources: []domain.Source{{Node: "S"}},
			Input:   domain.PortData{domain.PortMain: {{item}}}, Output: domain.PortData{domain.PortMain: {{item}, {}}},
			Error: &domain.ErrorInfo{Kind: domain.ErrorKindWorkflow, Message: "w"},
		}}},
		Order:   []engine.RunRef{{Node: "A", RunIndex: 0}},
		Pending: &engine.PendingWait{Node: "A", RunIndex: 0, Until: now, ResumeToken: "tok", Output: [][]domain.Item{{item}}},
	}
	raw, err := jsoncodec.EncodeState(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := jsoncodec.DecodeState(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("state changed in round trip\nwant %+v\n got %+v\njson %s", want, got, raw)
	}
	if _, err := jsoncodec.DecodeState([]byte(strings.Repeat("x", 3))); err == nil {
		t.Fatal("garbage must fail")
	}
}

func TestStateResumeSecret(t *testing.T) {
	tests := []struct {
		name   string
		secret string
		json   string // extra raw JSON fragment for the state document, if any
	}{
		{"set secret round-trips", "s3cret", ""},
		{"empty secret stays empty", "", ""},
		{"state saved before the field decodes with an empty secret", "", `{"executionId":"ex_1","status":"waiting","runData":{}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := []byte(tt.json)
			if tt.json == "" {
				var err error
				raw, err = jsoncodec.EncodeState(&engine.State{ExecutionID: "ex_1", Status: domain.StatusWaiting, ResumeSecret: tt.secret})
				if err != nil {
					t.Fatal(err)
				}
			}
			got, err := jsoncodec.DecodeState(raw)
			if err != nil {
				t.Fatal(err)
			}
			if got.ResumeSecret != tt.secret {
				t.Fatalf("secret = %q, want %q (json %s)", got.ResumeSecret, tt.secret, raw)
			}
		})
	}
}

func TestSavedPayloadsAreTold(t *testing.T) {
	var c jsoncodec.Codec
	raw, err := c.EncodeInput(domain.ExecutionInput{StartNode: "Start", Items: []domain.Item{{JSON: map[string]any{"a": 1.0}}}})
	if err != nil {
		t.Fatal(err)
	}
	in, st, err := c.DecodeSaved(raw)
	if err != nil || st != nil || in == nil || in.StartNode != "Start" || in.Items[0].JSON["a"] != 1.0 {
		t.Fatalf("input = %+v state = %+v err = %v", in, st, err)
	}
	raw, err = c.EncodeState(&engine.State{ExecutionID: "ex_1", Status: domain.StatusWaiting, RunData: map[string][]domain.NodeRun{}})
	if err != nil {
		t.Fatal(err)
	}
	in, st, err = c.DecodeSaved(raw)
	if err != nil || in != nil || st == nil || st.ExecutionID != "ex_1" {
		t.Fatalf("input = %+v state = %+v err = %v", in, st, err)
	}
	if _, _, err := c.DecodeSaved([]byte("nope")); err == nil {
		t.Fatal("garbage must fail")
	}
}

func TestNotesRoundTrip(t *testing.T) {
	doc := `{"name":"n","nodes":[],"connections":[],"notes":[
	  {"id":"nt_1","text":"**hello**","position":[10.5,-20],"size":{"width":300,"height":120},"color":"green"},
	  {"id":"nt_2","position":[0,0],"size":{"width":240,"height":160}}]}`
	w, err := jsoncodec.DecodeWorkflow([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	want := domain.Note{
		ID: "nt_1", Text: "**hello**", Position: domain.Position{X: 10.5, Y: -20},
		Size: domain.Size{Width: 300, Height: 120}, Color: domain.NoteColorGreen,
	}
	if len(w.Notes) != 2 || w.Notes[0] != want || w.Notes[1].Color != "" {
		t.Fatalf("notes decoded wrong: %+v", w.Notes)
	}
	raw, err := jsoncodec.EncodeWorkflow(w)
	if err != nil {
		t.Fatal(err)
	}
	again, err := jsoncodec.DecodeWorkflow(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(w, again) {
		t.Fatalf("round trip changed the notes:\n%+v\n%+v", w.Notes, again.Notes)
	}
	plain, err := jsoncodec.EncodeWorkflow(domain.Workflow{Name: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), "notes") {
		t.Fatalf("a workflow without notes must not emit the key: %s", plain)
	}
}
