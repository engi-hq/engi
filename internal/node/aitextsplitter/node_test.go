package aitextsplitter_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/engi-hq/engi/internal/node/aitextsplitter"
	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

func supply(t *testing.T, params map[string]any) (port.TextSplitter, error) {
	t.Helper()
	n := aitextsplitter.New()
	got, err := n.(nodesdk.Supplier).Supply(context.Background(), nodetest.New(n.Describe(), nil, params))
	if err != nil {
		return nil, err
	}
	return got.(port.TextSplitter), nil
}

func TestDescriptorIsValidSubNode(t *testing.T) {
	d := aitextsplitter.New().Describe()
	if errs := d.Validate(); errs != nil || !d.IsSubNode() {
		t.Fatalf("errs=%v subnode=%v", errs, d.IsSubNode())
	}
	_, err := aitextsplitter.New().Execute(context.Background(), nodetest.New(d, nil, nil))
	if !errors.Is(err, nodesdk.ErrUnavailable) {
		t.Fatalf("Execute err = %v", err)
	}
}

func TestSupplyDefaultsAndParams(t *testing.T) {
	s, err := supply(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Split(strings.Repeat("word ", 500)); len(got) < 2 {
		t.Fatalf("default splitter produced %d chunks", len(got))
	}
	s, err = supply(t, map[string]any{"chunkSize": float64(5), "chunkOverlap": float64(0), "separators": `["|"]`, "keepSeparator": false})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Split("ab|cd|ef"); strings.Join(got, ",") != "ab|cd,ef" {
		t.Fatalf("got %q", got)
	}
}

func TestSupplyRejectsBadSettings(t *testing.T) {
	for name, params := range map[string]map[string]any{
		"overlap":          {"chunkSize": float64(10), "chunkOverlap": float64(10)},
		"size":             {"chunkSize": float64(0), "chunkOverlap": float64(0)},
		"separators text":  {"separators": "nope"},
		"separators type":  {"separators": float64(3)},
		"separators entry": {"separators": []any{"a", 1.0}},
	} {
		if _, err := supply(t, params); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}
