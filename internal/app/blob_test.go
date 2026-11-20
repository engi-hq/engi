package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/service/execution"
)

// uploadFlowDoc receives a file on a webhook and forwards it with the HTTP
// Request node, which sends the stored binary field as the request body.
const uploadFlowDoc = `{
  "name": "upload",
  "nodes": [
    {"name": "In", "type": "engi.trigger.webhook", "position": [0, 0], "parameters": {"httpMethod": "POST", "path": "upload", "responseMode": "lastNode"}},
    {"name": "Forward", "type": "engi.http.request", "position": [200, 0], "parameters": {
      "method": "POST", "url": %q, "bodyType": "binary", "inputBinaryField": "doc"}}
  ],
  "connections": [{"from": {"node": "In"}, "to": {"node": "Forward"}}]
}`

// downloadDoc fetches a URL as binary data.
const downloadDoc = `{
  "name": "download",
  "nodes": [
    {"name": "Start", "type": "engi.trigger.manual", "position": [0, 0]},
    {"name": "Fetch", "type": "engi.http.request", "position": [200, 0], "parameters": {
      "method": "GET", "url": %q, "responseFormat": "binary", "outputBinaryField": "file"}}
  ],
  "connections": [{"from": {"node": "Start"}, "to": {"node": "Fetch"}}]
}`

func blobConfig(t *testing.T) func(*config.Config) {
	return func(c *config.Config) {
		c.Blob.FS.Dir = filepath.Join(t.TempDir(), "blobs")
		c.Blob.OrphanGrace = time.Millisecond
	}
}

