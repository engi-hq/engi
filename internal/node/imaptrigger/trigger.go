// Package imaptrigger implements the IMAP trigger: a poll trigger that emits
// one item per new mail of a mailbox.
//
// Progress is a cursor of (UIDVALIDITY, highest UID handed over), stored by the
// host in TriggerState, so a restart or a new leader never emits a mail twice;
// the dedup key uidvalidity:uid backs that up if a crash lands between starting
// executions and storing the cursor.
package imaptrigger

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	credimap "github.com/engi-hq/engi/internal/credtype/imap"
	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.trigger.imap"

// Trigger is the IMAP trigger node.
type Trigger struct{ lim Limits }

var _ nodesdk.PollTrigger = (*Trigger)(nil)

// New returns the IMAP trigger.
func New(lim Limits) *Trigger { return &Trigger{lim: lim} }

// Describe implements nodesdk.Node.
func (*Trigger) Describe() nodesdk.Descriptor {
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "IMAP Trigger", Group: nodesdk.GroupTrigger, Icon: "mail",
		Description: "Starts the workflow for every new mail of an IMAP mailbox. Polls on the leader instance.",
		Outputs:     []nodesdk.PortSpec{nodekit.Main("main", "")},
		Credentials: []nodesdk.CredentialRef{{Name: credimap.Name, Type: credimap.Name, Required: true}},
		Properties: []nodesdk.Property{
			{Name: "mailbox", DisplayName: "Mailbox", Type: nodesdk.PropString, Default: defaultMailbox},
			{Name: "intervalSeconds", DisplayName: "Poll interval (seconds)", Type: nodesdk.PropNumber, Default: float64(defaultInterval),
				Description: "Not below the server's poll.minInterval."},
			{Name: "unseenOnly", DisplayName: "Only unread mails", Type: nodesdk.PropBoolean, Default: true},
			{Name: "from", DisplayName: "From contains", Type: nodesdk.PropString},
			{Name: "subject", DisplayName: "Subject contains", Type: nodesdk.PropString},
			{Name: "afterProcessing", DisplayName: "After processing", Type: nodesdk.PropOptions, Default: afterNothing, Options: []nodesdk.Option{
				nodekit.Opt(afterNothing, "Leave the mail as it is"), nodekit.Opt(afterMarkRead, "Mark as read"), nodekit.Opt(afterMove, "Move to another mailbox")}},
			{Name: "moveTo", DisplayName: "Move to mailbox", Type: nodesdk.PropString, DisplayOptions: nodekit.Show("afterProcessing", afterMove)},
			{Name: "emitExisting", DisplayName: "Emit existing mails on activation", Type: nodesdk.PropBoolean, Default: false,
				Description: "Off: the first poll only remembers the newest mail, so only later mails start executions."},
		},
	}
}

