package expression

import (
	"errors"
	"strings"
)

const (
	openDelim  = "{{"
	closeDelim = "}}"
)

// segment is a literal run of text or a block of JavaScript.
type segment struct {
	text   string
	isCode bool
}

var errUnterminated = errors.New("unterminated {{ block")

// parseTemplate splits a template (without its leading "=") into literal and
// code segments. The end of a block is the first "}}" outside of strings,
// comments and nested braces, so object literals may be used inside a block.
func parseTemplate(tpl string) ([]segment, error) {
	var segs []segment
	for tpl != "" {
		start := strings.Index(tpl, openDelim)
		if start < 0 {
			segs = append(segs, segment{text: tpl})
			break
		}
		if start > 0 {
			segs = append(segs, segment{text: tpl[:start]})
		}
		tpl = tpl[start+len(openDelim):]
		end := findBlockEnd(tpl)
		if end < 0 {
			return nil, errUnterminated
		}
		segs = append(segs, segment{text: tpl[:end], isCode: true})
		tpl = tpl[end+len(closeDelim):]
	}
	return segs, nil
}

// findBlockEnd returns the index of the closing delimiter in code, or -1.
func findBlockEnd(code string) int {
	depth := 0
	for i := 0; i < len(code); i++ {
		c := code[i]
		switch c {
		case '\'', '"', '`':
			i = skipString(code, i)
		case '/':
			if i+1 < len(code) {
				switch code[i+1] {
				case '/':
					i = skipUntil(code, i+2, "\n")
				case '*':
					i = skipUntil(code, i+2, "*/")
				}
			}
		case '{':
			depth++
		case '}':
			if depth == 0 && strings.HasPrefix(code[i:], closeDelim) {
				return i
			}
			if depth > 0 {
				depth--
			}
		}
		if i < 0 {
			return -1
		}
	}
	return -1
}

// skipString returns the index of the closing quote of the string starting at i,
// or -1 if the string is unterminated.
func skipString(code string, i int) int {
	quote := code[i]
	for j := i + 1; j < len(code); j++ {
		switch code[j] {
		case '\\':
			j++
		case quote:
			return j
		}
	}
	return -1
}

// skipUntil returns the index of the last byte of the terminator found at or
// after from, or the end of code for line comments, or -1 if not found.
func skipUntil(code string, from int, terminator string) int {
	idx := strings.Index(code[from:], terminator)
	if idx < 0 {
		if terminator == "\n" {
			return len(code)
		}
		return -1
	}
	return from + idx + len(terminator) - 1
}
