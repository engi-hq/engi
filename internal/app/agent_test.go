package app

import (
	"strings"
	"testing"
)

// agentDoc wires a model, memory and two tools into an agent: the engine
// validates the sub-port kinds and cardinalities of the real descriptors.
const agentDoc = `{
  "name": "agent",
  "nodes": [
    {"name": "Start", "type": "engi.trigger.manual", "position": [0, 0]},
    {"name": "Agent", "type": "engi.ai.agent", "position": [200, 0]},
    {"name": "Model", "type": "engi.ai.model", "position": [100, 200]},
    {"name": "Memory", "type": "engi.ai.memory.window", "position": [200, 200]},
    {"name": "Search", "type": "engi.ai.tool.http", "position": [300, 200], "parameters": {"url": "https://example.test/{q}",
       "parameters": "{\"type\":\"object\",\"properties\":{\"q\":{\"type\":\"string\"}}}"}},
    {"name": "Calc", "type": "engi.ai.tool.code", "position": [400, 200]}
  ],
  "connections": [
    {"from": {"node": "Start"}, "to": {"node": "Agent"}},
    {"from": {"node": "Model", "port": "ai.model"}, "to": {"node": "Agent", "port": "ai.model"}},
    {"from": {"node": "Memory", "port": "ai.memory"}, "to": {"node": "Agent", "port": "ai.memory"}},
    {"from": {"node": "Search", "port": "ai.tool"}, "to": {"node": "Agent", "port": "ai.tool"}},
    {"from": {"node": "Calc", "port": "ai.tool"}, "to": {"node": "Agent", "port": "ai.tool"}}
  ]
}`

func TestAgentWorkflowValidates(t *testing.T) {
	a := newApp(t)
	if err := a.Validate(workflow(t, agentDoc)); err != nil {
		t.Fatal(err)
	}
}

func TestAgentWorkflowRejectsASecondModel(t *testing.T) {
	a := newApp(t)
	doc := strings.Replace(agentDoc, `{"name": "Calc", "type": "engi.ai.tool.code", "position": [400, 200]}`,
		`{"name": "Model2", "type": "engi.ai.model", "position": [400, 200]}`, 1)
	doc = strings.Replace(doc, `{"from": {"node": "Calc", "port": "ai.tool"}, "to": {"node": "Agent", "port": "ai.tool"}}`, `{"from": {"node": "Model2", "port": "ai.model"}, "to": {"node": "Agent", "port": "ai.model"}}`, 1)
	err := a.Validate(workflow(t, doc))
	if err == nil {
		t.Fatal("two models on one agent must be rejected")
	}
}

func TestAgentWithoutModelFailsBeforeRunning(t *testing.T) {
	a := newApp(t)
	doc := strings.Replace(agentDoc, `{"from": {"node": "Model", "port": "ai.model"}, "to": {"node": "Agent", "port": "ai.model"}},`, ``, 1)
	state, err := a.Run(t.Context(), RunRequest{Workflow: workflow(t, doc)})
	if err != nil {
		t.Fatal(err)
	}
	if state.Error == nil || !strings.Contains(state.Error.Message, "required") {
		t.Fatalf("state = %+v", state.Error)
	}
}
