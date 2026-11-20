package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

const calcDoc = `{
  "name": "calc",
  "nodes": [
    {"name": "In", "type": "engi.trigger.webhook", "position": [0, 0], "parameters": {"httpMethod": "POST", "path": "calc/:unit", "responseMode": "lastNode"}},
    {"name": "Calc", "type": "engi.transform.set", "position": [200, 0], "parameters": {"fields": [
      {"name": "double", "type": "number", "value": "={{ $json.body.n * 2 }}"},
      {"name": "unit", "type": "string", "value": "={{ $json.params.unit }}"}]}}
  ],
  "connections": [{"from": {"node": "In"}, "to": {"node": "Calc"}}]
}`

const respondDoc = `{
  "name": "respond",
  "nodes": [
    {"name": "In", "type": "engi.trigger.webhook", "position": [0, 0], "parameters": {"httpMethod": "GET", "path": "early", "responseMode": "responseNode"}},
    {"name": "Reply", "type": "engi.flow.respondToWebhook", "position": [200, 0], "parameters": {"respondWith": "text", "responseBody": "hello", "responseCode": 201}},
    {"name": "After", "type": "engi.transform.set", "position": [400, 0], "parameters": {"fields": [{"name": "after", "value": "yes"}]}}
  ],
  "connections": [{"from": {"node": "In"}, "to": {"node": "Reply"}}, {"from": {"node": "Reply"}, "to": {"node": "After"}}]
}`

// hookServer mounts the runtime's webhook handler like `engi serve` does.
func (f *runtimeFixture) hookServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for _, p := range f.rt.Webhooks.Patterns() {
		mux.Handle(p, f.rt.Webhooks)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func post(t *testing.T, url, body string) (code int, text string) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestRuntimeAnswersAWebhookWithTheLastNodeOutput(t *testing.T) {
	f := newRuntime(t)
	f.start(t)
	if err := f.rt.StartTriggers(context.Background()); err != nil {
		t.Fatal(err)
	}
	srv := f.hookServer(t)
	wf := f.save(t, calcDoc)

	if code, _ := post(t, srv.URL+"/hook/calc/m", `{"n": 21}`); code != http.StatusNotFound {
		t.Fatalf("inactive workflow answered %d", code)
	}
	if err := f.rt.Triggers.Activate(context.Background(), wf.ID); err != nil {
		t.Fatal(err)
	}
	code, body := post(t, srv.URL+"/hook/calc/m", `{"n": 21}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d: %s", code, body)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil || out["double"] != 42.0 || out["unit"] != "m" {
		t.Fatalf("body = %s (%v)", body, err)
	}
	if err := f.rt.Triggers.Deactivate(context.Background(), wf.ID); err != nil {
		t.Fatal(err)
	}
	if code, _ := post(t, srv.URL+"/hook/calc/m", `{"n": 1}`); code != http.StatusNotFound {
		t.Fatalf("deactivated workflow answered %d", code)
	}
}

func TestRuntimeRespondsFromARespondToWebhookNode(t *testing.T) {
	f := newRuntime(t)
	f.start(t)
	if err := f.rt.StartTriggers(context.Background()); err != nil {
		t.Fatal(err)
	}
	srv := f.hookServer(t)
	wf := f.save(t, respondDoc)
	if err := f.rt.Triggers.Activate(context.Background(), wf.ID); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(srv.URL + "/hook/early")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 201 || string(b) != "hello" {
		t.Fatalf("%d %q", resp.StatusCode, b)
	}
	// the workflow carries on after the reply
	res, err := f.rt.Executions.List(context.Background(), portExecutionFilter(wf.ID), portPage())
	if err != nil || len(res.Items) != 1 {
		t.Fatalf("executions = %v, %v", res.Items, err)
	}
	if ex := f.status(t, res.Items[0].ID, domain.StatusSuccess); outputOf(ex, "After")[0]["after"] != "yes" || ex.Mode != domain.ModeWebhook {
		t.Fatalf("execution = %+v", ex)
	}
}

func TestRuntimeWaitResumeRoute(t *testing.T) {
	f := newRuntime(t)
	f.start(t)
	srv := f.hookServer(t)
	wf := f.save(t, strings.Replace(waitDoc, "%s", `{"resume": "webhook"}`, 1))
	id := f.begin(t, wf, "Start", items(1)...)
	f.status(t, id, domain.StatusWaiting)

	token, err := f.rt.Waiter.PendingToken(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := post(t, srv.URL+"/hook-wait/"+string(id)+".wrong", `{}`); code != http.StatusNotFound {
		t.Fatalf("wrong token = %d", code)
	}
	if code, _ := post(t, srv.URL+"/hook-wait/nonsense", `{}`); code != http.StatusNotFound {
		t.Fatalf("garbage token = %d", code)
	}
	if ex := f.status(t, id, domain.StatusWaiting); ex.Status != domain.StatusWaiting {
		t.Fatal("a wrong token must not resume")
	}
	if code, body := post(t, srv.URL+"/hook-wait/"+token, `{"approved": true}`); code != http.StatusOK {
		t.Fatalf("right token = %d %s", code, body)
	}
	ex := f.status(t, id, domain.StatusSuccess)
	got := outputOf(ex, "After")
	if len(got) != 1 || got[0]["after"] != "yes" || got[0]["body"].(map[string]any)["approved"] != true {
		t.Fatalf("after = %v", got)
	}
	if code, _ := post(t, srv.URL+"/hook-wait/"+token, `{}`); code != http.StatusNotFound {
		t.Fatalf("second use = %d", code)
	}
}

func TestRuntimeRestoresActiveWorkflowsOnStart(t *testing.T) {
	f := newRuntime(t)
	f.start(t)
	wf := f.save(t, calcDoc)
	if err := f.rt.Workflows.SetActive(context.Background(), wf.ID, true); err != nil {
		t.Fatal(err)
	}
	srv := f.hookServer(t)
	if err := f.rt.StartTriggers(context.Background()); err != nil {
		t.Fatal(err)
	}
	if code, body := post(t, srv.URL+"/hook/calc/cm", `{"n": 2}`); code != http.StatusOK || !strings.Contains(body, `"double":4`) {
		t.Fatalf("%d %s", code, body)
	}
}

func portExecutionFilter(id domain.WorkflowID) port.ExecutionFilter {
	return port.ExecutionFilter{WorkflowID: id}
}

func portPage() port.Page { return port.Page{} }
