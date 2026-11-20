//go:build integration

package conformance_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/adapter/plugin/httprpc"
	"github.com/engi-hq/engi/internal/adapter/plugin/pluginnode"
	"github.com/engi-hq/engi/internal/adapter/plugin/stdio"
	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/pkg/nodesdk"
	"github.com/engi-hq/engi/pkg/pluginproto"
)

const (
	parallelCalls = 8
	blobLimit     = 1 << 20
)

var hostInfo = pluginproto.Info{Name: "engi-conformance", Version: "0.0.0"}

// forEachSDK runs fn as a subtest per SDK, skipping when a tool is missing.
func forEachSDK(t *testing.T, fn func(t *testing.T, c sdkCase)) {
	t.Helper()
	for _, c := range sdkCases() {
		t.Run(c.name, func(t *testing.T) {
			need(t, c.tools...)
			fn(t, c)
		})
	}
}

// startStdio starts p, checks the handshake and returns its nodes by type.
func startStdio(t *testing.T, p plugin) (sup *stdio.Supervisor, nodes map[string]nodesdk.Descriptor) {
	t.Helper()
	return startStdioLogged(t, p, discardLogger())
}

// startStdioLogged is startStdio with the server's own logger.
func startStdioLogged(t *testing.T, p plugin, log *slog.Logger) (sup *stdio.Supervisor, nodes map[string]nodesdk.Descriptor) {
	t.Helper()
	sup = stdio.New(supervisorConfig("conformance", p), log)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace+time.Second)
		defer cancel()
		_ = sup.Close(ctx)
	})
	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()
	res, err := sup.Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return sup, checkNodes(t, res, p.nodes)
}

func checkNodes(t *testing.T, res pluginproto.InitializeResult, declared []string) map[string]nodesdk.Descriptor {
	t.Helper()
	got := map[string]nodesdk.Descriptor{}
	var types []string
	for _, d := range res.Nodes {
		desc := d.ToDescriptor()
		if err := pluginproto.CheckDescriptor(desc, declared); err != nil {
			t.Errorf("CheckDescriptor(%s): %v", desc.Type, err)
		}
		got[desc.Type] = desc
		types = append(types, desc.Type)
	}
	want := slices.Clone(declared)
	slices.Sort(want)
	slices.Sort(types)
	if !slices.Equal(want, types) {
		t.Fatalf("Start returned nodes %v, manifest declares %v", types, want)
	}
	return got
}

func newNode(d nodesdk.Descriptor, c pluginnode.Caller) nodesdk.Node {
	return pluginnode.New(d, c, pluginnode.Options{MaxBlobBytes: blobLimit})
}

func textItems() []nodesdk.Item {
	return nodetest.Items(map[string]any{"text": "Ada"}, map[string]any{"text": "Grace"})
}

func execCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	t.Cleanup(cancel)
	return ctx
}

// assertReversed checks the example output for textItems.
func assertReversed(t *testing.T, out nodesdk.Output) {
	t.Helper()
	if len(out.Ports) != 1 || len(out.Ports[0]) != 2 {
		t.Fatalf("output ports = %+v, want one port with two items", out.Ports)
	}
	for i, want := range []string{"adA", "ecarG"} {
		it := out.Ports[0][i]
		if it.JSON["reversed"] != want {
			t.Errorf("item %d reversed = %v, want %q", i, it.JSON["reversed"], want)
		}
		if len(it.PairedItem) != 1 || it.PairedItem[0] != (nodesdk.PairedItem{Input: 0, Item: i}) {
			t.Errorf("item %d pairedItem = %+v, want [{0 %d}]", i, it.PairedItem, i)
		}
	}
}

func TestStartAndExecute(t *testing.T) {
	forEachSDK(t, func(t *testing.T, c sdkCase) {
		sup, nodes := startStdio(t, c.example(t))
		node := newNode(nodes[c.reverseType], sup)
		out, err := node.Execute(execCtx(t), nodetest.New(node.Describe(), textItems(), nil))
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		assertReversed(t, out)
	})
}

