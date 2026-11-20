package wait

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

type fixedClock struct{ now time.Time }

// secretInput is an Input that carries an execution's resume secret.
type secretInput struct {
	*nodetest.Input
	secret string
}

func (s secretInput) ResumeSecret() string { return s.secret }

func (c fixedClock) Now() time.Time { return c.now }

var epoch = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func TestExecute(t *testing.T) {
	n := New(fixedClock{epoch})
	d := n.Describe()
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		params    map[string]any
		wantUntil time.Time // zero = no wait
		wantToken bool
	}{
		{"default one second", nil, epoch.Add(time.Second), false},
		{"seconds", map[string]any{"amount": 30.0, "unit": "seconds"}, epoch.Add(30 * time.Second), false},
		{"minutes", map[string]any{"amount": 5.0, "unit": "minutes"}, epoch.Add(5 * time.Minute), false},
		{"hours", map[string]any{"amount": 2.0, "unit": "hours"}, epoch.Add(2 * time.Hour), false},
		{"days", map[string]any{"amount": 1.0, "unit": "days"}, epoch.Add(24 * time.Hour), false},
		{"fractional", map[string]any{"amount": 0.5, "unit": "minutes"}, epoch.Add(30 * time.Second), false},
		{"numeric string amount", map[string]any{"amount": "3", "unit": "seconds"}, epoch.Add(3 * time.Second), false},
		{"zero continues at once", map[string]any{"amount": 0.0}, time.Time{}, false},
		{"specific time in the future", map[string]any{"resume": "specificTime", "dateTime": "2026-03-02T00:00:00Z"}, time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC), false},
		{"specific time with offset", map[string]any{"resume": "specificTime", "dateTime": "2026-03-01T14:00:00+02:00"}, time.Time{}, false},
		{"specific time already passed", map[string]any{"resume": "specificTime", "dateTime": "2020-01-01"}, time.Time{}, false},
		{"webhook is a token wait", map[string]any{"resume": "webhook"}, time.Time{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := secretInput{nodetest.New(d, nodetest.Items(map[string]any{"a": 1.0}), tt.params), "secret_1"}
			in.Info = nodesdk.ExecutionInfo{ExecutionID: "ex_1", NodeName: "Wait", RunIndex: 0}
			out := nodetest.Run(t, n, in)
			if len(out.Ports[0]) != 1 || out.Ports[0][0].JSON["a"] != 1.0 {
				t.Fatalf("items must pass through, got %v", out.Ports)
			}
			switch {
			case tt.wantToken:
				if out.Wait == nil || out.Wait.ResumeToken == "" || !out.Wait.Until.IsZero() {
					t.Fatalf("wait = %+v", out.Wait)
				}
			case tt.wantUntil.IsZero():
				if out.Wait != nil {
					t.Fatalf("expected no wait, got %+v", out.Wait)
				}
			default:
				if out.Wait == nil || !out.Wait.Until.Equal(tt.wantUntil) {
					t.Fatalf("wait = %+v, want until %v", out.Wait, tt.wantUntil)
				}
			}
		})
	}
	t.Run("resume token is the execution id and its secret", func(t *testing.T) {
		in := secretInput{nodetest.New(d, nodetest.Items(map[string]any{}), map[string]any{"resume": "webhook"}), "secret_1"}
		in.Info = nodesdk.ExecutionInfo{ExecutionID: "ex_1", NodeName: "Wait"}
		if tok := nodetest.Run(t, n, in).Wait.ResumeToken; tok != "ex_1.secret_1" {
			t.Fatalf("token = %q, want ex_1.secret_1", tok)
		}
	})
	t.Run("two waits of one execution share the token", func(t *testing.T) {
		var tokens []string
		for _, name := range []string{"Wait A", "Wait B"} {
			in := secretInput{nodetest.New(d, nodetest.Items(map[string]any{}), map[string]any{"resume": "webhook"}), "secret_1"}
			in.Info = nodesdk.ExecutionInfo{ExecutionID: "ex_1", NodeName: name}
			tokens = append(tokens, nodetest.Run(t, n, in).Wait.ResumeToken)
		}
		if tokens[0] != tokens[1] {
			t.Fatalf("tokens differ: %v", tokens)
		}
	})
	t.Run("input without a resume secret is an error", func(t *testing.T) {
		_, err := n.Execute(context.Background(), nodetest.New(d, nodetest.Items(map[string]any{}), map[string]any{"resume": "webhook"}))
		if err == nil {
			t.Fatal("expected an error")
		}
	})
	t.Run("empty input still waits", func(t *testing.T) {
		out := nodetest.Run(t, n, nodetest.New(d, nil, nil))
		if out.Wait == nil {
			t.Fatal("expected a wait")
		}
	})
}

func TestErrors(t *testing.T) {
	n := New(fixedClock{epoch})
	tests := []struct {
		name   string
		params map[string]any
		want   string
	}{
		{"negative", map[string]any{"amount": -1.0}, "negative"},
		{"unknown unit", map[string]any{"unit": "fortnights"}, "unknown unit"},
		{"overflow", map[string]any{"amount": 1e12, "unit": "days"}, "maximum"},
		{"unknown resume", map[string]any{"resume": "never"}, "unknown resume"},
		{"missing date", map[string]any{"resume": "specificTime"}, "dateTime is required"},
		{"bad date", map[string]any{"resume": "specificTime", "dateTime": "later"}, "dateTime"},
		{"bad amount", map[string]any{"amount": "lots"}, "amount"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := n.Execute(context.Background(), nodetest.New(n.Describe(), nodetest.Items(map[string]any{}), tt.params))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}
