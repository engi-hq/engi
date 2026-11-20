package imaptrigger

import (
	"encoding/json"
	"fmt"
	"slices"
)

// cursor is the poll state kept in TriggerState: the highest UID already
// handed to the engine, valid only for one UIDVALIDITY of the mailbox.
type cursor struct {
	UIDValidity uint32 `json:"uidValidity"`
	LastUID     uint32 `json:"lastUid"`
}

func decodeCursor(b []byte) (*cursor, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var c cursor
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("decoding the poll cursor: %w", err)
	}
	return &c, nil
}

func (c cursor) encode() []byte {
	b, _ := json.Marshal(c) // two integers cannot fail to marshal
	return b
}

// plan decides which of the matching UIDs to emit and where the cursor moves.
//
//   - No cursor, or a changed UIDVALIDITY (the old UIDs are meaningless): the
//     cursor jumps to the newest UID without emitting, unless emitExisting asks
//     for everything already in the mailbox.
//   - Otherwise the UIDs above the cursor are emitted in ascending order, at
//     most limit of them; the rest follow on the next polls.
//   - When all of them fit, the cursor moves to uidNext-1, so messages that the
//     filters skipped are not searched again.
//
// found is what the server returned for the filters; uidNext is the mailbox's
// next UID at SELECT time.
func plan(cur *cursor, validity, uidNext uint32, found []uint32, limit int, emitExisting bool) (emit []uint32, next cursor) {
	newest := uidNext - 1
	if uidNext == 0 {
		newest = 0
	}
	if cur == nil || cur.UIDValidity != validity {
		if !emitExisting {
			return nil, cursor{UIDValidity: validity, LastUID: newest}
		}
		cur = &cursor{UIDValidity: validity}
	}
	for _, u := range found {
		if u > cur.LastUID {
			emit = append(emit, u)
		}
	}
	slices.Sort(emit)
	emit = slices.Compact(emit)
	next = *cur
	if limit > 0 && len(emit) > limit {
		emit = emit[:limit]
		next.LastUID = emit[len(emit)-1]
		return emit, next
	}
	next.LastUID = max(cur.LastUID, newest)
	if len(emit) > 0 {
		next.LastUID = max(next.LastUID, emit[len(emit)-1])
	}
	return emit, next
}

// dedupKey identifies one message across polls and restarts.
func dedupKey(validity, uid uint32) string { return fmt.Sprintf("%d:%d", validity, uid) }
