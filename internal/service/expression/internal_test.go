package expression

import (
	"fmt"
	"reflect"
	"testing"
)

func TestParseTemplate(t *testing.T) {
	tests := []struct {
		in      string
		want    []segment
		wantErr bool
	}{
		{"", nil, false},
		{"plain", []segment{{text: "plain"}}, false},
		{"{{a}}", []segment{{text: "a", isCode: true}}, false},
		{"x{{ a }}y{{ b }}", []segment{{text: "x"}, {text: " a ", isCode: true}, {text: "y"}, {text: " b ", isCode: true}}, false},
		{"{{ {a:{b:1}} }}", []segment{{text: " {a:{b:1}} ", isCode: true}}, false},
		{`{{ "}}" }}`, []segment{{text: ` "}}" `, isCode: true}}, false},
		{`{{ '\'}}' }}`, []segment{{text: ` '\'}}' `, isCode: true}}, false},
		{"{{ a /* }} */ }}", []segment{{text: " a /* }} */ ", isCode: true}}, false},
		{"{{ a", nil, true},
		{`{{ "abc }}`, nil, true},
		{"{{ a /* }}", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := parseTemplate(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestProgramCacheEvictsLeastRecentlyUsed(t *testing.T) {
	c := newProgramCache(2)
	k := keyFor
	a, b, d := &compiledTemplate{}, &compiledTemplate{}, &compiledTemplate{}
	c.put(k("a"), a)
	c.put(k("b"), b)
	if got, ok := c.get(k("a")); !ok || got != a { // a becomes most recent
		t.Fatal("a missing")
	}
	c.put(k("d"), d) // evicts b
	if _, ok := c.get(k("b")); ok {
		t.Fatal("b should have been evicted")
	}
	for _, name := range []string{"a", "d"} {
		if _, ok := c.get(k(name)); !ok {
			t.Fatalf("%s should be cached", name)
		}
	}
	if c.len() != 2 {
		t.Fatalf("len = %d", c.len())
	}
	c.put(k("a"), b) // replace existing
	if got, _ := c.get(k("a")); got != b || c.len() != 2 {
		t.Fatal("replace failed")
	}
}

func TestCompiledIsCached(t *testing.T) {
	e, err := New(defaultCfgForTest(), clockFunc(func() (t0 timeT) { return }), func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	c1, err := e.compile("{{ 1 }}")
	if err != nil {
		t.Fatal(err)
	}
	c2, _ := e.compile("{{ 1 }}")
	if c1 != c2 {
		t.Fatal("second compile must hit the cache")
	}
	if !c1.single() {
		t.Fatal("single block expected")
	}
	if fmt.Sprint(e.cache.len()) != "1" {
		t.Fatal("cache size")
	}
}
