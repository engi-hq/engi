package cronexpr

import (
	"strings"
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	l, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("tz database unavailable: %v", err)
	}
	return l
}

func TestNext(t *testing.T) {
	utc := time.UTC
	at := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	tests := []struct {
		name, expr, after, want string
	}{
		{"every minute", "* * * * *", "2026-05-06T07:08:09Z", "2026-05-06T07:09:00Z"},
		{"strictly after", "*/5 * * * *", "2026-05-06T07:10:00Z", "2026-05-06T07:15:00Z"},
		{"seconds field", "*/20 * * * * *", "2026-05-06T07:08:09Z", "2026-05-06T07:08:20Z"},
		{"daily 09:30", "30 9 * * *", "2026-05-06T10:00:00Z", "2026-05-07T09:30:00Z"},
		{"weekdays names", "0 8 * * MON-FRI", "2026-05-08T09:00:00Z", "2026-05-11T08:00:00Z"}, // Fri -> Mon
		{"sunday as 7", "0 0 * * 7", "2026-05-06T00:00:00Z", "2026-05-10T00:00:00Z"},
		{"list and range", "0 9-11,15 * * *", "2026-05-06T11:30:00Z", "2026-05-06T15:00:00Z"},
		{"step with start", "5/20 * * * *", "2026-05-06T07:30:00Z", "2026-05-06T07:45:00Z"},
		{"month names", "0 0 1 JAN,JUL *", "2026-05-06T00:00:00Z", "2026-07-01T00:00:00Z"},
		{"dom or dow", "0 0 13 * FRI", "2026-05-06T00:00:00Z", "2026-05-08T00:00:00Z"},
		{"leap day", "0 0 29 2 *", "2026-05-06T00:00:00Z", "2028-02-29T00:00:00Z"},
		{"year rollover", "0 0 1 1 *", "2026-12-31T23:59:59Z", "2027-01-01T00:00:00Z"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, err := Parse(tt.expr)
			if err != nil {
				t.Fatal(err)
			}
			if got := e.Next(at(tt.after), utc); !got.Equal(at(tt.want)) {
				t.Fatalf("Next = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNextNeverMatches(t *testing.T) {
	e, err := Parse("0 0 31 2 *")
	if err != nil {
		t.Fatal(err)
	}
	if got := e.Next(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.UTC); !got.IsZero() {
		t.Fatalf("Next = %v, want zero", got)
	}
}

func TestTimezone(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	e, _ := Parse("0 9 * * *")
	got := e.Next(time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC), ny)
	if want := time.Date(2026, 1, 15, 14, 0, 0, 0, time.UTC); !got.Equal(want) { // EST = UTC-5
		t.Fatalf("winter = %v, want %v", got, want)
	}
	got = e.Next(time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC), ny)
	if want := time.Date(2026, 7, 15, 13, 0, 0, 0, time.UTC); !got.Equal(want) { // EDT = UTC-4
		t.Fatalf("summer = %v, want %v", got, want)
	}
}

func TestDaylightSaving(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	// 2026: clocks jump 02:00 -> 03:00 on March 8, and 02:00 -> 01:00 on November 1.
	t.Run("skipped time fires at the jump", func(t *testing.T) {
		e, _ := Parse("30 2 * * *")
		got := e.Next(time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC), ny)             // before 02:30 EST
		if want := time.Date(2026, 3, 8, 7, 0, 0, 0, time.UTC); !got.Equal(want) { // 03:00 EDT
			t.Fatalf("got %v, want %v", got, want)
		}
		// Fires once: the next one is the following day at 02:30 EDT.
		next := e.Next(got, ny)
		if want := time.Date(2026, 3, 9, 6, 30, 0, 0, time.UTC); !next.Equal(want) {
			t.Fatalf("next = %v, want %v", next, want)
		}
	})
	t.Run("hourly across the gap does not double fire", func(t *testing.T) {
		e, _ := Parse("30 * * * *")
		a := e.Next(time.Date(2026, 3, 8, 6, 31, 0, 0, time.UTC), ny)            // 01:31 EST
		if want := time.Date(2026, 3, 8, 7, 0, 0, 0, time.UTC); !a.Equal(want) { // 02:30 is skipped: jump instant
			t.Fatalf("a = %v, want %v", a, want)
		}
		b := e.Next(a, ny)
		if want := time.Date(2026, 3, 8, 7, 30, 0, 0, time.UTC); !b.Equal(want) { // 03:30 EDT
			t.Fatalf("b = %v, want %v", b, want)
		}
	})
	t.Run("repeated time fires once", func(t *testing.T) {
		e, _ := Parse("30 1 * * *")
		first := e.Next(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), ny)
		if want := time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC); !first.Equal(want) { // 01:30 EDT
			t.Fatalf("first = %v, want %v", first, want)
		}
		second := e.Next(first, ny) // the repeated 01:30 EST (06:30Z) must not fire
		if want := time.Date(2026, 11, 2, 6, 30, 0, 0, time.UTC); !second.Equal(want) {
			t.Fatalf("second = %v, want %v", second, want)
		}
	})
}

func TestParseErrors(t *testing.T) {
	for expr, want := range map[string]string{
		"* * * *":        "5 or 6 fields",
		"60 * * * *":     "outside",
		"* 24 * * *":     "outside",
		"* * 0 * *":      "outside",
		"* * * 13 *":     "outside",
		"*/0 * * * *":    "bad step",
		"5-1 * * * *":    "backwards",
		"x * * * *":      "unknown value",
		"* * * * FUNDAY": "unknown value",
	} {
		if _, err := Parse(expr); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%q) err = %v, want %q", expr, err, want)
		}
	}
}
