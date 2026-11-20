package nodesdk

import (
	"context"
	"testing"
)

func TestHTTPCallOptions(t *testing.T) {
	if got := HTTPCallOptionsFrom(context.Background()); got != (HTTPCallOptions{}) {
		t.Fatalf("zero context should give zero options, got %+v", got)
	}
	ctx := WithHTTPCallOptions(context.Background(), HTTPCallOptions{NoRedirect: true})
	if !HTTPCallOptionsFrom(ctx).NoRedirect {
		t.Fatal("options lost")
	}
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	if !HTTPCallOptionsFrom(child).NoRedirect {
		t.Fatal("options must survive derived contexts")
	}
}
