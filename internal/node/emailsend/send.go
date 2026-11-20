// Package emailsend implements the Send Email node: it renders each input item
// as a MIME message and delivers it over SMTP, reusing one connection for all
// items of a run.
//
// It uses the standard library's net/smtp for the protocol (no third-party
// dependency); dialing, TLS and deadlines are handled here because net/smtp
// has no context support.
package emailsend

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	credsmtp "github.com/engi-hq/engi/internal/credtype/smtp"
	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.email.send"

// messageIDBytes is the random part of a generated Message-ID.
const messageIDBytes = 16

// Limits are the host settings of the node (nodes.email.*).
type Limits struct {
	ConnectTimeout  time.Duration
	SendTimeout     time.Duration
	MaxRecipients   int
	MaxMessageBytes int64
}

// Clock tells the time; the Date header comes from it.
type Clock interface{ Now() time.Time }

// Node is the Send Email node.
type Node struct {
	lim    Limits
	clock  Clock
	random io.Reader
}

var _ nodesdk.Node = Node{}

// New returns the node. random supplies Message-ID entropy (crypto/rand in production).
func New(lim Limits, clock Clock, random io.Reader) nodesdk.Node {
	return Node{lim: lim, clock: clock, random: random}
}

// Describe implements nodesdk.Node.
func (Node) Describe() nodesdk.Descriptor {
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "Send Email", Group: nodesdk.GroupAction, Icon: "mail",
		Description: "Send one email per input item over SMTP.",
		Inputs:      []nodesdk.PortSpec{nodekit.Main("main", "")},
		Outputs:     []nodesdk.PortSpec{nodekit.Main("main", "")},
		Credentials: []nodesdk.CredentialRef{{Name: credsmtp.Name, Type: credsmtp.Name, Required: true}},
		Properties: []nodesdk.Property{
			{Name: "from", DisplayName: "From", Type: nodesdk.PropString, Placeholder: "Engi <noreply@example.com>",
				Description: "Empty uses the credential's default from address."},
			{Name: "to", DisplayName: "To", Type: nodesdk.PropString, Required: true, Description: "Comma-separated addresses, or a list from an expression."},
			{Name: "cc", DisplayName: "Cc", Type: nodesdk.PropString},
			{Name: "bcc", DisplayName: "Bcc", Type: nodesdk.PropString},
			{Name: "replyTo", DisplayName: "Reply-To", Type: nodesdk.PropString},
			{Name: "subject", DisplayName: "Subject", Type: nodesdk.PropString, Required: true},
			{Name: "text", DisplayName: "Text body", Type: nodesdk.PropString, Description: "Plain-text body. Set text, HTML or both."},
			{Name: "html", DisplayName: "HTML body", Type: nodesdk.PropString,
				Description: "With both bodies the mail is multipart/alternative."},
			{Name: "attachments", DisplayName: "Attachments", Type: nodesdk.PropString, Placeholder: "data, invoice",
				Description: "Comma-separated names of the binary fields of the item to attach."},
		},
	}
}

