package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/engi-hq/engi/internal/node/chatkit"
	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// ErrSlack is wrapped by every error Slack reports with ok:false.
var ErrSlack = errors.New("slack API error")

type client struct {
	cfg   Config
	http  nodesdk.HTTPClient
	cred  nodesdk.Credential
	token string
}

// api calls a Web API method and returns the decoded envelope. GET carries
// params in the query, POST sends them as a JSON body.
func (c *client) api(ctx context.Context, method, httpMethod string, params map[string]any) (map[string]any, error) {
	resp, err := c.cfg.Caller.Do(ctx, c.http, func(ctx context.Context) (*http.Request, error) {
		u := strings.TrimRight(c.cfg.BaseURL, "/") + "/" + method
		var body *bytes.Reader
		if httpMethod == http.MethodGet {
			q := url.Values{}
			for k, v := range params {
				q.Set(k, fmt.Sprint(v))
			}
			u += "?" + q.Encode()
			body = bytes.NewReader(nil)
		} else {
			b, err := json.Marshal(params)
			if err != nil {
				return nil, fmt.Errorf("encoding params: %w", err)
			}
			body = bytes.NewReader(b)
		}
		req, err := http.NewRequestWithContext(ctx, httpMethod, u, body)
		if err != nil {
			return nil, fmt.Errorf("creating request: %w", err)
		}
		if httpMethod != http.MethodGet {
			req.Header.Set("Content-Type", "application/json; charset=utf-8")
		}
		return req, c.cred.Authenticate(req)
	}, c.token)
	if err != nil {
		return nil, fmt.Errorf("slack %s: %w", method, err)
	}
	var env map[string]any
	if err := json.Unmarshal(resp.Body, &env); err != nil {
		return nil, &nodesdk.NodeError{Message: fmt.Sprintf("slack %s: unexpected response (HTTP %d)", method, resp.Status), HTTPStatus: resp.Status}
	}
	if ok, _ := env["ok"].(bool); !ok {
		code, _ := env["error"].(string)
		return nil, &nodesdk.NodeError{Message: fmt.Sprintf("slack %s: %s", method, chatkit.Redact(code, c.token)), HTTPStatus: resp.Status, Err: ErrSlack}
	}
	delete(env, "ok")
	return env, nil
}

func (c *client) run(ctx context.Context, in nodesdk.Input, op string, i int) (map[string]any, error) {
	str := func(name string) (string, error) { return nodesdk.ParamString(ctx, in, name, i) }
	required := func(name string) (string, error) {
		v, err := str(name)
		if err == nil && strings.TrimSpace(v) == "" {
			err = fmt.Errorf("%s must not be empty", name)
		}
		return v, err
	}
	switch op {
	case opPost, opUpdate:
		p, err := c.messageParams(ctx, in, op, i)
		if err != nil {
			return nil, err
		}
		if op == opPost {
			return c.api(ctx, "chat.postMessage", http.MethodPost, p)
		}
		return c.api(ctx, "chat.update", http.MethodPost, p)
	case opDelete:
		ch, err := required("channel")
		if err != nil {
			return nil, err
		}
		ts, err := required("ts")
		if err != nil {
			return nil, err
		}
		return c.api(ctx, "chat.delete", http.MethodPost, map[string]any{"channel": ch, "ts": ts})
	case opUpload:
		return c.upload(ctx, in, i)
	case opChannels:
		return c.channels(ctx, in, i)
	case opUser:
		email, err := required("email")
		if err != nil {
			return nil, err
		}
		return c.api(ctx, "users.lookupByEmail", http.MethodGet, map[string]any{"email": email})
	}
	return nil, fmt.Errorf("unknown operation %q", op)
}

func (c *client) messageParams(ctx context.Context, in nodesdk.Input, op string, i int) (map[string]any, error) {
	p := map[string]any{}
	ch, err := nodesdk.ParamString(ctx, in, "channel", i)
	if err != nil || strings.TrimSpace(ch) == "" {
		return nil, errors.Join(err, errors.New("channel must not be empty"))
	}
	p["channel"] = ch
	text, err := nodesdk.ParamString(ctx, in, "text", i)
	if err != nil {
		return nil, err
	}
	blocks, err := blocksParam(ctx, in, i)
	if err != nil {
		return nil, err
	}
	if text == "" && blocks == nil {
		return nil, errors.New("text or blocks is required")
	}
	if text != "" {
		p["text"] = text
	}
	if blocks != nil {
		p["blocks"] = blocks
	}
	if op == opUpdate {
		ts, err := nodesdk.ParamString(ctx, in, "ts", i)
		if err != nil || ts == "" {
			return nil, errors.Join(err, errors.New("ts must not be empty"))
		}
		p["ts"] = ts
		return p, nil
	}
	if th, err := nodesdk.ParamString(ctx, in, "threadTs", i); err != nil {
		return nil, err
	} else if th != "" {
		p["thread_ts"] = th
	}
	return p, nil
}

