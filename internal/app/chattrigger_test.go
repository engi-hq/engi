package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const chatDoc = `{
  "name": "chat",
  "nodes": [
    {"name": "Chat", "type": "engi.trigger.chat", "position": [0, 0], "parameters": {"path": "support", "title": "Help <desk>"}},
    {"name": "Reply", "type": "engi.transform.set", "position": [200, 0], "parameters": {"fields": [
      {"name": "output", "type": "string", "value": "={{ 'echo: ' + $json.chatInput + ' (' + $json.sessionId + ')' }}"}]}}
  ],
  "connections": [{"from": {"node": "Chat"}, "to": {"node": "Reply"}}]
}`

// TestRuntimeServesAChatTriggerEndToEnd drives the real trigger service, store
// and executor: activation registers the chat path, a message runs the workflow
// and its last node answers, and the widget is served for the same path.
func TestRuntimeServesAChatTriggerEndToEnd(t *testing.T) {
	f := newRuntime(t)
	f.start(t)
	if err := f.rt.StartTriggers(context.Background()); err != nil {
		t.Fatal(err)
	}
	srv := f.hookServer(t)
	wf := f.save(t, chatDoc)

	if code, _ := post(t, srv.URL+"/chat/support", `{"message":"hi"}`); code != http.StatusNotFound {
		t.Fatalf("inactive workflow answered %d", code)
	}
	if err := f.rt.Triggers.Activate(context.Background(), wf.ID); err != nil {
		t.Fatal(err)
	}
	code, body := post(t, srv.URL+"/chat/support", `{"message":"hi","sessionId":"abc"}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d: %s", code, body)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil || out["output"] != "echo: hi (abc)" || out["sessionId"] != "abc" {
		t.Fatalf("body = %s (%v)", body, err)
	}
	code, body = post(t, srv.URL+"/chat/support", `{"message":"again"}`)
	if err := json.Unmarshal([]byte(body), &out); err != nil || code != http.StatusOK || out["sessionId"] == "" || !strings.HasPrefix(out["output"].(string), "echo: again (") {
		t.Fatalf("generated session: %d %s", code, body)
	}

	resp, err := http.Get(srv.URL + "/chat/support/widget")
	if err != nil {
		t.Fatal(err)
	}
	var page strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		page.Write(buf[:n])
		if err != nil {
			break
		}
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(page.String(), "Help &lt;desk&gt;") || resp.Header.Get("Content-Security-Policy") == "" {
		t.Fatalf("widget: %d %v %s", resp.StatusCode, resp.Header, page.String())
	}

	if err := f.rt.Triggers.Deactivate(context.Background(), wf.ID); err != nil {
		t.Fatal(err)
	}
	if code, _ := post(t, srv.URL+"/chat/support", `{"message":"hi"}`); code != http.StatusNotFound {
		t.Fatalf("deactivated workflow answered %d", code)
	}
}
