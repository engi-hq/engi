package nodekit

import (
	"reflect"
	"strings"
	"testing"
)

func TestGetPath(t *testing.T) {
	data := map[string]any{
		"a":     map[string]any{"b": []any{map[string]any{"c": 1.0}, "x"}},
		"dot.k": "literal",
		"nil":   nil,
	}
	tests := []struct {
		path   string
		want   any
		wantOK bool
	}{
		{"a.b[0].c", 1.0, true},
		{"a.b[1]", "x", true},
		{"a.b[2]", nil, false},
		{"a.missing", nil, false},
		{"dot.k", "literal", true},
		{"nil", nil, true},
		{"a.b.c", nil, false},
		{"a[0]", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got, ok := GetPath(data, tt.path)
			if ok != tt.wantOK || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("GetPath(%q) = %v, %v; want %v, %v", tt.path, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestSetPath(t *testing.T) {
	tests := []struct {
		name  string
		start map[string]any
		path  string
		dot   bool
		want  map[string]any
	}{
		{"flat", map[string]any{}, "a", true, map[string]any{"a": 1.0}},
		{"nested creates", map[string]any{}, "a.b.c", true, map[string]any{"a": map[string]any{"b": map[string]any{"c": 1.0}}}},
		{"literal when dot off", map[string]any{}, "a.b", false, map[string]any{"a.b": 1.0}},
		{"keeps siblings", map[string]any{"a": map[string]any{"x": 2.0}}, "a.b", true, map[string]any{"a": map[string]any{"x": 2.0, "b": 1.0}}},
		{"replaces scalar in the way", map[string]any{"a": "str"}, "a.b", true, map[string]any{"a": map[string]any{"b": 1.0}}},
		{"existing array index", map[string]any{"a": []any{0.0, 0.0}}, "a[1]", true, map[string]any{"a": []any{0.0, 1.0}}},
		{"empty path is a no-op", map[string]any{}, "", true, map[string]any{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			SetPath(tt.start, tt.path, 1.0, tt.dot)
			if !reflect.DeepEqual(tt.start, tt.want) {
				t.Fatalf("got %v, want %v", tt.start, tt.want)
			}
		})
	}
}

func TestDeletePath(t *testing.T) {
	m := map[string]any{"a": map[string]any{"b": 1.0, "c": 2.0}, "x.y": 3.0}
	if !DeletePath(m, "a.b") || DeletePath(m, "a.b") {
		t.Fatal("first delete should succeed, second report false")
	}
	if !DeletePath(m, "x.y") {
		t.Fatal("literal dotted key should be deleted")
	}
	if DeletePath(m, "nope.z") || DeletePath(m, "") {
		t.Fatal("missing paths must report false")
	}
	if want := map[string]any{"a": map[string]any{"c": 2.0}}; !reflect.DeepEqual(m, want) {
		t.Fatalf("got %v", m)
	}
}

func TestCloneIsDeep(t *testing.T) {
	src := map[string]any{"a": []any{map[string]any{"b": 1.0}}}
	c := CloneMap(src)
	c["a"].([]any)[0].(map[string]any)["b"] = 2.0
	if src["a"].([]any)[0].(map[string]any)["b"] != 1.0 {
		t.Fatal("clone shares memory with the source")
	}
	if CloneMap(nil) != nil {
		t.Fatal("nil map must stay nil")
	}
}

func TestKeyAndEqual(t *testing.T) {
	if !Equal(map[string]any{"a": 1.0, "b": 2.0}, map[string]any{"b": 2.0, "a": 1.0}) {
		t.Fatal("map equality must ignore order")
	}
	if !Equal(1, 1.0) {
		t.Fatal("int and float of the same value are equal")
	}
	if Equal("1", 1.0) {
		t.Fatal("string and number differ")
	}
}

func TestConversions(t *testing.T) {
	t.Run("ToFloat", func(t *testing.T) {
		tests := []struct {
			in      any
			loose   bool
			want    float64
			wantErr bool
		}{
			{1.5, false, 1.5, false},
			{int64(3), false, 3, false},
			{"2.5", false, 0, true},
			{"2.5", true, 2.5, false},
			{" 7 ", true, 7, false},
			{"abc", true, 0, true},
			{"NaN", true, 0, true},
			{"Inf", true, 0, true},
			{true, true, 1, false},
			{nil, true, 0, false},
			{nil, false, 0, true},
			{[]any{}, true, 0, true},
		}
		for _, tt := range tests {
			got, err := ToFloat(tt.in, tt.loose)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("ToFloat(%v, %v) = %v, %v", tt.in, tt.loose, got, err)
			}
		}
	})
	t.Run("ToBool", func(t *testing.T) {
		tests := []struct {
			in      any
			loose   bool
			want    bool
			wantErr bool
		}{
			{true, false, true, false},
			{"true", false, false, true},
			{"true", true, true, false},
			{"No", true, false, false},
			{"maybe", true, false, true},
			{1.0, true, true, false},
			{0.0, true, false, false},
		}
		for _, tt := range tests {
			got, err := ToBool(tt.in, tt.loose)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("ToBool(%v, %v) = %v, %v", tt.in, tt.loose, got, err)
			}
		}
	})
	t.Run("ToString", func(t *testing.T) {
		tests := map[string]any{"": nil, "x": "x", "true": true, "1.5": 1.5, "3": 3.0, `{"a":1}`: map[string]any{"a": 1.0}, "[1,2]": []any{1.0, 2.0}}
		for want, in := range tests {
			if got := ToString(in); got != want {
				t.Errorf("ToString(%v) = %q, want %q", in, got, want)
			}
		}
	})
}

func TestCoerce(t *testing.T) {
	tests := []struct {
		name    string
		v       any
		typ     string
		want    any
		wantErr string
	}{
		{"auto keeps", "5", "auto", "5", ""},
		{"empty type keeps", 5.0, "", 5.0, ""},
		{"string", 5.0, "string", "5", ""},
		{"number", "5", "number", 5.0, ""},
		{"bad number", "x", "number", nil, "convert"},
		{"boolean", "true", "boolean", true, ""},
		{"array from json", "[1,2]", "array", []any{1.0, 2.0}, ""},
		{"array passthrough", []any{1.0}, "array", []any{1.0}, ""},
		{"array bad", 3.0, "array", nil, "array"},
		{"array bad json", "[", "array", nil, "parse"},
		{"object from json", `{"a":1}`, "object", map[string]any{"a": 1.0}, ""},
		{"object bad", 3.0, "object", nil, "object"},
		{"unknown", 1.0, "weird", nil, "unknown type"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Coerce(tt.v, tt.typ)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, %v; want %v", got, err, tt.want)
			}
		})
	}
}

