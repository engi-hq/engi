package app

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

const varsWorkflow = `{
  "name": "vars",
  "nodes": [
    {"name": "Start", "type": "engi.trigger.manual", "typeVersion": 1, "position": [0, 0]},
    {"name": "Read", "type": "engi.code.javascript", "typeVersion": 1, "position": [200, 0],
     "parameters": {"mode": "runOnceForEachItem", "jsCode": "return { region: $vars.REGION, token: $vars.TOKEN };"}}
  ],
  "connections": [{"from": {"node": "Start", "port": "main", "index": 0}, "to": {"node": "Read", "port": "main", "index": 0}}]
}`

func TestVariablesResolveInRunsAndSecretsAreRedacted(t *testing.T) {
	f := newServerFixture(t)
	f.startWorker()
	s := f.ownerSession()
	const secret = "s3cr3t-token-value"

	plain := f.as(s, req{method: "POST", path: "/api/v1/variables", body: map[string]any{"key": "REGION", "value": "eu-west"}})
	wantStatus(t, plain, http.StatusCreated)
	sec := f.as(s, req{method: "POST", path: "/api/v1/variables", body: map[string]any{"key": "TOKEN", "value": secret, "secret": true}})
	wantStatus(t, sec, http.StatusCreated)
	secID := sec.json(t)["id"].(string)

	wantProblem(t, f.as(s, req{method: "POST", path: "/api/v1/variables", body: map[string]any{"key": "REGION", "value": "x"}}), http.StatusConflict)
	wantProblem(t, f.as(s, req{method: "POST", path: "/api/v1/variables", body: map[string]any{"key": "1bad", "value": "x"}}), http.StatusUnprocessableEntity)

	for _, r := range []resp{
		sec,
		f.as(s, req{method: "GET", path: "/api/v1/variables"}),
		f.as(s, req{method: "PATCH", path: "/api/v1/variables/" + secID, body: map[string]any{"key": "TOKEN"}}),
	} {
		if strings.Contains(string(r.body), secret) {
			t.Fatalf("response leaks the secret: %s", r.body)
		}
	}
	if got := f.as(s, req{method: "PATCH", path: "/api/v1/variables/" + secID, body: map[string]any{"key": "TOKEN"}}).json(t); got["secret"] != true || got["value"] == secret {
		t.Fatalf("patched secret = %v", got)
	}
	// A secret cannot be exposed by flipping the flag alone.
	wantProblem(t, f.as(s, req{method: "PATCH", path: "/api/v1/variables/" + secID, body: map[string]any{"secret": false}}), http.StatusUnprocessableEntity)

	wf := f.as(s, req{method: "POST", path: "/api/v1/workflows", raw: varsWorkflow})
	wantStatus(t, wf, http.StatusCreated)
	run := f.as(s, req{method: "POST", path: "/api/v1/workflows/" + wf.json(t)["id"].(string) + "/run"})
	wantStatus(t, run, http.StatusAccepted)
	ex := waitForExecution(t, f, s, run.json(t)["executionId"].(string), "success")
	out := fmt.Sprint(ex["runData"].(map[string]any)["Read"])
	if !strings.Contains(out, "eu-west") || !strings.Contains(out, secret) {
		t.Fatalf("$vars did not resolve: %s", out)
	}

	wantStatus(t, f.as(s, req{method: "PATCH", path: "/api/v1/variables/" + secID, body: map[string]any{"value": "rotated"}}), http.StatusOK)
	wantStatus(t, f.as(s, req{method: "DELETE", path: "/api/v1/variables/" + secID}), http.StatusNoContent)
	wantProblem(t, f.as(s, req{method: "PATCH", path: "/api/v1/variables/" + secID, body: map[string]any{"value": "x"}}), http.StatusNotFound)
}

func TestTagsLifecycleAndWorkflowFilter(t *testing.T) {
	f := newServerFixture(t)
	s := f.ownerSession()
	body := func(name string, tags ...string) string { // %q renders a Go slice; build JSON by hand
		parts := make([]string, len(tags))
		for i, tg := range tags {
			parts[i] = fmt.Sprintf("%q", tg)
		}
		return strings.Replace(wfBody(name), `"nodes"`, `"tags": [`+strings.Join(parts, ",")+`], "nodes"`, 1)
	}
	a := f.as(s, req{method: "POST", path: "/api/v1/workflows", raw: body("a", "prod", "billing")})
	wantStatus(t, a, http.StatusCreated)
	wantStatus(t, f.as(s, req{method: "POST", path: "/api/v1/workflows", raw: body("b", "billing")}), http.StatusCreated)

	created := f.as(s, req{method: "POST", path: "/api/v1/tags", body: map[string]any{"name": "ops"}})
	wantStatus(t, created, http.StatusCreated)
	wantProblem(t, f.as(s, req{method: "POST", path: "/api/v1/tags", body: map[string]any{"name": "ops"}}), http.StatusConflict)
	wantProblem(t, f.as(s, req{method: "POST", path: "/api/v1/tags", body: map[string]any{"name": "  "}}), http.StatusUnprocessableEntity)

	list := f.as(s, req{method: "GET", path: "/api/v1/tags"})
	wantStatus(t, list, http.StatusOK)
	ids := map[string]string{}
	for _, it := range list.json(t)["data"].([]any) { // tags saved with workflows are listed too
		m := it.(map[string]any)
		ids[m["name"].(string)] = m["id"].(string)
	}
	if len(ids) != 3 || ids["prod"] == "" || ids["billing"] == "" || ids["ops"] == "" {
		t.Fatalf("tags = %v", ids)
	}

	count := func(tag string) int {
		r := f.as(s, req{method: "GET", path: "/api/v1/workflows?tag=" + tag})
		wantStatus(t, r, http.StatusOK)
		return len(r.json(t)["data"].([]any))
	}
	if count("billing") != 2 || count("prod") != 1 {
		t.Fatal("tag filter does not match")
	}

	wantProblem(t, f.as(s, req{method: "PATCH", path: "/api/v1/tags/" + ids["prod"], body: map[string]any{"name": "billing"}}), http.StatusConflict)
	renamed := f.as(s, req{method: "PATCH", path: "/api/v1/tags/" + ids["billing"], body: map[string]any{"name": "finance"}})
	wantStatus(t, renamed, http.StatusOK)
	if count("billing") != 0 || count("finance") != 2 {
		t.Fatal("rename did not reach the workflows")
	}
	got := f.as(s, req{method: "GET", path: "/api/v1/workflows/" + a.json(t)["id"].(string)}).json(t)
	if fmt.Sprint(got["tags"]) != "[finance prod]" {
		t.Fatalf("workflow tags = %v", got["tags"])
	}

	wantStatus(t, f.as(s, req{method: "DELETE", path: "/api/v1/tags/" + ids["billing"]}), http.StatusNoContent)
	if count("finance") != 0 {
		t.Fatal("deleted tag still on workflows")
	}
	wantProblem(t, f.as(s, req{method: "DELETE", path: "/api/v1/tags/" + ids["billing"]}), http.StatusNotFound)
}
