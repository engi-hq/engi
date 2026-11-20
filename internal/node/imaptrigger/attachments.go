package imaptrigger

import (
	"bytes"
	"context"
	"fmt"

	"github.com/engi-hq/engi/pkg/nodesdk"
)

// binaryFieldPrefix names the binary fields of attachments: attachment_0, attachment_1...
const binaryFieldPrefix = "attachment_"

// storeAttachments saves the attachments of a mail that passed the filter as
// binary fields of the polled item, and names each field in the item's
// attachments list (binaryField, or binaryError when one could not be stored). Without a blob store the attachments stay listed only.
func storeAttachments(ctx context.Context, blobs nodesdk.BlobAllocator, p parsed, out *nodesdk.PolledItem) error {
	if blobs == nil || len(p.files) == 0 {
		return nil
	}
	execID, access, err := blobs.Allocate()
	if err != nil {
		return fmt.Errorf("reserving binary storage: %w", err)
	}
	out.ExecutionID = execID
	out.Item.Binary = make(map[string]nodesdk.BinaryRef, len(p.files))
	list, _ := p.item["attachments"].([]any)
	for i, f := range p.files {
		field := fmt.Sprintf("%s%d", binaryFieldPrefix, i)
		ref, err := access.Put(ctx, f.name, f.mime, bytes.NewReader(f.data))
		m, _ := list[i].(map[string]any)
		if err != nil {
			// One unstorable attachment (over the size limit, say) must not
			// block the mailbox: the mail is delivered with the reason.
			if m != nil {
				m["binaryError"] = err.Error()
			}
			continue
		}
		out.Item.Binary[field] = ref
		if m != nil {
			m["binaryField"] = field
		}
	}
	return nil
}
