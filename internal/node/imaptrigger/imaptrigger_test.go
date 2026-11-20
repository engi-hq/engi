package imaptrigger

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPlan(t *testing.T) {
	tests := []struct {
		name         string
		cur          *cursor
		validity     uint32
		uidNext      uint32
		found        []uint32
		limit        int
		emitExisting bool
		wantEmit     []uint32
		wantNext     cursor
	}{
		{"first poll only sets the cursor", nil, 7, 11, []uint32{3, 10}, 50, false, nil, cursor{7, 10}},
		{"first poll can emit existing", nil, 7, 11, []uint32{3, 10}, 50, true, []uint32{3, 10}, cursor{7, 10}},
		{"emits only UIDs above the cursor", &cursor{7, 5}, 7, 9, []uint32{9 - 1, 6, 5, 4}, 50, false, []uint32{6, 8}, cursor{7, 8}},
		{"nothing new keeps the cursor", &cursor{7, 5}, 7, 6, nil, 50, false, nil, cursor{7, 5}},
		{"cursor skips filtered-out mail", &cursor{7, 5}, 7, 20, []uint32{8}, 50, false, []uint32{8}, cursor{7, 19}},
		{"limit leaves the rest for the next poll", &cursor{7, 0}, 7, 10, []uint32{4, 2, 3, 9}, 2, false, []uint32{2, 3}, cursor{7, 3}},
		{"UIDVALIDITY change resets", &cursor{6, 50}, 7, 4, []uint32{1, 2}, 50, false, nil, cursor{7, 3}},
		{"UIDVALIDITY change with emitExisting starts over", &cursor{6, 50}, 7, 4, []uint32{1, 2}, 50, true, []uint32{1, 2}, cursor{7, 3}},
		{"duplicates are emitted once", &cursor{7, 0}, 7, 5, []uint32{2, 2, 3}, 50, false, []uint32{2, 3}, cursor{7, 4}},
		{"empty mailbox", nil, 7, 1, nil, 50, false, nil, cursor{7, 0}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			emit, next := plan(tc.cur, tc.validity, tc.uidNext, tc.found, tc.limit, tc.emitExisting)
			if !reflect.DeepEqual(emit, tc.wantEmit) {
				t.Errorf("emit = %v, want %v", emit, tc.wantEmit)
			}
			if next != tc.wantNext {
				t.Errorf("next = %+v, want %+v", next, tc.wantNext)
			}
		})
	}
}

func TestCursorRoundTrip(t *testing.T) {
	c := cursor{UIDValidity: 3, LastUID: 42}
	got, err := decodeCursor(c.encode())
	if err != nil || got == nil || *got != c {
		t.Fatalf("round trip = %+v, %v", got, err)
	}
	if got, err := decodeCursor(nil); got != nil || err != nil {
		t.Errorf("empty cursor = %+v, %v", got, err)
	}
	if _, err := decodeCursor([]byte("{")); err == nil {
		t.Error("corrupt cursor accepted")
	}
	if dedupKey(3, 42) != "3:42" {
		t.Errorf("dedupKey = %q", dedupKey(3, 42))
	}
}

