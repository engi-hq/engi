package airtable

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/engi-hq/engi/internal/node/chatkit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// ErrAirtable is wrapped by every error Airtable's API reports.
var ErrAirtable = errors.New("airtable API error")

type client struct {
	cfg   Config
	http  nodesdk.HTTPClient
	cred  nodesdk.Credential
	token string
}

// call sends one request. path is relative to the base URL and already
// escaped. A non-2xx answer becomes a *nodesdk.NodeError carrying the status
// and Airtable's error type and message.
func (c *client) call(ctx context.Context, method, path string, query url.Values, body any) (map[string]any, error) {
	var payload []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encoding request: %w", err)
		}
		payload = b
	}
	resp, err := c.cfg.Caller.Do(ctx, c.http, func(ctx context.Context) (*http.Request, error) {
		u := strings.TrimRight(c.cfg.BaseURL, "/") + path
		if len(query) > 0 {
			u += "?" + query.Encode()
		}
		req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("creating request: %w", err)
		}
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		return req, c.cred.Authenticate(req)
	}, c.token)
	if err != nil {
		return nil, fmt.Errorf("airtable %s: %w", method, err)
	}
	var out map[string]any
	parseErr := json.Unmarshal(resp.Body, &out)
	if resp.Status/100 != 2 {
		return nil, &nodesdk.NodeError{Message: "airtable: " + errorText(out, resp.Status, c.token), HTTPStatus: resp.Status, Err: ErrAirtable}
	}
	if parseErr != nil {
		return nil, &nodesdk.NodeError{Message: fmt.Sprintf("airtable: unexpected response (HTTP %d)", resp.Status), HTTPStatus: resp.Status}
	}
	return out, nil
}

// errorText reads both error shapes: {"error":"NOT_FOUND"} and
// {"error":{"type":"...","message":"..."}}.
func errorText(body map[string]any, status int, secret string) string {
	switch e := body["error"].(type) {
	case string:
		return chatkit.Redact(e, secret)
	case map[string]any:
		typ, _ := e["type"].(string)
		msg, _ := e["message"].(string)
		return chatkit.Redact(strings.TrimSpace(typ+": "+msg), secret)
	}
	return fmt.Sprintf("HTTP %d", status)
}
