package chatkit

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type doer func(*http.Request) (*http.Response, error)

func (f doer) Do(r *http.Request) (*http.Response, error) { return f(r) }

func TestRetryAfterParsing(t *testing.T) {
	for in, want := range map[string]time.Duration{"3": 3 * time.Second, "1.5": 1500 * time.Millisecond, "": time.Second, "soon": time.Second, "-2": time.Second} {
		if got := retryAfter(in); got != want {
			t.Errorf("retryAfter(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestSleepStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (Caller{}).sleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestScrub(t *testing.T) {
	sentinel := errors.New("x")
	if got := scrub(sentinel, []string{"", "y"}); !errors.Is(got, sentinel) {
		t.Error("untouched errors must be returned as is")
	}
	if got := scrub(errors.New("a SECRET b"), []string{"SECRET"}).Error(); strings.Contains(got, "SECRET") {
		t.Errorf("not scrubbed: %s", got)
	}
}

func TestDoBuildError(t *testing.T) {
	c := Caller{}
	_, err := c.Do(context.Background(), doer(func(*http.Request) (*http.Response, error) { return nil, nil }),
		func(context.Context) (*http.Request, error) { return nil, errors.New("token SECRET missing") }, "SECRET")
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("got %v", err)
	}
}

func TestDoReturnsNonRateLimitedStatus(t *testing.T) {
	c := Caller{}
	resp, err := c.Do(context.Background(), doer(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader("boom"))}, nil
	}), func(ctx context.Context) (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, "http://x.test/", http.NoBody)
	})
	if err != nil || resp.Status != 500 || string(resp.Body) != "boom" {
		t.Fatalf("%+v %v", resp, err)
	}
}
