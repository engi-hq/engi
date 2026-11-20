package emailsend

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"strings"
	"time"
)

// maxHeaderLine is the RFC 5322 hard limit for one header line.
const maxHeaderLine = 998

// base64LineLen is the line length of base64 attachments (RFC 2045).
const base64LineLen = 76

// Attachment is one file of a message.
type Attachment struct {
	Name string
	// MIME is the media type; empty means application/octet-stream.
	MIME string
	Data []byte
}

// Message is an outgoing email, ready to be rendered by Build.
type Message struct {
	From    mail.Address
	To      []mail.Address
	Cc      []mail.Address
	Bcc     []mail.Address
	ReplyTo []mail.Address
	Subject string
	Text    string
	HTML    string
	// MessageID is without angle brackets.
	MessageID   string
	Date        time.Time
	Attachments []Attachment
}

// Recipients returns the envelope recipients: to, cc and bcc, in that order.
// Bcc addresses never appear in the rendered headers.
func (m Message) Recipients() []string {
	var out []string
	for _, list := range [][]mail.Address{m.To, m.Cc, m.Bcc} {
		for _, a := range list {
			out = append(out, a.Address)
		}
	}
	return out
}

// errHeaderInjection is returned for a header value that contains a line break.
var errHeaderInjection = errors.New("contains a line break or control character (header injection)")

// checkHeaderValue rejects values that could start a new header line.
func checkHeaderValue(what, v string) error {
	if strings.ContainsAny(v, "\r\n\x00") {
		return fmt.Errorf("%s %w", what, errHeaderInjection)
	}
	return nil
}

// ParseAddresses turns a comma-separated list or a list of strings into
// addresses. Display names are kept; line breaks anywhere are refused.
func ParseAddresses(what string, v any) ([]mail.Address, error) {
	var raw []string
	switch t := v.(type) {
	case nil:
		return nil, nil
	case string:
		raw = []string{t}
	case []string:
		raw = t
	case []any:
		for _, e := range t {
			s, ok := e.(string)
			if !ok {
				return nil, fmt.Errorf("%s: list entries must be strings, got %T", what, e)
			}
			raw = append(raw, s)
		}
	default:
		return nil, fmt.Errorf("%s must be a string or a list of strings, got %T", what, v)
	}
	var out []mail.Address
	for _, r := range raw {
		if err := checkHeaderValue(what, r); err != nil {
			return nil, err
		}
		if strings.TrimSpace(r) == "" {
			continue
		}
		list, err := mail.ParseAddressList(r)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", what, err)
		}
		for _, a := range list {
			out = append(out, *a)
		}
	}
	return out, nil
}

// ParseAddress parses one address; empty input gives the zero Address.
func ParseAddress(what, s string) (mail.Address, error) {
	list, err := ParseAddresses(what, s)
	if err != nil {
		return mail.Address{}, err
	}
	switch len(list) {
	case 0:
		return mail.Address{}, nil
	case 1:
		return list[0], nil
	}
	return mail.Address{}, fmt.Errorf("%s must be a single address", what)
}

func joinAddresses(list []mail.Address) string {
	parts := make([]string, len(list))
	for i := range list {
		parts[i] = list[i].String()
	}
	return strings.Join(parts, ", ")
}

// Build renders the message as RFC 5322 / MIME bytes with CRLF line ends.
// Text and HTML together become multipart/alternative, attachments wrap the
// body in multipart/mixed, and non-ASCII header text uses RFC 2047 encoded words.
func Build(m Message) ([]byte, error) {
	if m.From.Address == "" {
		return nil, errors.New("message has no From address")
	}
	if len(m.To)+len(m.Cc)+len(m.Bcc) == 0 {
		return nil, errors.New("message has no recipients")
	}
	if m.Text == "" && m.HTML == "" {
		return nil, errors.New("message needs a text or an HTML body")
	}
	if err := checkHeaderValue("subject", m.Subject); err != nil {
		return nil, err
	}
	if err := checkHeaderValue("message id", m.MessageID); err != nil {
		return nil, err
	}

	// Messages built by hand (not through ParseAddresses) get the same check.
	for _, list := range [][]mail.Address{{m.From}, m.To, m.Cc, m.Bcc, m.ReplyTo} {
		for _, a := range list {
			if err := checkHeaderValue("address", a.Name+a.Address); err != nil {
				return nil, err
			}
		}
	}

	var hdr bytes.Buffer
	put := func(name, value string) error {
		line := name + ": " + value
		if len(line) > maxHeaderLine {
			return fmt.Errorf("header %s is longer than %d bytes", name, maxHeaderLine)
		}
		hdr.WriteString(line + "\r\n")
		return nil
	}
	fields := []struct{ name, value string }{
		{"Date", m.Date.Format(time.RFC1123Z)},
		{"From", m.From.String()},
		{"To", joinAddresses(m.To)},
		{"Cc", joinAddresses(m.Cc)},
		{"Reply-To", joinAddresses(m.ReplyTo)},
		{"Subject", mime.QEncoding.Encode("utf-8", m.Subject)},
		{"Message-ID", "<" + m.MessageID + ">"},
		{"MIME-Version", "1.0"},
	}
	for _, f := range fields {
		if f.value == "" || (f.name == "Message-ID" && m.MessageID == "") {
			continue
		}
		if err := put(f.name, f.value); err != nil {
			return nil, err
		}
	}

	contentType, cte, body, err := bodyOf(m)
	if err != nil {
		return nil, err
	}
	if len(m.Attachments) > 0 {
		contentType, body, err = withAttachments(m.Attachments, contentType, cte, body)
		cte = ""
		if err != nil {
			return nil, err
		}
	}
	if err := put("Content-Type", contentType); err != nil {
		return nil, err
	}
	if cte != "" {
		if err := put("Content-Transfer-Encoding", cte); err != nil {
			return nil, err
		}
	}
	hdr.WriteString("\r\n")
	hdr.Write(body)
	return hdr.Bytes(), nil
}

