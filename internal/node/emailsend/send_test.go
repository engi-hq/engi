package emailsend_test

import (
	"bufio"
	"context"
	"crypto/rand"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/node/emailsend"
	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// fakeSMTP is a minimal SMTP server that records what it receives.
type fakeSMTP struct {
	ln    net.Listener
	mu    sync.Mutex
	conns int
	mails []string
	froms []string
	rcpts [][]string
	auth  string
}

func newFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{ln: ln}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			f.mu.Lock()
			f.conns++
			f.mu.Unlock()
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeSMTP) port() float64 { return float64(f.ln.Addr().(*net.TCPAddr).Port) }

func (f *fakeSMTP) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	say := func(s string) { _, _ = c.Write([]byte(s + "\r\n")) }
	say("220 fake ESMTP")
	var rc []string
	var from string
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		switch cmd := strings.ToUpper(line); {
		case strings.HasPrefix(cmd, "EHLO"):
			_, _ = c.Write([]byte("250-fake\r\n250 AUTH PLAIN\r\n"))
		case strings.HasPrefix(cmd, "AUTH"):
			f.mu.Lock()
			f.auth = line
			f.mu.Unlock()
			say("235 ok")
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			from, rc = strings.Trim(line[len("MAIL FROM:"):], "<>"), nil
			say("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			addr := strings.Trim(line[len("RCPT TO:"):], "<>")
			if strings.HasPrefix(addr, "bad@") {
				say("550 no such user")
				continue
			}
			rc = append(rc, addr)
			say("250 ok")
		case cmd == "DATA":
			say("354 go")
			var sb strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				sb.WriteString(l)
			}
			f.mu.Lock()
			f.mails, f.froms, f.rcpts = append(f.mails, sb.String()), append(f.froms, from), append(f.rcpts, rc)
			f.mu.Unlock()
			say("250 queued")
		case cmd == "RSET":
			say("250 ok")
		case cmd == "QUIT":
			say("221 bye")
			return
		default:
			say("502 unknown")
		}
	}
}

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }

func testNode() nodesdk.Node {
	return emailsend.New(emailsend.Limits{ConnectTimeout: 2 * time.Second, SendTimeout: 5 * time.Second, MaxRecipients: 3, MaxMessageBytes: 1 << 20},
		fixedClock{}, rand.Reader)
}

func input(f *fakeSMTP, items []nodesdk.Item, params map[string]any) *nodetest.Input {
	in := nodetest.New(testNode().Describe(), items, params)
	in.Credentials = map[string]nodesdk.Credential{"engi.smtp": {Type: "engi.smtp", Data: map[string]any{
		"host": "127.0.0.1", "port": f.port(), "user": "u", "password": "p", "security": "none", "fromAddress": "Engi <noreply@example.com>",
	}}}
	return in
}

func TestSendsEveryItemOverOneConnection(t *testing.T) {
	f := newFakeSMTP(t)
	in := input(f, []nodesdk.Item{{JSON: map[string]any{}}, {JSON: map[string]any{}}}, map[string]any{
		"to": "a@example.com, b@example.com", "bcc": []any{"hidden@example.com"}, "subject": "Hi", "text": "body",
	})
	out, err := testNode().Execute(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Ports[0]) != 2 {
		t.Fatalf("output items = %d", len(out.Ports[0]))
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.conns != 1 {
		t.Errorf("connections = %d, want 1", f.conns)
	}
	if len(f.mails) != 2 || f.froms[0] != "noreply@example.com" {
		t.Fatalf("mails = %d from %v", len(f.mails), f.froms)
	}
	if got := strings.Join(f.rcpts[0], ","); got != "a@example.com,b@example.com,hidden@example.com" {
		t.Errorf("envelope recipients = %s", got)
	}
	if strings.Contains(f.mails[0], "hidden@example.com") {
		t.Error("Bcc visible in the message")
	}
	if f.auth == "" {
		t.Error("client did not authenticate")
	}
}

func TestRefusedRecipientIsReported(t *testing.T) {
	f := newFakeSMTP(t)
	in := input(f, []nodesdk.Item{{JSON: map[string]any{}}}, map[string]any{"to": "ok@example.com, bad@example.com", "subject": "Hi", "text": "x"})
	out, err := testNode().Execute(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	rej := out.Ports[0][0].JSON["rejected"].([]any)
	if len(rej) != 1 || rej[0] != "bad@example.com" {
		t.Errorf("rejected = %v", rej)
	}
	in = input(f, []nodesdk.Item{{JSON: map[string]any{}}}, map[string]any{"to": "bad@example.com", "subject": "Hi", "text": "x"})
	if _, err := testNode().Execute(context.Background(), in); err == nil {
		t.Error("run succeeded although every recipient was refused")
	}
}

func TestRejectsBadInputBeforeConnecting(t *testing.T) {
	f := newFakeSMTP(t)
	tests := map[string]map[string]any{
		"header injection":   {"to": "a@example.com", "subject": "x\r\nBcc: e@example.com", "text": "x"},
		"too many":           {"to": "a@x.io,b@x.io,c@x.io,d@x.io", "subject": "x", "text": "x"},
		"no recipients":      {"subject": "x", "text": "x"},
		"no body":            {"to": "a@example.com", "subject": "x"},
		"missing attachment": {"to": "a@example.com", "subject": "x", "text": "x", "attachments": "data"},
	}
	for name, params := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := testNode().Execute(context.Background(), input(f, []nodesdk.Item{{JSON: map[string]any{}}}, params)); err == nil {
				t.Fatal("no error")
			}
		})
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.conns != 0 {
		t.Errorf("connected %d times for invalid input", f.conns)
	}
}