// TestParamError makes the host report "field" as unresolvable and expects
// every SDK to surface it as a node error naming the parameter.
func TestParamError(t *testing.T) {
	forEachSDK(t, func(t *testing.T, c sdkCase) {
		sup, nodes := startStdio(t, c.example(t))
		desc := nodes[c.reverseType]
		props := slices.Clone(desc.Properties)
		for i := range props {
			if props[i].Name == "field" {
				props[i].Default, props[i].Required = nil, true
			}
		}
		desc.Properties = props
		_, err := newNode(desc, sup).Execute(execCtx(t), nodetest.New(desc, textItems(), nil))
		var ne *nodesdk.NodeError
		if !errors.As(err, &ne) {
			t.Fatalf("error = %T %v, want *nodesdk.NodeError", err, err)
		}
		if !strings.Contains(ne.Error(), "field") {
			t.Errorf("error %q does not name the parameter", ne.Error())
		}
		// docs/15 section 15.3: a paramErrors entry fails the node as a node
		// error (-32001) for that item, with the host's message once.
		if ne.ItemIndex == nil || *ne.ItemIndex != 0 {
			t.Errorf("ItemIndex = %v, want 0", ne.ItemIndex)
		}
		if strings.Count(ne.Error(), "required parameter is missing") > 1 || strings.Contains(ne.Error(), "code -32603") {
			t.Errorf("error %q is not a plain node error", ne.Error())
		}
	})
}

// TestMissingField points "field" at a field the items lack. The Go, Python
// and TypeScript examples fail with a node error; the .NET example silently
// yields "" (known cross-SDK difference, reported rather than hidden).
func TestMissingField(t *testing.T) {
	forEachSDK(t, func(t *testing.T, c sdkCase) {
		sup, nodes := startStdio(t, c.example(t))
		node := newNode(nodes[c.reverseType], sup)
		_, err := node.Execute(execCtx(t), nodetest.New(node.Describe(), textItems(), map[string]any{"field": "nope"}))
		var ne *nodesdk.NodeError
		if !errors.As(err, &ne) || !strings.Contains(ne.Error(), "nope") {
			t.Fatalf("error = %v, want a NodeError naming field \"nope\"", err)
		}
		if ne.ItemIndex == nil || *ne.ItemIndex != 0 {
			t.Errorf("ItemIndex = %v, want 0", ne.ItemIndex)
		}
	})
}

func TestConcurrentExecute(t *testing.T) {
	forEachSDK(t, func(t *testing.T, c sdkCase) {
		sup, nodes := startStdio(t, c.example(t))
		node := newNode(nodes[c.reverseType], sup)
		var wg sync.WaitGroup
		for n := range parallelCalls {
			wg.Add(1)
			go func() {
				defer wg.Done()
				out, err := node.Execute(execCtx(t), nodetest.New(node.Describe(), textItems(), nil))
				if err != nil {
					t.Errorf("call %d: %v", n, err)
					return
				}
				if len(out.Ports) != 1 || len(out.Ports[0]) != 2 || out.Ports[0][1].JSON["reversed"] != "ecarG" {
					t.Errorf("call %d: unexpected output %+v", n, out.Ports)
				}
			}()
		}
		wg.Wait()
	})
}

func processCount(marker string) int {
	out, err := exec.Command("pgrep", "-f", marker).Output()
	if err != nil {
		return 0
	}
	return len(strings.Fields(string(out)))
}

func TestClose(t *testing.T) {
	if _, err := exec.LookPath("pgrep"); err != nil {
		t.Skip("pgrep not found; cannot check that the process is gone")
	}
	forEachSDK(t, func(t *testing.T, c sdkCase) {
		p := c.example(t)
		// An extra argument no example reads makes the process findable.
		marker := fmt.Sprintf("conformance-marker-%s-%d", c.name, time.Now().UnixNano())
		p.command = append(slices.Clone(p.command), marker)
		sup := stdio.New(supervisorConfig("conformance-close", p), discardLogger())
		ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
		defer cancel()
		if _, err := sup.Start(ctx); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if processCount(marker) == 0 {
			t.Fatal("plugin process not found after Start")
		}
		begin := time.Now()
		if err := sup.Close(ctx); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if took := time.Since(begin); took > shutdownGrace+time.Second {
			t.Errorf("Close took %v, want <= %v", took, shutdownGrace+time.Second)
		} else {
			t.Logf("Close took %v", took)
		}
		if n := processCount(marker); n != 0 {
			t.Errorf("%d plugin process(es) still running after Close", n)
		}
	})
}

