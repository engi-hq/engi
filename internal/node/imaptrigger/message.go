package imaptrigger

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/emersion/go-message"
	// Registers the legacy charsets (ISO-8859-x, Windows-125x, ...) so that
	// non-UTF-8 mails decode instead of failing.
	_ "github.com/emersion/go-message/charset"
	"github.com/emersion/go-message/mail"
)

// parsed is a message ready to become an item.
type parsed struct {
	from      string // "Name <address>" for filtering
	subject   string
	item      map[string]any
	truncated bool
	// files are the attachment bytes, kept in memory (the message is bounded by
	// the node's maxMessageBytes) until the mail passes the filter.
	files []attachment
}

// attachment is one decoded attachment.
type attachment struct {
	name, mime string
	data       []byte
}

// parseMessage turns RFC 5322 bytes into the item fields. Bodies larger than
// maxText are cut; attachments are listed (name, type, size) but their bytes
// are returned in parsed.files for the caller to store as binary fields.
func parseMessage(raw []byte, maxText int) (parsed, error) {
	mr, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil && !message.IsUnknownCharset(err) && !message.IsUnknownEncoding(err) {
		return parsed{}, fmt.Errorf("parsing the message: %w", err)
	}
	var p parsed
	h := mr.Header
	p.subject, _ = h.Subject()
	from, _ := h.AddressList("From")
	p.from = addressText(from)
	item := map[string]any{
		"from": addressList(from), "to": addressesOf(h, "To"), "cc": addressesOf(h, "Cc"), "replyTo": addressesOf(h, "Reply-To"),
		"subject": p.subject, "messageId": strings.Trim(h.Get("Message-Id"), "<> "), "headers": headerMap(h),
	}
	if d, err := h.Date(); err == nil {
		item["date"] = d.UTC().Format(time.RFC3339)
	}
	var text, html strings.Builder
	atts := []any{}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil && !message.IsUnknownCharset(err) && !message.IsUnknownEncoding(err) {
			return parsed{}, fmt.Errorf("reading a message part: %w", err)
		}
		if part == nil {
			continue
		}
		switch ph := part.Header.(type) {
		case *mail.InlineHeader:
			typ, _, _ := ph.ContentType()
			dst := &text
			if typ == "text/html" {
				dst = &html
			} else if typ != "text/plain" {
				continue
			}
			if dst.Len() == 0 {
				p.truncated = readCapped(dst, part.Body, maxText) || p.truncated
			}
		case *mail.AttachmentHeader:
			name, _ := ph.Filename()
			typ, _, _ := ph.ContentType()
			data, _ := io.ReadAll(part.Body)
			p.files = append(p.files, attachment{name: name, mime: typ, data: data})
			atts = append(atts, map[string]any{"filename": name, "contentType": typ, "size": float64(len(data))})
		}
	}
	item["text"], item["html"], item["attachments"] = text.String(), html.String(), atts
	p.item = item
	return p, nil
}

// readCapped copies at most limit bytes and reports whether it cut the body.
func readCapped(dst *strings.Builder, r io.Reader, limit int) bool {
	n, _ := io.Copy(dst, io.LimitReader(r, int64(limit)))
	if n < int64(limit) {
		return false
	}
	var one [1]byte
	m, _ := r.Read(one[:])
	return m > 0
}

func addressText(list []*mail.Address) string {
	parts := make([]string, len(list))
	for i, a := range list {
		parts[i] = strings.TrimSpace(a.Name + " <" + a.Address + ">") // decoded, for matching
	}
	return strings.Join(parts, ", ")
}

func addressList(list []*mail.Address) []any {
	out := make([]any, len(list))
	for i, a := range list {
		out[i] = map[string]any{"name": a.Name, "address": a.Address}
	}
	return out
}

func addressesOf(h mail.Header, key string) []any {
	list, _ := h.AddressList(key)
	return addressList(list)
}

// headerMap lists every header by lower-case name; a repeated header becomes a list.
func headerMap(h mail.Header) map[string]any {
	out := map[string]any{}
	f := h.Fields()
	for f.Next() {
		k := strings.ToLower(f.Key())
		v, err := f.Text()
		if err != nil {
			v = f.Value()
		}
		switch old := out[k].(type) {
		case nil:
			out[k] = v
		case string:
			out[k] = []any{old, v}
		case []any:
			out[k] = append(old, v)
		}
	}
	return out
}
