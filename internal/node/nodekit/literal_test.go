package nodekit

import (
	"context"
	"strings"
	"testing"

	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// storedInput is a nodetest.Input that also reports the parameters as stored,
// the way the engine's RawParamSource does.
type storedInput struct {
	*nodetest.Input
	stored map[string]any
}

func (s storedInput) RawParam(name string, _ int) (nodesdk.Value, bool) {
	v, ok := s.stored[name]
	return v, ok
}

func condsOf(left any, op string, right any) []any {
	return []any{map[string]any{"left": left, "operator": op, "right": right}}
}

func TestLiteralOperands(t *testing.T) {
	tests := []struct {
		name     string
		stored   any
		resolved any
		loose    bool
		noRaw    bool
		wantTrue bool
		wantErr  string
	}{
		{name: "literal number text is converted", stored: condsOf("13", "number:gt", "12"), resolved: condsOf("13", "number:gt", "12"), wantTrue: true},
		{name: "literal number with spaces", stored: condsOf("13", "number:gt", " 12 "), resolved: condsOf("13", "number:gt", " 12 "), wantTrue: true},
		{name: "literal number that is not a number fails", stored: condsOf("13", "number:gt", "abc"), resolved: condsOf("13", "number:gt", "abc"),
			wantErr: `conditions[0] (Number: is greater than): value 2: cannot convert "abc" to a number`},
		{name: "empty literal number fails", stored: condsOf("", "number:equals", "1"), resolved: condsOf("", "number:equals", "1"),
			wantErr: `value 1: cannot convert "" to a number`},
		{name: "literal boolean true", stored: condsOf("true", "boolean:isTrue", nil), resolved: condsOf("true", "boolean:isTrue", nil), wantTrue: true},
		{name: "literal boolean false", stored: condsOf("false", "boolean:isFalse", nil), resolved: condsOf("false", "boolean:isFalse", nil), wantTrue: true},
		{name: "literal boolean that is not one fails", stored: condsOf("maybe", "boolean:isTrue", nil), resolved: condsOf("maybe", "boolean:isTrue", nil),
			wantErr: `cannot convert "maybe" to a boolean`},
		{name: "literal array length converted", stored: condsOf([]any{1.0, 2.0}, "array:lengthEquals", "2"),
			resolved: condsOf([]any{1.0, 2.0}, "array:lengthEquals", "2"), wantTrue: true},
		{name: "literal date text stays a date", stored: condsOf("2026-02-01", "dateTime:after", "2026-01-01"),
			resolved: condsOf("2026-02-01", "dateTime:after", "2026-01-01"), wantTrue: true},
		{name: "literal invalid date fails at evaluation", stored: condsOf("soon", "dateTime:after", "2026-01-01"),
			resolved: condsOf("soon", "dateTime:after", "2026-01-01"), wantErr: "ISO-8601"},
		{name: "string operators keep literal text", stored: condsOf("12", "string:equals", "12"), resolved: condsOf("12", "string:equals", "12"), wantTrue: true},
		{name: "expression result stays strict", stored: condsOf("={{ $json.n }}", "number:gt", "={{ $json.min }}"),
			resolved: condsOf("13", "number:gt", "12"), wantErr: "expected a number"},
		{name: "expression result converted with looseTypes", stored: condsOf("={{ $json.n }}", "number:gt", "={{ $json.min }}"),
			resolved: condsOf("13", "number:gt", "12"), loose: true, wantTrue: true},
		{name: "literal next to expression converts only the literal", stored: condsOf("={{ $json.n }}", "number:gt", "12"),
			resolved: condsOf("13", "number:gt", "12"), wantErr: "expected a number"},
		{name: "whole parameter from expression stays strict", stored: "={{ $json.conds }}",
			resolved: condsOf("13", "number:gt", "12"), wantErr: "expected a number"},
		{name: "no raw source keeps values strict", stored: condsOf("13", "number:gt", "12"),
			resolved: condsOf("13", "number:gt", "12"), noRaw: true, wantErr: "expected a number"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := nodetest.New(nodesdk.Descriptor{}, nil, map[string]any{"conditions": tt.resolved, "looseTypes": tt.loose})
			var src nodesdk.Input = in
			if !tt.noRaw {
				src = storedInput{Input: in, stored: map[string]any{"conditions": tt.stored}}
			}
			g, o, err := ReadGroup(context.Background(), src, 0)
			if err == nil {
				var ok bool
				ok, err = g.Eval(o)
				if err == nil && ok != tt.wantTrue {
					t.Fatalf("got %v, want %v", ok, tt.wantTrue)
				}
			}
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestLiteralConditionValuesAreTyped(t *testing.T) {
	in := nodetest.New(nodesdk.Descriptor{}, nil, map[string]any{"conditions": condsOf("13", "number:gt", "12")})
	g, _, err := ReadGroup(context.Background(), storedInput{Input: in, stored: map[string]any{"conditions": condsOf("13", "number:gt", "12")}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := g.Conditions[0]; got.Left != 13.0 || got.Right != 12.0 {
		t.Fatalf("left=%#v right=%#v, want 13.0 and 12.0", got.Left, got.Right)
	}
}

func TestLiteralFromExpressionPrefixIsNotCoerced(t *testing.T) {
	if ExpressionPrefix != "=" {
		t.Fatalf("ExpressionPrefix = %q, the engine resolves \"=\"", ExpressionPrefix)
	}
	got, err := coerceLiteral("=12", "=12", TypeNumber)
	if err != nil || got != "=12" {
		t.Fatalf("got %#v, %v; a string with the prefix must stay as is", got, err)
	}
}