func TestFilterMatches(t *testing.T) {
	tests := []struct {
		name          string
		f             filters
		from, subject string
		seen          bool
		want          bool
	}{
		{"no filters", filters{}, "A <a@x.io>", "Hi", true, true},
		{"unseen only drops read mail", filters{unseenOnly: true}, "a@x.io", "Hi", true, false},
		{"unseen only keeps unread", filters{unseenOnly: true}, "a@x.io", "Hi", false, true},
		{"from matches name", filters{from: "alice"}, "Alice <a@x.io>", "Hi", false, true},
		{"from matches address case-insensitively", filters{from: "BOSS@"}, "b@x.io, boss@corp.io", "Hi", false, true},
		{"from mismatch", filters{from: "alice"}, "Bob <b@x.io>", "Hi", false, false},
		{"subject contains", filters{subject: "invoice"}, "a@x.io", "Your INVOICE #4", false, true},
		{"subject mismatch", filters{subject: "invoice"}, "a@x.io", "Receipt", false, false},
		{"all must match", filters{unseenOnly: true, from: "a@", subject: "x"}, "a@x.io", "y", false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.f.matches(tc.from, tc.subject, tc.seen); got != tc.want {
				t.Errorf("matches = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCriteria(t *testing.T) {
	c := filters{unseenOnly: true, from: "bob", subject: "hi"}.criteria(10)
	if len(c.UID) != 1 || c.UID[0].String() != "11:*" {
		t.Errorf("uid range = %v", c.UID)
	}
	if len(c.NotFlag) != 1 || len(c.Header) != 2 {
		t.Errorf("criteria = %+v", c)
	}
	if c := (filters{}).criteria(0); len(c.NotFlag) != 0 || len(c.Header) != 0 {
		t.Errorf("empty filters added criteria: %+v", c)
	}
}

const sample = "From: =?utf-8?q?J=C3=BCrgen?= <j@example.com>\r\n" +
	"To: a@example.com, \"Doe, Jane\" <jane@example.com>\r\n" +
	"Subject: =?utf-8?q?Gr=C3=BC=C3=9Fe?=\r\n" +
	"Date: Fri, 02 Jan 2026 03:04:05 +0000\r\n" +
	"Message-ID: <m1@example.com>\r\n" +
	"Received: a\r\nReceived: b\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/mixed; boundary=XX\r\n\r\n" +
	"--XX\r\nContent-Type: multipart/alternative; boundary=YY\r\n\r\n" +
	"--YY\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\ncaf=C3=A9\r\n" +
	"--YY\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p>caf&eacute;</p>\r\n--YY--\r\n" +
	"--XX\r\nContent-Type: application/pdf; name=a.pdf\r\nContent-Disposition: attachment; filename=a.pdf\r\nContent-Transfer-Encoding: base64\r\n\r\nQUJD\r\n" +
	"--XX--\r\n"

func TestParseMessage(t *testing.T) {
	p, err := parseMessage([]byte(sample), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if p.subject != "Grüße" || !strings.Contains(p.from, "Jürgen") {
		t.Errorf("subject %q from %q", p.subject, p.from)
	}
	if p.item["text"] != "café" || !strings.Contains(p.item["html"].(string), "<p>") {
		t.Errorf("text %q html %q", p.item["text"], p.item["html"])
	}
	if p.item["date"] != time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC).Format(time.RFC3339) {
		t.Errorf("date = %v", p.item["date"])
	}
	if p.item["messageId"] != "m1@example.com" {
		t.Errorf("messageId = %v", p.item["messageId"])
	}
	to := p.item["to"].([]any)
	if len(to) != 2 || to[1].(map[string]any)["name"] != "Doe, Jane" {
		t.Errorf("to = %v", to)
	}
	atts := p.item["attachments"].([]any)
	if len(atts) != 1 || atts[0].(map[string]any)["filename"] != "a.pdf" || atts[0].(map[string]any)["size"] != float64(3) {
		t.Errorf("attachments = %v", atts)
	}
	if h := p.item["headers"].(map[string]any); len(h["received"].([]any)) != 2 || h["message-id"] == nil {
		t.Errorf("headers = %v", h)
	}
}

func TestParseMessageCapsBody(t *testing.T) {
	raw := "From: a@x.io\r\nSubject: s\r\nContent-Type: text/plain\r\n\r\n" + strings.Repeat("x", 100)
	p, err := parseMessage([]byte(raw), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.item["text"].(string)) != 10 || !p.truncated {
		t.Errorf("text len %d truncated %v", len(p.item["text"].(string)), p.truncated)
	}
}

func TestParseSettings(t *testing.T) {
	s, err := parseSettings(nil)
	if err != nil || s.mailbox != "INBOX" || s.interval != time.Minute || !s.filters.unseenOnly || s.after != afterNothing {
		t.Fatalf("defaults = %+v, %v", s, err)
	}
	for name, p := range map[string]map[string]any{
		"zero interval":    {"intervalSeconds": float64(0)},
		"move needs dest":  {"afterProcessing": "move"},
		"move to itself":   {"afterProcessing": "move", "moveTo": "INBOX"},
		"unknown action":   {"afterProcessing": "delete"},
		"newline in query": {"subject": "a\r\nb"},
	} {
		if _, err := parseSettings(p); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	s, err = parseSettings(map[string]any{"intervalSeconds": float64(30), "unseenOnly": false, "afterProcessing": "move", "moveTo": "Done"})
	if err != nil || s.interval != 30*time.Second || s.filters.unseenOnly || s.moveTo != "Done" {
		t.Errorf("custom = %+v, %v", s, err)
	}
}
