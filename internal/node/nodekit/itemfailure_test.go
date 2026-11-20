package nodekit

import (
	"errors"
	"fmt"
	"testing"

	"github.com/engi-hq/engi/pkg/nodesdk"
)

func TestItemFailureKeepsStatusOnOneError(t *testing.T) {
	cause := &nodesdk.NodeError{Message: "api", HTTPStatus: 404}
	var ne *nodesdk.NodeError
	if err := ItemFailure(2, fmt.Errorf("wrapped: %w", cause)); !errors.As(err, &ne) || ne.HTTPStatus != 404 || ne.ItemIndex == nil || *ne.ItemIndex != 2 {
		t.Fatalf("got %#v", err)
	}
	if cause.ItemIndex != nil {
		t.Error("the original error must not be modified")
	}
	if err := ItemFailure(1, errors.New("plain")); !errors.As(err, &ne) || ne.ItemIndex == nil || *ne.ItemIndex != 1 {
		t.Fatalf("got %#v", err)
	}
}
