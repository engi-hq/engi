package aiagent_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/mcp"
	"github.com/engi-hq/engi/internal/node/aiagent"
	"github.com/engi-hq/engi/internal/node/aitoolmcp"
	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// scriptLLM replays replies in order and records the requests.
type scriptLLM struct {
	mu      sync.Mutex
	reqs    []port.LLMRequest
	replies []func(port.LLMRequest) (port.LLMResponse, error)
}

func (s *scriptLLM) Chat(_ context.Context, req port.LLMRequest) (port.LLMResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reqs = append(s.reqs, req)
	i := len(s.reqs) - 1
	if i >= len(s.replies) {
		i = len(s.replies) - 1
	}
	return s.replies[i](req)
}

func (s *scriptLLM) Stream(ctx context.Context, req port.LLMRequest, _ func(port.StreamEvent) error) (port.LLMResponse, error) {
	return s.Chat(ctx, req)
}

func say(text string) func(port.LLMRequest) (port.LLMResponse, error) {
	return func(port.LLMRequest) (port.LLMResponse, error) {
		return port.LLMResponse{Content: text, StopReason: port.StopEnd, Model: "m", Usage: port.LLMUsage{InputTokens: 10, OutputTokens: 5}}, nil
	}
}

func call(calls ...port.LLMToolCall) func(port.LLMRequest) (port.LLMResponse, error) {
	return func(port.LLMRequest) (port.LLMResponse, error) {
		return port.LLMResponse{ToolCalls: calls, StopReason: port.StopToolUse, Model: "m", Usage: port.LLMUsage{InputTokens: 10, OutputTokens: 5}}, nil
	}
}

func tc(id, name, args string) port.LLMToolCall {
	return port.LLMToolCall{ID: id, Name: name, Arguments: json.RawMessage(args)}
}

type modelProv struct {
	llm  port.LLM
	tmpl port.LLMRequest
}

func (m modelProv) Model(context.Context) (port.LLM, port.LLMRequest, error) {
	return m.llm, m.tmpl, nil
}

type fakeTool struct {
	name  string
	fn    func(context.Context, json.RawMessage) (port.ToolResult, error)
	calls atomic.Int32
}

func (f *fakeTool) Definition() port.ToolDefinition {
	return port.ToolDefinition{Name: f.name, Description: "d", Parameters: json.RawMessage(`{"type":"object"}`)}
}

func (f *fakeTool) Call(ctx context.Context, a json.RawMessage) (port.ToolResult, error) {
	f.calls.Add(1)
	return f.fn(ctx, a)
}

type toolProv []port.Tool

func (t toolProv) Tools(context.Context) ([]port.Tool, error) { return t, nil }

type fakeMemory struct {
	history []port.LLMMessage
	saved   []port.LLMMessage
}

func (f *fakeMemory) History(context.Context) ([]port.LLMMessage, error) { return f.history, nil }
func (f *fakeMemory) Save(_ context.Context, m []port.LLMMessage) error {
	f.saved = append(f.saved, m...)
	return nil
}
func (f *fakeMemory) Clear(context.Context) error { return nil }

type clock struct{}

func (clock) Now() time.Time { return time.Now() }

var limits = aiagent.Limits{MaxIterations: 5, MaxTotalTokens: 1000, Timeout: 5 * time.Second, ToolConcurrency: 2, MaxToolResultBytes: 64}

type rig struct {
	llm    *scriptLLM
	tools  []port.Tool
	memory *fakeMemory
	params map[string]any
	limits aiagent.Limits
	items  []map[string]any
	// toolSubs, when set, replaces the single tools sub-node of the rig.
	toolSubs []nodesdk.Supplied
}

func newRig(replies ...func(port.LLMRequest) (port.LLMResponse, error)) *rig {
	return &rig{llm: &scriptLLM{replies: replies}, params: map[string]any{"prompt": "hi"}, limits: limits, items: []map[string]any{{"a": 1}}}
}

func (r *rig) run(t *testing.T) (nodesdk.Output, error) {
	t.Helper()
	n := aiagent.New(aiagent.Config{Limits: r.limits, Clock: clock{}})
	in := nodetest.New(n.Describe(), nodetest.Items(r.items...), r.params)
	in.Subs = map[nodesdk.PortKind][]nodesdk.Supplied{
		nodesdk.PortAIModel: {{Node: "Model", Value: modelProv{llm: r.llm, tmpl: port.LLMRequest{Model: "m", System: "base"}}}},
	}
	if len(r.tools) > 0 {
		in.Subs[nodesdk.PortAITool] = []nodesdk.Supplied{{Node: "Tools", Value: toolProv(r.tools)}}
	}
	if r.toolSubs != nil {
		in.Subs[nodesdk.PortAITool] = r.toolSubs
	}
	if r.memory != nil {
		in.Subs[nodesdk.PortAIMemory] = []nodesdk.Supplied{{Node: "Mem", Value: r.memory}}
	}
	return n.Execute(context.Background(), in)
}

