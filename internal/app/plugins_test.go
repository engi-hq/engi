package app

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/pkg/nodesdk"
	"github.com/engi-hq/engi/pkg/pluginproto"
	"github.com/engi-hq/engi/pkg/pluginsdk"
)

// upperNode upper-cases the string field named by its "field" parameter.
type upperNode struct{}

func (upperNode) Describe() nodesdk.Descriptor {
	main := []nodesdk.PortSpec{{Name: "main"}}
	return nodesdk.Descriptor{
		Type: "acme.text.upper", Version: 1, DisplayName: "Upper", Group: nodesdk.GroupTransform,
		Inputs: main, Outputs: main,
		Properties: []nodesdk.Property{{Name: "field", Type: nodesdk.PropString, Default: "text"}},
	}
}

func (upperNode) Execute(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	items := in.Items(0)
	for i := range items {
		field, err := nodesdk.ParamString(ctx, in, "field", i)
		if err != nil {
			return nodesdk.Output{}, err
		}
		s, _ := items[i].JSON[field].(string)
		items[i].JSON[field] = strings.ToUpper(s)
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{items}}, nil
}

// httpPlugin serves upperNode over HTTP and writes a plugin directory for it.
func httpPlugin(t *testing.T, name string, nodes ...string) string {
	t.Helper()
	srv := httptest.NewServer(pluginsdk.Handler(pluginproto.Info{Name: name, Version: "1.0.0"}, pluginsdk.Options{}, upperNode{}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	manifest := "name: " + name + "\nversion: 1.0.0\nruntime:\n  url: " + srv.URL + "\nnodes: [" + strings.Join(nodes, ", ") + "]\n"
	if err := os.WriteFile(filepath.Join(dir, pluginproto.ManifestFile), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func pluginConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.Plugins.Dir = filepath.Join(t.TempDir(), "plugins")
	return cfg
}

func testOptions(logs *bytes.Buffer) Options {
	return Options{LogOutput: logs, Version: "test", LookupEnv: func(string) (string, bool) { return "", false }}
}

func TestInstalledPluginNodesRunInWorkflows(t *testing.T) {
	cfg := pluginConfig(t)
	var logs bytes.Buffer
	m, err := NewPluginManager(cfg, testOptions(&logs))
	if err != nil {
		t.Fatal(err)
	}
	st, err := m.Install(t.Context(), httpPlugin(t, "acme-text", "acme.text.upper"), false)
	if err != nil {
		t.Fatal(err)
	}
	if st.Transport != transportHTTP || !slices.Equal(st.Nodes, []string{"acme.text.upper"}) {
		t.Fatalf("install status = %+v", st)
	}

	a, err := New(t.Context(), cfg, testOptions(&logs))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.ClosePlugins(context.Background()) })
	got := a.Plugins()
	if len(got) != 1 || got[0].State != domain.PluginLoaded || got[0].Name != "acme-text" {
		t.Fatalf("plugins = %+v", got)
	}

	wf := workflow(t, `{"name":"plugin","nodes":[
	  {"name":"Start","type":"engi.trigger.manual","position":[0,0]},
	  {"name":"Upper","type":"acme.text.upper","position":[1,0],"parameters":{"field":"={{ 'name' }}"}}],
	  "connections":[{"from":{"node":"Start"},"to":{"node":"Upper"}}]}`)
	if err := a.Validate(wf); err != nil {
		t.Fatal(err)
	}
	state, err := a.Run(t.Context(), RunRequest{Workflow: wf, Items: []domain.Item{{JSON: map[string]any{"name": "ada"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != domain.StatusSuccess {
		t.Fatalf("status = %s, error = %+v", state.Status, state.Error)
	}
	if out := state.RunData["Upper"][0].Output[domain.PortMain][0]; len(out) != 1 || out[0].JSON["name"] != "ADA" {
		t.Fatalf("output = %+v", out)
	}
}

func TestBrokenPluginsAreReportedNotFatal(t *testing.T) {
	cfg := pluginConfig(t)
	var logs bytes.Buffer
	m, err := NewPluginManager(cfg, testOptions(&logs))
	if err != nil {
		t.Fatal(err)
	}
	// Two plugins serve the same node type: the first by name loads, the other fails.
	clash := httpPlugin(t, "clash", "acme.text.upper")
	if _, err := m.Install(t.Context(), clash, false); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Install(t.Context(), httpPlugin(t, "twin", "acme.text.upper"), false); err != nil {
		t.Fatal(err)
	}
	// A plugin whose service is gone by the time the server starts.
	gone := filepath.Join(cfg.Plugins.Dir, "gone", "1.0.0")
	if err := os.MkdirAll(gone, 0o750); err != nil {
		t.Fatal(err)
	}
	manifest := "name: gone\nversion: 1.0.0\nruntime:\n  url: http://127.0.0.1:1\nnodes: [acme.gone]\n"
	if err := os.WriteFile(filepath.Join(gone, pluginproto.ManifestFile), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.Use("gone", "1.0.0"); err != nil {
		t.Fatal(err)
	}

	a, err := New(t.Context(), cfg, testOptions(&logs))
	if err != nil {
		t.Fatalf("a broken plugin must not stop the start: %v", err)
	}
	t.Cleanup(func() { _ = a.ClosePlugins(context.Background()) })
	states := map[string]domain.PluginStatus{}
	for _, s := range a.Plugins() {
		states[s.Name] = s
	}
	if states["clash"].State != domain.PluginLoaded {
		t.Fatalf("clash = %+v", states["clash"])
	}
	if s := states["twin"]; s.State != domain.PluginFailed || !strings.Contains(s.Error, "already registered") {
		t.Fatalf("twin = %+v", s)
	}
	if s := states["gone"]; s.State != domain.PluginFailed || s.Error == "" {
		t.Fatalf("gone = %+v", s)
	}
}

func TestInstallRefusesAPluginThatBreaksTheRules(t *testing.T) {
	cfg := pluginConfig(t)
	var logs bytes.Buffer
	m, err := NewPluginManager(cfg, testOptions(&logs))
	if err != nil {
		t.Fatal(err)
	}
	// The manifest does not list the type the plugin serves.
	src := httpPlugin(t, "sneaky", "acme.other")
	if _, err := m.Install(t.Context(), src, false); err == nil || !strings.Contains(err.Error(), "not listed") {
		t.Fatalf("err = %v", err)
	}
	if list, err := m.List(); err != nil || len(list) != 0 {
		t.Fatalf("a refused plugin was installed: %+v, %v", list, err)
	}
}

func TestPluginEnvironmentIsAllowListedAndNeverCarriesEngiSettings(t *testing.T) {
	env := pluginEnv{
		cfg: config.PluginsConfig{InheritEnv: []string{"PATH", "MISSING", "ENGI_ENCRYPTION_KEYS"}},
		lookupEnv: func(k string) (string, bool) {
			v, ok := map[string]string{"PATH": "/bin", "HOME": "/root", "ENGI_ENCRYPTION_KEYS": "secret"}[k]
			return v, ok
		},
	}
	m := pluginproto.Manifest{Runtime: pluginproto.Runtime{Env: map[string]string{"MODE": "fast", "engi_x": "y"}}}
	got := pluginEnviron(env, m)
	if !slices.Equal(got, []string{"PATH=/bin", "MODE=fast"}) {
		t.Fatalf("environment = %v", got)
	}
}

func TestPluginsEndpointListsInstalledPlugins(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "plugins")
	cfg := config.Default()
	cfg.Plugins.Dir = dir
	var logs bytes.Buffer
	m, err := NewPluginManager(cfg, testOptions(&logs))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Install(t.Context(), httpPlugin(t, "acme-text", "acme.text.upper"), false); err != nil {
		t.Fatal(err)
	}
	f := newServerFixture(t, func(c *config.Config) { c.Plugins.Dir = dir })
	t.Cleanup(func() { _ = f.app.ClosePlugins(context.Background()) })

	wantProblem(t, f.do(req{method: "GET", path: "/api/v1/plugins"}), http.StatusUnauthorized)
	s := f.ownerSession()
	r := f.as(s, req{method: "GET", path: "/api/v1/plugins"})
	wantStatus(t, r, http.StatusOK)
	data, _ := r.json(t)["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("plugins: %s", r.body)
	}
	p := data[0].(map[string]any)
	if p["name"] != "acme-text" || p["state"] != "loaded" || p["transport"] != "http" || p["nodes"].([]any)[0] != "acme.text.upper" {
		t.Fatalf("plugin = %v", p)
	}
	nodes := f.as(s, req{method: "GET", path: "/api/v1/node-types/acme.text.upper"})
	wantStatus(t, nodes, http.StatusOK)
}