func TestKeyValues(t *testing.T) {
	got, err := KeyValues([]any{map[string]any{"name": "a", "value": 1.0}, map[string]any{"name": "", "value": 2.0}})
	if err != nil || !reflect.DeepEqual(got, []KeyValue{{"a", 1.0}}) {
		t.Fatalf("array form: %v %v", got, err)
	}
	got, err = KeyValues(map[string]any{"b": "2", "a": "1"})
	if err != nil || !reflect.DeepEqual(got, []KeyValue{{"a", "1"}, {"b", "2"}}) {
		t.Fatalf("object form: %v %v", got, err)
	}
	if got, err := KeyValues(nil); got != nil || err != nil {
		t.Fatalf("nil: %v %v", got, err)
	}
	if _, err := KeyValues("nope"); err == nil {
		t.Fatal("expected error for a string")
	}
	if _, err := KeyValues([]any{"x"}); err == nil {
		t.Fatal("expected error for non-object entries")
	}
}

func TestStringList(t *testing.T) {
	got, err := StringList(" a, b ,,c")
	if err != nil || !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("%v %v", got, err)
	}
	got, err = StringList([]any{"x", " y "})
	if err != nil || !reflect.DeepEqual(got, []string{"x", "y"}) {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := StringList([]any{1.0}); err == nil {
		t.Fatal("expected error for non-string entries")
	}
	if _, err := StringList(1.0); err == nil {
		t.Fatal("expected error for a number")
	}
}

func TestToTime(t *testing.T) {
	for _, s := range []string{"2026-01-02", "2026-01-02T03:04:05", "2026-01-02 03:04:05", "2026-01-02T03:04:05Z", "2026-01-02T03:04:05+02:00"} {
		if _, err := ToTime(s); err != nil {
			t.Errorf("ToTime(%q): %v", s, err)
		}
	}
	for _, bad := range []any{"yesterday", 12.0, nil} {
		if _, err := ToTime(bad); err == nil {
			t.Errorf("ToTime(%v) should fail", bad)
		}
	}
}

