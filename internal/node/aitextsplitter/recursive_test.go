package aitextsplitter_test

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/engi-hq/engi/internal/node/aitextsplitter"
)

var defaultSeps = []string{"\n\n", "\n", " ", ""}

func TestSplit(t *testing.T) {
	tests := []struct {
		name          string
		text          string
		size, overlap int
		seps          []string
		keep          bool
		want          []string
	}{
		{"short text is one chunk", "hello world", 100, 0, defaultSeps, true, []string{"hello world"}},
		{"empty", "", 10, 0, defaultSeps, true, nil},
		{"whitespace only", " \n\t ", 10, 0, defaultSeps, true, nil},
		{"paragraphs", "aaaa\n\nbbbb\n\ncccc", 10, 0, defaultSeps, false, []string{"aaaa\n\nbbbb", "cccc"}},
		{"paragraphs one each", "aaaa\n\nbbbb\n\ncccc", 5, 0, defaultSeps, false, []string{"aaaa", "bbbb", "cccc"}},
		{"words with overlap", "one two three four five", 13, 7, []string{" "}, false, []string{"one two three", "three four", "four five"}},
		{"falls back to next separator", "aaa bbb ccc\n\nddd", 7, 0, defaultSeps, false, []string{"aaa bbb", "ccc", "ddd"}},
		{"hard cut without separators", "abcdefghij", 4, 0, nil, true, []string{"abcd", "efgh", "ij"}},
		{"hard cut with overlap", "abcdefghij", 4, 1, []string{""}, true, []string{"abcd", "defg", "ghij"}},
		{"unbreakable word then hard cut", "xxxxxxxxxx yy", 4, 0, defaultSeps, false, []string{"xxxx", "xxxx", "xx", "yy"}},
		{"separator absent uses character cut", "abcdef", 3, 0, []string{"\n"}, false, []string{"abc", "def"}},
		{"keep separator", "a. b. c.", 4, 0, []string{". "}, true, []string{"a. b", ". c."}},
		{"unicode is cut by character", "日本語のテキスト", 3, 0, []string{""}, true, []string{"日本語", "のテキ", "スト"}},
		{"emoji not split", "😀😀😀😀😀", 2, 0, nil, true, []string{"😀😀", "😀😀", "😀"}},
		{"combining marks stay valid utf8", "héllo wörld", 5, 0, defaultSeps, false, []string{"héllo", "wörld"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := aitextsplitter.NewRecursive(tt.size, tt.overlap, tt.seps, tt.keep)
			if err != nil {
				t.Fatal(err)
			}
			got := s.Split(tt.text)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %q\nwant %q", got, tt.want)
			}
			for _, c := range got {
				if !utf8.ValidString(c) || utf8.RuneCountInString(c) > tt.size {
					t.Fatalf("bad chunk %q", c)
				}
			}
		})
	}
}

func TestSplitInvariants(t *testing.T) {
	text := strings.Repeat("Lorem ipsum dolor sit amet, consectetur.\nSed do eiusmod tempor.\n\nÜber 日本語 😀 text ", 30)
	for _, size := range []int{1, 7, 50, 200} {
		for _, overlap := range []int{0, size / 2} {
			s, err := aitextsplitter.NewRecursive(size, overlap, defaultSeps, true)
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range s.Split(text) {
				if c == "" || !utf8.ValidString(c) || utf8.RuneCountInString(c) > size {
					t.Fatalf("size %d overlap %d: bad chunk %q", size, overlap, c)
				}
			}
		}
	}
}

func TestNewRecursiveValidates(t *testing.T) {
	for name, tt := range map[string]struct{ size, overlap int }{
		"zero size": {0, 0}, "negative overlap": {10, -1}, "overlap equals size": {10, 10}, "overlap above size": {10, 20},
	} {
		if _, err := aitextsplitter.NewRecursive(tt.size, tt.overlap, nil, true); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}
