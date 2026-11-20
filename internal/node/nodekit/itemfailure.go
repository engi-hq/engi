package nodekit

import (
	"errors"

	"github.com/engi-hq/engi/pkg/nodesdk"
)

// ItemFailure points err at an input item. Unlike ItemError, an error that is
// already a *nodesdk.NodeError keeps its message and HTTP status and gains the
// item index, so one NodeError carries both.
func ItemFailure(itemIndex int, err error) error {
	var ne *nodesdk.NodeError
	if errors.As(err, &ne) && ne.ItemIndex == nil {
		c := *ne
		c.ItemIndex = &itemIndex
		return &c
	}
	return ItemError(itemIndex, err)
}