func TestCompare(t *testing.T) {
	if Compare(1.0, 2.0) >= 0 || Compare("b", "a") <= 0 || Compare(true, false) <= 0 || Compare(2.0, 2) != 0 {
		t.Fatal("unexpected ordering")
	}
}

func TestConditions(t *testing.T) {
	strict, loose, fold := EvalOptions{}, EvalOptions{Loose: true}, EvalOptions{IgnoreCase: true}
	tests := []struct {
		name    string
		c       Condition
		o       EvalOptions
		want    bool
		wantErr string
	}{
		{"string equals", Condition{"a", "string:equals", "a"}, strict, true, ""},
		{"string equals ignore case", Condition{"A", "string:equals", "a"}, fold, true, ""},
		{"string equals case sensitive", Condition{"A", "string:equals", "a"}, strict, false, ""},
		{"string not equals", Condition{"a", "string:notEquals", "b"}, strict, true, ""},
		{"string contains", Condition{"hello world", "string:contains", "lo w"}, strict, true, ""},
		{"string not contains", Condition{"hello", "string:notContains", "z"}, strict, true, ""},
		{"starts with", Condition{"hello", "string:startsWith", "he"}, strict, true, ""},
		{"ends with", Condition{"hello", "string:endsWith", "lo"}, strict, true, ""},
		{"regex", Condition{"abc123", "string:regex", `\d+$`}, strict, true, ""},
		{"regex ignore case", Condition{"ABC", "string:regex", `^abc$`}, fold, true, ""},
		{"not regex", Condition{"abc", "string:notRegex", `\d`}, strict, true, ""},
		{"bad regex", Condition{"abc", "string:regex", `(`}, strict, false, "regular expression"},
		{"string empty nil", Condition{nil, "string:isEmpty", nil}, strict, true, ""},
		{"string not empty", Condition{"x", "string:isNotEmpty", nil}, strict, true, ""},
		{"string op on number strict", Condition{5.0, "string:equals", "5"}, strict, false, "expected a string"},
		{"string op on number loose", Condition{5.0, "string:equals", "5"}, loose, true, ""},
		{"number equals", Condition{2.0, "number:equals", 2.0}, strict, true, ""},
		{"number gt", Condition{3.0, "number:gt", 2.0}, strict, true, ""},
		{"number gte equal", Condition{2.0, "number:gte", 2.0}, strict, true, ""},
		{"number lt", Condition{1.0, "number:lt", 2.0}, strict, true, ""},
		{"number lte", Condition{3.0, "number:lte", 2.0}, strict, false, ""},
		{"number not equals", Condition{3.0, "number:notEquals", 2.0}, strict, true, ""},
		{"number string strict", Condition{"3", "number:gt", 2.0}, strict, false, "expected a number"},
		{"number string loose", Condition{"3", "number:gt", "2"}, loose, true, ""},
		{"number bad right", Condition{3.0, "number:gt", "x"}, loose, false, "right value"},
		{"bool true", Condition{true, "boolean:isTrue", nil}, strict, true, ""},
		{"bool false", Condition{false, "boolean:isFalse", nil}, strict, true, ""},
		{"bool string strict", Condition{"true", "boolean:isTrue", nil}, strict, false, "expected a boolean"},
		{"bool string loose", Condition{"true", "boolean:isTrue", nil}, loose, true, ""},
		{"date after", Condition{"2026-02-01", "dateTime:after", "2026-01-01"}, strict, true, ""},
		{"date before", Condition{"2026-01-01T00:00:00Z", "dateTime:before", "2026-01-01T00:00:01Z"}, strict, true, ""},
		{"date equals across offsets", Condition{"2026-01-01T02:00:00+02:00", "dateTime:equals", "2026-01-01T00:00:00Z"}, strict, true, ""},
		{"date after or equals", Condition{"2026-01-01", "dateTime:afterOrEquals", "2026-01-01"}, strict, true, ""},
		{"date before or equals", Condition{"2026-01-02", "dateTime:beforeOrEquals", "2026-01-01"}, strict, false, ""},
		{"date not equals", Condition{"2026-01-02", "dateTime:notEquals", "2026-01-01"}, strict, true, ""},
		{"date invalid", Condition{"soon", "dateTime:after", "2026-01-01"}, strict, false, "ISO-8601"},
		{"array contains", Condition{[]any{"a", "b"}, "array:contains", "b"}, strict, true, ""},
		{"array not contains", Condition{[]any{"a"}, "array:notContains", "z"}, strict, true, ""},
		{"array contains loose numbers", Condition{[]any{1.0, 2.0}, "array:contains", "2"}, loose, true, ""},
		{"array length equals", Condition{[]any{1.0, 2.0}, "array:lengthEquals", 2.0}, strict, true, ""},
		{"array length not equals", Condition{[]any{1.0}, "array:lengthNotEquals", 2.0}, strict, true, ""},
		{"array length gt", Condition{[]any{1.0, 2.0}, "array:lengthGt", 1.0}, strict, true, ""},
		{"array length gte", Condition{[]any{1.0}, "array:lengthGte", 1.0}, strict, true, ""},
		{"array length lt", Condition{[]any{}, "array:lengthLt", 1.0}, strict, true, ""},
		{"array length lte", Condition{[]any{1.0, 2.0}, "array:lengthLte", 1.0}, strict, false, ""},
		{"array empty", Condition{nil, "array:isEmpty", nil}, strict, true, ""},
		{"array not empty", Condition{[]any{1.0}, "array:isNotEmpty", nil}, strict, true, ""},
		{"array op on string strict", Condition{"a", "array:isEmpty", nil}, strict, false, "expected an array"},
		{"object empty", Condition{map[string]any{}, "object:isEmpty", nil}, strict, true, ""},
		{"object not empty", Condition{map[string]any{"a": 1.0}, "object:isNotEmpty", nil}, strict, true, ""},
		{"object op on array strict", Condition{[]any{}, "object:isEmpty", nil}, strict, false, "expected an object"},
		{"exists", Condition{"x", "exists", nil}, strict, true, ""},
		{"exists false for null", Condition{nil, "exists", nil}, strict, false, ""},
		{"not exists", Condition{nil, "notExists", nil}, strict, true, ""},
		{"unknown operator", Condition{1.0, "number:between", 1.0}, strict, false, "unknown operator"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.c.Eval(tt.o)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got %v, %v; want %v", got, err, tt.want)
			}
		})
	}
}

