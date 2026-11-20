package stdio_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync/atomic"
	"testing"
	"time"

	"github.com/engi-hq/engi/pkg/nodesdk"
	"github.com/engi-hq/engi/pkg/pluginproto"
	"github.com/engi-hq/engi/pkg/pluginsdk"
)

// modeEnv selects what the re-executed test binary does. When it is unset the
// binary runs the tests.
const modeEnv = "ENGI_TEST_PLUGIN_MODE"

const nodeType = "acme.test.node"

func TestMain(m *testing.M) {
	if mode := os.Getenv(modeEnv); mode != "" {
		os.Exit(runPlugin(mode))
	}
	os.Exit(m.Run())
}

func descriptor(typ string, group nodesdk.Group) nodesdk.Descriptor {
	return nodesdk.Descriptor{
		Type: typ, Version: 1, DisplayName: "T", Group: group,
		Inputs: []nodesdk.PortSpec{{Name: "main"}}, Outputs: []nodesdk.PortSpec{{Name: "main"}},
		Properties: []nodesdk.Property{
			{Name: "action", Type: nodesdk.PropString},
			{Name: "marker", Type: nodesdk.PropString},
		},
	}
}

var current, peak atomic.Int64

type pluginNode struct{}

func (pluginNode) Describe() nodesdk.Descriptor { return descriptor(nodeType, nodesdk.GroupAction) }

func (pluginNode) Execute(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	action, _ := nodesdk.ParamString(ctx, in, "action", 0)
	out := map[string]any{"pid": float64(os.Getpid())}
	switch action {
	case "echo":
	case "env":
		out["env"] = os.Environ()
	case "crash":
		os.Exit(3)
	case "block":
		marker, _ := nodesdk.ParamString(ctx, in, "marker", 0)
		<-ctx.Done()
		_ = os.WriteFile(marker, []byte("cancelled"), 0o600)
		return nodesdk.Output{}, ctx.Err()
	case "conc":
		n := current.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(150 * time.Millisecond)
		out["peak"] = float64(peak.Load())
		current.Add(-1)
	case "log":
		in.Logger().Info("from node", "k", "v")
	case "late-log":
		// Logs after the call has returned: its callId no longer names a call in flight.
		go func() {
			time.Sleep(300 * time.Millisecond)
			in.Logger().Info("too late")
		}()
	case "callbacks":
		v, err := in.EvalExpression(ctx, "1+1", 0)
		if err != nil {
			return nodesdk.Output{}, err
		}
		ref, err := in.Blob().Put(ctx, "a.txt", "text/plain", stringsReader("payload"))
		if err != nil {
			return nodesdk.Output{}, err
		}
		rc, err := in.Blob().Open(ctx, ref)
		if err != nil {
			return nodesdk.Output{}, err
		}
		b, _ := io.ReadAll(rc)
		out["eval"], out["blob"] = v, string(b)
	default:
		return nodesdk.Output{}, fmt.Errorf("unknown action %q", action)
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{{{JSON: out}}}}, nil
}

func runPlugin(mode string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	info := pluginproto.Info{Name: "testplugin", Version: "1.0.0"}
	switch mode {
	case "normal":
		return serve(ctx, info, pluginNode{})
	case "stderr":
		fmt.Fprintln(os.Stderr, "hello from stderr")
		fmt.Fprintln(os.Stderr, string(make([]byte, 0))+longLine())
		return serve(ctx, info, pluginNode{})
	case "crashstart":
		for i := 1; i <= 5; i++ {
			fmt.Fprintf(os.Stderr, "startup line %d\n", i)
		}
		return 1
	case "badversion", "badtype", "badgroup", "stubborn", "rawlog":
		return runRaw(mode)
	}
	fmt.Fprintln(os.Stderr, "unknown mode", mode)
	return 2
}

func serve(ctx context.Context, info pluginproto.Info, n nodesdk.Node) int {
	if err := pluginsdk.Serve(ctx, info, n); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// runRaw speaks just enough protocol to misbehave in a chosen way.
func runRaw(mode string) int {
	var conn *pluginproto.Conn
	h := func(_ context.Context, req pluginproto.Request) (any, error) {
		if req.Method != pluginproto.MethodInitialize {
			return struct{}{}, nil
		}
		if mode == "rawlog" {
			// A log outside any call carries no callId.
			_ = conn.Notify(pluginproto.MethodLog, pluginproto.LogParams{Level: pluginproto.LogWarn, Message: "startup note"})
		}
		d := descriptor(nodeType, nodesdk.GroupAction)
		res := pluginproto.InitializeResult{ProtocolVersion: 1, Plugin: pluginproto.Info{Name: "raw"}}
		switch mode {
		case "badversion":
			res.ProtocolVersion = 2
		case "badtype":
			d.Type = "engi.reserved"
		case "badgroup":
			d.Group = nodesdk.GroupTrigger
		}
		res.Nodes = []pluginproto.Descriptor{pluginproto.FromDescriptor(d)}
		return res, nil
	}
	conn = pluginproto.NewConn(os.Stdin, os.Stdout, h, pluginproto.ConnOptions{MaxMessageBytes: 1 << 20, MaxConcurrent: 4})
	_ = conn.Run(context.Background())
	if mode == "stubborn" {
		time.Sleep(time.Hour) // ignores shutdown and the closed stdin
	}
	return 0
}
