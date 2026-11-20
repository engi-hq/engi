package pluginproto_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/engi-hq/engi/pkg/nodesdk"
	"github.com/engi-hq/engi/pkg/pluginproto"
)

const testMax = 1 << 20

// pair connects two Conns through pipes and runs both until the test ends.
func pair(t *testing.T, a, b pluginproto.Handler, opts pluginproto.ConnOptions) (ca, cb *pluginproto.Conn) {
	t.Helper()
	ar, bw := io.Pipe()
	br, aw := io.Pipe()
	ca = pluginproto.NewConn(ar, aw, a, opts)
	cb = pluginproto.NewConn(br, bw, b, opts)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = ca.Run(ctx) }()
	go func() { _ = cb.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = aw.Close()
		_ = bw.Close()
		<-ca.Done()
		<-cb.Done()
	})
	return ca, cb
}

func opts() pluginproto.ConnOptions {
	return pluginproto.ConnOptions{MaxMessageBytes: testMax, MaxConcurrent: 4}
}

func noHandler(context.Context, pluginproto.Request) (any, error) {
	return nil, &pluginproto.Error{Code: pluginproto.CodeMethodNotFound, Message: "no methods"}
}

func TestCallBothWaysWithNestedCallback(t *testing.T) {
	var pluginSide *pluginproto.Conn
	p, h := pair(t,
		func(ctx context.Context, r pluginproto.Request) (any, error) {
			// plugin side: answer "execute" after calling back into the host.
			var v pluginproto.EvalResult
			if err := pluginSide.Call(ctx, pluginproto.MethodEvalExpression, pluginproto.EvalParams{Expression: "1+1"}, &v); err != nil {
				return nil, err
			}
			return map[string]any{"got": v.Value}, nil
		},
		func(_ context.Context, r pluginproto.Request) (any, error) {
			if r.Method != pluginproto.MethodEvalExpression {
				return nil, errors.New("unexpected " + r.Method)
			}
			return pluginproto.EvalResult{Value: 2.0}, nil
		}, opts())
	pluginSide = p // the plugin calls back through its own end
	var res map[string]any
	if err := h.Call(context.Background(), pluginproto.MethodExecute, nil, &res); err != nil {
		t.Fatal(err)
	}
	if res["got"] != 2.0 {
		t.Fatalf("result = %v", res)
	}
}

func TestErrorsKeepCodeAndNodeErrorData(t *testing.T) {
	idx := 3
	_, h := pair(t, func(context.Context, pluginproto.Request) (any, error) {
		return nil, &nodesdk.NodeError{Message: "bad input", Description: "field x", HTTPStatus: 422, ItemIndex: &idx}
	}, noHandler, opts())
	err := h.Call(context.Background(), "x", nil, nil)
	var we *pluginproto.Error
	if !errors.As(err, &we) || we.Code != pluginproto.CodeNodeError {
		t.Fatalf("err = %v", err)
	}
	ne := we.NodeError()
	if ne.Message != "bad input" || ne.Description != "field x" || ne.HTTPStatus != 422 || ne.ItemIndex == nil || *ne.ItemIndex != 3 {
		t.Fatalf("node error = %+v", ne)
	}
}

func TestCancelReachesTheHandler(t *testing.T) {
	cancelled := make(chan struct{})
	_, h := pair(t, func(ctx context.Context, _ pluginproto.Request) (any, error) {
		<-ctx.Done()
		close(cancelled)
		return nil, ctx.Err()
	}, noHandler, opts())
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := h.Call(ctx, "slow", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("handler was not cancelled")
	}
}

func TestBusyAboveConcurrencyCap(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	o := opts()
	o.MaxConcurrent = 1
	_, h := pair(t, func(context.Context, pluginproto.Request) (any, error) {
		close(started)
		<-release
		return nil, nil
	}, noHandler, o)
	first := make(chan error, 1)
	go func() { first <- h.Call(context.Background(), "a", nil, nil) }()
	<-started
	var we *pluginproto.Error
	if err := h.Call(context.Background(), "b", nil, nil); !errors.As(err, &we) || we.Code != pluginproto.CodeBusy {
		t.Fatalf("err = %v, want busy", err)
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
}

func TestMessageTooLargeIsRefusedBeforeSending(t *testing.T) {
	o := opts()
	o.MaxMessageBytes = 64
	_, h := pair(t, noHandler, noHandler, o)
	err := h.Call(context.Background(), "x", strings.Repeat("a", 100), nil)
	if !errors.Is(err, pluginproto.ErrMessageTooLarge) {
		t.Fatalf("err = %v", err)
	}
}

func TestOversizedIncomingLineEndsRun(t *testing.T) {
	r := strings.NewReader(strings.Repeat("x", 200) + "\n")
	c := pluginproto.NewConn(r, io.Discard, noHandler, pluginproto.ConnOptions{MaxMessageBytes: 100, MaxConcurrent: 1})
	if err := c.Run(context.Background()); !errors.Is(err, pluginproto.ErrMessageTooLarge) {
		t.Fatalf("err = %v", err)
	}
}

func TestPendingCallsFailWhenPeerGoesAway(t *testing.T) {
	pr, pw := io.Pipe()
	c := pluginproto.NewConn(pr, io.Discard, noHandler, opts())
	go func() { _ = c.Run(context.Background()) }()
	errc := make(chan error, 1)
	go func() { errc <- c.Call(context.Background(), "x", nil, nil) }()
	time.Sleep(20 * time.Millisecond)
	_ = pw.Close()
	if err := <-errc; !errors.Is(err, pluginproto.ErrClosed) {
		t.Fatalf("err = %v", err)
	}
	if err := c.Call(context.Background(), "x", nil, nil); !errors.Is(err, pluginproto.ErrClosed) {
		t.Fatalf("after close err = %v", err)
	}
}

func TestNotificationsAndInvalidJSON(t *testing.T) {
	got := make(chan string, 1)
	var out strings.Builder
	in := strings.NewReader("not json\n" + `{"jsonrpc":"2.0","method":"log","params":{"level":"info","message":"hi"}}` + "\n")
	c := pluginproto.NewConn(in, &out, func(_ context.Context, r pluginproto.Request) (any, error) {
		var p pluginproto.LogParams
		_ = json.Unmarshal(r.Params, &p)
		got <- p.Message
		return nil, nil
	}, opts())
	if err := c.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m := <-got; m != "hi" {
		t.Fatalf("message = %q", m)
	}
	if !strings.Contains(out.String(), `"code":-32700`) {
		t.Fatalf("no parse error reply: %s", out.String())
	}
}
