package trigger_test

import (
	"context"
	"errors"
	"testing"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/node/chattrigger"
	"github.com/engi-hq/engi/internal/node/noop"
	"github.com/engi-hq/engi/internal/node/registry"
	"github.com/engi-hq/engi/internal/node/respondtowebhook"
)

func respondEnv(t *testing.T) *env {
	t.Helper()
	return newEnvWith(t, []registry.Factory{chattrigger.New, respondtowebhook.New}, blockingSleeper{}, nil)
}

func respondNode(name string, disabled bool) domain.Node {
	return domain.Node{Name: name, Type: respondtowebhook.Type, TypeVersion: 1, Disabled: disabled}
}

func passNode(name string, disabled bool) domain.Node {
	return domain.Node{Name: name, Type: noop.Type, TypeVersion: 1, Disabled: disabled}
}

func link(from, to string) domain.Connection {
	return domain.Connection{
		From: domain.Endpoint{Node: from, Port: domain.PortMain},
		To:   domain.Endpoint{Node: to, Port: domain.PortMain},
	}
}

// saveWired stores a workflow with connections, which env.save does not set.
func (e *env) saveWired(ctx context.Context, t *testing.T, conns []domain.Connection, nodes ...domain.Node) domain.Workflow {
	t.Helper()
	id, err := e.idSource.WorkflowID()
	if err != nil {
		t.Fatal(err)
	}
	wf, err := e.store.Save(ctx, domain.Workflow{ID: id, Name: "wf", Nodes: nodes, Connections: conns})
	if err != nil {
		t.Fatal(err)
	}
	return wf
}

func TestResponseNodeNeedsAReachableRespondToWebhook(t *testing.T) {
	e := respondEnv(t)
	ctx := context.Background()
	responseHook := func() domain.Node {
		return hookNode("Hook", map[string]any{"responseMode": "responseNode"})
	}
	cases := []struct {
		name    string
		nodes   []domain.Node
		conns   []domain.Connection
		wantErr bool
	}{
		{"no respond node", []domain.Node{responseHook()}, nil, true},
		{"respond node not connected", []domain.Node{responseHook(), respondNode("Reply", false)}, nil, true},
		{"respond node on another branch", []domain.Node{responseHook(), passNode("Other", false), respondNode("Reply", false)},
			[]domain.Connection{link("Other", "Reply")}, true},
		{"respond node disabled", []domain.Node{responseHook(), respondNode("Reply", true)},
			[]domain.Connection{link("Hook", "Reply")}, true},
		{"respond node connected", []domain.Node{responseHook(), respondNode("Reply", false)},
			[]domain.Connection{link("Hook", "Reply")}, false},
		{"respond node two hops away", []domain.Node{responseHook(), passNode("Set", false), respondNode("Reply", false)},
			[]domain.Connection{link("Hook", "Set"), link("Set", "Reply")}, false},
		{"reached through a disabled node", []domain.Node{responseHook(), passNode("Skip", true), respondNode("Reply", false)},
			[]domain.Connection{link("Hook", "Skip"), link("Skip", "Reply")}, false},
		{"last node mode needs no respond node", []domain.Node{hookNode("Hook", map[string]any{"responseMode": "lastNode"})}, nil, false},
		{"immediate mode needs no respond node", []domain.Node{hookNode("Hook", nil)}, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wf := e.saveWired(ctx, t, tc.conns, tc.nodes...)
			err := e.svc.Activate(ctx, wf.ID)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("Activate: %v", err)
				}
				return
			}
			var ve *domain.ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("err = %v, want a validation error", err)
			}
			if e.active(ctx, t, wf.ID) {
				t.Fatal("must stay inactive")
			}
		})
	}
}

func TestResponseNodeValidationNamesTheTrigger(t *testing.T) {
	e := respondEnv(t)
	ctx := context.Background()
	wf := e.saveWired(ctx, t, nil, hookNode("Hook", map[string]any{"responseMode": "responseNode"}))
	err := e.svc.Activate(ctx, wf.ID)
	var ve *domain.ValidationError
	if !errors.As(err, &ve) || len(ve.Issues) != 1 {
		t.Fatalf("err = %v, want one issue", err)
	}
	want := `Webhook "Hook" responds with a Respond to Webhook node, but the workflow has none connected after it`
	if ve.Issues[0].Path != "nodes[0]" || ve.Issues[0].Message != want {
		t.Fatalf("issue = %+v", ve.Issues[0])
	}
}

func TestChatResponseNodeNeedsAReachableRespondToWebhook(t *testing.T) {
	e := respondEnv(t)
	ctx := context.Background()
	chat := func() domain.Node {
		return domain.Node{Name: "Chat", Type: chattrigger.Type, TypeVersion: 1, Parameters: map[string]any{"responseMode": "responseNode"}}
	}
	missing := e.saveWired(ctx, t, nil, chat())
	err := e.svc.Activate(ctx, missing.ID)
	var ve *domain.ValidationError
	if !errors.As(err, &ve) || len(ve.Issues) != 1 ||
		ve.Issues[0].Message != `Chat Trigger "Chat" responds with a Respond to Webhook node, but the workflow has none connected after it` {
		t.Fatalf("err = %v", err)
	}
	if e.active(ctx, t, missing.ID) {
		t.Fatal("must stay inactive")
	}
	ok := e.saveWired(ctx, t, []domain.Connection{link("Chat", "Reply")}, chat(), respondNode("Reply", false))
	if err := e.svc.Activate(ctx, ok.ID); err != nil {
		t.Fatalf("Activate: %v", err)
	}
}

func TestArmTestNeedsAReachableRespondToWebhook(t *testing.T) {
	e := respondEnv(t)
	ctx := context.Background()
	hook := hookNode("Hook", map[string]any{"responseMode": "responseNode"})
	missing := e.saveWired(ctx, t, nil, hook)
	if _, err := e.svc.ArmTest(ctx, missing.ID, "Hook"); err == nil {
		t.Fatal("arming without a respond node must fail")
	} else {
		var ve *domain.ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("err = %v, want a validation error", err)
		}
	}
	ok := e.saveWired(ctx, t, []domain.Connection{link("Hook", "Reply")}, hook, respondNode("Reply", false))
	if _, err := e.svc.ArmTest(ctx, ok.ID, "Hook"); err != nil {
		t.Fatalf("ArmTest: %v", err)
	}
}
