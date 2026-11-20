package nodekit

import (
	"strconv"
	"strings"
)

// segment is one step of a path: an object key or an array index.
type segment struct {
	key   string
	index int
	isIdx bool
}

// parsePath splits "a.b[2].c" into segments. Empty segments are skipped.
func parsePath(path string) []segment {
	var segs []segment
	for _, part := range strings.Split(path, ".") {
		for part != "" {
			open := strings.IndexByte(part, '[')
			if open < 0 {
				segs = append(segs, segment{key: part})
				break
			}
			if open > 0 {
				segs = append(segs, segment{key: part[:open]})
			}
			closeIdx := strings.IndexByte(part[open:], ']')
			if closeIdx < 0 {
				segs = append(segs, segment{key: part[open:]})
				break
			}
			n, err := strconv.Atoi(part[open+1 : open+closeIdx])
			if err != nil || n < 0 {
				segs = append(segs, segment{key: part[open : open+closeIdx+1]})
			} else {
				segs = append(segs, segment{index: n, isIdx: true})
			}
			part = part[open+closeIdx+1:]
		}
	}
	return segs
}

// GetPath reads a value from nested data. "a.b[1].c" walks objects and arrays.
// A key that exists literally on the top-level object (including keys with dots)
// wins over path interpretation. ok is false when any step is missing.
func GetPath(data map[string]any, path string) (value any, ok bool) {
	if v, found := data[path]; found {
		return v, true
	}
	var cur any = data
	for _, s := range parsePath(path) {
		switch c := cur.(type) {
		case map[string]any:
			if s.isIdx {
				return nil, false
			}
			next, found := c[s.key]
			if !found {
				return nil, false
			}
			cur = next
		case []any:
			if !s.isIdx || s.index >= len(c) {
				return nil, false
			}
			cur = c[s.index]
		default:
			return nil, false
		}
	}
	return cur, true
}

// SetPath writes value at path, creating intermediate objects. With dot false
// the whole path is one literal key. A non-object in the way is replaced by an
// object. Array indices only address elements that already exist.
func SetPath(data map[string]any, path string, value any, dot bool) {
	if !dot {
		data[path] = value
		return
	}
	segs := parsePath(path)
	if len(segs) == 0 {
		return
	}
	setSegments(data, segs, value)
}

func setSegments(m map[string]any, segs []segment, value any) {
	s := segs[0]
	if s.isIdx { // an index cannot address an object key: treat it as a literal key
		s = segment{key: "[" + strconv.Itoa(s.index) + "]"}
	}
	if len(segs) == 1 {
		m[s.key] = value
		return
	}
	next := segs[1]
	switch child := m[s.key].(type) {
	case []any:
		if next.isIdx && next.index < len(child) {
			if len(segs) == 2 {
				child[next.index] = value
				return
			}
			if sub, ok := child[next.index].(map[string]any); ok {
				setSegments(sub, segs[2:], value)
				return
			}
		}
	case map[string]any:
		setSegments(child, segs[1:], value)
		return
	}
	child := map[string]any{}
	m[s.key] = child
	setSegments(child, segs[1:], value)
}

// DeletePath removes the value at path if present and reports whether it did.
func DeletePath(data map[string]any, path string) bool {
	if _, found := data[path]; found {
		delete(data, path)
		return true
	}
	segs := parsePath(path)
	if len(segs) == 0 {
		return false
	}
	var cur any = data
	for _, s := range segs[:len(segs)-1] {
		switch c := cur.(type) {
		case map[string]any:
			if s.isIdx {
				return false
			}
			cur = c[s.key]
		case []any:
			if !s.isIdx || s.index >= len(c) {
				return false
			}
			cur = c[s.index]
		default:
			return false
		}
	}
	last := segs[len(segs)-1]
	m, ok := cur.(map[string]any)
	if !ok || last.isIdx {
		return false
	}
	if _, found := m[last.key]; !found {
		return false
	}
	delete(m, last.key)
	return true
}