// bodyOf renders the text and HTML bodies: one part, or multipart/alternative.
func bodyOf(m Message) (contentType, cte string, body []byte, err error) {
	switch {
	case m.HTML == "":
		return "text/plain; charset=utf-8", "quoted-printable", qp(m.Text), nil
	case m.Text == "":
		return "text/html; charset=utf-8", "quoted-printable", qp(m.HTML), nil
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	// Alternatives run from the simplest to the preferred one (RFC 2046).
	for _, p := range []struct{ ct, text string }{{"text/plain", m.Text}, {"text/html", m.HTML}} {
		pw, err := w.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {p.ct + "; charset=utf-8"},
			"Content-Transfer-Encoding": {"quoted-printable"},
		})
		if err != nil {
			return "", "", nil, fmt.Errorf("creating the %s part: %w", p.ct, err)
		}
		if _, err := pw.Write(qp(p.text)); err != nil {
			return "", "", nil, fmt.Errorf("writing the %s part: %w", p.ct, err)
		}
	}
	if err := w.Close(); err != nil {
		return "", "", nil, fmt.Errorf("closing the alternative parts: %w", err)
	}
	return "multipart/alternative; boundary=" + w.Boundary(), "", buf.Bytes(), nil
}

// withAttachments wraps an already rendered body in multipart/mixed.
func withAttachments(atts []Attachment, ct, cte string, body []byte) (contentType string, out []byte, err error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	first := textproto.MIMEHeader{"Content-Type": {ct}}
	if cte != "" {
		first.Set("Content-Transfer-Encoding", cte)
	}
	if err := writePart(w, first, body); err != nil {
		return "", nil, err
	}
	for _, a := range atts {
		h, err := attachmentHeader(a)
		if err != nil {
			return "", nil, err
		}
		if err := writePart(w, h, wrapBase64(a.Data)); err != nil {
			return "", nil, err
		}
	}
	if err := w.Close(); err != nil {
		return "", nil, fmt.Errorf("closing the message parts: %w", err)
	}
	return "multipart/mixed; boundary=" + w.Boundary(), buf.Bytes(), nil
}

func writePart(w *multipart.Writer, h textproto.MIMEHeader, body []byte) error {
	pw, err := w.CreatePart(h)
	if err != nil {
		return fmt.Errorf("creating a message part: %w", err)
	}
	if _, err := pw.Write(body); err != nil {
		return fmt.Errorf("writing a message part: %w", err)
	}
	return nil
}

func attachmentHeader(a Attachment) (textproto.MIMEHeader, error) {
	if a.Name == "" {
		return nil, errors.New("attachment has no file name")
	}
	if err := checkHeaderValue("attachment name", a.Name); err != nil {
		return nil, err
	}
	typ := a.MIME
	if typ == "" {
		typ = "application/octet-stream"
	}
	if err := checkHeaderValue("attachment type", typ); err != nil {
		return nil, err
	}
	media, params, err := mime.ParseMediaType(typ)
	if err != nil {
		return nil, fmt.Errorf("attachment %q has an invalid type %q: %w", a.Name, typ, err)
	}
	params["name"] = a.Name
	ct := mime.FormatMediaType(media, params)
	disp := mime.FormatMediaType("attachment", map[string]string{"filename": a.Name})
	if ct == "" || disp == "" {
		return nil, fmt.Errorf("attachment %q cannot be named in a header", a.Name)
	}
	return textproto.MIMEHeader{
		"Content-Type":              {ct},
		"Content-Disposition":       {disp},
		"Content-Transfer-Encoding": {"base64"},
	}, nil
}

func qp(s string) []byte {
	var buf bytes.Buffer
	w := quotedprintable.NewWriter(&buf)
	_, _ = io.WriteString(w, s) // a bytes.Buffer does not fail
	_ = w.Close()
	return buf.Bytes()
}

func wrapBase64(data []byte) []byte {
	enc := base64.StdEncoding.EncodeToString(data)
	var buf bytes.Buffer
	for len(enc) > base64LineLen {
		buf.WriteString(enc[:base64LineLen] + "\r\n")
		enc = enc[base64LineLen:]
	}
	buf.WriteString(enc)
	return buf.Bytes()
}