func multipartBody(t *testing.T, file []byte) (body *bytes.Buffer, contentType string) {
	t.Helper()
	body = &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	if err := mw.WriteField("note", "hello"); err != nil {
		t.Fatal(err)
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="doc"; filename="report.pdf"`)
	h.Set("Content-Type", "application/pdf")
	part, err := mw.CreatePart(h)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(file)
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return body, mw.FormDataContentType()
}

func (f *serverFixture) saveDoc(doc string) domain.Workflow {
	f.t.Helper()
	wf := workflow(f.t, doc)
	id, err := f.app.ids.WorkflowID()
	if err != nil {
		f.t.Fatal(err)
	}
	wf.ID = id
	saved, err := f.srv.Runtime.Workflows.Save(context.Background(), wf)
	if err != nil {
		f.t.Fatal(err)
	}
	return saved
}

func (f *serverFixture) waitTerminal(ctx context.Context, id domain.ExecutionID) domain.Execution {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		ex, err := f.srv.Runtime.Executions.Get(ctx, id)
		if err != nil {
			f.t.Fatal(err)
		}
		if ex.Status.Terminal() {
			return ex
		}
		time.Sleep(10 * time.Millisecond)
	}
	f.t.Fatalf("execution %s did not finish", id)
	return domain.Execution{}
}

func TestWebhookUploadBecomesBinaryAndHTTPNodeSendsIt(t *testing.T) {
	payload := append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte{0, 1, 2, 0xff}, 4096)...)
	var gotBody []byte
	var gotType string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotType = r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"received":true}`)
	}))
	t.Cleanup(target.Close)

	f := newServerFixture(t, blobConfig(t))
	s := f.ownerSession()
	f.startWorker()
	rt := f.srv.Runtime
	if err := rt.StartTriggers(context.Background()); err != nil {
		t.Fatal(err)
	}
	wf := f.saveDoc(fmt.Sprintf(uploadFlowDoc, target.URL))
	if err := rt.Triggers.Activate(context.Background(), wf.ID); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	for _, p := range rt.Webhooks.Patterns() {
		mux.Handle(p, rt.Webhooks)
	}
	hooks := httptest.NewServer(mux)
	t.Cleanup(hooks.Close)

	body, ct := multipartBody(t, payload)
	res, err := http.Post(hooks.URL+"/hook/upload", ct, body)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(out), `"received":true`) {
		t.Fatalf("webhook = %d %s", res.StatusCode, out)
	}
	if !bytes.Equal(gotBody, payload) || gotType != "application/pdf" {
		t.Fatalf("target got %d bytes as %q; want %d as application/pdf", len(gotBody), gotType, len(payload))
	}

	// The trigger item holds a reference and the text field, not the bytes.
	list, err := rt.Executions.List(context.Background(), portExecutionFilter(wf.ID), portPage())
	if err != nil || len(list.Items) != 1 {
		t.Fatalf("executions = %v, %v", list.Items, err)
	}
	exID := list.Items[0].ID
	ex := f.waitTerminal(context.Background(), exID)
	trigger := ex.RunData["In"][0].Output[domain.PortMain][0][0]
	ref, ok := trigger.Binary["doc"]
	if !ok || ref.Name != "report.pdf" || ref.MIME != "application/pdf" || ref.Size != int64(len(payload)) || !strings.HasPrefix(ref.Ref, "blob://"+string(exID)+"/") {
		t.Fatalf("binary = %+v", trigger.Binary)
	}
	if body, _ := trigger.JSON["body"].(map[string]any); body["note"] != "hello" {
		t.Errorf("text field lost: %v", trigger.JSON["body"])
	}

	// The editor downloads it through the API.
	blobID := strings.TrimPrefix(ref.Ref, "blob://"+string(exID)+"/")
	path := "/api/v1/executions/" + string(exID) + "/binary/" + blobID
	if r := f.do(req{method: "GET", path: path}); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("anonymous download = %d; want 401", r.StatusCode)
	}
	dl := f.as(s, req{method: "GET", path: path})
	if dl.StatusCode != http.StatusOK || !bytes.Equal(dl.body, payload) {
		t.Fatalf("download = %d (%d bytes)", dl.StatusCode, len(dl.body))
	}
	if dl.Header.Get("Content-Type") != "application/pdf" || !strings.Contains(dl.Header.Get("Content-Disposition"), `attachment; filename=report.pdf`) {
		t.Errorf("headers: %v", dl.Header)
	}
	if dl.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff")
	}
	for _, bad := range []string{"/binary/missing", "/binary/..%2F..%2Fetc", "/binary/" + blobID + "x"} {
		if r := f.as(s, req{method: "GET", path: "/api/v1/executions/" + string(exID) + bad}); r.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s = %d; want 404", bad, r.StatusCode)
		}
	}
	if r := f.as(s, req{method: "GET", path: "/api/v1/executions/ex_other/binary/" + blobID}); r.StatusCode != http.StatusNotFound {
		t.Errorf("another execution's id served the blob: %d", r.StatusCode)
	}

	// Deleting the execution deletes its binary data.
	if r := f.as(s, req{method: "DELETE", path: "/api/v1/executions/" + string(exID)}); r.StatusCode != http.StatusNoContent {
		t.Fatalf("delete = %d %s", r.StatusCode, r.body)
	}
	if _, _, err := f.app.Blobs.Open(context.Background(), ref.Ref); err == nil {
		t.Error("binary data survived the deletion of its execution")
	}
}

func TestWebhookUploadOverTheLimitIsRefusedAndLeavesNothing(t *testing.T) {
	f := newServerFixture(t, blobConfig(t), func(c *config.Config) { c.Blob.MaxItemBytes = 1024 })
	f.ownerSession()
	f.startWorker()
	rt := f.srv.Runtime
	if err := rt.StartTriggers(context.Background()); err != nil {
		t.Fatal(err)
	}
	wf := f.saveDoc(fmt.Sprintf(uploadFlowDoc, "http://127.0.0.1:1"))
	if err := rt.Triggers.Activate(context.Background(), wf.ID); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	for _, p := range rt.Webhooks.Patterns() {
		mux.Handle(p, rt.Webhooks)
	}
	hooks := httptest.NewServer(mux)
	t.Cleanup(hooks.Close)

	body, ct := multipartBody(t, bytes.Repeat([]byte("x"), 4096))
	res, err := http.Post(hooks.URL+"/hook/upload", ct, body)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d; want 413", res.StatusCode)
	}
	entries, _ := os.ReadDir(f.app.Config.Blob.FS.Dir)
	for _, e := range entries {
		files, _ := os.ReadDir(filepath.Join(f.app.Config.Blob.FS.Dir, e.Name()))
		if len(files) != 0 {
			t.Errorf("rejected upload left files in %s: %v", e.Name(), files)
		}
	}
}