func (r *rig) mustRun(t *testing.T) map[string]any {
	t.Helper()
	out, err := r.run(t)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return out.Ports[0][0].JSON
}

func okTool(name, reply string) *fakeTool {
	return &fakeTool{name: name, fn: func(context.Context, json.RawMessage) (port.ToolResult, error) {
		return port.ToolResult{Content: reply}, nil
	}}
}

func TestDescriptorIsValid(t *testing.T) {
	d := aiagent.New(aiagent.Config{}).Describe()
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	m, ok := d.PortOfKind(true, nodesdk.PortAIModel, 0)
	mem, _ := d.PortOfKind(true, nodesdk.PortAIMemory, 0)
	tool, _ := d.PortOfKind(true, nodesdk.PortAITool, 0)
	if !ok || !m.Required || m.MaxConnections != 1 || mem.MaxConnections != 1 || tool.MaxConnections != 0 || tool.Required {
		t.Fatalf("sub-ports = %+v %+v %+v", m, mem, tool)
	}
}

func TestAnswerWithoutTools(t *testing.T) {
	r := newRig(say("hello"))
	r.params["systemPrompt"] = "be brief"
	j := r.mustRun(t)
	if j["output"] != "hello" || j["iterations"] != float64(1) || j["stopReason"] != "end" || j["model"] != "m" {
		t.Fatalf("json = %v", j)
	}
	if steps := j["steps"].([]any); len(steps) != 0 {
		t.Fatalf("steps = %v", steps)
	}
	if u := j["usage"].(map[string]any); u["totalTokens"] != float64(15) {
		t.Fatalf("usage = %v", u)
	}
	req := r.llm.reqs[0]
	if req.System != "base\n\nbe brief" || len(req.Messages) != 1 || req.Messages[0].Content != "hi" || len(req.Tools) != 0 {
		t.Fatalf("request = %+v", req)
	}
}

func TestToolLoopFeedsResultsBack(t *testing.T) {
	weather := okTool("weather", `{"temp":4}`)
	r := newRig(call(tc("c1", "weather", `{"city":"Oslo"}`)), say("It is 4 degrees."))
	r.tools = []port.Tool{weather}
	j := r.mustRun(t)
	if j["output"] != "It is 4 degrees." || j["iterations"] != float64(2) {
		t.Fatalf("json = %v", j)
	}
	step := j["steps"].([]any)[0].(map[string]any)
	if step["tool"] != "weather" || step["isError"] != false || step["input"].(map[string]any)["city"] != "Oslo" ||
		step["output"].(map[string]any)["temp"] != float64(4) || step["iteration"] != float64(1) {
		t.Fatalf("step = %v", step)
	}
	if _, ok := step["durationMs"].(float64); !ok {
		t.Fatalf("durationMs missing: %v", step)
	}
	second := r.llm.reqs[1].Messages
	if len(second) != 3 || second[1].Role != port.RoleAssistant || second[1].ToolCalls[0].ID != "c1" ||
		second[2].Role != port.RoleTool || second[2].ToolCallID != "c1" || second[2].Content != `{"temp":4}` {
		t.Fatalf("second request messages = %+v", second)
	}
	if len(r.llm.reqs[0].Tools) != 1 || r.llm.reqs[0].Tools[0].Name != "weather" {
		t.Fatalf("tool definitions = %+v", r.llm.reqs[0].Tools)
	}
}

func TestToolErrorsAreFedBack(t *testing.T) {
	failing := &fakeTool{name: "fail", fn: func(context.Context, json.RawMessage) (port.ToolResult, error) {
		return port.ToolResult{Content: "bad input", IsError: true}, nil
	}}
	broken := &fakeTool{name: "broken", fn: func(context.Context, json.RawMessage) (port.ToolResult, error) {
		return port.ToolResult{}, errors.New("cannot run")
	}}
	panicky := &fakeTool{name: "panicky", fn: func(context.Context, json.RawMessage) (port.ToolResult, error) { panic("oops") }}
	r := newRig(call(tc("1", "fail", `{}`), tc("2", "broken", `{}`), tc("3", "nope", `{}`), tc("4", "panicky", ``)), say("done"))
	r.tools = []port.Tool{failing, broken, panicky}
	j := r.mustRun(t)
	if j["output"] != "done" {
		t.Fatalf("json = %v", j)
	}
	steps := j["steps"].([]any)
	wantErr := []string{"bad input", "cannot run", "no tool named", "crashed"}
	for i, w := range wantErr {
		s := steps[i].(map[string]any)
		if s["isError"] != true || !strings.Contains(s["output"].(string), w) {
			t.Fatalf("step %d = %v, want error containing %q", i, s, w)
		}
	}
	for i, m := range r.llm.reqs[1].Messages[2:] {
		if m.Role != port.RoleTool || !m.IsError || m.ToolCallID != string(rune('1'+i)) {
			t.Fatalf("tool message %d = %+v", i, m)
		}
	}
}