// blocksParam accepts the blocks as an array or as JSON text.
func blocksParam(ctx context.Context, in nodesdk.Input, i int) ([]any, error) {
	v, err := in.Param(ctx, "blocks", i)
	if err != nil {
		return nil, fmt.Errorf("reading parameter %q: %w", "blocks", err)
	}
	switch t := v.(type) {
	case nil:
		return nil, nil
	case []any:
		return t, nil
	case string:
		if strings.TrimSpace(t) == "" {
			return nil, nil
		}
		var out []any
		if err := json.Unmarshal([]byte(t), &out); err != nil {
			return nil, fmt.Errorf("blocks is not a JSON array: %w", err)
		}
		return out, nil
	}
	return nil, fmt.Errorf("blocks must be an array, got %s", nodekit.TypeName(v))
}

// upload runs Slack's three-step external upload: reserve, send the bytes, complete.
func (c *client) upload(ctx context.Context, in nodesdk.Input, i int) (map[string]any, error) {
	get := func(name string) (string, error) { return nodesdk.ParamString(ctx, in, name, i) }
	ch, err := get("channel")
	if err != nil || ch == "" {
		return nil, errors.Join(err, errors.New("channel must not be empty"))
	}
	content, err := get("content")
	if err != nil {
		return nil, err
	}
	name, err := get("filename")
	if err != nil || name == "" {
		return nil, errors.Join(err, errors.New("filename must not be empty"))
	}
	title, _ := get("title")
	comment, _ := get("comment")
	thread, _ := get("threadTs")

	res, err := c.api(ctx, "files.getUploadURLExternal", http.MethodGet, map[string]any{"filename": name, "length": strconv.Itoa(len(content))})
	if err != nil {
		return nil, err
	}
	uploadURL, _ := res["upload_url"].(string)
	fileID, _ := res["file_id"].(string)
	if uploadURL == "" || fileID == "" {
		return nil, errors.New("slack files.getUploadURLExternal: response lacks upload_url or file_id")
	}
	resp, err := c.cfg.Caller.Do(ctx, c.http, func(ctx context.Context) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, strings.NewReader(content))
		if err != nil {
			return nil, fmt.Errorf("creating request: %w", err)
		}
		req.Header.Set("Content-Type", "application/octet-stream")
		return req, nil
	}, c.token, uploadURL)
	if err != nil {
		return nil, fmt.Errorf("slack file upload: %w", err)
	}
	if resp.Status/100 != 2 {
		return nil, &nodesdk.NodeError{Message: "slack file upload failed", HTTPStatus: resp.Status}
	}
	file := map[string]any{"id": fileID}
	if title != "" {
		file["title"] = title
	}
	p := map[string]any{"files": []any{file}, "channel_id": ch}
	if comment != "" {
		p["initial_comment"] = comment
	}
	if thread != "" {
		p["thread_ts"] = thread
	}
	return c.api(ctx, "files.completeUploadExternal", http.MethodPost, p)
}

// channels follows next_cursor until the list ends or MaxListResults is reached.
func (c *client) channels(ctx context.Context, in nodesdk.Input, i int) (map[string]any, error) {
	types, err := nodesdk.ParamString(ctx, in, "types", i)
	if err != nil {
		return nil, err
	}
	excl, err := nodesdk.ParamBool(ctx, in, "excludeArchived", i)
	if err != nil {
		return nil, err
	}
	var all []any
	truncated := false
	cursor := ""
	for {
		p := map[string]any{"limit": strconv.Itoa(c.cfg.PageSize), "exclude_archived": strconv.FormatBool(excl)}
		if types != "" {
			p["types"] = types
		}
		if cursor != "" {
			p["cursor"] = cursor
		}
		res, err := c.api(ctx, "conversations.list", http.MethodGet, p)
		if err != nil {
			return nil, err
		}
		page, _ := res["channels"].([]any)
		all = append(all, page...)
		if len(all) >= c.cfg.MaxListResults {
			truncated = len(all) > c.cfg.MaxListResults || nextCursor(res) != ""
			all = all[:c.cfg.MaxListResults]
			break
		}
		if cursor = nextCursor(res); cursor == "" {
			break
		}
	}
	return map[string]any{"channels": all, "count": float64(len(all)), "truncated": truncated}, nil
}

func nextCursor(res map[string]any) string {
	m, _ := res["response_metadata"].(map[string]any)
	s, _ := m["next_cursor"].(string)
	return s
}
