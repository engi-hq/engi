// Package webhooktrigger implements the Webhook trigger node. The trigger
// service registers its route when the workflow is activated and turns each
// HTTP call into the node's output item {headers, params, query, body}.
package webhooktrigger

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.trigger.webhook"

// Credential slots, one per authentication kind.
const (
	credBasic  = "basicAuth"
	credHeader = "headerAuth"
)

// Response data choices of the lastNode mode.
const (
	dataFirstEntry = "firstEntryJson"
	dataAllEntries = "allEntries"
	dataNone       = "noData"
)

var methods = []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead}

// Node is the Webhook trigger.
type Node struct{}

var _ nodesdk.WebhookTrigger = Node{}

// New returns a Webhook trigger node.
func New() nodesdk.Node { return Node{} }

// Describe implements nodesdk.Node.
func (Node) Describe() nodesdk.Descriptor {
	methodOpts := make([]nodesdk.Option, len(methods))
	for i, m := range methods {
		methodOpts[i] = nodesdk.Option{Name: m, Value: m}
	}
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "Webhook", Group: nodesdk.GroupTrigger, Icon: "webhook",
		Description: "Starts the workflow when its URL is called.",
		Outputs:     []nodesdk.PortSpec{nodekit.Main("main", "")},
		Credentials: []nodesdk.CredentialRef{
			{Name: credBasic, Type: "engi.http.basic"},
			{Name: credHeader, Type: "engi.http.header"},
		},
		Properties: []nodesdk.Property{
			{Name: "httpMethod", DisplayName: "HTTP method", Type: nodesdk.PropOptions, Default: http.MethodGet, Options: methodOpts},
			{Name: "path", DisplayName: "Path", Type: nodesdk.PropString, Placeholder: "orders/:id",
				Description: "Relative to the webhook base path. A segment starting with : captures a value into $json.params. Empty uses <workflow id>/<node name>."},
			{Name: "responseMode", DisplayName: "Respond", Type: nodesdk.PropOptions, Default: nodesdk.ResponseImmediately, Options: []nodesdk.Option{
				{Name: "Immediately", Value: nodesdk.ResponseImmediately, Description: "Answer at once; the workflow runs in the background."},
				{Name: "When the last node finishes", Value: nodesdk.ResponseLastNode},
				{Name: "Using a Respond to Webhook node", Value: nodesdk.ResponseNode}}},
			{Name: "authentication", DisplayName: "Authentication", Type: nodesdk.PropOptions, Default: nodesdk.WebhookAuthNone, Options: []nodesdk.Option{
				{Name: "None", Value: nodesdk.WebhookAuthNone}, {Name: "Basic auth", Value: nodesdk.WebhookAuthBasic},
				{Name: "Header token", Value: nodesdk.WebhookAuthHeader}}},
			{Name: "responseCode", DisplayName: "Response code", Type: nodesdk.PropNumber,
				Description:    "Status of the answer. Default 202 for immediate answers, 200 otherwise.",
				DisplayOptions: nodekit.Show("responseMode", nodesdk.ResponseImmediately, nodesdk.ResponseLastNode)},
			{Name: "responseBody", DisplayName: "Response body", Type: nodesdk.PropString,
				Description:    "Immediate answer body. Empty answers {\"executionId\": ...}.",
				DisplayOptions: nodekit.Show("responseMode", nodesdk.ResponseImmediately)},
			{Name: "responseData", DisplayName: "Response data", Type: nodesdk.PropOptions, Default: dataFirstEntry,
				DisplayOptions: nodekit.Show("responseMode", nodesdk.ResponseLastNode), Options: []nodesdk.Option{
					{Name: "First entry (JSON)", Value: dataFirstEntry}, {Name: "All entries", Value: dataAllEntries},
					{Name: "No data", Value: dataNone}}},
			{Name: "rawBody", DisplayName: "Raw body", Type: nodesdk.PropBoolean, Default: false,
				Description: "Deliver the body as text in $json.body instead of parsing it."},
		},
	}
}

// Execute implements nodesdk.Node. The host builds the trigger item; the node
// forwards it.
func (Node) Execute(_ context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	items := in.Items(0)
	out := make([]nodesdk.Item, len(items))
	for i, it := range items {
		out[i] = nodekit.Paired(it, i)
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}

// Webhook implements nodesdk.WebhookTrigger. params are the node parameters as
// saved, so expressions are not evaluated here.
func (Node) Webhook(params map[string]any) (nodesdk.WebhookSpec, error) {
	spec := nodesdk.WebhookSpec{
		Method:       strings.ToUpper(str(params, "httpMethod", http.MethodGet)),
		Path:         strings.Trim(str(params, "path", ""), "/"),
		ResponseMode: str(params, "responseMode", nodesdk.ResponseImmediately),
		Auth:         str(params, "authentication", nodesdk.WebhookAuthNone),
		ResponseBody: str(params, "responseBody", ""),
		ResponseData: str(params, "responseData", dataFirstEntry),
		RawBody:      flag(params, "rawBody"),
	}
	if !slices.Contains(methods, spec.Method) {
		return nodesdk.WebhookSpec{}, fmt.Errorf("httpMethod %q is not one of %s", spec.Method, strings.Join(methods, ", "))
	}
	if !slices.Contains([]string{nodesdk.ResponseImmediately, nodesdk.ResponseLastNode, nodesdk.ResponseNode}, spec.ResponseMode) {
		return nodesdk.WebhookSpec{}, fmt.Errorf("responseMode %q is not supported", spec.ResponseMode)
	}
	switch spec.Auth {
	case nodesdk.WebhookAuthNone:
	case nodesdk.WebhookAuthBasic:
		spec.AuthCredential = credBasic
	case nodesdk.WebhookAuthHeader:
		spec.AuthCredential = credHeader
	default:
		return nodesdk.WebhookSpec{}, fmt.Errorf("authentication %q is not supported", spec.Auth)
	}
	if !slices.Contains([]string{dataFirstEntry, dataAllEntries, dataNone}, spec.ResponseData) {
		return nodesdk.WebhookSpec{}, fmt.Errorf("responseData %q is not supported", spec.ResponseData)
	}
	if v, ok := params["responseCode"]; ok && v != nil && v != "" {
		code, err := nodekit.ToFloat(v, true)
		if err != nil || code != float64(int(code)) || code < 100 || code > 599 {
			return nodesdk.WebhookSpec{}, fmt.Errorf("responseCode %v is not an HTTP status", v)
		}
		spec.ResponseCode = int(code)
	}
	return spec, nil
}

func str(m map[string]any, key, def string) string {
	if s, ok := m[key].(string); ok && s != "" {
		return s
	}
	return def
}

func flag(m map[string]any, key string) bool {
	b, _ := m[key].(bool)
	return b
}
