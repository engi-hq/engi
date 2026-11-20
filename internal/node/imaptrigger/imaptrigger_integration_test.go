//go:build integration

package imaptrigger_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/engi-hq/engi/internal/node/emailsend"
	"github.com/engi-hq/engi/internal/node/imaptrigger"
	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

const (
	greenmailImage = "greenmail/standalone:2.1.3"
	smtpPort       = "3025/tcp"
	imapPort       = "3143/tcp"
	mailUser       = "alice"
	mailPassword   = "secret"
	mailAddress    = "alice@example.com"
)

type sysClock struct{}

func (sysClock) Now() time.Time { return time.Now() }

// mailServer is one GreenMail container offering SMTP and IMAP.
type mailServer struct {
	host               string
	smtpPort, imapPort float64
	smtpCred, imapCred nodesdk.Credential
	sender             nodesdk.Node
	trigger            func() *imaptrigger.Trigger
	pollInput          func(params map[string]any) nodesdk.PollInput
}

func newMailServer(t *testing.T) *mailServer {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        greenmailImage,
			ExposedPorts: []string{smtpPort, imapPort},
			Env: map[string]string{"GREENMAIL_OPTS": "-Dgreenmail.setup.test.all -Dgreenmail.hostname=0.0.0.0 " +
				fmt.Sprintf("-Dgreenmail.users=%s:%s@example.com", mailUser, mailPassword)},
			WaitingFor: wait.ForLog("Starting GreenMail API server").WithStartupTimeout(2 * time.Minute),
		},
		Started: true,
	})
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("starting greenmail: %v", err)
	}
	host, err := c.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port := func(p string) float64 {
		mp, err := c.MappedPort(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		return float64(mp.Num())
	}
	s := &mailServer{host: host, smtpPort: port(smtpPort), imapPort: port(imapPort)}
	s.smtpCred = nodesdk.Credential{Type: "engi.smtp", Data: map[string]any{
		"host": host, "port": s.smtpPort, "user": mailUser, "password": mailPassword, "security": "none", "fromAddress": mailAddress,
	}}
	s.imapCred = nodesdk.Credential{Type: "engi.imap", Data: map[string]any{
		"host": host, "port": s.imapPort, "user": mailUser, "password": mailPassword, "security": "none",
	}}
	s.sender = emailsend.New(emailsend.Limits{ConnectTimeout: 10 * time.Second, SendTimeout: time.Minute, MaxRecipients: 10, MaxMessageBytes: 1 << 20},
		sysClock{}, rand.Reader)
	s.trigger = func() *imaptrigger.Trigger {
		return imaptrigger.New(imaptrigger.Limits{ConnectTimeout: 10 * time.Second, Timeout: time.Minute, MaxMessages: 50, MaxMessageBytes: 1 << 20})
	}
	s.pollInput = func(params map[string]any) nodesdk.PollInput {
		return nodesdk.PollInput{
			WorkflowID: "wf", NodeName: "Mail", Params: params,
			Credentials: map[string]nodesdk.Credential{"engi.imap": s.imapCred},
			Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		}
	}
	return s
}

// send delivers subjects as separate messages over one connection.
func (s *mailServer) send(t *testing.T, subjects ...string) {
	t.Helper()
	items := make([]nodesdk.Item, len(subjects))
	for i, sub := range subjects {
		items[i] = nodesdk.Item{JSON: map[string]any{"subject": sub}}
	}
	in := nodetest.New(s.sender.Describe(), items, nil)
	in.Credentials = map[string]nodesdk.Credential{"engi.smtp": s.smtpCred}
	in.ParamFunc = func(name string, i int) (any, bool) {
		switch name {
		case "to":
			return mailAddress, true
		case "subject":
			return subjects[i], true
		case "text":
			return "Hello from Engi, äöü", true
		case "html":
			return "<p>Hello from <b>Engi</b></p>", true
		}
		return nil, false
	}
	if _, err := s.sender.Execute(context.Background(), in); err != nil {
		t.Fatalf("sending %v: %v", subjects, err)
	}
}

