package engine_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sync"
	"time"

	"github.com/engi-hq/engi/internal/node/registry"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// fakeClock is a manual clock. Sleep records the requested delay and returns
// at once (or with the context error), so retry/backoff tests run instantly.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	sleeps []time.Duration
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sleeps = append(c.sleeps, d)
	c.now = c.now.Add(d)
	return nil
}

func (c *fakeClock) Sleeps() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.sleeps...)
}

// funcNode is a test node defined by a descriptor and a function.
type funcNode struct {
	desc nodesdk.Descriptor
	fn   func(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error)
}

func (n funcNode) Describe() nodesdk.Descriptor { return n.desc }
func (n funcNode) Execute(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	return n.fn(ctx, in)
}

func port(name string) nodesdk.PortSpec { return nodesdk.PortSpec{Name: name, Kind: nodesdk.PortMain} }

func desc(typ string, inputs, outputs []nodesdk.PortSpec, policy nodesdk.MergePolicy) nodesdk.Descriptor {
	return nodesdk.Descriptor{Type: typ, Version: 1, DisplayName: typ, Group: nodesdk.GroupAction, Inputs: inputs, Outputs: outputs, MergePolicy: policy}
}

func one() []nodesdk.PortSpec { return []nodesdk.PortSpec{port("main")} }
func two() []nodesdk.PortSpec { return []nodesdk.PortSpec{port("a"), port("b")} }

func passThrough(_ context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	return nodesdk.Output{Ports: [][]nodesdk.Item{in.Items(0)}}, nil
}