func TestToolsRunInParallelWithBound(t *testing.T) {
	var running, peak atomic.Int32
	slow := &fakeTool{name: "slow", fn: func(context.Context, json.RawMessage) (port.ToolResult, error) {
		cur := running.Add(1)
		for {
			p := peak.Load()
			if cur <= p || peak.CompareAndSwap(p, cur) {
				break
			}
		}
		time.Sleep(60 * time.Millisecond)
		running.Add(-1)
		return port.ToolResult{Content: "ok"}, nil
	}}
	r := newRig(call(tc("1", "slow", `{}`), tc("2", "slow", `{}`), tc("3", "slow", `{}`), tc("4", "slow", `{}`)), say("done"))
	r.tools = []port.Tool{slow}
	start := time.Now()
	r.mustRun(t)
	if p := peak.Load(); p != 2 {
		t.Fatalf("peak concurrency = %d, want 2 (limit)", p)
	}
	if d := time.Since(start); d > 220*time.Millisecond {
		t.Fatalf("took %s: calls did not run in parallel", d)
	}
	if slow.calls.Load() != 4 {
		t.Fatalf("calls = %d", slow.calls.Load())
	}
	msgs := r.llm.reqs[1].Messages
	for i := 0; i < 4; i++ { // results keep the order of calls
		if msgs[2+i].ToolCallID != string(rune('1'+i)) {
			t.Fatalf("result order: %+v", msgs)
		}
	}
}

func TestToolResultIsTruncated(t *testing.T) {
	big := okTool("big", strings.Repeat("é", 100))
	r := newRig(call(tc("1", "big", `{}`)), say("ok"))
	r.tools = []port.Tool{big}
	r.mustRun(t)
	got := r.llm.reqs[1].Messages[2].Content
	if !strings.Contains(got, "[truncated:") || len(got) > 64+40 {
		t.Fatalf("content = %q", got)
	}
	if !strings.HasPrefix(got, "é") || strings.ContainsRune(got, '�') {
		t.Fatalf("cut in the middle of a rune: %q", got)
	}
}

func TestMaxIterations(t *testing.T) {
	loopTool := okTool("t", "x")
	r := newRig(call(tc("1", "t", `{}`)))
	r.tools = []port.Tool{loopTool}
	r.limits.MaxIterations = 3
	_, err := r.run(t)
	if err == nil || !strings.Contains(err.Error(), "limit of 3 model calls") {
		t.Fatalf("err = %v", err)
	}
	if loopTool.calls.Load() != 3 {
		t.Fatalf("tool calls = %d, want 3", loopTool.calls.Load())
	}
	r = newRig(call(tc("1", "t", `{}`)))
	r.tools = []port.Tool{okTool("t", "x")}
	r.limits.MaxIterations = 2
	r.params["onLimit"] = "return"
	j := r.mustRun(t)
	if j["stopReason"] != "maxIterations" || j["iterations"] != float64(2) || len(j["steps"].([]any)) != 2 {
		t.Fatalf("json = %v", j)
	}
}

func TestNodeOverridesLimits(t *testing.T) {
	r := newRig(call(tc("1", "t", `{}`)))
	r.tools = []port.Tool{okTool("t", "x")}
	r.params["maxIterations"] = float64(1)
	r.params["onLimit"] = "return"
	if j := r.mustRun(t); j["iterations"] != float64(1) {
		t.Fatalf("json = %v", j)
	}
}

func TestTokenBudget(t *testing.T) {
	r := newRig(call(tc("1", "t", `{}`)))
	r.tools = []port.Tool{okTool("t", "x")}
	r.limits.MaxTotalTokens = 20 // each call uses 15
	r.params["onLimit"] = "return"
	j := r.mustRun(t)
	if j["stopReason"] != "tokenBudget" || j["iterations"] != float64(2) {
		t.Fatalf("json = %v", j)
	}
	r.params["onLimit"] = "error"
	if _, err := r.run(t); err == nil || !strings.Contains(err.Error(), "token budget") {
		t.Fatalf("err = %v", err)
	}
}

