package aimemorywindow_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/engi-hq/engi/internal/node/aimemorywindow"
	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

type fakeStore struct {
	msgs     map[string][]port.LLMMessage
	lastLoad int
}

func (f *fakeStore) Load(_ context.Context, key string, limit int) ([]port.LLMMessage, error) {
	f.lastLoad = limit
	m := f.msgs[key]
	if len(m) > limit {
		m = m[len(m)-limit:]
	}
	return m, nil
}

func (f *fakeStore) Append(_ context.Context, key string, msgs []port.LLMMessage) error {
	f.msgs[key] = append(f.msgs[key], msgs...)
	return nil
}

func (f *fakeStore) Clear(_ context.Context, key string) error { delete(f.msgs, key); return nil }

func newNode(store port.ChatMemoryStore) *aimemorywindow.Node {
	return aimemorywindow.New(aimemorywindow.Config{Store: store, DefaultWindow: 4, MaxWindow: 10})
}

func supply(t *testing.T, n *aimemorywindow.Node, wf string, params map[string]any) (port.ChatMemory, error) {
	t.Helper()
	in := nodetest.New(n.Describe(), nil, params)
	in.Info.WorkflowID = wf
	v, err := n.Supply(context.Background(), in)
	if err != nil {
		return nil, err
	}
	return v.(port.ChatMemory), nil
}

func TestDescriptor(t *testing.T) {
	d := newNode(nil).Describe()
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if !d.IsSubNode() || d.Outputs[0].Kind != nodesdk.PortAIMemory {
		t.Fatalf("descriptor = %+v", d)
	}
}

func TestRoundTripAndScoping(t *testing.T) {
	store := &fakeStore{msgs: map[string][]port.LLMMessage{}}
	n := newNode(store)
	ctx := context.Background()
	a, err := supply(t, n, "wf1", map[string]any{"sessionKey": "s1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Save(ctx, []port.LLMMessage{{Role: port.RoleUser, Content: "hi"}, {Role: port.RoleAssistant, Content: "hello"}}); err != nil {
		t.Fatal(err)
	}
	same, _ := supply(t, n, "wf1", map[string]any{"sessionKey": "s1"})
	got, err := same.History(ctx)
	if err != nil || len(got) != 2 || got[1].Content != "hello" {
		t.Fatalf("history = %v, %v", got, err)
	}
	if store.lastLoad != 4 {
		t.Fatalf("default window = %d, want 4", store.lastLoad)
	}
	other, _ := supply(t, n, "wf2", map[string]any{"sessionKey": "s1"})
	if got, _ := other.History(ctx); len(got) != 0 {
		t.Fatalf("another workflow sees %v", got)
	}
	if err := same.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := same.History(ctx); len(got) != 0 {
		t.Fatalf("after clear = %v", got)
	}
}

func TestWindowStartsAtUserMessage(t *testing.T) {
	store := &fakeStore{msgs: map[string][]port.LLMMessage{}}
	n := newNode(store)
	m, _ := supply(t, n, "w", map[string]any{"sessionKey": "s", "windowSize": float64(3)})
	_ = m.Save(context.Background(), []port.LLMMessage{
		{Role: port.RoleUser, Content: "q"},
		{Role: port.RoleAssistant, ToolCalls: []port.LLMToolCall{{ID: "1", Name: "t", Arguments: []byte("{}")}}},
		{Role: port.RoleTool, ToolCallID: "1", Content: "r"},
		{Role: port.RoleAssistant, Content: "a"},
	})
	got, _ := m.History(context.Background())
	if len(got) != 0 { // the last 3 are assistant, tool, assistant: no user message
		t.Fatalf("history = %v, want none", got)
	}
}

func TestSupplyErrors(t *testing.T) {
	store := &fakeStore{msgs: map[string][]port.LLMMessage{}}
	n := newNode(store)
	if _, err := supply(t, n, "w", map[string]any{"sessionKey": "  "}); err == nil || !strings.Contains(err.Error(), "session key") {
		t.Fatalf("empty key err = %v", err)
	}
	if _, err := supply(t, n, "w", map[string]any{"sessionKey": "s", "windowSize": float64(11)}); err == nil {
		t.Fatal("window over the cap must fail")
	}
	if _, err := supply(t, n, "w", map[string]any{"sessionKey": "s", "windowSize": float64(-1)}); err == nil {
		t.Fatal("negative window must fail")
	}
	if _, err := supply(t, newNode(nil), "w", map[string]any{"sessionKey": "s"}); !errors.Is(err, nodesdk.ErrUnavailable) {
		t.Fatalf("nil store err = %v", err)
	}
}

func TestStoreKeyIsUnambiguous(t *testing.T) {
	if aimemorywindow.StoreKey("a:b", "c") == aimemorywindow.StoreKey("a", "b:c") {
		t.Fatal("keys collide")
	}
}