// poll runs the trigger until it finds want items or the deadline passes
// (SMTP delivery into the mailbox is asynchronous). With want 0 it polls once.
func (s *mailServer) poll(t *testing.T, tr *imaptrigger.Trigger, params map[string]any, cursor []byte, want int) nodesdk.PollResult {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		res, err := tr.Poll(context.Background(), s.pollInput(params), cursor)
		if err != nil {
			t.Fatalf("poll: %v", err)
		}
		if want == 0 || len(res.Items) >= want || time.Now().After(deadline) {
			return res
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func subjectsOf(res nodesdk.PollResult) string {
	var out []string
	for _, it := range res.Items {
		out = append(out, it.Item.JSON["subject"].(string))
	}
	return strings.Join(out, ",")
}

func TestSendThenPollOnceThenRestartWithoutDuplicates(t *testing.T) {
	s := newMailServer(t)
	params := map[string]any{"mailbox": "INBOX", "afterProcessing": "markRead"}

	// 1. The first poll after activation only establishes the cursor.
	first := s.poll(t, s.trigger(), params, nil, 0)
	if len(first.Items) != 0 || first.Next == nil {
		t.Fatalf("first poll: items %d, cursor %s", len(first.Items), first.Next)
	}

	// 2. Two mails sent through the node over one connection are picked up once.
	s.send(t, "first", "second")
	got := s.poll(t, s.trigger(), params, first.Next, 2)
	if subjectsOf(got) != "first,second" {
		t.Fatalf("subjects = %q", subjectsOf(got))
	}
	m := got.Items[0].Item.JSON
	if m["text"] == nil || !strings.Contains(m["text"].(string), "Hello from Engi, äöü") || !strings.Contains(m["html"].(string), "<b>Engi</b>") {
		t.Errorf("bodies = %q / %q", m["text"], m["html"])
	}
	if from := m["from"].([]any)[0].(map[string]any)["address"]; from != mailAddress {
		t.Errorf("from = %v", from)
	}
	if !strings.Contains(got.Items[0].DedupKey, ":") {
		t.Errorf("dedup key = %q", got.Items[0].DedupKey)
	}
	if got.Done == nil {
		t.Fatal("markRead requested but no Done callback")
	}
	if err := got.Done(context.Background()); err != nil {
		t.Fatalf("marking as read: %v", err)
	}

	// 3. Polling again with the stored cursor finds nothing.
	again := s.poll(t, s.trigger(), params, got.Next, 0)
	if len(again.Items) != 0 {
		t.Fatalf("duplicate emission: %q", subjectsOf(again))
	}

	// 4. Restart: a fresh trigger with only the persisted cursor emits nothing
	// old, and exactly the one new mail.
	s.send(t, "third")
	after := s.poll(t, s.trigger(), params, again.Next, 1)
	if subjectsOf(after) != "third" {
		t.Fatalf("after restart: %q", subjectsOf(after))
	}

	// 5. Even if the cursor is lost, the read mails are skipped by the unread
	// filter, and with the filter off the dedup keys repeat exactly.
	lost := s.poll(t, s.trigger(), map[string]any{"emitExisting": true}, nil, 1)
	if subjectsOf(lost) != "third" {
		t.Errorf("lost cursor, unread only: %q", subjectsOf(lost))
	}
	all := s.poll(t, s.trigger(), map[string]any{"emitExisting": true, "unseenOnly": false}, nil, 3)
	if len(all.Items) != 3 || all.Items[0].DedupKey != got.Items[0].DedupKey {
		t.Errorf("replay: %d items, first key %q want %q", len(all.Items), all.Items[0].DedupKey, got.Items[0].DedupKey)
	}

	// 6. The subject filter works against the real server.
	filtered := s.poll(t, s.trigger(), map[string]any{"emitExisting": true, "unseenOnly": false, "subject": "SEC"}, nil, 1)
	if subjectsOf(filtered) != "second" {
		t.Errorf("subject filter: %q", subjectsOf(filtered))
	}
}
