package imaptrigger

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

type allocator struct {
	blobs *nodetest.MemoryBlobs
	err   error
}

func (a allocator) Allocate() (string, nodesdk.BlobAccess, error) {
	return "ex_new", a.blobs, a.err
}

func TestAttachmentsBecomeBinaryFields(t *testing.T) {
	p, err := parseMessage([]byte(sample), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	blobs := nodetest.NewMemoryBlobs()
	var polled nodesdk.PolledItem
	polled.Item.JSON = p.item
	if err := storeAttachments(context.Background(), allocator{blobs: blobs}, p, &polled); err != nil {
		t.Fatal(err)
	}
	ref, ok := polled.Item.Binary["attachment_0"]
	if !ok || polled.ExecutionID != "ex_new" || ref.Name != "a.pdf" || ref.MIME != "application/pdf" || ref.Size != 3 {
		t.Fatalf("item = %+v", polled)
	}
	rc, err := blobs.Open(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := io.ReadAll(rc); string(b) != "ABC" {
		t.Errorf("stored %q", b)
	}
	att := p.item["attachments"].([]any)[0].(map[string]any)
	if att["binaryField"] != "attachment_0" {
		t.Errorf("attachment entry = %v", att)
	}
}

func TestWithoutBlobStorageAttachmentsStayListed(t *testing.T) {
	p, _ := parseMessage([]byte(sample), 1<<20)
	var polled nodesdk.PolledItem
	if err := storeAttachments(context.Background(), nil, p, &polled); err != nil || polled.Item.Binary != nil || polled.ExecutionID != "" {
		t.Errorf("got %+v, %v", polled, err)
	}
}

func TestAllocationFailureFailsThePoll(t *testing.T) {
	p, _ := parseMessage([]byte(sample), 1<<20)
	err := storeAttachments(context.Background(), allocator{err: errors.New("boom")}, p, &nodesdk.PolledItem{})
	if err == nil {
		t.Fatal("allocation failure ignored")
	}
}
