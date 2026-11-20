// Package aitoolhttp implements the HTTP tool sub-node (engi.ai.tool.http): a
// tool that calls one HTTP endpoint. The URL path, query, headers and body are
// templates with {name} placeholders the model fills in, declared by the
// tool's argument schema. The host is fixed by the node: a placeholder in the
// scheme or host is rejected, so a model cannot steer the request elsewhere.
package aitoolhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/engi-hq/engi/internal/node/aikit"
	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.ai.tool.http"

// Body types.
const (
	bodyNone = "none"
	bodyJSON = "json"
	bodyText = "text"
)

// Authentication modes.
const (
	authNone       = "none"
	authPredefined = "predefined"
	authGeneric    = "generic"
)

const bindingPredefined = "predefined"

// genericTypes are the credential types offered for generic authentication;
// each is also the binding name (same set as the HTTP Request node).
var genericTypes = []struct{ name, display string }{
	{"engi.http.basic", "Basic Auth"},
	{"engi.http.header", "Header Auth"},
	{"engi.http.bearer", "Bearer Token"},
	{"engi.http.queryKey", "API Key in Query"},
	{"engi.oauth2", "OAuth2"},
}

// truncationNote is appended to a response cut at the byte limit.
const truncationNote = "\n[response truncated]"

// Config is what the node needs from the host.
type Config struct {
	// Timeout bounds one request (nodes.ai.tool.httpTimeout).
	Timeout time.Duration
	// MaxResponseBytes truncates the returned body (nodes.ai.tool.httpMaxResponseBytes).
	MaxResponseBytes int
}

// Node is the HTTP tool sub-node.
type Node struct{ cfg Config }

var _ nodesdk.Supplier = (*Node)(nil)

// New returns an HTTP tool node.
func New(cfg Config) *Node { return &Node{cfg: cfg} }

// Describe implements nodesdk.Node.
func (n *Node) Describe() nodesdk.Descriptor {
	methods := []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}
	methodOpts := make([]nodesdk.Option, len(methods))
	for i, m := range methods {
		methodOpts[i] = nodekit.Opt(m, m)
	}
	pairs := func(name, display, desc string) nodesdk.Property {
		return nodesdk.Property{Name: name, DisplayName: display, Type: nodesdk.PropFixedCollection, Description: desc,
			Children: []nodesdk.Property{
				{Name: "name", DisplayName: "Name", Type: nodesdk.PropString, Required: true},
				{Name: "value", DisplayName: "Value", Type: nodesdk.PropString},
			}}
	}
	genericOpts := make([]nodesdk.Option, len(genericTypes))
	creds := []nodesdk.CredentialRef{{Name: bindingPredefined}}
	for i, g := range genericTypes {
		genericOpts[i] = nodekit.Opt(g.name, g.display)
		creds = append(creds, nodesdk.CredentialRef{Name: g.name, Type: g.name})
	}
	hasBody := nodekit.Show("bodyType", bodyJSON, bodyText)
	props := aikit.ToolProperties("call_api", "Calls an HTTP API and returns the response.")
	props = append(props,
		aikit.SchemaProperty("JSON Schema of the arguments the model fills in. Every {name} placeholder below must be one of its properties."),
		nodesdk.Property{Name: "method", DisplayName: "Method", Type: nodesdk.PropOptions, Default: http.MethodGet, Options: methodOpts},
		nodesdk.Property{Name: "url", DisplayName: "URL", Type: nodesdk.PropString, Required: true, Placeholder: "https://api.example.com/users/{id}",
			Description: "Use {name} for values the model fills in (escaped for you). Not allowed in the scheme or host."},
		pairs("query", "Query parameters", "Values may use {name} placeholders."),
		pairs("headers", "Headers", "Values may use {name} placeholders."),
		nodesdk.Property{Name: "bodyType", DisplayName: "Body", Type: nodesdk.PropOptions, Default: bodyNone, Options: []nodesdk.Option{
			nodekit.Opt(bodyNone, "None"), nodekit.Opt(bodyJSON, "JSON"), nodekit.Opt(bodyText, "Raw text")}},
		nodesdk.Property{Name: "body", DisplayName: "Body template", Type: nodesdk.PropString, DisplayOptions: hasBody,
			Description: "JSON: a string value that is exactly {name} becomes the argument with its type, {name} inside longer text is inserted as text. Raw text: {name} is inserted as text."},
		nodesdk.Property{Name: "authentication", DisplayName: "Authentication", Type: nodesdk.PropOptions, Default: authNone, Options: []nodesdk.Option{
			nodekit.Opt(authNone, "None"), nodekit.Opt(authPredefined, "Predefined credential"), nodekit.Opt(authGeneric, "Generic credential")}},
		nodesdk.Property{Name: "genericAuthType", DisplayName: "Credential type", Type: nodesdk.PropOptions, Default: genericTypes[0].name,
			Options: genericOpts, DisplayOptions: nodekit.Show("authentication", authGeneric)},
	)
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "HTTP Tool", Group: nodesdk.GroupAI, Icon: "globe",
		Description: "A tool that lets an agent call an HTTP API with values it fills in.",
		Outputs:     []nodesdk.PortSpec{{Name: "tool", Kind: nodesdk.PortAITool, DisplayName: "Tool"}},
		Credentials: creds,
		Properties:  props,
	}
}