func TestTimeout(t *testing.T) {
	hang := &fakeTool{name: "hang", fn: func(ctx context.Context, _ json.RawMessage) (port.ToolResult, error) {
		<-ctx.Done()
		return port.ToolResult{}, ctx.Err()
	}}
	r := newRig(call(tc("1", "hang", `{}`)))
	r.tools = []port.Tool{hang}
	r.limits.Timeout = 50 * time.Millisecond
	_, err := r.run(t)
	if err == nil || !strings.Contains(err.Error(), "ran longer than") {
		t.Fatalf("err = %v", err)
	}
	r.params["onLimit"] = "return"
	if j := r.mustRun(t); j["stopReason"] != "timeout" {
		t.Fatalf("json = %v", j)
	}
}

func TestModelErrorFailsTheItem(t *testing.T) {
	r := newRig(func(port.LLMRequest) (port.LLMResponse, error) {
		return port.LLMResponse{}, &port.LLMError{Provider: "x", Status: 429, Retryable: true}
	})
	_, err := r.run(t)
	if err == nil || !port.IsRetryableLLMError(err) || !strings.Contains(err.Error(), "item 0") {
		t.Fatalf("err = %v", err)
	}
}

func TestMemoryRoundTrip(t *testing.T) {
	mem := &fakeMemory{history: []port.LLMMessage{{Role: port.RoleUser, Content: "my name is Ann"}, {Role: port.RoleAssistant, Content: "hi Ann"}}}
	r := newRig(call(tc("1", "t", `{}`)), say("Ann"))
	r.tools = []port.Tool{okTool("t", "x")}
	r.memory = mem
	r.params["prompt"] = "who am I?"
	r.mustRun(t)
	first := r.llm.reqs[0].Messages
	if len(first) != 3 || first[0].Content != "my name is Ann" || first[2].Content != "who am I?" {
		t.Fatalf("first request = %+v", first)
	}
	if len(mem.saved) != 2 || mem.saved[0].Role != port.RoleUser || mem.saved[0].Content != "who am I?" ||
		mem.saved[1].Role != port.RoleAssistant || mem.saved[1].Content != "Ann" {
		t.Fatalf("saved = %+v", mem.saved)
	}
	mem.saved = nil
	r = newRig(call(tc("1", "t", `{}`)), say("Ann"))
	r.tools = []port.Tool{okTool("t", "x")}
	r.memory = mem
	r.params["saveToolMessages"] = true
	r.mustRun(t)
	if len(mem.saved) != 4 || mem.saved[1].ToolCalls[0].Name != "t" || mem.saved[2].Role != port.RoleTool {
		t.Fatalf("saved with tools = %+v", mem.saved)
	}
}

func TestNothingSavedOnFailureOrLimit(t *testing.T) {
	mem := &fakeMemory{}
	r := newRig(call(tc("1", "t", `{}`)))
	r.tools, r.memory = []port.Tool{okTool("t", "x")}, mem
	r.limits.MaxIterations = 1
	r.params["onLimit"] = "return"
	r.mustRun(t)
	if len(mem.saved) != 0 {
		t.Fatalf("saved after limit: %+v", mem.saved)
	}
}

func TestStructuredOutput(t *testing.T) {
	r := newRig(say("```json\n{\"n\":3}\n```"))
	r.params["responseFormat"] = "jsonSchema"
	r.params["schema"] = map[string]any{"type": "object", "properties": map[string]any{"n": map[string]any{"type": "number"}}}
	j := r.mustRun(t)
	if j["json"].(map[string]any)["n"] != float64(3) {
		t.Fatalf("json = %v", j)
	}
	if f := r.llm.reqs[0].Format; f.Kind != port.ResponseJSONSchema || f.Name != "response" {
		t.Fatalf("format = %+v", f)
	}
	bad := newRig(say("not json"))
	bad.params["responseFormat"] = "json"
	if _, err := bad.run(t); err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("err = %v", err)
	}
}

func TestEmptyPromptAndDuplicateTools(t *testing.T) {
	r := newRig(say("x"))
	r.params["prompt"] = "  "
	if _, err := r.run(t); err == nil || !strings.Contains(err.Error(), "prompt is empty") {
		t.Fatalf("err = %v", err)
	}
	d := newRig(say("x"))
	d.tools = []port.Tool{okTool("same", "a"), okTool("same", "b")}
	if _, err := d.run(t); !errors.Is(err, port.ErrDuplicateTool) {
		t.Fatalf("err = %v", err)
	}
}

