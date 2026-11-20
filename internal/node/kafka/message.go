package kafka

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/engi-hq/engi/pkg/nodesdk"
)

// header is one Kafka record header.
type header struct {
	Key   string
	Value []byte
}

// message is a consumed record, free of client types so the mapping and the
// mode logic can be tested without a broker.
type message struct {
	Topic     string
	Partition int32
	Offset    int64
	Timestamp time.Time
	Key       []byte
	Value     []byte
	Headers   []header
}

// decodeValue turns the record value into item data: parsed JSON, or text.
func decodeValue(format string, value []byte) (any, error) {
	if format == formatText {
		return string(value), nil
	}
	if len(value) == 0 {
		return nil, nil
	}
	var v any
	if err := json.Unmarshal(value, &v); err != nil {
		return nil, fmt.Errorf("value is not valid JSON: %w", err)
	}
	return v, nil
}

// item maps a message to a trigger item. value is the decoded value; a message
// whose value failed to decode keeps its raw text and carries valueError.
func (m message) item(value any, decodeErr error) nodesdk.Item {
	var key any
	if m.Key != nil {
		key = string(m.Key)
	}
	data := map[string]any{
		"topic": m.Topic, "partition": float64(m.Partition), "offset": float64(m.Offset),
		"timestamp": m.Timestamp.UTC().Format(time.RFC3339Nano),
		"key":       key, "value": value, "headers": headerMap(m.Headers),
	}
	if decodeErr != nil {
		data["value"] = string(m.Value)
		data["valueError"] = decodeErr.Error()
	}
	return nodesdk.Item{JSON: data}
}

// headerMap collects headers by name; a repeated name becomes a list.
func headerMap(hs []header) map[string]any {
	out := make(map[string]any, len(hs))
	for _, h := range hs {
		v := string(h.Value)
		switch prev := out[h.Key].(type) {
		case nil:
			out[h.Key] = v
		case string:
			out[h.Key] = []any{prev, v}
		case []any:
			out[h.Key] = append(prev, v)
		}
	}
	return out
}

// dedupKey names a single message.
func (m message) dedupKey() string {
	return m.Topic + ":" + strconv.Itoa(int(m.Partition)) + ":" + strconv.FormatInt(m.Offset, 10)
}

// partitionKey identifies a topic partition.
type partitionKey struct {
	topic     string
	partition int32
}

// batch is a run of messages of one partition that becomes one execution.
type batch struct {
	partitionKey
	msgs []message
}

// dedupKey names the batch by its offset range. The range, not only the first
// offset, because a batch redelivered after a crash can end elsewhere than the
// first attempt did; a key on the first offset alone would then swallow the
// extra messages.
func (b batch) dedupKey() string {
	first, last := b.msgs[0].Offset, b.msgs[len(b.msgs)-1].Offset
	return b.topic + ":" + strconv.Itoa(int(b.partition)) + ":" + strconv.FormatInt(first, 10) + "-" + strconv.FormatInt(last, 10)
}

// batcher groups messages per partition until a batch holds max messages or its
// oldest message has waited wait. It is not safe for concurrent use.
type batcher struct {
	max  int
	wait time.Duration
	bufs map[partitionKey]*pending
}

type pending struct {
	since time.Time
	msgs  []message
}

func newBatcher(size int, wait time.Duration) *batcher {
	return &batcher{max: size, wait: wait, bufs: map[partitionKey]*pending{}}
}

// add buffers m and returns its batch when that is now full.
func (b *batcher) add(m message, now time.Time) (batch, bool) {
	k := partitionKey{m.Topic, m.Partition}
	p := b.bufs[k]
	if p == nil {
		p = &pending{since: now}
		b.bufs[k] = p
	}
	p.msgs = append(p.msgs, m)
	if len(p.msgs) >= b.max {
		delete(b.bufs, k)
		return batch{partitionKey: k, msgs: p.msgs}, true
	}
	return batch{}, false
}

// due removes and returns the batches whose oldest message waited long enough.
func (b *batcher) due(now time.Time) []batch {
	var out []batch
	for k, p := range b.bufs {
		if now.Sub(p.since) >= b.wait {
			out = append(out, batch{partitionKey: k, msgs: p.msgs})
			delete(b.bufs, k)
		}
	}
	return out
}

// nextDue is when the oldest open batch expires; ok is false with no open batch.
func (b *batcher) nextDue() (at time.Time, ok bool) {
	for _, p := range b.bufs {
		if t := p.since.Add(b.wait); !ok || t.Before(at) {
			at, ok = t, true
		}
	}
	return at, ok
}

func (b *batcher) empty() bool { return len(b.bufs) == 0 }
