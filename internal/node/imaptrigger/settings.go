package imaptrigger

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	credimap "github.com/engi-hq/engi/internal/credtype/imap"
)

// After-processing actions (parameter afterProcessing).
const (
	afterNothing  = "nothing"
	afterMarkRead = "markRead"
	afterMove     = "move"
)

// Defaults of the trigger parameters.
const (
	defaultMailbox  = "INBOX"
	defaultInterval = 60 // seconds
)

// Limits are the host settings of the trigger (nodes.email.imap*).
type Limits struct {
	ConnectTimeout time.Duration
	// Timeout bounds one poll.
	Timeout time.Duration
	// MaxMessages caps the messages one poll emits.
	MaxMessages int
	// MaxMessageBytes: larger messages are emitted with headers only.
	MaxMessageBytes int64
}

// settings are the parsed node parameters.
type settings struct {
	mailbox      string
	interval     time.Duration
	filters      filters
	after        string
	moveTo       string
	emitExisting bool
}

func parseSettings(p map[string]any) (settings, error) {
	s := settings{mailbox: defaultMailbox, interval: defaultInterval * time.Second, after: afterNothing,
		filters: filters{unseenOnly: true}}
	str := func(k string) string { v, _ := p[k].(string); return strings.TrimSpace(v) }
	if v := str("mailbox"); v != "" {
		s.mailbox = v
	}
	if v, ok := p["intervalSeconds"]; ok && v != nil {
		secs, err := number(v)
		if err != nil || secs <= 0 {
			return s, fmt.Errorf("intervalSeconds must be a positive number, got %v", v)
		}
		s.interval = time.Duration(secs * float64(time.Second))
	}
	if v, ok := p["unseenOnly"].(bool); ok {
		s.filters.unseenOnly = v
	}
	s.filters.from, s.filters.subject = str("from"), str("subject")
	for k, v := range map[string]string{"mailbox": s.mailbox, "from": s.filters.from, "subject": s.filters.subject} {
		if strings.ContainsAny(v, "\r\n\x00") {
			return s, fmt.Errorf("%s must not contain line breaks", k)
		}
	}
	if v := str("afterProcessing"); v != "" {
		s.after = v
	}
	s.moveTo = str("moveTo")
	switch s.after {
	case afterNothing, afterMarkRead:
	case afterMove:
		if s.moveTo == "" {
			return s, errors.New("afterProcessing \"move\" needs the moveTo mailbox")
		}
		if s.moveTo == s.mailbox {
			return s, errors.New("moveTo must differ from the mailbox")
		}
	default:
		return s, fmt.Errorf("afterProcessing %q is not %s, %s or %s", s.after, afterNothing, afterMarkRead, afterMove)
	}
	s.emitExisting, _ = p["emitExisting"].(bool)
	return s, nil
}

func number(v any) (float64, error) {
	switch n := v.(type) {
	case float64:
		return n, nil
	case int:
		return float64(n), nil
	case int64:
		return float64(n), nil
	case string:
		f, err := strconv.ParseFloat(n, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, fmt.Errorf("%q is not a number", n)
		}
		return f, nil
	}
	return 0, fmt.Errorf("%T is not a number", v)
}

// connConfig is the IMAP credential, parsed.
type connConfig struct {
	host, user, password, security string
	port                           int
}

func parseConn(d map[string]any) (connConfig, error) {
	str := func(k string) string { s, _ := d[k].(string); return strings.TrimSpace(s) }
	c := connConfig{host: str("host"), user: str("user"), password: str("password"), security: str("security")}
	if c.host == "" || c.user == "" {
		return c, errors.New("the IMAP credential needs a host and a user")
	}
	switch c.security {
	case "":
		c.security = credimap.SecurityTLS
	case credimap.SecurityNone, credimap.SecuritySTARTTLS, credimap.SecurityTLS:
	default:
		return c, fmt.Errorf("the IMAP credential has an unknown security mode %q", c.security)
	}
	if n, err := number(d["port"]); err == nil {
		c.port = int(n)
	}
	if c.port <= 0 || c.port > 65535 {
		return c, fmt.Errorf("the IMAP credential has an invalid port %v", d["port"])
	}
	return c, nil
}
