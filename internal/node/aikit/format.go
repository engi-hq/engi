package aikit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Response format ids (the responseFormat parameter).
const (
	FormatText       = "text"
	FormatJSON       = "json"
	FormatJSONSchema = "jsonSchema"
)

// DefaultSchemaName names the JSON schema when the node sets no name.
const DefaultSchemaName = "response"

// FormatProperties are the responseFormat, schemaName and schema parameters.
func FormatProperties() []nodesdk.Property {
	schemaOnly := nodekit.Show("responseFormat", FormatJSONSchema)
	return []nodesdk.Property{
		{Name: "responseFormat", DisplayName: "Response format", Type: nodesdk.PropOptions, Default: FormatText,
			Options: []nodesdk.Option{
				nodekit.Opt(FormatText, "Text"), nodekit.Opt(FormatJSON, "JSON"), nodekit.Opt(FormatJSONSchema, "JSON schema"),
			}},
		{Name: "schemaName", DisplayName: "Schema name", Type: nodesdk.PropString, Default: DefaultSchemaName, DisplayOptions: schemaOnly},
		{Name: "schema", DisplayName: "JSON schema", Type: nodesdk.PropJSON, Required: true, DisplayOptions: schemaOnly,
			Description: "A JSON Schema object the final reply must conform to."},
	}
}

// ReadFormat reads the response format of item i. The second result is the
// format id (FormatText when unset).
func ReadFormat(ctx context.Context, in nodesdk.Input, i int) (port.ResponseFormat, string, error) {
	format, err := nodesdk.ParamString(ctx, in, "responseFormat", i)
	if err != nil {
		return port.ResponseFormat{}, "", err //nolint:wrapcheck // SDK errors are descriptive
	}
	switch format {
	case "", FormatText:
		return port.ResponseFormat{Kind: port.ResponseText}, FormatText, nil
	case FormatJSON:
		return port.ResponseFormat{Kind: port.ResponseJSON}, FormatJSON, nil
	case FormatJSONSchema:
		v, err := in.Param(ctx, "schema", i)
		if err != nil {
			return port.ResponseFormat{}, "", fmt.Errorf("reading parameter %q: %w", "schema", err)
		}
		var raw []byte
		switch t := v.(type) {
		case nil:
			return port.ResponseFormat{}, "", errors.New("schema is required for the JSON schema response format")
		case string:
			raw = []byte(t)
		default:
			if raw, err = json.Marshal(t); err != nil {
				return port.ResponseFormat{}, "", fmt.Errorf("encoding schema: %w", err)
			}
		}
		var obj map[string]any
		if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
			return port.ResponseFormat{}, "", errors.New("schema must be a JSON object")
		}
		name, err := nodesdk.ParamString(ctx, in, "schemaName", i)
		if err != nil {
			return port.ResponseFormat{}, "", err //nolint:wrapcheck // SDK errors are descriptive
		}
		if name = strings.TrimSpace(name); name == "" {
			name = DefaultSchemaName
		}
		return port.ResponseFormat{Kind: port.ResponseJSONSchema, Name: name, Schema: raw}, FormatJSONSchema, nil
	}
	return port.ResponseFormat{}, "", fmt.Errorf("unknown response format %q", format)
}

// ParseJSONReply decodes a model reply as one JSON value, tolerating a
// Markdown code fence around it.
func ParseJSONReply(text string) (any, error) {
	s := strings.TrimSpace(text)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
		if nl := strings.IndexByte(s, '\n'); nl >= 0 {
			s = s[nl+1:]
		}
		s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "```"))
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(s)))
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("decoding reply: %w", err)
	}
	if dec.More() {
		return nil, errors.New("unexpected text after the JSON value")
	}
	return v, nil
}