// Execute implements nodesdk.Node: every item becomes one message, all sent
// over one SMTP connection.
func (n Node) Execute(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	items := in.Items(0)
	if len(items) == 0 {
		return nodesdk.Output{Ports: [][]nodesdk.Item{nil}}, nil
	}
	cred, err := in.Credential(ctx, credsmtp.Name)
	if err != nil {
		return nodesdk.Output{}, fmt.Errorf("loading the SMTP credential: %w", err)
	}
	cfg, err := parseConn(cred.Data)
	if err != nil {
		return nodesdk.Output{}, err
	}
	// Build every message first: a bad item fails the run before anything is sent.
	msgs := make([]Message, len(items))
	raws := make([][]byte, len(items))
	for i := range items {
		if msgs[i], err = n.message(ctx, in, i, cfg.from); err != nil {
			return nodesdk.Output{}, nodekit.ItemError(i, err)
		}
		if raws[i], err = Build(msgs[i]); err != nil {
			return nodesdk.Output{}, nodekit.ItemError(i, err)
		}
		if int64(len(raws[i])) > n.lim.MaxMessageBytes {
			return nodesdk.Output{}, nodekit.ItemError(i, fmt.Errorf("message is %d bytes, over the limit of %d (nodes.email.maxMessageBytes)", len(raws[i]), n.lim.MaxMessageBytes))
		}
	}
	ctx, cancel := context.WithTimeout(ctx, n.lim.SendTimeout)
	defer cancel()
	sess, err := dial(ctx, cfg, n.lim.ConnectTimeout)
	if err != nil {
		return nodesdk.Output{}, err
	}
	defer sess.close()

	out := make([]nodesdk.Item, len(items))
	for i, m := range msgs {
		accepted, rejected, err := sess.send(m.From.Address, m.Recipients(), raws[i])
		if err != nil {
			return nodesdk.Output{}, nodekit.ItemError(i, err)
		}
		out[i] = nodekit.Paired(nodesdk.Item{JSON: map[string]any{
			"messageId": m.MessageID, "from": m.From.Address, "accepted": toAny(accepted), "rejected": toAny(rejected),
		}}, i)
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}

func toAny(s []string) []any {
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}

// message reads the parameters of item i into a Message.
func (n Node) message(ctx context.Context, in nodesdk.Input, i int, defaultFrom mail.Address) (Message, error) {
	var m Message
	addrs := func(name string) ([]mail.Address, error) {
		v, err := in.Param(ctx, name, i)
		if err != nil {
			return nil, fmt.Errorf("reading parameter %q: %w", name, err)
		}
		return ParseAddresses(name, v)
	}
	var err error
	if m.To, err = addrs("to"); err != nil {
		return m, err
	}
	if m.Cc, err = addrs("cc"); err != nil {
		return m, err
	}
	if m.Bcc, err = addrs("bcc"); err != nil {
		return m, err
	}
	if m.ReplyTo, err = addrs("replyTo"); err != nil {
		return m, err
	}
	if len(m.To)+len(m.Cc)+len(m.Bcc) == 0 {
		return m, errors.New("the message has no recipients")
	}
	if total := len(m.To) + len(m.Cc) + len(m.Bcc); total > n.lim.MaxRecipients {
		return m, fmt.Errorf("the message has %d recipients, over the limit of %d (nodes.email.maxRecipients)", total, n.lim.MaxRecipients)
	}
	from, err := nodesdk.ParamString(ctx, in, "from", i)
	if err != nil {
		return m, err
	}
	if m.From, err = ParseAddress("from", from); err != nil {
		return m, err
	}
	if m.From.Address == "" {
		m.From = defaultFrom
	}
	if m.From.Address == "" {
		return m, errors.New("no From address: set it on the node or as the credential's default from address")
	}
	for name, dst := range map[string]*string{"subject": &m.Subject, "text": &m.Text, "html": &m.HTML} {
		if *dst, err = nodesdk.ParamString(ctx, in, name, i); err != nil {
			return m, err
		}
	}
	if m.Attachments, err = n.attachments(ctx, in, i); err != nil {
		return m, err
	}
	id := make([]byte, messageIDBytes)
	if _, err := io.ReadFull(n.random, id); err != nil {
		return m, fmt.Errorf("generating a message id: %w", err)
	}
	domain := "engi.invalid"
	if at := strings.LastIndexByte(m.From.Address, '@'); at >= 0 {
		domain = m.From.Address[at+1:]
	}
	m.MessageID, m.Date = hex.EncodeToString(id)+"@"+domain, n.clock.Now()
	return m, nil
}

// attachments loads the item's binary fields named by the attachments parameter.
func (n Node) attachments(ctx context.Context, in nodesdk.Input, i int) ([]Attachment, error) {
	names, err := nodekit.ParamStringList(ctx, in, "attachments", i)
	if err != nil || len(names) == 0 {
		return nil, err //nolint:wrapcheck // already specific
	}
	item := in.Items(0)[i]
	var out []Attachment
	var total int64
	for _, name := range names {
		ref, ok := item.Binary[name]
		if !ok {
			return nil, fmt.Errorf("the item has no binary field %q to attach", name)
		}
		rc, err := in.Blob().Open(ctx, ref)
		if err != nil {
			return nil, fmt.Errorf("opening binary field %q: %w", name, err)
		}
		data, err := io.ReadAll(io.LimitReader(rc, n.lim.MaxMessageBytes-total+1))
		_ = rc.Close()
		if err != nil {
			return nil, fmt.Errorf("reading binary field %q: %w", name, err)
		}
		if total += int64(len(data)); total > n.lim.MaxMessageBytes {
			return nil, fmt.Errorf("attachments exceed %d bytes (nodes.email.maxMessageBytes)", n.lim.MaxMessageBytes)
		}
		fileName := ref.Name
		if fileName == "" {
			fileName = name
		}
		out = append(out, Attachment{Name: fileName, MIME: ref.MIME, Data: data})
	}
	return out, nil
}

// connConfig is the SMTP credential, parsed.
type connConfig struct {
	host, user, password, security string
	port                           int
	from                           mail.Address
}

func parseConn(d map[string]any) (connConfig, error) {
	str := func(k string) string { s, _ := d[k].(string); return strings.TrimSpace(s) }
	c := connConfig{host: str("host"), user: str("user"), password: str("password"), security: str("security")}
	if c.host == "" {
		return c, errors.New("the SMTP credential has no host")
	}
	switch c.security {
	case "":
		c.security = credsmtp.SecuritySTARTTLS
	case credsmtp.SecurityNone, credsmtp.SecuritySTARTTLS, credsmtp.SecurityTLS:
	default:
		return c, fmt.Errorf("the SMTP credential has an unknown security mode %q", c.security)
	}
	switch p := d["port"].(type) {
	case float64:
		c.port = int(p)
	case int:
		c.port = p
	case string:
		c.port, _ = strconv.Atoi(p)
	}
	if c.port <= 0 || c.port > 65535 {
		return c, fmt.Errorf("the SMTP credential has an invalid port %v", d["port"])
	}
	if f := str("fromAddress"); f != "" {
		var err error
		if c.from, err = ParseAddress("default from address", f); err != nil {
			return c, err
		}
	}
	return c, nil
}

// session is one authenticated SMTP connection.
type session struct {
	c    *smtp.Client
	conn net.Conn
	used bool
	stop func() bool
}

func dial(ctx context.Context, cfg connConfig, connectTimeout time.Duration) (*session, error) {
	addr := net.JoinHostPort(cfg.host, strconv.Itoa(cfg.port))
	d := net.Dialer{Timeout: connectTimeout}
	tlsCfg := &tls.Config{ServerName: cfg.host, MinVersion: tls.VersionTLS12}
	var conn net.Conn
	var err error
	if cfg.security == credsmtp.SecurityTLS {
		conn, err = (&tls.Dialer{NetDialer: &d, Config: tlsCfg}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", addr, err)
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	s := &session{conn: conn, stop: context.AfterFunc(ctx, func() { _ = conn.Close() })}
	if s.c, err = smtp.NewClient(conn, cfg.host); err != nil {
		s.close()
		return nil, fmt.Errorf("SMTP greeting from %s: %w", addr, err)
	}
	if cfg.security == credsmtp.SecuritySTARTTLS {
		if err := s.c.StartTLS(tlsCfg); err != nil {
			s.close()
			return nil, fmt.Errorf("starting TLS: %w", err)
		}
	}
	if cfg.user != "" {
		if err := s.c.Auth(smtp.PlainAuth("", cfg.user, cfg.password, cfg.host)); err != nil {
			s.close()
			return nil, fmt.Errorf("SMTP authentication: %w", err)
		}
	}
	return s, nil
}

// send delivers one message. Recipients the server refuses are returned in
// rejected; it is an error only if none is accepted.
func (s *session) send(from string, rcpts []string, raw []byte) (accepted, rejected []string, err error) {
	if s.used {
		if err := s.c.Reset(); err != nil {
			return nil, nil, fmt.Errorf("resetting the SMTP session: %w", err)
		}
	}
	s.used = true
	if err := s.c.Mail(from); err != nil {
		return nil, nil, fmt.Errorf("sender %s refused: %w", from, err)
	}
	var lastErr error
	for _, r := range rcpts {
		if err := s.c.Rcpt(r); err != nil {
			rejected, lastErr = append(rejected, r), err
			continue
		}
		accepted = append(accepted, r)
	}
	if len(accepted) == 0 {
		return nil, rejected, fmt.Errorf("every recipient was refused: %w", lastErr)
	}
	w, err := s.c.Data()
	if err != nil {
		return nil, nil, fmt.Errorf("starting the message data: %w", err)
	}
	if _, err := w.Write(raw); err != nil {
		return nil, nil, fmt.Errorf("sending the message data: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, nil, fmt.Errorf("the server refused the message: %w", err)
	}
	return accepted, rejected, nil
}

func (s *session) close() {
	if s.stop != nil {
		s.stop()
	}
	if s.c != nil {
		_ = s.c.Quit()
	}
	_ = s.conn.Close()
}
