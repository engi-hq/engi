// Package httprequest implements the HTTP Request node: one request per input
// item through the shared HTTP client.
//
// Authentication is applied from a credential: either one bound under
// "predefined" (any credential type that declares how to authenticate), or a
// generic one of a chosen type (basic, header, query key, bearer, OAuth2),
// bound under the type's own name.
//
// Binary data goes through the blob store: the "binary" response format stores
// the body as a binary field, and the "binary" body type sends one.
//
// TODO: pagination.
package httprequest

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.http.request"

// Body types.
const (
	bodyNone = "none"
	bodyJSON = "json"
	bodyForm = "form"
	bodyRaw  = "raw"
	// bodyBinary sends a binary field of the input item.
	bodyBinary = "binary"
)

// Response formats.
const (
	formatAuto   = "auto"
	formatJSON   = "json"
	formatText   = "text"
	formatBase64 = "base64"
	// formatBinary stores the body in the blob store.
	formatBinary = "binary"
)

// Authentication modes of the "authentication" parameter.
const (
	authNone       = "none"
	authPredefined = "predefined"
	authGeneric    = "generic"
)

// bindingPredefined is the credential binding used for predefined authentication.
const bindingPredefined = "predefined"

// genericTypes are the credential types offered for generic authentication.
// Each is also the binding name its credential is stored under.
var genericTypes = []struct{ name, display string }{
	{"engi.http.basic", "Basic Auth"},
	{"engi.http.header", "Header Auth"},
	{"engi.http.bearer", "Bearer Token"},
	{"engi.http.queryKey", "API Key in Query"},
	{"engi.oauth2", "OAuth2"},
}

// maxErrorBody bounds how much of a failing response body is quoted in an error.
const maxErrorBody = 512

// Node is the HTTP Request node.
type Node struct{}

// New returns an HTTP Request node.
func New() nodesdk.Node { return Node{} }

// Describe implements nodesdk.Node.
func (Node) Describe() nodesdk.Descriptor {
	hasBody := nodekit.Show("method", http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete)
	methods := []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions}
	methodOpts := make([]nodesdk.Option, len(methods))
	for i, m := range methods {
		methodOpts[i] = nodesdk.Option{Name: m, Value: m}
	}
	pairs := func(name, display, desc string, show *nodesdk.DisplayOptions) nodesdk.Property {
		return nodesdk.Property{Name: name, DisplayName: display, Type: nodesdk.PropFixedCollection, Description: desc, DisplayOptions: show,
			Children: []nodesdk.Property{
				{Name: "name", DisplayName: "Name", Type: nodesdk.PropString, Required: true},
				{Name: "value", DisplayName: "Value", Type: nodesdk.PropString},
			}}
	}
	genericOpts := make([]nodesdk.Option, len(genericTypes))
	creds := []nodesdk.CredentialRef{{Name: bindingPredefined}}
	for i, g := range genericTypes {
		genericOpts[i] = nodesdk.Option{Name: g.display, Value: g.name}
		creds = append(creds, nodesdk.CredentialRef{Name: g.name, Type: g.name})
	}
	return nodesdk.Descriptor{
		Credentials: creds,
		Type:        Type, Version: 1, DisplayName: "HTTP Request", Group: nodesdk.GroupAction, Icon: "globe",
		Description: "Call an HTTP endpoint once per input item.",
		Inputs:      []nodesdk.PortSpec{nodekit.Main("main", "")},
		Outputs:     []nodesdk.PortSpec{nodekit.Main("main", "")},
		Properties: []nodesdk.Property{
			{Name: "method", DisplayName: "Method", Type: nodesdk.PropOptions, Default: http.MethodGet, Options: methodOpts},
			{Name: "url", DisplayName: "URL", Type: nodesdk.PropString, Required: true, Placeholder: "https://api.example.com/items"},
			pairs("query", "Query parameters", "Appended to the URL; repeated names add repeated parameters.", nil),
			pairs("headers", "Headers", "Request headers.", nil),
			{Name: "authentication", DisplayName: "Authentication", Type: nodesdk.PropOptions, Default: authNone, Options: []nodesdk.Option{
				{Name: "None", Value: authNone},
				{Name: "Predefined credential", Value: authPredefined, Description: "Any credential type that knows how to authenticate requests."},
				{Name: "Generic credential", Value: authGeneric}}},
			{Name: "genericAuthType", DisplayName: "Credential type", Type: nodesdk.PropOptions, Default: genericTypes[0].name,
				Options: genericOpts, DisplayOptions: nodekit.Show("authentication", authGeneric)},
			{Name: "bodyType", DisplayName: "Body", Type: nodesdk.PropOptions, Default: bodyNone, DisplayOptions: hasBody, Options: []nodesdk.Option{
				{Name: "None", Value: bodyNone}, {Name: "JSON", Value: bodyJSON},
				{Name: "Form (urlencoded)", Value: bodyForm}, {Name: "Raw text", Value: bodyRaw},
				{Name: "Binary file", Value: bodyBinary, Description: "Send a binary field of the input item."}}},
			{Name: "inputBinaryField", DisplayName: "Input binary field", Type: nodesdk.PropString, Default: defaultBinaryField,
				DisplayOptions: nodekit.Show("bodyType", bodyBinary)},
			{Name: "jsonBody", DisplayName: "JSON body", Type: nodesdk.PropJSON, DisplayOptions: nodekit.Show("bodyType", bodyJSON),
				Description: "An object or array, or a string holding JSON."},
			pairs("formFields", "Form fields", "Sent as application/x-www-form-urlencoded.", nodekit.Show("bodyType", bodyForm)),
			{Name: "rawBody", DisplayName: "Raw body", Type: nodesdk.PropString, DisplayOptions: nodekit.Show("bodyType", bodyRaw)},
			{Name: "rawContentType", DisplayName: "Content type", Type: nodesdk.PropString, Default: "text/plain", DisplayOptions: nodekit.Show("bodyType", bodyRaw)},
			{Name: "responseFormat", DisplayName: "Response format", Type: nodesdk.PropOptions, Default: formatAuto, Options: []nodesdk.Option{
				{Name: "Auto (JSON when the server says so, else text)", Value: formatAuto},
				{Name: "JSON", Value: formatJSON}, {Name: "Text", Value: formatText},
				{Name: "Binary as base64 text", Value: formatBase64},
				{Name: "Binary file (stored, referenced by the item)", Value: formatBinary}}},
			{Name: "outputBinaryField", DisplayName: "Output binary field", Type: nodesdk.PropString, Default: defaultBinaryField,
				DisplayOptions: nodekit.Show("responseFormat", formatBinary)},
			{Name: "fullResponse", DisplayName: "Include status and headers", Type: nodesdk.PropBoolean, Default: false,
				Description: "Output {statusCode, statusMessage, headers, body} instead of just the body."},
			{Name: "failOnError", DisplayName: "Fail on non-2xx status", Type: nodesdk.PropBoolean, Default: true},
			{Name: "followRedirects", DisplayName: "Follow redirects", Type: nodesdk.PropBoolean, Default: true},
			{Name: "timeoutMs", DisplayName: "Timeout (ms)", Type: nodesdk.PropNumber, Default: float64(0),
				Description: "Per-request timeout. 0 uses the configured default."},
		},
	}
}

