package llmkit_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/llmkit"
	"github.com/engi-hq/engi/internal/port"
)

func TestReadSSE(t *testing.T) {
	in := ": comment\nevent: a\ndata: one\n\ndata: two\ndata: three\n\nevent: last\ndata: tail"
	type ev struct{ name, data string }
	var got []ev
	if err := llmkit.ReadSSE(strings.NewReader(in), func(e, d string) error { got = append(got, ev{e, d}); return nil }); err != nil {
		t.Fatal(err)
	}
	want := []ev{{"a", "one"}, {"", "two\nthree"}, {"last", "tail"}}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d = %v, want %v", i, got[i], want[i])
		}
	}
	stop := errors.New("stop")
	if err := llmkit.ReadSSE(strings.NewReader("data: x\n\n"), func(string, string) error { return stop }); !errors.Is(err, stop) {
		t.Fatalf("err = %v", err)
	}
}

func TestReadNDJSON(t *testing.T) {
	var got []string
	err := llmkit.ReadNDJSON(strings.NewReader("{\"a\":1}\r\n\n  {\"b\":2}  \n{\"c\":3}"), func(l []byte) error { got = append(got, string(l)); return nil })
	if err != nil || strings.Join(got, ";") != `{"a":1};{"b":2};{"c":3}` {
		t.Fatalf("got %v err %v", got, err)
	}
}

func TestErrorMessage(t *testing.T) {
	long := strings.Repeat("x", 800)
	for name, tc := range map[string]struct{ body, want string }{
		"nested":  {`{"error":{"message":"bad"}}`, "bad"},
		"string":  {`{"error":"plain"}`, "plain"},
		"top":     {`{"message":"top"}`, "top"},
		"raw":     {" not json ", "not json"},
		"empty":   {"", ""},
		"clipped": {long, strings.Repeat("x", 500) + "..."},
	} {
		if got := llmkit.ErrorMessage([]byte(tc.body)); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
}

func TestNormalizeArgs(t *testing.T) {
	for in, want := range map[string]string{"": "{}", "null": "{}", "\t{\"a\":1}": `{"a":1}`} {
		if got := string(llmkit.NormalizeArgs([]byte(in))); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

type fakeLLM struct {
	resp port.LLMResponse
	err  error
	ctx  context.Context
}

func (f *fakeLLM) Chat(ctx context.Context, _ port.LLMRequest) (port.LLMResponse, error) {
	f.ctx = ctx
	return f.resp, f.err
}

func (f *fakeLLM) Stream(ctx context.Context, r port.LLMRequest, _ func(port.StreamEvent) error) (port.LLMResponse, error) {
	return f.Chat(ctx, r)
}

type recordedCall struct {
	provider, model, outcome string
	d                        time.Duration
	usage                    port.LLMUsage
}

type recMetrics struct {
	port.NopMetrics
	calls []recordedCall
}

func (m *recMetrics) LLMCall(p, mo, o string, d time.Duration, u port.LLMUsage) {
	m.calls = append(m.calls, recordedCall{p, mo, o, d, u})
}

func TestWithMetrics(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantOut string
		usage   port.LLMUsage
	}{
		{"ok", nil, port.LLMOutcomeOK, port.LLMUsage{InputTokens: 4, OutputTokens: 5}},
		{"rate limited", &port.LLMError{Status: 429, Retryable: true}, port.LLMOutcomeRateLimited, port.LLMUsage{}},
		{"error", &port.LLMError{Status: 500}, port.LLMOutcomeError, port.LLMUsage{}},
		{"plain error", errors.New("boom"), port.LLMOutcomeError, port.LLMUsage{}},
	}
	for _, tt := range tests {
		for _, stream := range []bool{false, true} {
			t.Run(tt.name, func(t *testing.T) {
				rec := &recMetrics{}
				tick := time.Unix(0, 0)
				now := func() time.Time { tick = tick.Add(time.Second); return tick }
				l := llmkit.WithMetrics(&fakeLLM{resp: port.LLMResponse{Usage: tt.usage}, err: tt.err}, "anthropic", rec, now)
				var err error
				if stream {
					_, err = l.Stream(context.Background(), port.LLMRequest{Model: "claude-x"}, nil)
				} else {
					_, err = l.Chat(context.Background(), port.LLMRequest{Model: "claude-x"})
				}
				if !errors.Is(err, tt.err) {
					t.Fatalf("err = %v", err)
				}
				want := recordedCall{"anthropic", "claude-x", tt.wantOut, time.Second, tt.usage}
				if len(rec.calls) != 1 || rec.calls[0] != want {
					t.Fatalf("calls = %+v, want %+v", rec.calls, want)
				}
			})
		}
	}
}

func TestWithTimeout(t *testing.T) {
	f := &fakeLLM{}
	l := llmkit.WithTimeout(f, time.Minute)
	if _, err := l.Chat(context.Background(), port.LLMRequest{}); err != nil {
		t.Fatal(err)
	}
	if dl, ok := f.ctx.Deadline(); !ok || time.Until(dl) > time.Minute {
		t.Fatalf("deadline = %v %v", dl, ok)
	}
	if _, err := l.Stream(context.Background(), port.LLMRequest{}, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.ctx.Deadline(); !ok {
		t.Fatal("stream has no deadline")
	}
}
