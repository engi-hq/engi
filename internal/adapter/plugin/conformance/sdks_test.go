//go:build integration

package conformance_test

import (
	"bufio"
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/engi-hq/engi/pkg/nodesdk"
	"github.com/engi-hq/engi/pkg/pluginproto"
	"github.com/engi-hq/engi/pkg/pluginsdk"
)

// sdkCase describes how to build and start the plugins of one SDK.
type sdkCase struct {
	name  string
	tools []string
	// reverseType is the example node, evalType the callback test node.
	reverseType, evalType string
	// example returns the SDK's example plugin; evalPlugin the callback one.
	example, evalPlugin func(t *testing.T) plugin
	// startHTTP serves the reverse node over HTTP and returns the base URL;
	// nil means the SDK has no way to do so (skipReason says why).
	startHTTP  func(t *testing.T) string
	skipReason string
}

func sdkCases() []sdkCase {
	return []sdkCase{
		{name: "go", tools: []string{"go"}, reverseType: "example.go.reverse", evalType: "conformance.go.eval",
			example: goPlugin(nil, "example.go.reverse"), evalPlugin: goPlugin([]string{"eval"}, "conformance.go.eval"),
			startHTTP: startGoHTTP},
		{name: "python", tools: []string{"python3"}, reverseType: "example.python.reverse", evalType: "conformance.python.eval",
			example: examplePlugin("sdk/python/examples/text", nil), evalPlugin: staticPlugin("python3", "eval.py", "py", "conformance.python.eval"),
			startHTTP: startPythonHTTP},
		{name: "typescript", tools: []string{"node", "npm"}, reverseType: "example.ts.reverse", evalType: "conformance.ts.eval",
			example: examplePlugin("sdk/typescript/examples/text", buildTypeScript), evalPlugin: tsEvalPlugin,
			startHTTP: startTypeScriptHTTP},
		{name: "dotnet", tools: []string{"dotnet"}, reverseType: "example.dotnet.reverse", evalType: "conformance.dotnet.eval",
			example: dotnetExample, evalPlugin: dotnetConformance("eval", "conformance.dotnet.eval"),
			startHTTP: startDotnetHTTP},
	}
}

// manifestOf parses dir/engi-plugin.yaml.
func manifestOf(t *testing.T, dir string) pluginproto.Manifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "engi-plugin.yaml"))
	if err != nil {
		t.Fatalf("reading manifest: %v", err)
	}
	m, err := pluginproto.ParseManifest(data)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	return m
}

// examplePlugin runs an example straight from its directory using its manifest.
func examplePlugin(rel string, prepare func(t *testing.T)) func(*testing.T) plugin {
	return func(t *testing.T) plugin {
		t.Helper()
		if prepare != nil {
			prepare(t)
		}
		dir := filepath.Join(repoRoot, filepath.FromSlash(rel))
		m := manifestOf(t, dir)
		return plugin{dir: dir, command: m.Runtime.Command, env: hostEnv(), nodes: m.Nodes}
	}
}

// staticPlugin runs a script from testdata/<sub>.
func staticPlugin(prog, script, sub, node string) func(*testing.T) plugin {
	return func(t *testing.T) plugin {
		t.Helper()
		return plugin{dir: filepath.Join(repoRoot, "internal", "adapter", "plugin", "conformance", "testdata", sub),
			command: []string{prog, script}, env: hostEnv(), nodes: []string{node}}
	}
}

func tsEvalPlugin(t *testing.T) plugin {
	t.Helper()
	buildTypeScript(t)
	return staticPlugin("node", "eval.mjs", "ts", "conformance.ts.eval")(t)
}

func buildTypeScript(t *testing.T) {
	t.Helper()
	sdkDir := filepath.Join(repoRoot, "sdk", "typescript")
	_, err := builds.get("ts", func() (string, error) {
		if _, err := os.Stat(filepath.Join(sdkDir, "node_modules")); err != nil {
			if err := run(sdkDir, nil, "npm", "ci"); err != nil {
				return "", err
			}
		}
		return sdkDir, run(sdkDir, nil, "npm", "run", "build")
	})
	if err != nil {
		t.Fatalf("building TypeScript SDK: %v", err)
	}
}

func goPlugin(args []string, node string) func(*testing.T) plugin {
	return func(t *testing.T) plugin {
		t.Helper()
		dir, err := builds.get("go", func() (string, error) {
			out := filepath.Join(workDir, "go")
			if err := os.MkdirAll(out, 0o750); err != nil {
				return "", err
			}
			return out, run(repoRoot, nil, "go", "build", "-o", filepath.Join(out, "goplugin"),
				"./internal/adapter/plugin/conformance/testdata/goplugin")
		})
		if err != nil {
			t.Fatalf("building Go plugin: %v", err)
		}
		return plugin{dir: dir, command: append([]string{"./goplugin"}, args...), env: hostEnv(), nodes: []string{node}}
	}
}

func publishDotnet(name, project string, props ...string) (string, error) {
	return builds.get("dotnet-"+name, func() (string, error) {
		root := filepath.Join(workDir, "dotnet-"+name)
		args := append([]string{"publish", project, "-c", "Release", "-o", filepath.Join(root, "dist"), "--nologo", "-p:UseSharedCompilation=false", "--disable-build-servers"}, props...)
		return root, run(repoRoot, dotnetEnv(), "dotnet", args...)
	})
}

