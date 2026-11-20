package main

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/engi-hq/engi/pkg/nodesdk"
	"github.com/engi-hq/engi/pkg/pluginproto"
	"github.com/engi-hq/engi/pkg/pluginsdk"
)

type echoNode struct{}

func (echoNode) Describe() nodesdk.Descriptor {
	main := []nodesdk.PortSpec{{Name: "main"}}
	return nodesdk.Descriptor{Type: "acme.echo", Version: 1, DisplayName: "Echo", Group: nodesdk.GroupTransform, Inputs: main, Outputs: main}
}

func (echoNode) Execute(_ context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	return nodesdk.Output{Ports: [][]nodesdk.Item{in.Items(0)}}, nil
}

// echoPlugin writes a plugin directory for an HTTP plugin serving echoNode.
func echoPlugin(t *testing.T, version string) string {
	t.Helper()
	srv := httptest.NewServer(pluginsdk.Handler(pluginproto.Info{Name: "acme-echo", Version: version}, pluginsdk.Options{}, echoNode{}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	manifest := "name: acme-echo\nversion: " + version + "\nruntime:\n  url: " + srv.URL + "\nnodes: [acme.echo]\n"
	if err := os.WriteFile(filepath.Join(dir, pluginproto.ManifestFile), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPluginCommands(t *testing.T) {
	environ := []string{"ENGI_PLUGINS_DIR=" + filepath.Join(t.TempDir(), "plugins")}
	cli := func(args ...string) (code int, stdout, stderr string) {
		var out, errOut bytes.Buffer
		code = execute(t.Context(), append([]string{"plugin"}, args...), &out, &errOut, environ)
		return code, out.String(), errOut.String()
	}
	steps := []struct {
		args     []string
		wantCode int
		want     string
	}{
		{[]string{"list"}, exitOK, "no plugins installed"},
		{[]string{"install", echoPlugin(t, "1.0.0")}, exitOK, "installed acme-echo 1.0.0 (http): acme.echo"},
		{[]string{"install", echoPlugin(t, "1.0.0")}, exitFailure, "--force"},
		{[]string{"install", echoPlugin(t, "1.1.0")}, exitOK, "installed acme-echo 1.1.0"},
		{[]string{"list"}, exitOK, "acme-echo\t1.0.0 *1.1.0"},
		{[]string{"use", "acme-echo", "1.0.0"}, exitOK, "1.0.0 is active"},
		{[]string{"remove", "acme-echo", "--version", "1.1.0"}, exitOK, "removed"},
		{[]string{"list"}, exitOK, "acme-echo\t*1.0.0"},
		{[]string{"remove", "acme-echo"}, exitOK, "removed"},
		{[]string{"use", "acme-echo"}, exitUsage, "expected 2"},
		{[]string{"bogus"}, exitUsage, `unknown command "bogus"`},
	}
	for _, s := range steps {
		code, out, errOut := cli(s.args...)
		if code != s.wantCode || !strings.Contains(out+errOut, s.want) {
			t.Fatalf("engi plugin %v: code=%d stdout=%q stderr=%q, want %d and %q", s.args, code, out, errOut, s.wantCode, s.want)
		}
	}
}
