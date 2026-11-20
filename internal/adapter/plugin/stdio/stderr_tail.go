package stdio

import (
	"fmt"
	"strings"
	"sync"
)

// stderrTail is a bounded ring buffer of the last lines a plugin process wrote
// to stderr. A failed start appends it to its error, so the operator sees why
// the plugin exited instead of only "exit status 1". Lines are already cut to
// the configured line length by lineLogger, so the buffer is bounded in bytes
// as well as in lines.
type stderrTail struct {
	mu    sync.Mutex
	lines []string
	next  int // slot the next line is written to
	count int // lines held, at most len(lines)
}

// newStderrTail returns a tail that keeps the last n lines; n <= 0 keeps none.
func newStderrTail(n int) *stderrTail {
	return &stderrTail{lines: make([]string, max(n, 0))}
}

// add records one line, dropping the oldest when the buffer is full.
func (t *stderrTail) add(line string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.lines) == 0 {
		return
	}
	t.lines[t.next] = line
	t.next = (t.next + 1) % len(t.lines)
	t.count = min(t.count+1, len(t.lines))
}

// text returns the kept lines, oldest first, separated by newlines.
func (t *stderrTail) text() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.count == 0 {
		return ""
	}
	start := (t.next - t.count + len(t.lines)) % len(t.lines)
	out := make([]string, 0, t.count)
	for i := range t.count {
		out = append(out, t.lines[(start+i)%len(t.lines)])
	}
	return strings.Join(out, "\n")
}

// withStderr appends the plugin's last stderr lines to err, or returns err
// unchanged when the plugin wrote nothing.
func withStderr(err error, tail *stderrTail) error {
	s := tail.text()
	if s == "" {
		return err
	}
	return fmt.Errorf("%w\nplugin stderr, last lines:\n%s", err, s)
}