func TestGroupEval(t *testing.T) {
	yes := Condition{1.0, "number:equals", 1.0}
	no := Condition{1.0, "number:equals", 2.0}
	bad := Condition{"x", "number:equals", 1.0}
	tests := []struct {
		name    string
		g       Group
		want    bool
		wantErr bool
	}{
		{"empty is true", Group{}, true, false},
		{"and all true", Group{CombinatorAnd, []Condition{yes, yes}}, true, false},
		{"and one false", Group{CombinatorAnd, []Condition{yes, no}}, false, false},
		{"default combinator is and", Group{"", []Condition{yes, no}}, false, false},
		{"or one true", Group{CombinatorOr, []Condition{no, yes}}, true, false},
		{"or none true", Group{CombinatorOr, []Condition{no, no}}, false, false},
		{"and short-circuits before error", Group{CombinatorAnd, []Condition{no, bad}}, false, false},
		{"or short-circuits before error", Group{CombinatorOr, []Condition{yes, bad}}, true, false},
		{"error reached", Group{CombinatorAnd, []Condition{yes, bad}}, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.g.Eval(EvalOptions{})
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("got %v, %v", got, err)
			}
		})
	}
}

func TestParseConditions(t *testing.T) {
	good := []any{map[string]any{"left": 1.0, "operator": "number:gt", "right": 0.0}}
	got, err := ParseConditions(good)
	if err != nil || len(got) != 1 || got[0].Operator != "number:gt" {
		t.Fatalf("%v %v", got, err)
	}
	if got, err := ParseConditions(nil); got != nil || err != nil {
		t.Fatal("nil must parse to no conditions")
	}
	for name, bad := range map[string]any{
		"not an array":  "x",
		"entry not obj": []any{"x"},
		"missing op":    []any{map[string]any{"left": 1.0}},
		"unknown op":    []any{map[string]any{"operator": "nope"}},
	} {
		if _, err := ParseConditions(bad); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestOperatorOptionsAreUnique(t *testing.T) {
	seen := map[any]bool{}
	for _, o := range OperatorOptions() {
		if seen[o.Value] {
			t.Fatalf("duplicate operator %v", o.Value)
		}
		seen[o.Value] = true
	}
	if len(seen) < 30 {
		t.Fatalf("only %d operators", len(seen))
	}
}
