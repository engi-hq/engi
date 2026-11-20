package porttest

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/port"
)

// ChatMemoryFactory builds a fresh, empty chat memory store bounded by cfg and
// registers its cleanup.
type ChatMemoryFactory func(t *testing.T, clk port.Clock, cfg config.AIMemoryConfig) port.ChatMemoryStore

// RunChatMemorySuite runs the port.ChatMemoryStore contract against an adapter.
func RunChatMemorySuite(t *testing.T, newStore ChatMemoryFactory) {
	t.Helper()
	limits := config.AIMemoryConfig{DefaultWindow: 2, MaxWindow: 5, MaxMessages: 5, Retention: time.Hour}
	cases := map[string]func(*testing.T, port.ChatMemoryStore, *FakeClock){
		"UnknownSessionIsEmpty":      memEmpty,
		"AppendKeepsOrder":           memOrder,
		"LoadReturnsLastN":           memLastN,
		"ToolMessagesRoundTrip":      memToolRoundTrip,
		"SessionsAreIsolated":        memIsolated,
		"ClearIsScopedAndIdempotent": memClear,
		"SessionCapDropsOldest":      memCap,
		"RetentionDropsOldMessages":  memRetention,
		"HostileKeyIsData":           memHostileKey,
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			clk := NewFakeClock()
			fn(t, newStore(t, clk, limits), clk)
		})
	}
}

func user(s string) port.LLMMessage { return port.LLMMessage{Role: port.RoleUser, Content: s} }

func mustAppend(t *testing.T, s port.ChatMemoryStore, key string, msgs ...port.LLMMessage) {
	t.Helper()
	if err := s.Append(context.Background(), key, msgs); err != nil {
		t.Fatalf("Append(%q): %v", key, err)
	}
}

func contents(t *testing.T, s port.ChatMemoryStore, key string, limit int) []string {
	t.Helper()
	got, err := s.Load(context.Background(), key, limit)
	if err != nil {
		t.Fatalf("Load(%q): %v", key, err)
	}
	out := make([]string, len(got))
	for i, m := range got {
		out[i] = m.Content
	}
	return out
}

func wantContents(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("messages = %v, want %v", got, want)
	}
}

func memEmpty(t *testing.T, s port.ChatMemoryStore, _ *FakeClock) {
	wantContents(t, contents(t, s, "nobody", 10))
}

func memOrder(t *testing.T, s port.ChatMemoryStore, _ *FakeClock) {
	mustAppend(t, s, "k", user("a"), user("b"))
	mustAppend(t, s, "k", user("c"))
	wantContents(t, contents(t, s, "k", 10), "a", "b", "c")
}

func memLastN(t *testing.T, s port.ChatMemoryStore, _ *FakeClock) {
	mustAppend(t, s, "k", user("a"), user("b"), user("c"), user("d"))
	wantContents(t, contents(t, s, "k", 2), "c", "d")
}

func memToolRoundTrip(t *testing.T, s port.ChatMemoryStore, _ *FakeClock) {
	msgs := []port.LLMMessage{
		user("weather?"),
		{Role: port.RoleAssistant, ToolCalls: []port.LLMToolCall{
			{ID: "c1", Name: "weather", Arguments: json.RawMessage(`{"city":"Oslo"}`)},
			{ID: "c2", Name: "time", Arguments: json.RawMessage(`{}`)},
		}},
		{Role: port.RoleTool, ToolCallID: "c1", Content: "rain"},
		{Role: port.RoleTool, ToolCallID: "c2", Content: "boom", IsError: true},
		{Role: port.RoleAssistant, Content: "It rains."},
	}
	mustAppend(t, s, "k", msgs...)
	got, err := s.Load(context.Background(), "k", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(msgs) {
		t.Fatalf("got %d messages, want %d", len(got), len(msgs))
	}
	for i := range msgs {
		w, g := msgs[i], got[i]
		if g.Role != w.Role || g.Content != w.Content || g.ToolCallID != w.ToolCallID || g.IsError != w.IsError || len(g.ToolCalls) != len(w.ToolCalls) {
			t.Fatalf("message %d = %+v, want %+v", i, g, w)
		}
		for j := range w.ToolCalls {
			if g.ToolCalls[j].ID != w.ToolCalls[j].ID || g.ToolCalls[j].Name != w.ToolCalls[j].Name ||
				string(g.ToolCalls[j].Arguments) != string(w.ToolCalls[j].Arguments) {
				t.Fatalf("message %d call %d = %+v, want %+v", i, j, g.ToolCalls[j], w.ToolCalls[j])
			}
		}
	}
}

func memIsolated(t *testing.T, s port.ChatMemoryStore, _ *FakeClock) {
	mustAppend(t, s, "a", user("one"))
	mustAppend(t, s, "b", user("two"))
	wantContents(t, contents(t, s, "a", 10), "one")
	wantContents(t, contents(t, s, "b", 10), "two")
}

func memClear(t *testing.T, s port.ChatMemoryStore, _ *FakeClock) {
	mustAppend(t, s, "a", user("one"))
	mustAppend(t, s, "b", user("two"))
	for range 2 {
		if err := s.Clear(context.Background(), "a"); err != nil {
			t.Fatal(err)
		}
	}
	wantContents(t, contents(t, s, "a", 10))
	wantContents(t, contents(t, s, "b", 10), "two")
}

func memCap(t *testing.T, s port.ChatMemoryStore, _ *FakeClock) {
	for i := range 8 {
		mustAppend(t, s, "k", user(fmt.Sprint(i)))
	}
	mustAppend(t, s, "other", user("keep"))
	wantContents(t, contents(t, s, "k", 100), "3", "4", "5", "6", "7")
	wantContents(t, contents(t, s, "other", 100), "keep")
}

func memRetention(t *testing.T, s port.ChatMemoryStore, clk *FakeClock) {
	mustAppend(t, s, "old", user("stale"))
	clk.Advance(2 * time.Hour)
	mustAppend(t, s, "new", user("fresh"))
	wantContents(t, contents(t, s, "old", 10))
	wantContents(t, contents(t, s, "new", 10), "fresh")
}

func memHostileKey(t *testing.T, s port.ChatMemoryStore, _ *FakeClock) {
	key := "wf'; DROP TABLE chat_messages;--:%_"
	mustAppend(t, s, key, user("x"))
	wantContents(t, contents(t, s, key, 10), "x")
	wantContents(t, contents(t, s, "wf", 10))
}
