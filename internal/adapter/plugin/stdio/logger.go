package stdio

import (
	"bytes"
	"log/slog"
	"sync"
)

// lineLogger is an io.Writer that logs each line written to it at info level,
// cut at max bytes. Longer lines are logged cut once and the rest is dropped.
type lineLogger struct {
	log *slog.Logger
	max int
	// tail, when set, keeps every non-empty line for start error messages.
	tail *stderrTail

	mu      sync.Mutex
	buf     []byte
	dropped bool
}

func newLineLogger(log *slog.Logger, maxBytes int, tail *stderrTail) *lineLogger {
	return &lineLogger{log: log, max: maxBytes, tail: tail}
}

func (l *lineLogger) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	rest := p
	for len(rest) > 0 {
		i := bytes.IndexByte(rest, '\n')
		if i < 0 {
			l.add(rest)
			break
		}
		l.add(rest[:i])
		l.emit()
		rest = rest[i+1:]
	}
	return len(p), nil
}

func (l *lineLogger) add(b []byte) {
	room := l.max - len(l.buf)
	if room < len(b) {
		b = b[:max(room, 0)]
		l.dropped = true
	}
	l.buf = append(l.buf, b...)
}

func (l *lineLogger) emit() {
	line := bytes.TrimRight(l.buf, "\r")
	if len(line) > 0 || l.dropped {
		l.log.Info(string(line), "stream", "stderr", "truncated", l.dropped)
	}
	if l.tail != nil && len(line) > 0 {
		l.tail.add(string(line))
	}
	l.buf, l.dropped = l.buf[:0], false
}

// flush logs a final line that had no newline.
func (l *lineLogger) flush() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.buf) > 0 {
		l.emit()
	}
}