// settings are the resolved parameters of one request.
type settings struct {
	method, rawURL, bodyType, responseFormat string
	// authBinding names the credential binding to authenticate with; empty
	// means no authentication.
	authBinding string
	// inputBinary and outputBinary name the binary fields of the binary body
	// type and the binary response format.
	inputBinary, outputBinary string
	// bodyRef is the binary value to upload; nil unless bodyType is binary.
	bodyRef                           *nodesdk.BinaryRef
	query, headers                    []nodekit.KeyValue
	body                              []byte
	contentType                       string
	fullResponse, failOnError, follow bool
	timeout                           time.Duration
}

// Execute implements nodesdk.Node.
func (Node) Execute(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	var out []nodesdk.Item
	creds := map[string]nodesdk.Credential{} // resolved once per binding per run
	for i := range in.Items(0) {
		s, err := readSettings(ctx, in, i)
		if err != nil {
			return nodesdk.Output{}, nodekit.ItemError(i, err)
		}
		var auth *nodesdk.Credential
		if s.authBinding != "" {
			c, ok := creds[s.authBinding]
			if !ok {
				if c, err = in.Credential(ctx, s.authBinding); err != nil {
					return nodesdk.Output{}, nodekit.ItemError(i, fmt.Errorf("loading credential %q: %w", s.authBinding, err))
				}
				creds[s.authBinding] = c
			}
			auth = &c
		}
		items, err := do(ctx, in, s, auth)
		if err != nil {
			return nodesdk.Output{}, itemErr(i, err)
		}
		for _, it := range items {
			out = append(out, nodekit.Paired(it, i))
		}
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}

// itemErr attaches the item index to a request error, keeping HTTP details.
func itemErr(i int, err error) error {
	var ne *nodesdk.NodeError
	if errors.As(err, &ne) && ne.ItemIndex == nil {
		ne.ItemIndex = &i
		return ne
	}
	return nodekit.ItemError(i, err)
}

func readSettings(ctx context.Context, in nodesdk.Input, i int) (settings, error) {
	var s settings
	var err error
	str := func(name string) string {
		if err != nil {
			return ""
		}
		var v string
		v, err = nodesdk.ParamString(ctx, in, name, i)
		return v
	}
	flag := func(name string) bool {
		if err != nil {
			return false
		}
		var v bool
		v, err = nodesdk.ParamBool(ctx, in, name, i)
		return v
	}
	mode := str("authentication")
	switch mode {
	case authNone, "":
	case authPredefined:
		s.authBinding = bindingPredefined
	case authGeneric:
		s.authBinding = str("genericAuthType")
		if s.authBinding == "" {
			return s, errors.New("genericAuthType is required for generic authentication")
		}
	default:
		return s, fmt.Errorf("unknown authentication mode %q", mode)
	}
	s.method = strings.ToUpper(str("method"))
	s.rawURL = strings.TrimSpace(str("url"))
	s.bodyType = str("bodyType")
	s.responseFormat = str("responseFormat")
	s.fullResponse, s.failOnError, s.follow = flag("fullResponse"), flag("failOnError"), flag("followRedirects")
	if err != nil {
		return s, err
	}
	if s.rawURL == "" {
		return s, errors.New("url is required")
	}
	if s.method == "" {
		s.method = http.MethodGet
	}
	switch s.responseFormat {
	case formatAuto, formatJSON, formatText, formatBase64, formatBinary, "":
	default:
		return s, fmt.Errorf("unknown response format %q", s.responseFormat)
	}
	for _, p := range []struct {
		name string
		dst  *[]nodekit.KeyValue
	}{{"query", &s.query}, {"headers", &s.headers}} {
		raw, perr := in.Param(ctx, p.name, i)
		if perr != nil {
			return s, fmt.Errorf("reading %s: %w", p.name, perr)
		}
		if *p.dst, err = nodekit.KeyValues(raw); err != nil {
			return s, fmt.Errorf("%s: %w", p.name, err)
		}
	}
	if s.responseFormat == formatBinary {
		if s.outputBinary, err = binaryFieldParam(ctx, in, "outputBinaryField", i); err != nil {
			return s, err
		}
	}
	ms, err := nodekit.ParamFloat(ctx, in, "timeoutMs", i)
	if err != nil {
		return s, err //nolint:wrapcheck // already descriptive
	}
	if ms < 0 {
		return s, fmt.Errorf("timeoutMs must not be negative, got %v", ms)
	}
	s.timeout = time.Duration(ms * float64(time.Millisecond))
	err = readBody(ctx, in, i, &s)
	return s, err
}

func readBody(ctx context.Context, in nodesdk.Input, i int, s *settings) error {
	switch s.bodyType {
	case bodyNone, "":
		return nil
	case bodyJSON:
		raw, err := in.Param(ctx, "jsonBody", i)
		if err != nil {
			return fmt.Errorf("reading jsonBody: %w", err)
		}
		if text, ok := raw.(string); ok {
			if !json.Valid([]byte(text)) {
				return errors.New("jsonBody is not valid JSON")
			}
			s.body = []byte(text)
		} else if s.body, err = json.Marshal(raw); err != nil {
			return fmt.Errorf("encoding jsonBody: %w", err)
		}
		s.contentType = "application/json"
	case bodyForm:
		raw, err := in.Param(ctx, "formFields", i)
		if err != nil {
			return fmt.Errorf("reading formFields: %w", err)
		}
		kvs, err := nodekit.KeyValues(raw)
		if err != nil {
			return fmt.Errorf("formFields: %w", err)
		}
		form := url.Values{}
		for _, kv := range kvs {
			form.Add(kv.Name, nodekit.ToString(kv.Value))
		}
		s.body, s.contentType = []byte(form.Encode()), "application/x-www-form-urlencoded"
	case bodyBinary:
		field, err := binaryFieldParam(ctx, in, "inputBinaryField", i)
		if err != nil {
			return err
		}
		ref, ok := in.Items(0)[i].Binary[field]
		if !ok {
			return fmt.Errorf("the input item has no binary field %q", field)
		}
		s.inputBinary, s.bodyRef = field, &ref
		s.contentType = ref.MIME
	case bodyRaw:
		text, err := nodesdk.ParamString(ctx, in, "rawBody", i)
		if err != nil {
			return fmt.Errorf("reading rawBody: %w", err)
		}
		s.body = []byte(text)
		if s.contentType, err = nodesdk.ParamString(ctx, in, "rawContentType", i); err != nil {
			return fmt.Errorf("reading rawContentType: %w", err)
		}
	default:
		return fmt.Errorf("unknown body type %q", s.bodyType)
	}
	return nil
}

func buildRequest(ctx context.Context, in nodesdk.Input, s settings) (*http.Request, error) {
	u, err := url.Parse(s.rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid url %q: %w", s.rawURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("url %q must use http or https", s.rawURL)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("url %q has no host", s.rawURL)
	}
	if len(s.query) > 0 {
		q := u.Query()
		for _, kv := range s.query {
			q.Add(kv.Name, nodekit.ToString(kv.Value))
		}
		u.RawQuery = q.Encode()
	}
	ctx = nodesdk.WithHTTPCallOptions(ctx, nodesdk.HTTPCallOptions{NoRedirect: !s.follow})
	var body io.Reader
	if s.body != nil {
		body = bytes.NewReader(s.body)
	}
	if s.bodyRef != nil {
		rc, err := in.Blob().Open(ctx, *s.bodyRef)
		if err != nil {
			return nil, fmt.Errorf("opening binary field %q: %w", s.inputBinary, err)
		}
		body = rc
	}
	req, err := http.NewRequestWithContext(ctx, s.method, u.String(), body)
	if err != nil {
		if rc, ok := body.(io.Closer); ok {
			_ = rc.Close()
		}
		return nil, fmt.Errorf("building request: %w", err)
	}
	if s.bodyRef != nil {
		req.ContentLength = s.bodyRef.Size
	}
	if s.contentType != "" {
		req.Header.Set("Content-Type", s.contentType)
	}
	for _, kv := range s.headers {
		req.Header.Set(kv.Name, nodekit.ToString(kv.Value))
	}
	return req, nil
}

func do(ctx context.Context, in nodesdk.Input, s settings, auth *nodesdk.Credential) ([]nodesdk.Item, error) {
	if s.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.timeout)
		defer cancel()
	}
	req, err := buildRequest(ctx, in, s)
	if err != nil {
		return nil, err
	}
	if auth != nil {
		// After the user's headers and query, so the credential wins.
		if err := auth.Authenticate(req); err != nil {
			return nil, &nodesdk.NodeError{Message: "applying the credential failed", Description: err.Error(), Err: err}
		}
	}
	resp, err := in.HTTP().Do(req)
	if err != nil {
		return nil, &nodesdk.NodeError{Message: "request failed", Description: err.Error(), Err: err}
	}
	defer resp.Body.Close()
	if s.responseFormat == formatBinary && (!s.failOnError || (resp.StatusCode >= 200 && resp.StatusCode <= 299)) {
		return binaryResponse(ctx, in, s, resp)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &nodesdk.NodeError{Message: "reading the response failed", Description: err.Error(), HTTPStatus: resp.StatusCode, Err: err}
	}
	if s.failOnError && (resp.StatusCode < 200 || resp.StatusCode > 299) {
		return nil, &nodesdk.NodeError{
			Message:     fmt.Sprintf("request failed with status %s", resp.Status),
			Description: truncate(string(data), maxErrorBody), HTTPStatus: resp.StatusCode,
		}
	}
	format := s.responseFormat
	if format == formatBinary { // a failed call (failOnError off) already returned above; this is an error body
		format = formatText
	}
	body, err := decodeBody(data, resp.Header.Get("Content-Type"), format)
	if err != nil {
		return nil, &nodesdk.NodeError{Message: "decoding the response failed", Err: err, HTTPStatus: resp.StatusCode}
	}
	if s.fullResponse {
		headers := headerMap(resp.Header)
		return []nodesdk.Item{{JSON: map[string]any{
			"statusCode": float64(resp.StatusCode), "statusMessage": strings.TrimSpace(strings.TrimPrefix(resp.Status, fmt.Sprint(resp.StatusCode))),
			"headers": headers, "body": body,
		}}}, nil
	}
	return bodyItems(body), nil
}

