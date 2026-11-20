package app

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/adapter/jsoncodec"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
)

func newApp(t *testing.T) *App {
	t.Helper()
	var logs bytes.Buffer
	a, err := New(t.Context(), config.Default(), Options{LogOutput: &logs, LookupEnv: func(string) (string, bool) { return "", false }})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func workflow(t *testing.T, doc string) domain.Workflow {
	t.Helper()
	wf, err := jsoncodec.DecodeWorkflow([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	return wf
}

func TestRegistryHasEveryBuiltin(t *testing.T) {
	reg, err := NewRegistry(config.Default().Nodes, fixed{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"engi.ai.agent", "engi.ai.chat", "engi.ai.embeddings", "engi.ai.memory.window", "engi.ai.model", "engi.ai.textSplitter", "engi.ai.tool.code", "engi.ai.tool.http", "engi.ai.tool.mcp", "engi.ai.tool.vectorSearch", "engi.ai.tool.workflow", "engi.ai.vectorStore", "engi.airtable", "engi.chat.slack", "engi.chat.teams", "engi.chat.telegram", "engi.code.javascript", "engi.code.python", "engi.db.mongodb", "engi.db.mysql", "engi.db.postgres", "engi.db.redis", "engi.email.send", "engi.flow.executeWorkflow", "engi.flow.if", "engi.flow.merge", "engi.flow.noop", "engi.flow.respondToWebhook", "engi.flow.switch", "engi.flow.wait",
		"engi.google.sheets", "engi.http.request", "engi.kafka.produce", "engi.storage.s3", "engi.transform.aggregate", "engi.transform.filter", "engi.transform.limit",
		"engi.transform.set", "engi.transform.sort", "engi.transform.splitOut", "engi.trigger.chat", "engi.trigger.error", "engi.trigger.executeWorkflow", "engi.trigger.imap", "engi.trigger.kafka", "engi.trigger.manual", "engi.trigger.mcpServer",
		"engi.trigger.schedule", "engi.trigger.webhook",
	}
	var got []string
	for _, d := range reg.Descriptors() {
		got = append(got, d.Type)
		if err := d.Validate(); err != nil {
			t.Errorf("%s: %v", d.Type, err)
		}
		if d.DisplayName == "" || d.Description == "" || d.Group == "" || d.Icon == "" {
			t.Errorf("%s: descriptor is missing display metadata", d.Type)
		}
		for _, o := range d.Outputs {
			if !o.EffectiveKind().Valid() {
				t.Errorf("%s: output %q has kind %q", d.Type, o.Name, o.Kind)
			}
		}
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("registered %v\nwant %v", got, want)
	}
}

type fixed struct{}

func (fixed) Now() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

func TestNewRejectsInvalidConfig(t *testing.T) {
	cfg := config.Default()
	cfg.Nodes.Code.Timeout = 0
	if _, err := New(t.Context(), cfg, Options{}); err == nil {
		t.Fatal("expected a validation error")
	}
}

func TestNewLogger(t *testing.T) {
	var buf bytes.Buffer
	l, err := NewLogger(config.LogConfig{Level: "debug", Format: "json"}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	l.Debug("hello")
	if !strings.Contains(buf.String(), `"msg":"hello"`) {
		t.Fatalf("log = %q", buf.String())
	}
	if _, err := NewLogger(config.LogConfig{Level: "loud", Format: "json"}, &buf); err == nil {
		t.Fatal("expected a level error")
	}
	if _, err := NewLogger(config.LogConfig{Level: "info", Format: "xml"}, &buf); err == nil {
		t.Fatal("expected a format error")
	}
}

const pipelineDoc = `{
  "name": "pipeline",
  "nodes": [
    {"name": "Start", "type": "engi.trigger.manual", "position": [0, 0]},
    {"name": "Set", "type": "engi.transform.set", "position": [200, 0], "parameters": {
      "fields": [{"name": "double", "type": "number", "value": "={{ $json.n * 2 }}"},
                 {"name": "label", "type": "string", "value": "=n={{ $json.n }}"}]}},
    {"name": "Big?", "type": "engi.flow.if", "position": [400, 0], "parameters": {
      "conditions": [{"left": "={{ $json.double }}", "operator": "number:gt", "right": 4}]}},
    {"name": "Big", "type": "engi.transform.set", "position": [600, -100], "parameters": {"fields": [{"name": "size", "value": "big"}]}},
    {"name": "Small", "type": "engi.transform.set", "position": [600, 100], "parameters": {"fields": [{"name": "size", "value": "small"}]}}
  ],
  "connections": [
    {"from": {"node": "Start"}, "to": {"node": "Set"}},
    {"from": {"node": "Set"}, "to": {"node": "Big?"}},
    {"from": {"node": "Big?", "index": 0}, "to": {"node": "Big"}},
    {"from": {"node": "Big?", "index": 1}, "to": {"node": "Small"}}
  ]
}`

func TestRunEvaluatesExpressionsThroughTheEngine(t *testing.T) {
	a := newApp(t)
	wf := workflow(t, pipelineDoc)
	if err := a.Validate(wf); err != nil {
		t.Fatal(err)
	}
	state, err := a.Run(context.Background(), RunRequest{Workflow: wf, Items: []domain.Item{
		{JSON: map[string]any{"n": 1.0}}, {JSON: map[string]any{"n": 5.0}}, {JSON: map[string]any{"n": 2.0}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != domain.StatusSuccess {
		t.Fatalf("status = %s, error = %+v", state.Status, state.Error)
	}
	big := state.RunData["Big"][0].Output[domain.PortMain][0]
	small := state.RunData["Small"][0].Output[domain.PortMain][0]
	if len(big) != 1 || big[0].JSON["label"] != "n=5" || big[0].JSON["double"] != 10.0 || big[0].JSON["size"] != "big" {
		t.Fatalf("big = %+v", big)
	}
	if len(small) != 2 || small[0].JSON["size"] != "small" {
		t.Fatalf("small = %+v", small)
	}
}

func TestRunReportsNodeFailure(t *testing.T) {
	a := newApp(t)
	wf := workflow(t, `{"name":"fail","nodes":[
	  {"name":"Start","type":"engi.trigger.manual","position":[0,0]},
	  {"name":"Code","type":"engi.code.javascript","position":[1,0],"parameters":{"jsCode":"throw new Error('nope')"}}],
	  "connections":[{"from":{"node":"Start"},"to":{"node":"Code"}}]}`)
	state, err := a.Run(context.Background(), RunRequest{Workflow: wf})
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != domain.StatusError || state.Error == nil || !strings.Contains(state.Error.Message, "nope") {
		t.Fatalf("status=%s error=%+v", state.Status, state.Error)
	}
	rep := BuildReport(state)
	if rep.Status != "error" || rep.Error == nil || rep.Error.Node != "Code" {
		t.Fatalf("report = %+v", rep)
	}
}

func TestRunResumesTimeBasedWaits(t *testing.T) {
	a := newApp(t)
	wf := workflow(t, `{"name":"wait","nodes":[
	  {"name":"Start","type":"engi.trigger.manual","position":[0,0]},
	  {"name":"Pause","type":"engi.flow.wait","position":[1,0],"parameters":{"amount":0.05,"unit":"seconds"}},
	  {"name":"After","type":"engi.flow.noop","position":[2,0]}],
	  "connections":[{"from":{"node":"Start"},"to":{"node":"Pause"}},{"from":{"node":"Pause"},"to":{"node":"After"}}]}`)
	start := time.Now()
	state, err := a.Run(context.Background(), RunRequest{Workflow: wf, Items: []domain.Item{{JSON: map[string]any{"k": "v"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != domain.StatusSuccess || time.Since(start) < 40*time.Millisecond {
		t.Fatalf("status=%s after %v", state.Status, time.Since(start))
	}
	if got := state.RunData["After"][0].Output[domain.PortMain][0][0].JSON["k"]; got != "v" {
		t.Fatalf("items must survive the wait, got %v", got)
	}
}

func TestRunStopsAtWebhookWait(t *testing.T) {
	a := newApp(t)
	wf := workflow(t, `{"name":"hook","nodes":[
	  {"name":"Start","type":"engi.trigger.manual","position":[0,0]},
	  {"name":"Pause","type":"engi.flow.wait","position":[1,0],"parameters":{"resume":"webhook"}}],
	  "connections":[{"from":{"node":"Start"},"to":{"node":"Pause"}}]}`)
	state, err := a.Run(context.Background(), RunRequest{Workflow: wf})
	if err != nil {
		t.Fatal(err)
	}
	rep := BuildReport(state)
	if state.Status != domain.StatusWaiting || rep.Pending == nil || rep.Pending.ResumeToken == "" || rep.Pending.Until != nil {
		t.Fatalf("status=%s pending=%+v", state.Status, rep.Pending)
	}
}

func TestRunWaitIsInterruptible(t *testing.T) {
	a := newApp(t)
	wf := workflow(t, `{"name":"long","nodes":[
	  {"name":"Start","type":"engi.trigger.manual","position":[0,0]},
	  {"name":"Pause","type":"engi.flow.wait","position":[1,0],"parameters":{"amount":1,"unit":"hours"}}],
	  "connections":[{"from":{"node":"Start"},"to":{"node":"Pause"}}]}`)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := a.Run(ctx, RunRequest{Workflow: wf}); err == nil {
		t.Fatal("expected the cancelled wait to return an error")
	}
}

func TestFindStartNode(t *testing.T) {
	a := newApp(t)
	node := func(name, typ string, x int) string {
		return `{"name":"` + name + `","type":"` + typ + `","position":[` + string(rune('0'+x)) + `,0]}`
	}
	tests := []struct {
		name    string
		nodes   []string
		conns   string
		want    string
		wantErr string
	}{
		{"single trigger", []string{node("A", "engi.flow.noop", 1), node("T", "engi.trigger.manual", 0)}, `[{"from":{"node":"T"},"to":{"node":"A"}}]`, "T", ""},
		{"two triggers", []string{node("T1", "engi.trigger.manual", 0), node("T2", "engi.trigger.manual", 1)}, `[]`, "", "choose one with --start"},
		{"no trigger, one root", []string{node("A", "engi.flow.noop", 0), node("B", "engi.flow.noop", 1)}, `[{"from":{"node":"A"},"to":{"node":"B"}}]`, "A", ""},
		{"no trigger, two roots", []string{node("A", "engi.flow.noop", 0), node("B", "engi.flow.noop", 1)}, `[]`, "", "cannot tell where to start"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wf := workflow(t, `{"name":"w","nodes":[`+strings.Join(tt.nodes, ",")+`],"connections":`+tt.conns+`}`)
			got, err := a.FindStartNode(wf)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
}

func TestValidateRejectsUnknownNodeType(t *testing.T) {
	a := newApp(t)
	wf := workflow(t, `{"name":"w","nodes":[{"name":"X","type":"engi.nope","position":[0,0]}],"connections":[]}`)
	if err := a.Validate(wf); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := a.Run(context.Background(), RunRequest{Workflow: wf, StartNode: "X"}); err == nil {
		t.Fatal("running an invalid workflow must fail")
	}
}

func TestReportEncodes(t *testing.T) {
	a := newApp(t)
	wf := workflow(t, pipelineDoc)
	state, err := a.Run(context.Background(), RunRequest{Workflow: wf, Items: []domain.Item{{JSON: map[string]any{"n": 5.0}}}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := BuildReport(state).Encode()
	if err != nil {
		t.Fatal(err)
	}
	var back Report
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.Status != "success" || back.Output == nil || back.Output.Node != "Big" || len(back.RunData["Big?"][0].Items) != 2 {
		t.Fatalf("report = %s", raw)
	}
}

func TestCodeNodeSeesTheWorkflowScope(t *testing.T) {
	cfg := config.Default()
	cfg.Expression.EnvAllowList = []string{"ENGI_TEST_REGION"}
	lookup := func(k string) (string, bool) {
		if k == "ENGI_TEST_REGION" || k == "SECRET" {
			return "eu-" + strings.ToLower(k[:1]), true
		}
		return "", false
	}
	var logs bytes.Buffer
	a, err := New(t.Context(), cfg, Options{LogOutput: &logs, LookupEnv: lookup})
	if err != nil {
		t.Fatal(err)
	}
	wf := workflow(t, `{
	  "name": "scope",
	  "nodes": [
	    {"name": "Start", "type": "engi.trigger.manual", "position": [0, 0]},
	    {"name": "Set", "type": "engi.transform.set", "position": [200, 0], "parameters": {"fields": [{"name": "tag", "value": "from-set"}]}},
	    {"name": "Code", "type": "engi.code.javascript", "position": [400, 0], "parameters": {"mode": "runOnceForEachItem", "jsCode":
	      "return { fromStart: $('Start').item.json.n, pairedNode: $node['Set'].json.tag, first: $('Set').first().json.tag, ran: $('Set').isExecuted, region: $env.ENGI_TEST_REGION, secret: $env.SECRET === undefined, vars: Object.keys($vars).length, sha: $fn.sha256('x').length };"}}
	  ],
	  "connections": [
	    {"from": {"node": "Start"}, "to": {"node": "Set"}},
	    {"from": {"node": "Set"}, "to": {"node": "Code"}}
	  ]
	}`)
	state, err := a.Run(context.Background(), RunRequest{Workflow: wf, Items: []domain.Item{{JSON: map[string]any{"n": 1.0}}, {JSON: map[string]any{"n": 2.0}}}})
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != domain.StatusSuccess {
		t.Fatalf("status = %s, error = %+v", state.Status, state.Error)
	}
	out := state.RunData["Code"][0].Output[domain.PortMain][0]
	for i, want := range []float64{1, 2} {
		got := out[i].JSON
		if got["fromStart"] != want || got["pairedNode"] != "from-set" || got["first"] != "from-set" || got["ran"] != true ||
			got["region"] != "eu-e" || got["secret"] != true || got["vars"] != 0.0 || got["sha"] != 64.0 {
			t.Fatalf("item %d = %v", i, got)
		}
	}
}
