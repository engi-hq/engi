// Package respondtowebhook implements the Respond to Webhook node. It answers
// the HTTP call that started the execution (a Webhook trigger in responseNode
// mode) and passes its input items on, so the workflow continues after the reply.
package respondtowebhook

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.flow.respondToWebhook"

// What to answer with.
const (
	withFirstItem = "firstItem"
	withAllItems  = "allItems"
	withText      = "text"
	withJSON      = "json"
	withNoData    = "noData"
)

// defaultStatus is the status when responseCode is not set.
const defaultStatus = http.StatusOK

// Node is the Respond to Webhook node.
type Node struct{}

// New returns a Respond to Webhook node.
func New() nodesdk.Node { return Node{} }

var _ nodesdk.WebhookResponder = Node{}

// RespondsToWebhook implements nodesdk.WebhookResponder.
func (Node) RespondsToWebhook() {}

// Describe implements nodesdk.Node.
func (Node) Describe() nodesdk.Descriptor {
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "Respond to Webhook", Group: nodesdk.GroupFlow, Icon: "reply",
		Description: "Answer the webhook call that started this execution, then continue.",
		Inputs:      []nodesdk.PortSpec{nodekit.Main("main", "")},
		Outputs:     []nodesdk.PortSpec{nodekit.Main("main", "")},
		Properties: []nodesdk.Property{
			{Name: "respondWith", DisplayName: "Respond with", Type: nodesdk.PropOptions, Default: withFirstItem, Options: []nodesdk.Option{
				{Name: "First incoming item", Value: withFirstItem}, {Name: "All incoming items", Value: withAllItems},
				{Name: "Text", Value: withText}, {Name: "JSON", Value: withJSON}, {Name: "No data", Value: withNoData}}},
			{Name: "responseBody", DisplayName: "Body", Type: nodesdk.PropString,
				Description:    "Text, or JSON for the JSON option. May be an expression.",
				DisplayOptions: nodekit.Show("respondWith", withText, withJSON)},
			{Name: "responseCode", DisplayName: "Status code", Type: nodesdk.PropNumber, Default: float64(defaultStatus)},
			{Name: "headers", DisplayName: "Headers", Type: nodesdk.PropFixedCollection, Description: "List of {name, value} response headers."},
		},
	}
}

// Execute implements nodesdk.Node.
func (Node) Execute(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	items := in.Items(0)
	out := make([]nodesdk.Item, len(items))
	for i, it := range items {
		out[i] = nodekit.Paired(it, i)
	}
	resp, err := build(ctx, in, items)
	if err != nil {
		return nodesdk.Output{}, err
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}, Response: resp}, nil
}

func build(ctx context.Context, in nodesdk.Input, items []nodesdk.Item) (*nodesdk.WebhookResponse, error) {
	with, err := nodesdk.ParamString(ctx, in, "respondWith", 0)
	if err != nil {
		return nil, fmt.Errorf("reading respondWith: %w", err)
	}
	code := defaultStatus
	if v, err := in.Param(ctx, "responseCode", 0); err != nil {
		return nil, fmt.Errorf("reading responseCode: %w", err)
	} else if v != nil && v != "" {
		f, err := nodekit.ToFloat(v, true)
		if err != nil || f != float64(int(f)) || f < 100 || f > 599 {
			return nil, fmt.Errorf("responseCode %v is not an HTTP status", v)
		}
		code = int(f)
	}
	headers := http.Header{}
	raw, err := in.Param(ctx, "headers", 0)
	if err != nil {
		return nil, fmt.Errorf("reading headers: %w", err)
	}
	kvs, err := nodekit.KeyValues(raw)
	if err != nil {
		return nil, fmt.Errorf("headers: %w", err)
	}
	for _, kv := range kvs {
		headers.Add(kv.Name, nodekit.ToString(kv.Value))
	}

	var body []byte
	contentType := ""
	switch with {
	case withFirstItem, "":
		doc := map[string]any{}
		if len(items) > 0 && items[0].JSON != nil {
			doc = items[0].JSON
		}
		body, contentType, err = encodeJSON(doc)
	case withAllItems:
		docs := make([]any, len(items))
		for i, it := range items {
			docs[i] = nodekit.Clone(it.JSON)
		}
		body, contentType, err = encodeJSON(docs)
	case withText:
		text, terr := in.Param(ctx, "responseBody", 0)
		if terr != nil {
			return nil, fmt.Errorf("reading responseBody: %w", terr)
		}
		body, contentType = []byte(nodekit.ToString(text)), "text/plain; charset=utf-8"
	case withJSON:
		v, jerr := in.Param(ctx, "responseBody", 0)
		if jerr != nil {
			return nil, fmt.Errorf("reading responseBody: %w", jerr)
		}
		if s, ok := v.(string); ok {
			if !json.Valid([]byte(s)) {
				return nil, fmt.Errorf("responseBody is not valid JSON")
			}
			body, contentType = []byte(s), "application/json"
		} else {
			body, contentType, err = encodeJSON(v)
		}
	case withNoData:
	default:
		return nil, fmt.Errorf("unknown respondWith %q", with)
	}
	if err != nil {
		return nil, err
	}
	if contentType != "" && !hasHeader(headers, "Content-Type") {
		headers.Set("Content-Type", contentType)
	}
	return &nodesdk.WebhookResponse{Status: code, Headers: headers, Body: body}, nil
}

func encodeJSON(v any) (body []byte, contentType string, err error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, "", fmt.Errorf("encoding response: %w", err)
	}
	return b, "application/json", nil
}

func hasHeader(h http.Header, name string) bool {
	for k := range h {
		if strings.EqualFold(k, name) {
			return true
		}
	}
	return false
}
