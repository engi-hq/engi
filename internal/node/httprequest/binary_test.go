package httprequest

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

func TestBinaryResponseIsStoredAsBinary(t *testing.T) {
	payload := []byte("\x89PNG\r\n\x1a\nnot-utf8\xff\xfe")
	srv, _ := server(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Disposition", `attachment; filename="logo.png"`)
		_, _ = w.Write(payload)
	})
	in := input(srv, 1, map[string]any{"responseFormat": "binary", "outputBinaryField": "logo"})
	blobs := nodetest.NewMemoryBlobs()
	in.Blobs = blobs
	out, err := New().Execute(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	ref, ok := out.Ports[0][0].Binary["logo"]
	if !ok || ref.MIME != "image/png" || ref.Name != "logo.png" || ref.Size != int64(len(payload)) {
		t.Fatalf("binary = %+v", out.Ports[0][0].Binary)
	}
	if !bytes.Equal(blobs.Bytes(ref), payload) {
		t.Error("stored bytes differ")
	}
}

func TestBinaryResponseFailureIsAnError(t *testing.T) {
	srv, _ := server(t, func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "nope", http.StatusTeapot) })
	in := input(srv, 1, map[string]any{"responseFormat": "binary"})
	in.Blobs = nodetest.NewMemoryBlobs()
	if _, err := New().Execute(context.Background(), in); err == nil {
		t.Fatal("a 418 stored as binary without error")
	}
}

func TestBinaryBodyIsSentWithItsMIME(t *testing.T) {
	srv, seen := server(t, jsonReply(`{}`))
	blobs := nodetest.NewMemoryBlobs()
	ref, err := blobs.Put(context.Background(), "a.pdf", "application/pdf", bytes.NewReader([]byte("%PDF-1")))
	if err != nil {
		t.Fatal(err)
	}
	in := input(srv, 1, map[string]any{"method": "POST", "bodyType": "binary", "inputBinaryField": "doc"})
	in.Blobs = blobs
	in.Ports[0][0].Binary = map[string]nodesdk.BinaryRef{"doc": ref}
	if _, err := New().Execute(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	got := (*seen)[0]
	if got.body != "%PDF-1" || got.contentType != "application/pdf" {
		t.Errorf("server saw %q as %q", got.body, got.contentType)
	}
}

func TestBinaryBodyNeedsTheField(t *testing.T) {
	srv, _ := server(t, jsonReply(`{}`))
	in := input(srv, 1, map[string]any{"method": "POST", "bodyType": "binary"})
	in.Blobs = nodetest.NewMemoryBlobs()
	if _, err := New().Execute(context.Background(), in); err == nil {
		t.Fatal("missing binary field accepted")
	}
}