// Execute implements nodesdk.Node: the host supplies the mail item.
func (*Trigger) Execute(_ context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	items := in.Items(0)
	out := make([]nodesdk.Item, len(items))
	for i, it := range items {
		out[i] = nodekit.Paired(it, i)
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}

// CheckPoll implements nodesdk.PollTrigger.
func (*Trigger) CheckPoll(params map[string]any) (nodesdk.PollSpec, error) {
	s, err := parseSettings(params)
	if err != nil {
		return nodesdk.PollSpec{}, err
	}
	return nodesdk.PollSpec{Interval: s.interval}, nil
}

// Poll implements nodesdk.PollTrigger.
func (t *Trigger) Poll(ctx context.Context, in nodesdk.PollInput, stored []byte) (nodesdk.PollResult, error) {
	s, err := parseSettings(in.Params)
	if err != nil {
		return nodesdk.PollResult{}, err
	}
	cred, ok := in.Credentials[credimap.Name]
	if !ok {
		return nodesdk.PollResult{}, fmt.Errorf("credential %q is not bound to the node", credimap.Name)
	}
	cfg, err := parseConn(cred.Data)
	if err != nil {
		return nodesdk.PollResult{}, err
	}
	cur, err := decodeCursor(stored)
	if err != nil {
		return nodesdk.PollResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, t.lim.Timeout)
	defer cancel()
	c, err := connect(ctx, cfg, t.lim.ConnectTimeout)
	if err != nil {
		return nodesdk.PollResult{}, err
	}
	defer c.close()

	sel, err := c.Select(s.mailbox, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		return nodesdk.PollResult{}, fmt.Errorf("selecting mailbox %q: %w", s.mailbox, err)
	}
	after, known := uint32(0), cur != nil && cur.UIDValidity == sel.UIDValidity
	if known {
		after = cur.LastUID
	}
	var found []uint32
	if known || s.emitExisting { // otherwise the poll only needs the newest UID
		res, err := c.UIDSearch(s.filters.criteria(after), nil).Wait()
		if err != nil {
			return nodesdk.PollResult{}, fmt.Errorf("searching mailbox %q: %w", s.mailbox, err)
		}
		for _, u := range res.AllUIDs() {
			found = append(found, uint32(u))
		}
	}
	emit, next := plan(cur, sel.UIDValidity, uint32(sel.UIDNext), found, t.lim.MaxMessages, s.emitExisting)
	res := nodesdk.PollResult{Next: next.encode()}
	if len(emit) == 0 {
		return res, nil
	}
	items, handled, err := t.fetch(ctx, c, in.Blobs, s.filters, sel.UIDValidity, emit)
	if err != nil {
		return nodesdk.PollResult{}, err
	}
	res.Items = items
	if s.after != afterNothing && len(handled) > 0 {
		res.Done = func(ctx context.Context) error { return t.finish(ctx, cfg, s, sel.UIDValidity, handled) }
	}
	return res, nil
}

// fetch reads the mails with the given UIDs. handled lists the UIDs that became
// items; mails dropped by the local filter stay untouched.
func (t *Trigger) fetch(ctx context.Context, c *client, blobs nodesdk.BlobAllocator, f filters, validity uint32, uids []uint32) (items []nodesdk.PolledItem, handled []imap.UID, err error) {
	var set imap.UIDSet
	for _, u := range uids {
		set.AddNum(imap.UID(u))
	}
	meta, err := c.Fetch(set, &imap.FetchOptions{UID: true, Flags: true, RFC822Size: true}).Collect()
	if err != nil {
		return nil, nil, fmt.Errorf("fetching message sizes: %w", err)
	}
	for _, m := range meta {
		section := &imap.FetchItemBodySection{Peek: true}
		tooBig := m.RFC822Size > t.lim.MaxMessageBytes
		if tooBig {
			section.Specifier = imap.PartSpecifierHeader
		}
		bufs, err := c.Fetch(imap.UIDSetNum(m.UID), &imap.FetchOptions{UID: true, BodySection: []*imap.FetchItemBodySection{section}}).Collect()
		if err != nil || len(bufs) != 1 {
			return nil, nil, fmt.Errorf("fetching message %d: %w", m.UID, errors.Join(err, errors.New("no data")))
		}
		p, err := parseMessage(bufs[0].FindBodySection(section), int(t.lim.MaxMessageBytes))
		if err != nil {
			return nil, nil, fmt.Errorf("message %d: %w", m.UID, err)
		}
		if !f.matches(p.from, p.subject, hasFlag(m.Flags, imap.FlagSeen)) {
			continue
		}
		p.item["uid"], p.item["uidValidity"] = float64(m.UID), float64(validity)
		p.item["size"] = float64(m.RFC822Size)
		if tooBig || p.truncated {
			p.item["truncated"] = true
		}
		polled := nodesdk.PolledItem{Item: nodesdk.Item{JSON: p.item}, DedupKey: dedupKey(validity, uint32(m.UID))}
		if err := storeAttachments(ctx, blobs, p, &polled); err != nil {
			return nil, nil, fmt.Errorf("message %d: %w", m.UID, err)
		}
		items = append(items, polled)
		handled = append(handled, m.UID)
	}
	return items, handled, nil
}

func hasFlag(flags []imap.Flag, want imap.Flag) bool {
	for _, f := range flags {
		if f == want {
			return true
		}
	}
	return false
}

// finish marks the mails read or moves them. It opens a fresh connection and
// does nothing if the mailbox was recreated in between (UIDVALIDITY changed).
func (t *Trigger) finish(ctx context.Context, cfg connConfig, s settings, validity uint32, uids []imap.UID) error {
	ctx, cancel := context.WithTimeout(ctx, t.lim.Timeout)
	defer cancel()
	c, err := connect(ctx, cfg, t.lim.ConnectTimeout)
	if err != nil {
		return err
	}
	defer c.close()
	sel, err := c.Select(s.mailbox, nil).Wait()
	if err != nil {
		return fmt.Errorf("selecting mailbox %q: %w", s.mailbox, err)
	}
	if sel.UIDValidity != validity {
		return errors.New("the mailbox was recreated; its mails were left as they are")
	}
	set := imap.UIDSetNum(uids...)
	if s.after == afterMove {
		if _, err := c.Move(set, s.moveTo).Wait(); err != nil {
			return fmt.Errorf("moving mails to %q: %w", s.moveTo, err)
		}
		return nil
	}
	if err := c.Store(set, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagSeen}}, nil).Close(); err != nil {
		return fmt.Errorf("marking mails as read: %w", err)
	}
	return nil
}

// client is a logged-in IMAP connection.
type client struct {
	*imapclient.Client
	stop func() bool
}

func (c *client) close() {
	c.stop()
	_ = c.Logout().Wait()
	_ = c.Close()
}

func connect(ctx context.Context, cfg connConfig, connectTimeout time.Duration) (*client, error) {
	addr := net.JoinHostPort(cfg.host, strconv.Itoa(cfg.port))
	d := net.Dialer{Timeout: connectTimeout}
	tlsCfg := &tls.Config{ServerName: cfg.host, MinVersion: tls.VersionTLS12}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", addr, err)
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	var ic *imapclient.Client
	switch cfg.security {
	case credimap.SecurityTLS:
		tc := tls.Client(conn, tlsCfg)
		if err := tc.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("TLS handshake with %s: %w", addr, err)
		}
		ic = imapclient.New(tc, nil)
	case credimap.SecuritySTARTTLS:
		if ic, err = imapclient.NewStartTLS(conn, &imapclient.Options{TLSConfig: tlsCfg}); err != nil {
			return nil, fmt.Errorf("starting TLS with %s: %w", addr, err)
		}
	default:
		ic = imapclient.New(conn, nil)
	}
	c := &client{Client: ic, stop: context.AfterFunc(ctx, func() { _ = ic.Close() })}
	if err := ic.Login(cfg.user, cfg.password).Wait(); err != nil {
		c.stop()
		_ = ic.Close()
		return nil, fmt.Errorf("IMAP login: %w", err)
	}
	return c, nil
}