// bodyItems maps a decoded body to items: an object is one item, an array is
// one item per element, anything else is {"data": body}.
func bodyItems(body any) []nodesdk.Item {
	switch t := body.(type) {
	case map[string]any:
		return []nodesdk.Item{{JSON: t}}
	case []any:
		items := make([]nodesdk.Item, len(t))
		for i, e := range t {
			if obj, ok := e.(map[string]any); ok {
				items[i] = nodesdk.Item{JSON: obj}
			} else {
				items[i] = nodesdk.Item{JSON: map[string]any{"data": e}}
			}
		}
		return items
	}
	return []nodesdk.Item{{JSON: map[string]any{"data": body}}}
}

func decodeBody(data []byte, contentType, format string) (any, error) {
	switch format {
	case formatBase64:
		return base64.StdEncoding.EncodeToString(data), nil
	case formatText:
		return string(data), nil
	case formatJSON:
		return parseJSON(data)
	case formatAuto, "":
		if strings.Contains(strings.ToLower(contentType), "json") && len(bytes.TrimSpace(data)) > 0 {
			if v, err := parseJSON(data); err == nil {
				return v, nil
			}
		}
		return string(data), nil
	}
	return nil, fmt.Errorf("unknown response format %q", format)
}

func parseJSON(data []byte) (any, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("response is not valid JSON: %w", err)
	}
	return v, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
