package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

const threeNodeWorkflow = `{
  "name": "partial",
  "nodes": [
    {"name": "Start", "type": "engi.trigger.manual", "typeVersion": 1, "position": [0, 0]},
    {"name": "Compute", "type": "engi.code.javascript", "typeVersion": 1, "position": [200, 0],
     "parameters": {"mode": "runOnceForEachItem", "jsCode": "return { ...$json, doubled: ($json.n || 21) * 2 };"}},
    {"name": "After", "type": "engi.code.javascript", "typeVersion": 1, "position": [400, 0],
     "parameters": {"mode": "runOnceForEachItem", "jsCode": "return { ...$json, after: true };"}}
  ],
  "connections": [
    {"from": {"node": "Start", "port": "main", "index": 0}, "to": {"node": "Compute", "port": "main", "index": 0}},
    {"from": {"node": "Compute", "port": "main", "index": 0}, "to": {"node": "After", "port": "main", "index": 0}}
  ]
}`

func TestPartialRunWithPinDataAndPreview(t *testing.T) {
	f := newServerFixture(t)
	f.startWorker()
	s := f.ownerSession()
	id := f.as(s, req{method: "POST", path: "/api/v1/workflows", raw: threeNodeWorkflow}).json(t)["id"].(string)
	runPath := "/api/v1/workflows/" + id + "/run"

	// Inclusive destination with pinned trigger output: Compute runs on the pin, After does not.
	run := f.as(s, req{method: "POST", path: runPath, body: map[string]any{
		"destination": map[string]any{"node": "Compute", "mode": "inclusive"},
		"pinData":     map[string]any{"Start": []map[string]any{{"n": 7}}},
	}})
	wantStatus(t, run, http.StatusAccepted)
	execID := run.json(t)["executionId"].(string)
	runData := waitForExecution(t, f, s, execID, "success")["runData"].(map[string]any)
	if _, ok := runData["After"]; ok {
		t.Fatalf("After must not run: %v", runData)
	}
	if !strings.Contains(fmt.Sprint(runData["Compute"]), "14") {
		t.Fatalf("Compute output: %v", runData["Compute"])
	}

	// Unsaved document, exclusive destination: Compute is not executed.
	run = f.as(s, req{method: "POST", path: runPath, body: map[string]any{
		"destination": map[string]any{"node": "Compute", "mode": "exclusive"},
		"workflow":    json.RawMessage(threeNodeWorkflow),
	}})
	wantStatus(t, run, http.StatusAccepted)
	runData = waitForExecution(t, f, s, run.json(t)["executionId"].(string), "success")["runData"].(map[string]any)
	if _, ok := runData["Compute"]; ok || runData["Start"] == nil {
		t.Fatalf("exclusive run data: %v", runData)
	}

	// Bad requests.
	wantProblem(t, f.as(s, req{method: "POST", path: runPath, body: map[string]any{"destination": map[string]any{"node": "Nope"}}}), http.StatusUnprocessableEntity)
	wantProblem(t, f.as(s, req{method: "POST", path: runPath, body: map[string]any{"destination": map[string]any{"node": "Compute", "mode": "sideways"}}}), http.StatusUnprocessableEntity)
	wantProblem(t, f.as(s, req{method: "POST", path: runPath, body: map[string]any{"pinData": map[string]any{"Ghost": []map[string]any{{}}}}}), http.StatusUnprocessableEntity)

	// Preview: success against Compute's recorded input (the pinned item).
	preview := func(expr string) resp {
		return f.as(s, req{method: "POST", path: "/api/v1/expressions/preview", body: map[string]any{
			"expression": expr, "executionId": execID, "node": "Compute", "itemIndex": 0, "runIndex": 0,
		}})
	}
	ok := preview("$json.n * 3")
	wantStatus(t, ok, http.StatusOK)
	if ok.json(t)["value"] != float64(21) {
		t.Fatalf("preview = %s", ok.body)
	}
	// Without an execution id the latest run of the workflow is used.
	latest := f.as(s, req{method: "POST", path: "/api/v1/expressions/preview", body: map[string]any{
		"expression": "$json.n", "workflowId": id, "node": "Start", "itemIndex": 0, "runIndex": 0,
	}})
	wantStatus(t, latest, http.StatusOK)

	bad := wantProblem(t, preview("$json.n.foo.bar"), http.StatusUnprocessableEntity)
	if !strings.HasSuffix(bad["type"].(string), "expression-error") {
		t.Fatalf("problem = %v", bad)
	}
	wantProblem(t, f.as(s, req{method: "POST", path: "/api/v1/expressions/preview", body: map[string]any{"expression": "1", "executionId": execID, "node": "Nope"}}), http.StatusNotFound)
	wantProblem(t, f.do(req{method: "POST", path: "/api/v1/expressions/preview", body: map[string]any{}}), http.StatusUnauthorized)
}