// Execute implements nodesdk.Node. Sub-nodes never run as steps.
func (n *Node) Execute(context.Context, nodesdk.Input) (nodesdk.Output, error) {
	return nodesdk.Output{}, fmt.Errorf("http tool: %w", nodesdk.ErrUnavailable)
}

// Supply implements nodesdk.Supplier and returns a port.ToolProvider.
func (n *Node) Supply(ctx context.Context, in nodesdk.Input) (any, error) {
	meta, err := aikit.ReadToolMeta(ctx, in)
	if err != nil {
		return nil, err //nolint:wrapcheck // descriptive
	}
	t := &tool{cfg: n.cfg, meta: meta, http: in.HTTP()}
	if err := t.read(ctx, in); err != nil {
		return nil, fmt.Errorf("tool %q: %w", meta.Def.Name, err)
	}
	if err := t.checkPlaceholders(); err != nil {
		return nil, fmt.Errorf("tool %q: %w", meta.Def.Name, err)
	}
	if t.authBinding != "" {
		cred, err := in.Credential(ctx, t.authBinding)
		if err != nil {
			return nil, fmt.Errorf("loading credential %q: %w", t.authBinding, err)
		}
		t.auth = &cred
	}
	return aikit.Tools{t}, nil
}

type tool struct {
	cfg  Config
	meta aikit.ToolMeta
	http nodesdk.HTTPClient

	method, urlTmpl, bodyType, bodyTmpl string
	query, headers                      []nodekit.KeyValue
	authBinding                         string
	auth                                *nodesdk.Credential
	jsonBody                            any
}

func (t *tool) read(ctx context.Context, in nodesdk.Input) error {
	var err error
	str := func(name string) string {
		if err != nil {
			return ""
		}
		var v string
		v, err = nodesdk.ParamString(ctx, in, name, 0)
		return v
	}
	t.method, t.urlTmpl = strings.ToUpper(str("method")), strings.TrimSpace(str("url"))
	t.bodyType, t.bodyTmpl = str("bodyType"), str("body")
	mode := str("authentication")
	generic := str("genericAuthType")
	if err != nil {
		return err
	}
	if t.method == "" {
		t.method = http.MethodGet
	}
	if t.urlTmpl == "" {
		return errors.New("url is required")
	}
	switch mode {
	case authNone, "":
	case authPredefined:
		t.authBinding = bindingPredefined
	case authGeneric:
		if t.authBinding = generic; t.authBinding == "" {
			return errors.New("genericAuthType is required for generic authentication")
		}
	default:
		return fmt.Errorf("unknown authentication mode %q", mode)
	}
	for _, p := range []struct {
		name string
		dst  *[]nodekit.KeyValue
	}{{"query", &t.query}, {"headers", &t.headers}} {
		raw, perr := in.Param(ctx, p.name, 0)
		if perr != nil {
			return fmt.Errorf("reading %s: %w", p.name, perr)
		}
		if *p.dst, err = nodekit.KeyValues(raw); err != nil {
			return fmt.Errorf("%s: %w", p.name, err)
		}
	}
	switch t.bodyType {
	case bodyNone, "":
		t.bodyType, t.bodyTmpl = bodyNone, ""
	case bodyJSON:
		if strings.TrimSpace(t.bodyTmpl) == "" {
			return errors.New("a JSON body needs a body template")
		}
		if err := json.Unmarshal([]byte(t.bodyTmpl), &t.jsonBody); err != nil {
			return fmt.Errorf("body template is not valid JSON: %w", err)
		}
	case bodyText:
	default:
		return fmt.Errorf("unknown body type %q", t.bodyType)
	}
	// Surfaces a placeholder in the host now, not at the first call.
	if _, err := expandURL(t.urlTmpl, nil); err != nil {
		return err
	}
	return nil
}

