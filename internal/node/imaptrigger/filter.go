package imaptrigger

import (
	"strings"

	"github.com/emersion/go-imap/v2"
)

// filters narrow the messages the trigger emits.
type filters struct {
	unseenOnly bool
	// from and subject are case-insensitive substrings.
	from, subject string
}

// criteria is the server-side search for the filters, limited to UIDs above after.
// The server narrows the candidates; matches repeats the test locally because
// IMAP servers differ in how they match header text.
func (f filters) criteria(after uint32) *imap.SearchCriteria {
	var set imap.UIDSet
	set.AddRange(imap.UID(after)+1, 0) // 0 is "*"
	c := &imap.SearchCriteria{UID: []imap.UIDSet{set}}
	if f.unseenOnly {
		c.NotFlag = append(c.NotFlag, imap.FlagSeen)
	}
	if f.from != "" {
		c.Header = append(c.Header, imap.SearchCriteriaHeaderField{Key: "From", Value: f.from})
	}
	if f.subject != "" {
		c.Header = append(c.Header, imap.SearchCriteriaHeaderField{Key: "Subject", Value: f.subject})
	}
	return c
}

// matches reports whether a fetched message passes the filters. fromText is the
// sender as "Name <address>", subject the decoded subject, seen the \Seen flag.
func (f filters) matches(fromText, subject string, seen bool) bool {
	if f.unseenOnly && seen {
		return false
	}
	return containsFold(fromText, f.from) && containsFold(subject, f.subject)
}

func containsFold(s, sub string) bool {
	return sub == "" || strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}
