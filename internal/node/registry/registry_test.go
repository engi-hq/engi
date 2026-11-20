package registry_test

import (
	"context"
	"errors"
	"testing"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/node/registry"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

type stub struct{ d nodesdk.Descriptor }

func (s stub) Describe() nodesdk.Descriptor { return s.d }
func (stub) Execute(context.Context, nodesdk.Input) (nodesdk.Output, error) {
	return nodesdk.Output{}, nil
}

func factory(typ string, version int) registry.Factory {
	return func() nodesdk.Node { return stub{nodesdk.Descriptor{Type: typ, Version: version}} }
}

func TestRegisterAndLookup(t *testing.T) {
	r := registry.New()
	for _, f := range []registry.Factory{factory("a.b", 1), factory("a.b", 2), factory("a.c", 1)} {
		if err := r.Register(f); err != nil {
			t.Fatal(err)
		}
	}
	n, err := r.Lookup("a.b", 1)
	if err != nil || n.Describe().Version != 1 {
		t.Fatalf("lookup v1: %v %v", n, err)
	}
	if n, _ = r.Lookup("a.b", 0); n.Describe().Version != 2 {
		t.Fatalf("version 0 must select the latest, got %d", n.Describe().Version)
	}
	d, err := r.Descriptor("a.c", 1)
	if err != nil || d.Type != "a.c" {
		t.Fatalf("descriptor: %+v %v", d, err)
	}
	got := r.Descriptors()
	if len(got) != 3 || got[0].Version != 1 || got[1].Version != 2 || got[2].Type != "a.c" {
		t.Fatalf("descriptors not ordered: %+v", got)
	}
}

func TestLookupUnknownIsNotFound(t *testing.T) {
	r := registry.New()
	_ = r.Register(factory("a.b", 1))
	for _, c := range []struct {
		typ string
		v   int
	}{{"x.y", 0}, {"a.b", 7}} {
		if _, err := r.Lookup(c.typ, c.v); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("Lookup(%q,%d) = %v, want ErrNotFound", c.typ, c.v, err)
		}
	}
}

func TestRegisterRejectsBadInput(t *testing.T) {
	r := registry.New()
	if err := r.Register(nil); err == nil {
		t.Error("nil factory must fail")
	}
	if err := r.Register(func() nodesdk.Node { return nil }); err == nil {
		t.Error("nil node must fail")
	}
	if err := r.Register(factory("", 1)); err == nil {
		t.Error("invalid descriptor must fail")
	}
	if err := r.Register(factory("a.b", 1)); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(factory("a.b", 1)); err == nil {
		t.Error("duplicate must fail")
	}
}