// checkPlaceholders makes sure every placeholder is an argument the model can
// supply.
func (t *tool) checkPlaceholders() error {
	var all []string
	all = append(all, placeholders(t.urlTmpl)...)
	for _, kv := range append(append([]nodekit.KeyValue{}, t.query...), t.headers...) {
		all = append(all, placeholders(kv.Name)...)
		all = append(all, placeholders(nodekit.ToString(kv.Value))...)
	}
	all = append(all, placeholders(t.bodyTmpl)...)
	for _, name := range all {
		if _, ok := t.meta.Schema.Properties[name]; !ok {
			return fmt.Errorf("placeholder {%s} is not a property of the arguments schema", name)
		}
	}
	return nil
}

// Definition implements port.Tool.
func (t *tool) Definition() port.ToolDefinition { return t.meta.Def }

// Call implements port.Tool.
func (t *tool) Call(ctx context.Context, raw json.RawMessage) (port.ToolResult, error) {
	args, err := t.meta.Schema.ParseArgs(raw)
	if err != nil {
		return aikit.ErrorResult("%v", err), nil
	}
	req, err := t.request(ctx, args)
	if err != nil {
		return aikit.ErrorResult("%v", err), nil
	}
	if t.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.cfg.Timeout)
		defer cancel()
		req = req.WithContext(ctx)
	}
	if t.auth != nil {
		if err := t.auth.Authenticate(req); err != nil {
			return aikit.ErrorResult("applying the credential failed: %v", err), nil
		}
	}
	resp, err := t.http.Do(req)
	if err != nil {
		if ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled) {
			return port.ToolResult{}, fmt.Errorf("http tool canceled: %w", ctx.Err())
		}
		return aikit.ErrorResult("request failed: %v", err), nil
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(t.cfg.MaxResponseBytes)+1))
	if err != nil {
		return aikit.ErrorResult("reading the response failed: %v", err), nil
	}
	body := string(data)
	if len(data) > t.cfg.MaxResponseBytes {
		body = string(data[:t.cfg.MaxResponseBytes]) + truncationNote
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return aikit.ErrorResult("HTTP %d: %s", resp.StatusCode, body), nil
	}
	return port.ToolResult{Content: body}, nil
}

func (t *tool) request(ctx context.Context, args map[string]any) (*http.Request, error) {
	rawURL, err := expandURL(t.urlTmpl, args)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}
	if len(t.query) > 0 {
		q := u.Query()
		for _, kv := range t.query {
			q.Add(interpolate(kv.Name, args, verbatim), interpolate(nodekit.ToString(kv.Value), args, verbatim))
		}
		u.RawQuery = q.Encode()
	}
	var body io.Reader
	contentType := ""
	switch t.bodyType {
	case bodyJSON:
		b, err := json.Marshal(expandJSON(t.jsonBody, args))
		if err != nil {
			return nil, fmt.Errorf("encoding the body: %w", err)
		}
		body, contentType = bytes.NewReader(b), "application/json"
	case bodyText:
		body, contentType = strings.NewReader(interpolate(t.bodyTmpl, args, verbatim)), "text/plain"
	}
	req, err := http.NewRequestWithContext(ctx, t.method, u.String(), body)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for _, kv := range t.headers {
		v := interpolate(nodekit.ToString(kv.Value), args, verbatim)
		if strings.ContainsAny(v, "\r\n") {
			return nil, fmt.Errorf("header %q value contains a line break", kv.Name)
		}
		req.Header.Set(kv.Name, v)
	}
	return req, nil
}
