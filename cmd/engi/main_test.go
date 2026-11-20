package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantOut  string
		wantErr  string
	}{
		{"version", []string{"version"}, 0, version + "\n", ""},
		{"no args", nil, 2, "", "usage:"},
		{"unknown", []string{"nope"}, 2, "", `unknown command "nope"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			if code := run(tt.args, &out, &errOut); code != tt.wantCode {
				t.Fatalf("code = %d, want %d", code, tt.wantCode)
			}
			if out.String() != tt.wantOut || !strings.Contains(errOut.String(), tt.wantErr) {
				t.Fatalf("stdout=%q stderr=%q", out.String(), errOut.String())
			}
		})
	}
}

// result is the subset of the `engi run` report these tests inspect.
type result struct {
	Status string `json:"status"`
	Error  *struct {
		Node    string `json:"node"`
		Message string `json:"message"`
	} `json:"error"`
	Output struct {
		Node  string             `json:"node"`
		Ports [][]map[string]any `json:"ports"`
	} `json:"output"`
	RunData map[string][]struct {
		Status string `json:"status"`
		Items  []int  `json:"items"`
	} `json:"runData"`
}

func runCLI(t *testing.T, args ...string) (code int, rep result, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = execute(context.Background(), args, &out, &errOut, nil)
	if out.Len() > 0 && args[0] == "run" {
		if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
			t.Fatalf("stdout is not a report: %v\n%s", err, out.String())
		}
	}
	return code, rep, errOut.String()
}

func jsonOf(item map[string]any) map[string]any {
	m, _ := item["json"].(map[string]any)
	return m
}

func TestExampleHTTPFetchIf(t *testing.T) {
	var gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/popular":
			_, _ = w.Write([]byte(`{"name":"engi","stars":420}`))
		default:
			_, _ = w.Write([]byte(`{"name":"tiny","stars":3}`))
		}
	}))
	defer srv.Close()

	tests := []struct {
		path        string
		wantTier    string
		wantSummary string
	}{
		{"/popular", "popular", "engi has 420 stars"},
		{"/small", "niche", "tiny is still small"},
	}
	for _, tt := range tests {
		t.Run(tt.wantTier, func(t *testing.T) {
			input := filepath.Join(t.TempDir(), "input.json")
			if err := os.WriteFile(input, []byte(`[{"url":"`+srv.URL+tt.path+`"}]`), 0o600); err != nil {
				t.Fatal(err)
			}
			code, rep, stderr := runCLI(t, "run", "../../examples/http-fetch-if.workflow.json", "--input", input)
			if code != 0 || rep.Status != "success" {
				t.Fatalf("code=%d status=%s stderr=%s error=%+v", code, rep.Status, stderr, rep.Error)
			}
			if rep.Output.Node != "Result" || len(rep.Output.Ports) != 1 || len(rep.Output.Ports[0]) != 1 {
				t.Fatalf("output = %+v", rep.Output)
			}
			got := jsonOf(rep.Output.Ports[0][0])
			if got["tier"] != tt.wantTier || got["summary"] != tt.wantSummary {
				t.Fatalf("got %v", got)
			}
			if gotAccept != "application/json" {
				t.Fatalf("Accept header = %q", gotAccept)
			}
		})
	}
}

func TestExampleCodeAggregate(t *testing.T) {
	code, rep, stderr := runCLI(t, "run", "../../examples/code-aggregate.workflow.json", "--input", "../../examples/code-aggregate.input.json")
	if code != 0 || rep.Status != "success" {
		t.Fatalf("code=%d stderr=%s report=%+v", code, stderr, rep)
	}
	got := jsonOf(rep.Output.Ports[0][0])
	wantItems := []any{"monitor", "keyboard", "dock"}
	wantTotals := []any{219.99, 99.0, 89.0}
	if !reflect.DeepEqual(got["items"], wantItems) || !reflect.DeepEqual(got["totals"], wantTotals) {
		t.Fatalf("got %v", got)
	}
}

func TestExampleSwitchRouting(t *testing.T) {
	code, rep, stderr := runCLI(t, "run", "../../examples/switch-routing.workflow.json", "--input", "../../examples/switch-routing.input.json")
	if code != 0 || rep.Status != "success" {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	count := func(node string) int { return rep.RunData[node][0].Items[0] }
	if count("Page on-call") != 2 || count("Queue for later") != 1 || count("Triage by hand") != 1 || count("Handled") != 3 {
		t.Fatalf("routing counts wrong: %+v", rep.RunData)
	}
}

func TestRunFlagsMayFollowThePositionalArgument(t *testing.T) {
	code, rep, stderr := runCLI(t, "run", "--start", "Start", "../../examples/switch-routing.workflow.json", "--input", "../../examples/switch-routing.input.json")
	if code != 0 || rep.Status != "success" {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
}

func TestRunExitsNonZeroOnExecutionError(t *testing.T) {
	dir := t.TempDir()
	wf := filepath.Join(dir, "fail.json")
	doc := `{"name":"fail","nodes":[
	  {"name":"Start","type":"engi.trigger.manual","position":[0,0]},
	  {"name":"Boom","type":"engi.code.javascript","position":[1,0],"parameters":{"jsCode":"throw new Error('kaput')"}}],
	  "connections":[{"from":{"node":"Start"},"to":{"node":"Boom"}}]}`
	if err := os.WriteFile(wf, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	code, rep, stderr := runCLI(t, "run", wf)
	if code != 1 || rep.Status != "error" || rep.Error == nil || rep.Error.Node != "Boom" || !strings.Contains(stderr, "kaput") {
		t.Fatalf("code=%d report=%+v stderr=%s", code, rep, stderr)
	}
}

func TestRunInputShorthand(t *testing.T) {
	input := filepath.Join(t.TempDir(), "plain.json")
	if err := os.WriteFile(input, []byte(`[{"id":1,"priority":"high"},{"id":2,"priority":"low"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, rep, stderr := runCLI(t, "run", "../../examples/switch-routing.workflow.json", "--input", input)
	if code != 0 || rep.RunData["Start"][0].Items[0] != 2 {
		t.Fatalf("code=%d stderr=%s report=%+v", code, stderr, rep.RunData["Start"])
	}
}

func TestRunAndValidateErrors(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	unknown := filepath.Join(dir, "unknown.json")
	notItems := filepath.Join(dir, "items.json")
	for path, content := range map[string]string{
		bad:      `{"name": "x", "nodes": [`,
		unknown:  `{"name":"x","nodes":[{"name":"A","type":"engi.nope","position":[0,0]}],"connections":[]}`,
		notItems: `"not an array"`,
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	good := "../../examples/switch-routing.workflow.json"
	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantErr  string
	}{
		{"run without file", []string{"run"}, 2, "expected exactly one workflow file"},
		{"run two files", []string{"run", good, good}, 2, "expected exactly one"},
		{"run missing file", []string{"run", filepath.Join(dir, "nope.json")}, 1, "reading workflow"},
		{"run malformed json", []string{"run", bad}, 1, "parsing json"},
		{"run unknown node type", []string{"run", unknown, "--start", "A"}, 1, "unknown node type"},
		{"run unknown start", []string{"run", good, "--start", "Nope"}, 1, "start node does not exist"},
		{"run missing input", []string{"run", good, "--input", filepath.Join(dir, "none.json")}, 1, "reading input"},
		{"run bad input", []string{"run", good, "--input", notItems}, 1, "array"},
		{"run unknown flag", []string{"run", good, "--bogus"}, 2, "flag provided but not defined"},
		{"run missing config", []string{"run", good, "--config", filepath.Join(dir, "none.yaml")}, 1, "reading config file"},
		{"validate ok", []string{"validate", good}, 0, ""},
		{"validate without file", []string{"validate"}, 2, "expected exactly one"},
		{"validate unknown type", []string{"validate", unknown}, 1, "unknown node type"},
		{"validate malformed", []string{"validate", bad}, 1, "parsing json"},
		{"help", []string{"help"}, 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := execute(context.Background(), tt.args, &out, &errOut, nil)
			if code != tt.wantCode || !strings.Contains(errOut.String(), tt.wantErr) {
				t.Fatalf("code=%d (want %d) stderr=%q (want containing %q)", code, tt.wantCode, errOut.String(), tt.wantErr)
			}
		})
	}
}

func TestValidatePrintsSummary(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := execute(context.Background(), []string{"validate", "../../examples/code-aggregate.workflow.json"}, &out, &errOut, nil); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "5 nodes") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestConfigFromFileAndEnvironment(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "engi.yaml")
	if err := os.WriteFile(cfg, []byte("nodes:\n  code:\n    timeout: 1s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	args := []string{"run", "../../examples/code-aggregate.workflow.json", "--input", "../../examples/code-aggregate.input.json", "--config", cfg}
	if code := execute(context.Background(), args, &out, &errOut, nil); code != 0 {
		t.Fatalf("file config: code=%d stderr=%s", code, errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if code := execute(context.Background(), args[:4], &out, &errOut, []string{"ENGI_NODES_CODE_TIMEOUT=banana"}); code != 1 || !strings.Contains(errOut.String(), "ENGI_NODES_CODE_TIMEOUT") {
		t.Fatalf("env config: code=%d stderr=%s", code, errOut.String())
	}
}

func TestServeFailsFastWithoutEncryptionKey(t *testing.T) {
	var out, errb bytes.Buffer
	env := []string{"ENGI_STORE_PATH=" + filepath.Join(t.TempDir(), "x.db")}
	code := execute(t.Context(), []string{"serve", "--server-addr", "127.0.0.1:0"}, &out, &errb, env)
	if code != exitFailure || !strings.Contains(errb.String(), "ENGI_ENCRYPTION_KEYS") {
		t.Fatalf("code = %d, stderr = %q", code, errb.String())
	}
}

func TestServeWarnsAboutUnknownEnvWithoutValues(t *testing.T) {
	const secret = "hunter2-secret-value"
	tests := []struct {
		name     string
		env      []string
		wantWarn bool
	}{
		{"unknown name is logged", []string{"ENGI_SECRET_TYPO=" + secret}, true},
		{"known names are not", []string{"ENGI_LOG_FORMAT=text"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			env := append([]string{"ENGI_STORE_PATH=" + filepath.Join(t.TempDir(), "x.db")}, tt.env...)
			// No encryption key: serve stops at the keyring check, after the warning.
			code := execute(t.Context(), []string{"serve", "--server-addr", "127.0.0.1:0"}, &out, &errb, env)
			if code != exitFailure {
				t.Fatalf("code = %d, stderr = %q", code, errb.String())
			}
			if strings.Contains(errb.String(), secret) {
				t.Fatalf("a value leaked into the log: %q", errb.String())
			}
			if got := strings.Contains(errb.String(), "ignoring unknown ENGI_* environment variables"); got != tt.wantWarn {
				t.Fatalf("warning logged = %v, want %v; stderr = %q", got, tt.wantWarn, errb.String())
			}
			if tt.wantWarn && (!strings.Contains(errb.String(), "ENGI_SECRET_TYPO") || !strings.Contains(errb.String(), "docs/11")) {
				t.Fatalf("warning lacks the name or hint: %q", errb.String())
			}
		})
	}
}

func TestMigrateUpAndDown(t *testing.T) {
	env := []string{"ENGI_STORE_PATH=" + filepath.Join(t.TempDir(), "m.db")}
	run := func(args ...string) (int, string) {
		var out, errb bytes.Buffer
		code := execute(t.Context(), args, &out, &errb, env)
		return code, out.String() + errb.String()
	}
	if code, msg := run("migrate", "up"); code != exitOK || !strings.Contains(msg, "applied") {
		t.Fatalf("up: %d %q", code, msg)
	}
	if code, msg := run("migrate", "down"); code != exitOK || !strings.Contains(msg, "reverted migration") {
		t.Fatalf("down: %d %q", code, msg)
	}
	if code, _ := run("migrate"); code != exitUsage {
		t.Fatalf("missing direction: %d", code)
	}
}
