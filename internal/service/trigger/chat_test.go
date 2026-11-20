package trigger_test

import (
	"context"
	"errors"
	"testing"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/node/chattrigger"
	"github.com/engi-hq/engi/internal/node/registry"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

func chatEnv(t *testing.T) *env {
	t.Helper()
	return newEnvWith(t, []registry.Factory{chattrigger.New}, blockingSleeper{}, nil)
}

func chatNode(name string, params map[string]any) domain.Node {
	return domain.Node{Name: name, Type: chattrigger.Type, TypeVersion: 1, Parameters: params}
}

func TestChatTriggerIsServedOnlyWhileActive(t *testing.T) {
	e := chatEnv(t)
	ctx := context.Background()
	wf := e.save(ctx, t, chatNode("Chat", map[string]any{"path": "support", "title": "Help", "allowedOrigins": "https://a.example"}))
	if _, err := e.svc.ResolveChat(ctx, "support"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("before activation: %v", err)
	}
	if err := e.svc.Activate(ctx, wf.ID); err != nil {
		t.Fatal(err)
	}
	r, err := e.svc.ResolveChat(ctx, "/support/")
	if err != nil {
		t.Fatal(err)
	}
	if r.WorkflowID != wf.ID || r.Node != "Chat" || r.Mode != domain.ModeWebhook || r.Spec.ResponseMode != nodesdk.ResponseLastNode ||
		r.Chat.Title != "Help" || len(r.Chat.AllowedOrigins) != 1 || r.Chat.ReplyField != "output" {
		t.Fatalf("route = %+v", r)
	}
	if err := e.svc.Deactivate(ctx, wf.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ResolveChat(ctx, "support"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("after deactivation: %v", err)
	}
}

func TestChatPathsAreUniqueAndSeparateFromWebhooks(t *testing.T) {
	e := chatEnv(t)
	ctx := context.Background()
	a := e.save(ctx, t, chatNode("Chat", map[string]any{"path": "same"}))
	b := e.save(ctx, t, chatNode("Chat", map[string]any{"path": "same"}))
	if err := e.svc.Activate(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Activate(ctx, b.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate chat path: %v", err)
	}
	// A webhook may use the same path: the namespaces are separate.
	h := e.save(ctx, t, hookNode("In", map[string]any{"httpMethod": "POST", "path": "same"}))
	if err := e.svc.Activate(ctx, h.ID); err != nil {
		t.Fatalf("webhook with the chat's path: %v", err)
	}
	// Webhook endpoints never reach a chat trigger, even with the pseudo method.
	if _, err := e.svc.ResolveLive(ctx, "CHAT", "same"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("ResolveLive(CHAT): %v", err)
	}
	if r, err := e.svc.ResolveLive(ctx, "POST", "same"); err != nil || r.Node != "In" {
		t.Fatalf("ResolveLive(POST) = %+v, %v", r, err)
	}
	// A chat trigger has no test URL, so it does not make test arming ambiguous.
	both := e.save(ctx, t, chatNode("Chat", map[string]any{"path": "other"}), hookNode("In", map[string]any{"path": "w"}))
	if tw, err := e.svc.ArmTest(ctx, both.ID, ""); err != nil || tw.Path != "w" {
		t.Fatalf("ArmTest = %+v, %v", tw, err)
	}
}

func TestChatTriggerActivationValidatesParameters(t *testing.T) {
	e := chatEnv(t)
	ctx := context.Background()
	wf := e.save(ctx, t, chatNode("Chat", map[string]any{"path": "x/widget"}))
	if err := e.svc.Activate(ctx, wf.ID); err == nil {
		t.Fatal("a path ending in a reserved name must fail")
	}
	wf = e.save(ctx, t, chatNode("Chat", map[string]any{"authentication": "basic"}))
	if err := e.svc.Activate(ctx, wf.ID); err == nil {
		t.Fatal("basic auth without a credential must fail")
	}
}
