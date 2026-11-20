package stdio

import (
	"errors"
	"strings"
	"testing"
)

func TestStderrTail(t *testing.T) {
	tests := []struct {
		name  string
		keep  int
		lines []string
		want  string
	}{
		{"keeps none", 0, []string{"a", "b"}, ""},
		{"empty", 3, nil, ""},
		{"under the limit", 3, []string{"a", "b"}, "a\nb"},
		{"exactly full", 2, []string{"a", "b"}, "a\nb"},
		{"drops the oldest", 2, []string{"a", "b", "c"}, "b\nc"},
		{"wraps several times", 3, []string{"1", "2", "3", "4", "5", "6", "7"}, "5\n6\n7"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tail := newStderrTail(tt.keep)
			for _, l := range tt.lines {
				tail.add(l)
			}
			if got := tail.text(); got != tt.want {
				t.Errorf("text() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWithStderr(t *testing.T) {
	base := errors.New("plugin x exited: exit status 1")
	tail := newStderrTail(2)
	if err := withStderr(base, tail); !errors.Is(err, base) || err.Error() != base.Error() {
		t.Errorf("empty tail must return the error unchanged, got %v", err)
	}
	tail.add("first")
	tail.add("boom")
	tail.add("last")
	err := withStderr(base, tail)
	if !errors.Is(err, base) {
		t.Errorf("the start error must stay wrapped, got %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "boom\nlast") || strings.Contains(msg, "first") {
		t.Errorf("message = %q, want the last two lines only", msg)
	}
}