// mergeFn concatenates both inputs and records which input each item came from.
func mergeFn(_ context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	var out []nodesdk.Item
	for p := 0; p < 2; p++ {
		for i, it := range in.Items(p) {
			it.PairedItem = []nodesdk.PairedItem{{Input: p, Item: i}}
			out = append(out, it)
		}
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}

// buildRegistry registers the fake node types used by the tests.
func buildRegistry() (*registry.Registry, error) {
	nodes := []funcNode{
		{desc("test.trigger", nil, one(), ""), passThrough},
		{desc("test.noop", one(), one(), ""), passThrough},
		{desc("test.set", one(), one(), ""), setFn},
		{desc("test.if", one(), two(), ""), ifFn},
		{desc("test.merge", two(), one(), nodesdk.MergeWaitAll), mergeFn},
		{desc("test.mergeAny", two(), one(), nodesdk.MergeAny), mergeFn},
		{desc("test.mergeFlush", two(), one(), nodesdk.MergePartialOnFlush), mergeFn},
		{desc("test.empty", one(), one(), ""), func(context.Context, nodesdk.Input) (nodesdk.Output, error) {
			return nodesdk.Output{Ports: [][]nodesdk.Item{nil}}, nil
		}},
		{desc("test.fail", one(), one(), ""), func(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
			msg, err := nodesdk.ParamString(ctx, in, "message", 0)
			if err != nil {
				return nodesdk.Output{}, err
			}
			return nodesdk.Output{}, &nodesdk.NodeError{Message: msg, Description: "always fails", HTTPStatus: 502}
		}},
		{desc("test.flaky", one(), one(), ""), func(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
			until, err := nodesdk.ParamInt(ctx, in, "succeedOn", 0)
			if err != nil {
				return nodesdk.Output{}, err
			}
			if a := in.ExecutionInfo().Attempt; a < until {
				return nodesdk.Output{}, fmt.Errorf("attempt %d failed", a)
			}
			return passThrough(ctx, in)
		}},
		{desc("test.wait", one(), one(), ""), func(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
			out, _ := passThrough(ctx, in)
			out.Wait = &nodesdk.Wait{ResumeToken: "tok-1"}
			return out, nil
		}},
		{desc("test.block", one(), one(), ""), func(ctx context.Context, _ nodesdk.Input) (nodesdk.Output, error) {
			<-ctx.Done()
			return nodesdk.Output{}, ctx.Err()
		}},
		{paramsDesc(), func(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
			greeting, err := nodesdk.ParamString(ctx, in, "greeting", 0)
			if err != nil {
				return nodesdk.Output{}, err
			}
			must, err := nodesdk.ParamString(ctx, in, "must", 0)
			if err != nil {
				return nodesdk.Output{}, err
			}
			return nodesdk.Output{Ports: [][]nodesdk.Item{{{JSON: map[string]any{"greeting": greeting, "must": must}}}}}, nil
		}},
		{desc("test.dropFirst", one(), one(), ""), func(_ context.Context, in nodesdk.Input) (nodesdk.Output, error) {
			var out []nodesdk.Item
			for i, it := range in.Items(0) {
				if i == 0 {
					continue
				}
				it.PairedItem = []nodesdk.PairedItem{{Input: 0, Item: i}}
				out = append(out, it)
			}
			return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
		}},
		{desc("test.aggregate", one(), one(), ""), func(_ context.Context, in nodesdk.Input) (nodesdk.Output, error) {
			item := nodesdk.Item{JSON: map[string]any{"count": float64(len(in.Items(0)))}}
			for i := range in.Items(0) {
				item.PairedItem = append(item.PairedItem, nodesdk.PairedItem{Input: 0, Item: i})
			}
			return nodesdk.Output{Ports: [][]nodesdk.Item{{item}}}, nil
		}},
		{desc("test.stale", one(), one(), ""), func(_ context.Context, in nodesdk.Input) (nodesdk.Output, error) {
			items := in.Items(0)
			for i := range items {
				items[i].PairedItem = []nodesdk.PairedItem{{Input: 0, Item: 99}} // a reference that does not exist here
			}
			return nodesdk.Output{Ports: [][]nodesdk.Item{items}}, nil
		}},
		{desc("test.count", one(), one(), ""), func(_ context.Context, in nodesdk.Input) (nodesdk.Output, error) {
			return nodesdk.Output{Ports: [][]nodesdk.Item{{{JSON: map[string]any{"count": float64(len(in.Items(0)))}}}}}, nil
		}},
		{desc("test.dup", one(), one(), ""), func(_ context.Context, in nodesdk.Input) (nodesdk.Output, error) {
			var out []nodesdk.Item
			for i, it := range in.Items(0) {
				for range 2 {
					c := it
					c.PairedItem = []nodesdk.PairedItem{{Input: 0, Item: i}}
					out = append(out, c)
				}
			}
			return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
		}},
		{desc("test.itemError", one(), one(), ""), func(_ context.Context, in nodesdk.Input) (nodesdk.Output, error) {
			var out []nodesdk.Item
			for i, it := range in.Items(0) {
				it.PairedItem = []nodesdk.PairedItem{{Input: 0, Item: i}}
				if i == 1 {
					it.Error = &nodesdk.ItemError{Message: "bad item"}
				}
				out = append(out, it)
			}
			return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
		}},
		{desc("test.cred", one(), one(), ""), func(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
			_, err := in.Credential(ctx, "api")
			return nodesdk.Output{}, err
		}},
		{desc("test.panic", one(), one(), ""), func(context.Context, nodesdk.Input) (nodesdk.Output, error) {
			panic("kaboom")
		}},
		{desc("test.tooManyPorts", one(), one(), ""), func(context.Context, nodesdk.Input) (nodesdk.Output, error) {
			return nodesdk.Output{Ports: make([][]nodesdk.Item, 3)}, nil
		}},
	}
	reg := registry.New()
	for _, n := range nodes {
		if err := reg.Register(func() nodesdk.Node { return n }); err != nil {
			return nil, fmt.Errorf("registering %s: %w", n.desc.Type, err)
		}
	}
	if err := registerSubPortFakes(reg); err != nil {
		return nil, err
	}
	return reg, nil
}

// setFn merges the "values" parameter (resolved per item) into each item.
func setFn(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	items := in.Items(0)
	out := make([]nodesdk.Item, len(items))
	for i, it := range items {
		raw, err := in.Param(ctx, "values", i)
		if err != nil {
			return nodesdk.Output{}, err
		}
		values, ok := raw.(map[string]any)
		if !ok && raw != nil {
			return nodesdk.Output{}, errors.New("values must be an object")
		}
		merged := map[string]any{}
		maps.Copy(merged, it.JSON)
		maps.Copy(merged, values)
		out[i] = nodesdk.Item{JSON: merged}
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}

// ifFn routes items to port 0 when "condition" is true, otherwise port 1.
func ifFn(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	var yes, no []nodesdk.Item
	for i, it := range in.Items(0) {
		ok, err := nodesdk.ParamBool(ctx, in, "condition", i)
		if err != nil {
			return nodesdk.Output{}, err
		}
		it.PairedItem = []nodesdk.PairedItem{{Input: 0, Item: i}}
		if ok {
			yes = append(yes, it)
		} else {
			no = append(no, it)
		}
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{yes, no}}, nil
}

func paramsDesc() nodesdk.Descriptor {
	d := desc("test.params", one(), one(), "")
	d.Properties = []nodesdk.Property{
		{Name: "greeting", Type: nodesdk.PropString, Default: "hi"},
		{Name: "must", Type: nodesdk.PropString, Required: true},
	}
	return d
}