func TestEachItemRunsOnItsOwn(t *testing.T) {
	r := newRig(say("a"), say("b"))
	r.items = []map[string]any{{"x": 1}, {"x": 2}}
	out, err := r.run(t)
	if err != nil {
		t.Fatal(err)
	}
	items := out.Ports[0]
	if len(items) != 2 || items[0].JSON["output"] != "a" || items[1].JSON["output"] != "b" || items[1].PairedItem[0].Item != 1 {
		t.Fatalf("items = %+v", items)
	}
}

// labelledMCP is an MCP server whose tools answer with the server's label, so
// a test can see which server a call reached.
type labelledMCP struct {
	label string
	tools []string
}

func (l labelledMCP) ListTools(context.Context) ([]mcp.ToolInfo, error) {
	var out []mcp.ToolInfo
	for _, n := range l.tools {
		out = append(out, mcp.ToolInfo{Name: n, Description: "d"})
	}
	return out, nil
}

func (l labelledMCP) CallTool(_ context.Context, name string, _ json.RawMessage) (mcp.CallToolResult, error) {
	return mcp.TextResult(l.label+":"+name, false), nil
}

// mcpSub connects a real MCP Client tool sub-node to a fake server.
func mcpSub(t *testing.T, node string, h labelledMCP) nodesdk.Supplied {
	t.Helper()
	s := mcp.NewServer(mcp.Implementation{Name: "t"}, h)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rep := s.Handle(r.Context(), body)
		if rep.Accepted {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(rep.Body)
	}))
	t.Cleanup(srv.Close)
	n := aitoolmcp.New(aitoolmcp.Config{
		Limits: aitoolmcp.Limits{RequestTimeout: 5 * time.Second, MaxResponseBytes: 1 << 20, MaxListPages: 3, MaxTools: 10, MaxResultBytes: 1000},
	})
	in := nodetest.New(n.Describe(), nil, map[string]any{"endpoint": srv.URL})
	in.Client = srv.Client()
	v, err := n.Supply(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	return nodesdk.Supplied{Node: node, Value: v}
}

func TestMCPToolsOfTwoServersAreRenamedAndRoutedToTheirServer(t *testing.T) {
	r := newRig(call(tc("c1", "MCP_Alpha__search", `{"q":1}`), tc("c2", "MCP_Beta__search", `{"q":2}`)), say("done"))
	r.toolSubs = []nodesdk.Supplied{
		mcpSub(t, "MCP Alpha", labelledMCP{label: "A", tools: []string{"search"}}),
		mcpSub(t, "MCP Beta", labelledMCP{label: "B", tools: []string{"search"}}),
	}
	r.mustRun(t)
	var names []string
	for _, d := range r.llm.reqs[0].Tools {
		names = append(names, d.Name)
	}
	if strings.Join(names, ",") != "MCP_Alpha__search,MCP_Beta__search" {
		t.Fatalf("offered tools = %v", names)
	}
	second := r.llm.reqs[1].Messages
	if got := second[len(second)-2].Content + "|" + second[len(second)-1].Content; got != "A:search|B:search" {
		t.Fatalf("tool results = %q", got)
	}
}

func TestDuplicateToolAcrossSubNodesNamesBothNodes(t *testing.T) {
	r := newRig(say("x"))
	r.toolSubs = []nodesdk.Supplied{
		{Node: "Alpha", Value: toolProv{okTool("search", "a")}},
		{Node: "Beta", Value: toolProv{okTool("search", "b")}},
	}
	_, err := r.run(t)
	if !errors.Is(err, port.ErrDuplicateTool) {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{`"search"`, `"Alpha"`, `"Beta"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err %q does not name %s", err, want)
		}
	}
}

func TestForcedMCPDuplicateFailsWithBothNodes(t *testing.T) {
	// "Mail Server" and "Mail_Server" sanitize to the same prefix, so their
	// "search" tools still clash after prefixing and the run must fail.
	r := newRig(say("x"))
	r.toolSubs = []nodesdk.Supplied{
		mcpSub(t, "Mail Server", labelledMCP{label: "A", tools: []string{"search"}}),
		mcpSub(t, "Mail_Server", labelledMCP{label: "B", tools: []string{"search"}}),
	}
	_, err := r.run(t)
	if !errors.Is(err, port.ErrDuplicateTool) {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{`"Mail Server"`, `"Mail_Server"`, `"Mail_Server__search"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err %q does not name %s", err, want)
		}
	}
}