func TestHTTPNodeStoresResponseAsBinary(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Disposition", `attachment; filename="logo.png"`)
		_, _ = w.Write([]byte("\x89PNG-bytes"))
	}))
	t.Cleanup(target.Close)
	f := newServerFixture(t, blobConfig(t))
	s := f.ownerSession()
	f.startWorker()
	wf := f.saveDoc(fmt.Sprintf(downloadDoc, target.URL))
	id, err := f.srv.Runtime.Executions.Start(context.Background(), execution.StartRequest{
		WorkflowID: wf.ID, Mode: domain.ModeManual, StartNode: "Start",
	})
	if err != nil {
		t.Fatal(err)
	}
	ex := f.waitTerminal(context.Background(), id)
	if ex.Status != domain.StatusSuccess {
		t.Fatalf("status %s: %+v", ex.Status, ex.Error)
	}
	ref := ex.RunData["Fetch"][0].Output[domain.PortMain][0][0].Binary["file"]
	if ref.MIME != "image/png" || ref.Name != "logo.png" || ref.Size != 10 {
		t.Fatalf("ref = %+v", ref)
	}
	blobID := strings.TrimPrefix(ref.Ref, "blob://"+string(id)+"/")
	dl := f.as(s, req{method: "GET", path: "/api/v1/executions/" + string(id) + "/binary/" + blobID + "?inline=true"})
	if dl.StatusCode != http.StatusOK || string(dl.body) != "\x89PNG-bytes" || !strings.HasPrefix(dl.Header.Get("Content-Disposition"), "inline") {
		t.Fatalf("download = %d %q %v", dl.StatusCode, dl.body, dl.Header)
	}
}

func TestPruningExecutionsDeletesTheirBinaryData(t *testing.T) {
	f := newServerFixture(t, blobConfig(t))
	f.ownerSession()
	f.startWorker()
	rt := f.srv.Runtime
	wf := f.saveDoc(fmt.Sprintf(simpleWorkflow, "prune"))
	ctx := context.Background()
	start := func() domain.ExecutionID {
		id, err := rt.Executions.Start(ctx, execution.StartRequest{WorkflowID: wf.ID, Mode: domain.ModeManual, StartNode: "Start"})
		if err != nil {
			t.Fatal(err)
		}
		f.waitTerminal(ctx, id)
		return id
	}
	old := start()
	time.Sleep(50 * time.Millisecond)
	cutoff := time.Now()
	time.Sleep(50 * time.Millisecond)
	recent := start()
	put := func(id domain.ExecutionID) string {
		ref, err := f.app.Blobs.Put(ctx, id, "a.txt", "text/plain", strings.NewReader("data"))
		if err != nil {
			t.Fatal(err)
		}
		return ref.Ref
	}
	oldRef, recentRef := put(old), put(recent)
	// An upload whose execution never existed is swept as well (grace is 1ms).
	orphanRef := put("ex_neverstarted")
	time.Sleep(10 * time.Millisecond)

	n, err := rt.Executions.Prune(ctx, map[domain.ExecutionStatus]time.Time{domain.StatusSuccess: cutoff})
	if err != nil || n != 1 {
		t.Fatalf("Prune = %d, %v; want 1", n, err)
	}
	for _, ref := range []string{oldRef, orphanRef} {
		if _, _, err := f.app.Blobs.Open(ctx, ref); err == nil {
			t.Errorf("%s survived pruning", ref)
		}
	}
	rc, _, err := f.app.Blobs.Open(ctx, recentRef)
	if err != nil {
		t.Fatalf("the blob of a kept execution is gone: %v", err)
	}
	rc.Close()
}