func dotnetExample(t *testing.T) plugin {
	t.Helper()
	exDir := filepath.Join(repoRoot, "sdk", "dotnet", "examples", "Text")
	m := manifestOf(t, exDir)
	root, err := publishDotnet("text", filepath.Join(exDir, "Text.csproj"))
	if err != nil {
		t.Fatalf("publishing .NET example: %v", err)
	}
	return plugin{dir: root, command: m.Runtime.Command, env: dotnetEnv(), nodes: m.Nodes}
}

// dotnetConformanceRoot publishes testdata/dotnet from a temp copy so no
// obj/ directory appears under testdata.
func dotnetConformanceRoot(t *testing.T) string {
	t.Helper()
	root, err := builds.get("dotnet-conf-src", func() (string, error) {
		src := filepath.Join(workDir, "dotnet-conf-src")
		if err := os.MkdirAll(src, 0o750); err != nil {
			return "", err
		}
		for _, f := range []string{"Conformance.csproj", "Program.cs"} {
			data, err := os.ReadFile(filepath.Join(repoRoot, "internal", "adapter", "plugin", "conformance", "testdata", "dotnet", f))
			if err != nil {
				return "", err
			}
			if err := os.WriteFile(filepath.Join(src, f), data, 0o600); err != nil {
				return "", err
			}
		}
		sdkProj := filepath.Join(repoRoot, "sdk", "dotnet", "src", "Engi.Plugin", "Engi.Plugin.csproj")
		return publishDotnet("conf", filepath.Join(src, "Conformance.csproj"), "-p:EngiPluginProject="+sdkProj)
	})
	if err != nil {
		t.Fatalf("publishing .NET conformance plugin: %v", err)
	}
	return root
}

func dotnetConformance(mode, node string) func(*testing.T) plugin {
	return func(t *testing.T) plugin {
		t.Helper()
		return plugin{dir: dotnetConformanceRoot(t), command: []string{"dotnet", "dist/Conformance.dll", mode}, env: dotnetEnv(), nodes: []string{node}}
	}
}

// goReverse is the Go SDK node behind the in-process HTTP test.
type goReverse struct{}

func (goReverse) Describe() nodesdk.Descriptor {
	return nodesdk.Descriptor{
		Type: "example.go.reverse", Version: 1, DisplayName: "Reverse", Group: nodesdk.GroupTransform,
		Inputs:     []nodesdk.PortSpec{{Name: "main", Kind: nodesdk.PortMain}},
		Outputs:    []nodesdk.PortSpec{{Name: "main", Kind: nodesdk.PortMain}},
		Properties: []nodesdk.Property{{Name: "field", DisplayName: "Field", Type: nodesdk.PropString, Default: "text", Required: true}},
	}
}

func (goReverse) Execute(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	var out []nodesdk.Item
	for i, item := range in.Items(0) {
		f, err := nodesdk.ParamString(ctx, in, "field", i)
		if err != nil {
			return nodesdk.Output{}, err
		}
		r := []rune(fmt.Sprint(item.JSON[f]))
		for a, b := 0, len(r)-1; a < b; a, b = a+1, b-1 {
			r[a], r[b] = r[b], r[a]
		}
		out = append(out, nodesdk.Item{JSON: map[string]any{"reversed": string(r)}, PairedItem: []nodesdk.PairedItem{{Input: 0, Item: i}}})
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}

func startGoHTTP(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(pluginsdk.Handler(pluginproto.Info{Name: "conformance-go-http", Version: "0.1.0"}, pluginsdk.Options{}, goReverse{}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// spawnPortPrinter runs a script that prints its HTTP port on the first stdout
// line and returns the base URL; the process is killed at test end.
func spawnPortPrinter(t *testing.T, dir string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir, cmd.Env = dir, hostEnv()
	cmd.Stderr = os.Stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("starting %v: %v", args, err)
	}
	t.Cleanup(func() { cancel(); _ = cmd.Wait() })
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatalf("reading port from %v: %v", args, err)
	}
	port, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatalf("bad port line %q: %v", line, err)
	}
	return "http://127.0.0.1:" + strconv.Itoa(port)
}

func startPythonHTTP(t *testing.T) string {
	t.Helper()
	ex := filepath.Join(repoRoot, "sdk", "python", "examples", "text")
	dir := filepath.Join(repoRoot, "internal", "adapter", "plugin", "conformance", "testdata", "py")
	return spawnPortPrinter(t, dir, "python3", "serve_http.py", ex)
}

func startTypeScriptHTTP(t *testing.T) string {
	t.Helper()
	buildTypeScript(t)
	dir := filepath.Join(repoRoot, "internal", "adapter", "plugin", "conformance", "testdata", "ts")
	return spawnPortPrinter(t, dir, "node", "serve_http.mjs")
}

func startDotnetHTTP(t *testing.T) string {
	t.Helper()
	root := dotnetConformanceRoot(t)
	port, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	prefix := fmt.Sprintf("http://127.0.0.1:%d/", port)
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "dotnet", "dist/Conformance.dll", "http", prefix)
	cmd.Dir, cmd.Env, cmd.Stderr = root, dotnetEnv(), os.Stderr
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("starting dotnet: %v", err)
	}
	t.Cleanup(func() { cancel(); _ = cmd.Wait() })
	return strings.TrimRight(prefix, "/")
}

// waitFor retries fn until it succeeds or the deadline passes.
func waitFor(d time.Duration, fn func() error) error {
	deadline := time.Now().Add(d)
	for {
		err := fn()
		if err == nil || time.Now().After(deadline) {
			return err
		}
		time.Sleep(200 * time.Millisecond)
	}
}