// TestExpressionCallback checks that a plugin's expression.eval reaches the
// input's EvalExpression with the right expression and item index.
func TestExpressionCallback(t *testing.T) {
	forEachSDK(t, func(t *testing.T, c sdkCase) {
		sup, nodes := startStdio(t, c.evalPlugin(t))
		node := newNode(nodes[c.evalType], sup)
		var mu sync.Mutex
		var calls []string
		in := nodetest.New(node.Describe(), textItems(), nil)
		in.EvalFunc = func(expr string, idx int) (any, error) {
			mu.Lock()
			defer mu.Unlock()
			calls = append(calls, fmt.Sprintf("%d:%s", idx, expr))
			return float64(2 + idx*10), nil
		}
		out, err := node.Execute(execCtx(t), in)
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if len(out.Ports) != 1 || len(out.Ports[0]) != 2 {
			t.Fatalf("output = %+v", out.Ports)
		}
		for i, want := range []float64{2, 12} {
			if got, ok := out.Ports[0][i].JSON["value"].(float64); !ok || got != want {
				t.Errorf("item %d value = %v, want %v", i, out.Ports[0][i].JSON["value"], want)
			}
		}
		slices.Sort(calls)
		if want := []string{"0:1+1", "1:1+1"}; !slices.Equal(calls, want) {
			t.Errorf("EvalExpression calls = %v, want %v", calls, want)
		}
	})
}

func TestHTTPTransport(t *testing.T) {
	forEachSDK(t, func(t *testing.T, c sdkCase) {
		if c.startHTTP == nil {
			t.Skip(c.skipReason)
		}
		url := c.startHTTP(t)
		cl := httprpc.New(httprpc.Config{
			Name: "conformance-http", URL: url, MaxMessageBytes: maxMessageBytes, CallTimeout: callTimeout,
			HostInfo: hostInfo, Declared: []string{c.reverseType},
		}, &http.Client{Timeout: callTimeout})
		var res pluginproto.InitializeResult
		// The server may still be binding its port.
		err := waitFor(startTimeout, func() (err error) {
			res, err = cl.Start(execCtx(t))
			return err
		})
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		nodes := checkNodes(t, res, []string{c.reverseType})
		node := newNode(nodes[c.reverseType], cl)
		out, err := node.Execute(execCtx(t), nodetest.New(node.Describe(), textItems(), nil))
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		assertReversed(t, out)
	})
}

// recordLog is a slog handler that keeps the messages it is given.
type recordLog struct {
	mu   sync.Mutex
	msgs []string
}

func (r *recordLog) Enabled(context.Context, slog.Level) bool { return true }
func (r *recordLog) Handle(_ context.Context, rec slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, rec.Message)
	return nil
}
func (r *recordLog) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *recordLog) WithGroup(string) slog.Handler      { return r }

func (r *recordLog) has(msg string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Contains(r.msgs, msg)
}

// nodeLogInput is a fake Input whose node logger is the given one.
type nodeLogInput struct {
	*nodetest.Input
	log *slog.Logger
}

func (i nodeLogInput) Logger() *slog.Logger { return i.log }

// TestPluginLogsReachNodeLogger checks that a line a plugin logs inside its
// handler reaches the node's logger (the run's log) and not the server log.
func TestPluginLogsReachNodeLogger(t *testing.T) {
	forEachSDK(t, func(t *testing.T, c sdkCase) {
		server, node := &recordLog{}, &recordLog{}
		sup, nodes := startStdioLogged(t, c.evalPlugin(t), slog.New(server))
		n := newNode(nodes[c.evalType], sup)
		in := nodeLogInput{Input: nodetest.New(n.Describe(), textItems(), nil), log: slog.New(node)}
		in.EvalFunc = func(string, int) (any, error) { return 2.0, nil }
		if _, err := n.Execute(execCtx(t), in); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		// A notification can be handled after the response; give it a moment.
		err := waitFor(5*time.Second, func() error {
			if node.has("evaluating") {
				return nil
			}
			return errors.New("the node logger has no line yet")
		})
		if err != nil {
			t.Errorf("node logger: %v", err)
		}
		if server.has("evaluating") {
			t.Error("a line logged inside a call must not reach the server log")
		}
	})
}
