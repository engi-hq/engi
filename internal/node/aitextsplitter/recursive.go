package aitextsplitter

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/engi-hq/engi/internal/port"
)

// Recursive is a port.TextSplitter that cuts text along a ranked list of
// separators. It tries the first separator that occurs in the text; pieces that
// still exceed the chunk size are cut again with the following separators, and
// the empty separator finally cuts between characters. Small neighbouring pieces
// are packed into chunks of at most Size characters, with up to Overlap
// characters repeated at the start of the next chunk.
//
// Lengths count Unicode characters (runes), never bytes, so multi-byte text is
// never cut inside a character.
type Recursive struct {
	size, overlap int
	separators    []string
	keepSeparator bool
}

var _ port.TextSplitter = (*Recursive)(nil)

// NewRecursive validates the settings and returns a splitter. size must be at
// least 1 and overlap in [0, size). An empty separator list means "cut between
// characters only". With keepSeparator the separator stays at the start of the
// piece that followed it; otherwise it is dropped (chunks are rejoined with it).
func NewRecursive(size, overlap int, separators []string, keepSeparator bool) (*Recursive, error) {
	if size < 1 {
		return nil, errors.New("chunk size must be at least 1")
	}
	if overlap < 0 || overlap >= size {
		return nil, fmt.Errorf("chunk overlap must be between 0 and the chunk size (%d)", size-1)
	}
	return &Recursive{size: size, overlap: overlap, separators: append([]string(nil), separators...), keepSeparator: keepSeparator}, nil
}

// Split implements port.TextSplitter.
func (r *Recursive) Split(text string) []string {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return r.split(text, r.separators)
}

func (r *Recursive) split(text string, seps []string) []string {
	sep, rest := "", []string(nil)
	for i, s := range seps {
		if s == "" || strings.Contains(text, s) {
			sep, rest = s, seps[i+1:]
			break
		}
	}
	pieces := cut(text, sep, r.keepSeparator)
	joiner := sep
	if r.keepSeparator {
		joiner = ""
	}
	var out, good []string
	flush := func() {
		out = append(out, r.merge(good, joiner)...)
		good = nil
	}
	for _, p := range pieces {
		switch {
		case utf8.RuneCountInString(p) <= r.size:
			good = append(good, p)
		case len(rest) == 0 && sep != "":
			// Out of separators: cut between characters.
			flush()
			out = append(out, r.split(p, []string{""})...)
		default:
			flush()
			out = append(out, r.split(p, rest)...)
		}
	}
	flush()
	return out
}

// cut splits text at sep ("" cuts between characters) and drops empty pieces.
func cut(text, sep string, keep bool) []string {
	if sep == "" {
		out := make([]string, 0, utf8.RuneCountInString(text))
		for _, c := range text {
			out = append(out, string(c))
		}
		return out
	}
	parts := strings.Split(text, sep)
	out := make([]string, 0, len(parts))
	for i, p := range parts {
		if keep && i > 0 {
			p = sep + p
		}
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// merge packs pieces (each at most size characters) into chunks joined by
// joiner, carrying up to overlap characters into the next chunk.
func (r *Recursive) merge(pieces []string, joiner string) []string {
	var out []string
	var cur []string
	total := 0 // characters of cur including joiners
	jl := utf8.RuneCountInString(joiner)
	emit := func() {
		if s := strings.TrimSpace(strings.Join(cur, joiner)); s != "" {
			out = append(out, s)
		}
	}
	for _, p := range pieces {
		l := utf8.RuneCountInString(p)
		add := l
		if len(cur) > 0 {
			add += jl
		}
		if len(cur) > 0 && total+add > r.size {
			emit()
			for len(cur) > 0 && (total > r.overlap || total+jl+l > r.size) {
				total -= utf8.RuneCountInString(cur[0])
				if len(cur) > 1 {
					total -= jl
				}
				cur = cur[1:]
			}
			add = l
			if len(cur) > 0 {
				add += jl
			}
		}
		cur = append(cur, p)
		total += add
	}
	if len(cur) > 0 {
		emit()
	}
	return out
}
