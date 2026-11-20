package trigger

import (
	"context"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// blobAllocator implements nodesdk.BlobAllocator.
type blobAllocator struct {
	ids   ExecutionIDs
	blobs Blobs
}

func (a blobAllocator) Allocate() (string, nodesdk.BlobAccess, error) {
	id, err := a.ids.ExecutionID()
	if err != nil {
		return "", nil, err //nolint:wrapcheck // id generation error is self-describing
	}
	return string(id), a.blobs.Scoped(id), nil
}

// dropBlobs deletes the binary data of an execution that was not started. The
// context may be the one that failed, so cleanup runs detached from it.
func (s *Service) dropBlobs(ctx context.Context, id domain.ExecutionID) {
	if s.d.Blobs == nil {
		return
	}
	if err := s.d.Blobs.DeleteExecution(context.WithoutCancel(ctx), id); err != nil {
		s.d.Logger.Warn("deleting unused binary data failed", "execution", id, "error", err)
	}
}

// binaryRefs converts an item's binary fields to the domain type.
func binaryRefs(in map[string]nodesdk.BinaryRef) map[string]domain.BinaryRef {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]domain.BinaryRef, len(in))
	for k, v := range in {
		out[k] = domain.BinaryRef(v)
	}
	return out
}
