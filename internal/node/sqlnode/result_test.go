package sqlnode

import (
	"math"
	"reflect"
	"testing"
	"time"
)

func TestNormalize(t *testing.T) {
	ts := time.Date(2024, 5, 6, 7, 8, 9, 0, time.FixedZone("x", 3600))
	str := ResultOptions{NumbersAsString: true}
	tests := []struct {
		name string
		v    any
		typ  string
		o    ResultOptions
		want any
	}{
		{"nil", nil, "INT4", ResultOptions{}, nil},
		{"timestamp RFC3339 UTC", ts, "TIMESTAMPTZ", ResultOptions{}, "2024-05-06T06:08:09Z"},
		{"bigint plain", int64(1) << 60, "INT8", ResultOptions{}, int64(1) << 60},
		{"bigint as string", int64(1) << 60, "INT8", str, "1152921504606846976"},
		{"int not stringified", int64(5), "INT4", str, int64(5)},
		{"numeric string default float", "12.50", "NUMERIC", ResultOptions{}, 12.5},
		{"numeric as string", "12.50", "NUMERIC", str, "12.50"},
		{"mysql decimal bytes", []byte("3.25"), "DECIMAL", ResultOptions{}, 3.25},
		{"mysql decimal as string", []byte("3.25"), "DECIMAL", str, "3.25"},
		{"mysql text bigint", []byte("42"), "BIGINT", str, "42"},
		{"mysql text int", []byte("42"), "INT", ResultOptions{}, int64(42)},
		{"mysql unsigned huge", uint64(math.MaxUint64), "UNSIGNED BIGINT", ResultOptions{}, "18446744073709551615"},
		{"mysql unsigned small", uint64(7), "UNSIGNED INT", ResultOptions{}, int64(7)},
		{"mysql text datetime", []byte("2024-05-06 07:08:09"), "DATETIME", ResultOptions{}, "2024-05-06T07:08:09Z"},
		{"json bytes", []byte(`{"a":[1,2]}`), "JSON", ResultOptions{}, map[string]any{"a": []any{1.0, 2.0}}},
		{"jsonb string", `{"a":1}`, "JSONB", ResultOptions{}, map[string]any{"a": 1.0}},
		{"bytea base64", []byte{0, 1, 2}, "BYTEA", ResultOptions{}, "AAEC"},
		{"text bytes", []byte("hi"), "VARCHAR", ResultOptions{}, "hi"},
		{"double bytes", []byte("1.5"), "DOUBLE", ResultOptions{}, 1.5},
		{"bool", true, "BOOL", ResultOptions{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalize(tt.v, tt.typ, tt.o); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %#v (%T), want %#v", got, got, tt.want)
			}
		})
	}
}

func TestCoerceArg(t *testing.T) {
	tests := []struct{ in, want any }{
		{3.0, int64(3)}, {3.5, 3.5}, {math.Pow(2, 60), math.Pow(2, 60)}, {"s", "s"}, {nil, nil},
		{map[string]any{"a": 1.0}, `{"a":1}`}, {[]any{"x", 2.0}, `["x",2]`},
	}
	for _, tt := range tests {
		if got := coerceArg(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("coerceArg(%#v) = %#v, want %#v", tt.in, got, tt.want)
		}
	}
}
