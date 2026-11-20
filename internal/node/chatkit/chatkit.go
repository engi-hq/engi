// Package chatkit holds what the Slack, Telegram and Teams nodes share: one
// HTTP call helper that honours rate limits (429 Retry-After) and keeps
// credentials out of errors.
package chatkit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Limits bound rate-limit handling (nodes.chat.maxRetryWait, nodes.chat.maxRetries).
type Limits struct {
	MaxRetryWait time.Duration
	MaxRetries   int
}

// defaultRetryAfter applies when a 429 carries no usable Retry-After header.
const defaultRetryAfter = time.Second

// ErrRateLimited is returned when the service keeps answering 429 or asks for a
// wait longer than Limits.MaxRetryWait.
var ErrRateLimited = errors.New("rate limited")

// Caller makes HTTP calls for a chat node.
type Caller struct {
	Limits Limits
	// Sleep waits for d or until ctx ends. Nil uses a timer; tests inject a fake.
	Sleep func(ctx context.Context, d time.Duration) error
}

// Response is a completed call.
type Response struct {
	Status int
	Body   []byte
}

// Do sends the request that build returns (called again for every retry, so a
// body can be re-read) and returns the final response. A 429 is retried after
// its Retry-After wait, capped by Limits. secrets are scrubbed from every error
// this function returns; transport errors never include the request URL.
func (c Caller) Do(ctx context.Context, client nodesdk.HTTPClient, build func(context.Context) (*http.Request, error), secrets ...string) (Response, error) {
	for attempt := 0; ; attempt++ {
		req, err := build(ctx)
		if err != nil {
			return Response{}, scrub(fmt.Errorf("building request: %w", err), secrets)
		}
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return Response{}, fmt.Errorf("sending request: %w", ctx.Err())
			}
			var ue *url.Error
			if errors.As(err, &ue) {
				err = ue.Err
			}
			return Response{}, scrub(fmt.Errorf("sending request to %s: %w", req.URL.Host, err), secrets)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return Response{}, scrub(fmt.Errorf("reading response: %w", err), secrets)
		}
		if resp.StatusCode != http.StatusTooManyRequests {
			return Response{Status: resp.StatusCode, Body: body}, nil
		}
		wait := retryAfter(resp.Header.Get("Retry-After"))
		if attempt >= c.Limits.MaxRetries {
			return Response{}, fmt.Errorf("%w: still limited after %d retries", ErrRateLimited, attempt)
		}
		if wait > c.Limits.MaxRetryWait {
			return Response{}, fmt.Errorf("%w: service asked to wait %s, longer than the allowed %s", ErrRateLimited, wait, c.Limits.MaxRetryWait)
		}
		if err := c.sleep(ctx, wait); err != nil {
			return Response{}, err
		}
	}
}

func (c Caller) sleep(ctx context.Context, d time.Duration) error {
	if c.Sleep != nil {
		return c.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("waiting out rate limit: %w", ctx.Err())
	case <-t.C:
		return nil
	}
}

func retryAfter(h string) time.Duration {
	secs, err := strconv.ParseFloat(strings.TrimSpace(h), 64)
	if err != nil || secs < 0 {
		return defaultRetryAfter
	}
	return time.Duration(secs * float64(time.Second))
}

// scrub hides every secret in err's message. The cause chain is kept so
// errors.Is still works on the result.
func scrub(err error, secrets []string) error {
	msg := err.Error()
	out := msg
	for _, s := range secrets {
		if s != "" {
			out = strings.ReplaceAll(out, s, "[redacted]")
		}
	}
	if out == msg {
		return err
	}
	return errors.New(out)
}

// Redact hides every secret in s.
func Redact(s string, secrets ...string) string {
	for _, v := range secrets {
		if v != "" {
			s = strings.ReplaceAll(s, v, "[redacted]")
		}
	}
	return s
}

// Secret returns the string field of a credential, or "".
func Secret(cred nodesdk.Credential, field string) string {
	s, _ := cred.Data[field].(string)
	return s
}
