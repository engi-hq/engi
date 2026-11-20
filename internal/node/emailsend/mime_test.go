package emailsend

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"testing"
	"time"
)

var testDate = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func baseMessage() Message {
	return Message{
		From: mail.Address{Name: "Engi", Address: "noreply@example.com"},
		To:   []mail.Address{{Address: "a@example.com"}}, Subject: "Hello", Text: "plain", MessageID: "id1@example.com", Date: testDate,
	}
}

func parse(t *testing.T, raw []byte) *mail.Message {
	t.Helper()
	m, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("rendered message does not parse: %v\n%s", err, raw)
	}
	return m
}

func TestBuildTextOnly(t *testing.T) {
	raw, err := Build(baseMessage())
	if err != nil {
		t.Fatal(err)
	}
	m := parse(t, raw)
	if got := m.Header.Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if m.Header.Get("Subject") != "Hello" || m.Header.Get("Message-ID") != "<id1@example.com>" {
		t.Errorf("headers = %v", m.Header)
	}
	body, _ := io.ReadAll(quotedprintable.NewReader(m.Body))
	if string(body) != "plain" {
		t.Errorf("body = %q", body)
	}
}

func TestBuildEncodesNonASCIIHeaders(t *testing.T) {
	msg := baseMessage()
	msg.Subject = "Grüße aus Köln ✓"
	msg.From.Name = "Jürgen"
	raw, err := Build(msg)
	if err != nil {
		t.Fatal(err)
	}
	head, _, _ := bytes.Cut(raw, []byte("\r\n\r\n"))
	if bytes.Contains(head, []byte("ü")) {
		t.Error("headers contain raw non-ASCII text")
	}
	m := parse(t, raw)
	dec := new(mime.WordDecoder)
	if got, err := dec.DecodeHeader(m.Header.Get("Subject")); err != nil || got != msg.Subject {
		t.Errorf("subject = %q, %v", got, err)
	}
	from, err := m.Header.AddressList("From")
	if err != nil || from[0].Name != "Jürgen" {
		t.Errorf("from = %v, %v", from, err)
	}
}

func TestBuildAlternativeWithAttachment(t *testing.T) {
	msg := baseMessage()
	msg.HTML = "<p>hi</p>"
	msg.Cc = []mail.Address{{Address: "c@example.com"}}
	msg.Bcc = []mail.Address{{Address: "secret@example.com"}}
	msg.Attachments = []Attachment{{Name: "re port.txt", MIME: "text/plain", Data: []byte(strings.Repeat("x", 200))}}
	raw, err := Build(msg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret@example.com") {
		t.Fatal("Bcc leaked into the message")
	}
	if got := msg.Recipients(); len(got) != 3 || got[2] != "secret@example.com" {
		t.Errorf("recipients = %v", got)
	}
	m := parse(t, raw)
	typ, params, err := mime.ParseMediaType(m.Header.Get("Content-Type"))
	if err != nil || typ != "multipart/mixed" {
		t.Fatalf("top type = %q, %v", typ, err)
	}
	mr := multipart.NewReader(m.Body, params["boundary"])
	first, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	altType, altParams, _ := mime.ParseMediaType(first.Header.Get("Content-Type"))
	if altType != "multipart/alternative" {
		t.Fatalf("first part = %q", altType)
	}
	ar := multipart.NewReader(first, altParams["boundary"])
	var kinds []string
	for {
		p, err := ar.NextPart()
		if err != nil {
			break
		}
		kinds = append(kinds, strings.Split(p.Header.Get("Content-Type"), ";")[0])
	}
	if strings.Join(kinds, ",") != "text/plain,text/html" {
		t.Errorf("alternatives = %v", kinds)
	}
	att, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if att.FileName() != "re port.txt" {
		t.Errorf("file name = %q", att.FileName())
	}
	enc, _ := io.ReadAll(att)
	for _, line := range strings.Split(strings.TrimSpace(string(enc)), "\r\n") {
		if len(line) > base64LineLen {
			t.Errorf("base64 line of %d bytes", len(line))
		}
	}
}

func TestBuildRejectsHeaderInjection(t *testing.T) {
	cases := map[string]func(*Message){
		"subject":         func(m *Message) { m.Subject = "Hi\r\nBcc: evil@example.com" },
		"subject lf":      func(m *Message) { m.Subject = "Hi\nX-Evil: 1" },
		"message id":      func(m *Message) { m.MessageID = "a>\r\nX: y" },
		"from name":       func(m *Message) { m.From.Name = "A\r\nBcc: x@y.z" },
		"to address":      func(m *Message) { m.To[0].Address = "a@b.c\r\nBcc: x@y.z" },
		"attachment name": func(m *Message) { m.Attachments = []Attachment{{Name: "a\r\nX: y", Data: []byte("d")}} },
		"attachment type": func(m *Message) {
			m.Attachments = []Attachment{{Name: "a", MIME: "text/plain\r\nX: y", Data: []byte("d")}}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m := baseMessage()
			mutate(&m)
			if _, err := Build(m); err == nil {
				t.Fatal("Build accepted a header injection")
			}
		})
	}
}

func TestBuildRequiresContent(t *testing.T) {
	m := baseMessage()
	m.Text = ""
	if _, err := Build(m); err == nil {
		t.Error("message without a body was accepted")
	}
	m = baseMessage()
	m.To = nil
	if _, err := Build(m); err == nil {
		t.Error("message without recipients was accepted")
	}
}

func TestParseAddresses(t *testing.T) {
	tests := []struct {
		name    string
		in      any
		want    []string
		wantErr bool
	}{
		{"nil", nil, nil, false},
		{"comma list", "a@x.io, B <b@x.io>", []string{"a@x.io", "b@x.io"}, false},
		{"quoted comma", `"Doe, Jane" <j@x.io>`, []string{"j@x.io"}, false},
		{"array", []any{"a@x.io", "b@x.io"}, []string{"a@x.io", "b@x.io"}, false},
		{"blank entries", []any{" ", ""}, nil, false},
		{"invalid", "not an address", nil, true},
		{"injection in string", "a@x.io\r\nBcc: e@x.io", nil, true},
		{"injection in array", []any{"a@x.io", "b@x.io\nBcc: e@x.io"}, nil, true},
		{"wrong type", 5, nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseAddresses("to", tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i].Address != tc.want[i] {
					t.Errorf("address %d = %q, want %q", i, got[i].Address, tc.want[i])
				}
			}
		})
	}
}
