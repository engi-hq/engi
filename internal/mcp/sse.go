package mcp

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

// sseEvent is one server-sent event.
type sseEvent struct {
	Name string
	Data string
}

// errEventTooLarge is returned when one event exceeds the reader's byte cap.
var errEventTooLarge = errors.New("server-sent event is too large")

// sseReader parses a text/event-stream. Lines longer than max, and events whose
// data exceeds it, fail the stream rather than growing memory without bound.
type sseReader struct {
	sc  *bufio.Scanner
	max int
}

func newSSEReader(r io.Reader, limit int) *sseReader {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), limit)
	return &sseReader{sc: sc, max: limit}
}

// Next returns the next event with data, or io.EOF at the end of the stream.
func (r *sseReader) Next() (sseEvent, error) {
	var ev sseEvent
	var data []string
	size := 0
	for r.sc.Scan() {
		line := r.sc.Text()
		switch {
		case line == "":
			if len(data) > 0 {
				ev.Data = strings.Join(data, "\n")
				return ev, nil
			}
			ev = sseEvent{} // a comment-only or empty block dispatches nothing
		case strings.HasPrefix(line, ":"):
		default:
			field, value, _ := strings.Cut(line, ":")
			value = strings.TrimPrefix(value, " ")
			switch field {
			case "event":
				ev.Name = value
			case "data":
				size += len(value) + 1
				if size > r.max {
					return sseEvent{}, errEventTooLarge
				}
				data = append(data, value)
			}
		}
	}
	if err := r.sc.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return sseEvent{}, errEventTooLarge
		}
		return sseEvent{}, fmt.Errorf("reading event stream: %w", err)
	}
	if len(data) > 0 { // a final event without the trailing blank line
		ev.Data = strings.Join(data, "\n")
		return ev, nil
	}
	return sseEvent{}, io.EOF
}
