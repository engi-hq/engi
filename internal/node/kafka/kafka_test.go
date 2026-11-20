package kafka

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

var testLimits = TriggerLimits{BatchMaxMessages: 500, BatchMaxWait: 10 * time.Second}

func TestDescriptorsAreValid(t *testing.T) {
	for _, n := range []nodesdk.Node{NewTrigger(nil, testLimits), NewProducer(nil, ProduceLimits{})} {
		if err := n.Describe().Validate(); err != nil {
			t.Errorf("%s: %v", n.Describe().Type, err)
		}
	}
}

func TestParseSettings(t *testing.T) {
	tests := []struct {
		name    string
		params  map[string]any
		wantErr string
		check   func(*testing.T, settings)
	}{
		{name: "defaults", params: map[string]any{"topics": "a, b"}, check: func(t *testing.T, s settings) {
			if !reflect.DeepEqual(s.topics, []string{"a", "b"}) || s.group != "engi-wf1-My-Kafka" || s.startOffset != offsetLatest ||
				s.mode != modePerMessage || s.format != formatJSON {
				t.Errorf("settings = %+v", s)
			}
		}},
		{name: "topics as list and explicit group", params: map[string]any{"topics": []any{"a"}, "groupId": "g1", "startOffset": "earliest"},
			check: func(t *testing.T, s settings) {
				if s.group != "g1" || s.startOffset != offsetEarliest || len(s.topics) != 1 {
					t.Errorf("settings = %+v", s)
				}
			}},
		{name: "batch values", params: map[string]any{"topics": "a", "mode": "batch", "batchSize": 20.0, "batchWaitMs": 250.0},
			check: func(t *testing.T, s settings) {
				if s.batchSize != 20 || s.batchWait != 250*time.Millisecond {
					t.Errorf("settings = %+v", s)
				}
			}},
		{name: "batch defaults", params: map[string]any{"topics": "a", "mode": "batch"}, check: func(t *testing.T, s settings) {
			if s.batchSize != defaultBatchSize || s.batchWait != defaultBatchWaitMs*time.Millisecond {
				t.Errorf("settings = %+v", s)
			}
		}},
		{name: "no topics", params: map[string]any{}, wantErr: "topics"},
		{name: "bad mode", params: map[string]any{"topics": "a", "mode": "x"}, wantErr: "mode"},
		{name: "bad offset", params: map[string]any{"topics": "a", "startOffset": "x"}, wantErr: "startOffset"},
		{name: "bad format", params: map[string]any{"topics": "a", "valueFormat": "avro"}, wantErr: "valueFormat"},
		{name: "batch size over cap", params: map[string]any{"topics": "a", "mode": "batch", "batchSize": 501.0}, wantErr: "batchSize"},
		{name: "batch wait over cap", params: map[string]any{"topics": "a", "mode": "batch", "batchWaitMs": 10001.0}, wantErr: "batchWaitMs"},
		{name: "batch size zero", params: map[string]any{"topics": "a", "mode": "batch", "batchSize": 0.0}, wantErr: "batchSize"},
		{name: "dead letter equals source", params: map[string]any{"topics": "a", "deadLetterTopic": "a"}, wantErr: "deadLetterTopic"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, err := parseSettings(tc.params, testLimits, "wf1", "My Kafka")
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, s)
		})
	}
}

func TestDecodeValue(t *testing.T) {
	if v, err := decodeValue(formatJSON, []byte(`{"a":[1,"x"]}`)); err != nil || !reflect.DeepEqual(v, map[string]any{"a": []any{1.0, "x"}}) {
		t.Errorf("json = %v, %v", v, err)
	}
	if v, err := decodeValue(formatJSON, nil); err != nil || v != nil {
		t.Errorf("empty json = %v, %v", v, err)
	}
	if _, err := decodeValue(formatJSON, []byte(`{nope`)); err == nil {
		t.Error("invalid JSON must fail")
	}
	if v, err := decodeValue(formatText, []byte(`{nope`)); err != nil || v != "{nope" {
		t.Errorf("text = %v, %v", v, err)
	}
}

func TestItemMapping(t *testing.T) {
	ts := time.Date(2026, 1, 2, 3, 4, 5, 6000000, time.UTC)
	m := message{Topic: "orders", Partition: 2, Offset: 41, Timestamp: ts, Key: []byte("k1"), Value: []byte(`{"id":1}`),
		Headers: []header{{"trace", []byte("t1")}, {"tag", []byte("a")}, {"tag", []byte("b")}, {"tag", []byte("c")}}}
	v, _ := decodeValue(formatJSON, m.Value)
	got := m.item(v, nil).JSON
	want := map[string]any{
		"topic": "orders", "partition": 2.0, "offset": 41.0, "timestamp": "2026-01-02T03:04:05.006Z", "key": "k1",
		"value": map[string]any{"id": 1.0}, "headers": map[string]any{"trace": "t1", "tag": []any{"a", "b", "c"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("item = %#v\nwant %#v", got, want)
	}
	m.Key = nil
	if k := m.item(nil, nil).JSON["key"]; k != nil {
		t.Errorf("missing key = %v, want nil", k)
	}
	bad := m.item(nil, errors.New("boom")).JSON
	if bad["value"] != `{"id":1}` || bad["valueError"] != "boom" {
		t.Errorf("undecodable item = %v", bad)
	}
}

func msgs(topic string, partition int32, from, n int64) []message {
	var out []message
	for i := range n {
		out = append(out, message{Topic: topic, Partition: partition, Offset: from + i})
	}
	return out
}

func TestDedupKeys(t *testing.T) {
	if k := (message{Topic: "t", Partition: 3, Offset: 9}).dedupKey(); k != "t:3:9" {
		t.Errorf("message key = %q", k)
	}
	b := batch{partitionKey: partitionKey{"t", 1}, msgs: msgs("t", 1, 10, 5)}
	if k := b.dedupKey(); k != "t:1:10-14" {
		t.Errorf("batch key = %q", k)
	}
	// A redelivered batch that ends elsewhere gets another key, so no message is swallowed.
	if b2 := (batch{partitionKey: b.partitionKey, msgs: msgs("t", 1, 10, 8)}); b2.dedupKey() == b.dedupKey() {
		t.Error("different ranges must not share a key")
	}
}

func TestBatcherFlushesBySizeAndByAge(t *testing.T) {
	t0 := time.Unix(1000, 0)
	b := newBatcher(3, time.Second)
	if !b.empty() {
		t.Fatal("new batcher must be empty")
	}
	if _, ok := b.nextDue(); ok {
		t.Fatal("empty batcher has no deadline")
	}
	var full []batch
	for i, m := range append(msgs("t", 0, 0, 3), msgs("t", 1, 100, 2)...) {
		if fb, ok := b.add(m, t0.Add(time.Duration(i)*time.Millisecond)); ok {
			full = append(full, fb)
		}
	}
	if len(full) != 1 || full[0].partition != 0 || len(full[0].msgs) != 3 || full[0].dedupKey() != "t:0:0-2" {
		t.Fatalf("full batches = %+v", full)
	}
	at, ok := b.nextDue()
	if !ok || !at.Equal(t0.Add(3*time.Millisecond).Add(time.Second)) {
		t.Errorf("nextDue = %v, %v", at, ok)
	}
	if d := b.due(t0.Add(500 * time.Millisecond)); len(d) != 0 {
		t.Errorf("batch flushed before max wait: %+v", d)
	}
	d := b.due(t0.Add(2 * time.Second))
	if len(d) != 1 || d[0].partition != 1 || len(d[0].msgs) != 2 || d[0].dedupKey() != "t:1:100-101" {
		t.Fatalf("due batches = %+v", d)
	}
	if !b.empty() {
		t.Error("batcher must be empty after flush")
	}
}

func TestPollWaitFollowsOpenBatches(t *testing.T) {
	now := time.Unix(1000, 0)
	c := &consumer{lim: TriggerLimits{PollInterval: 2 * time.Second}, now: func() time.Time { return now }}
	if w := c.pollWait(nil); w != 2*time.Second {
		t.Errorf("no batcher: wait = %v", w)
	}
	b := newBatcher(10, 500*time.Millisecond)
	b.add(message{Topic: "t"}, now)
	if w := c.pollWait(b); w != 500*time.Millisecond {
		t.Errorf("open batch: wait = %v", w)
	}
	now = now.Add(time.Hour)
	if w := c.pollWait(b); w != time.Millisecond {
		t.Errorf("overdue batch: wait = %v, want the 1ms floor", w)
	}
}

func TestProducerSettings(t *testing.T) {
	n := NewProducer(nil, ProduceLimits{})
	read := func(params map[string]any) (producerSettings, error) {
		base := map[string]any{"acks": "all", "compression": "none", "idempotent": true}
		for k, v := range params {
			base[k] = v
		}
		return readProducerSettings(context.Background(), nodetest.New(n.Describe(), nodetest.Items(map[string]any{}), base))
	}
	s, err := read(nil)
	if err != nil || s.variant() != "acks=all,compression=none,idempotent=true" {
		t.Errorf("defaults = %+v, %v", s, err)
	}
	if _, err := read(map[string]any{"acks": "leader"}); err == nil || !strings.Contains(err.Error(), "idempotent") {
		t.Errorf("idempotent with acks leader: %v", err)
	}
	if s, err := read(map[string]any{"acks": "none", "idempotent": false, "compression": "zstd"}); err != nil || s.compression != compZstd {
		t.Errorf("acks none = %+v, %v", s, err)
	}
	if _, err := read(map[string]any{"compression": "rar"}); err == nil {
		t.Error("unknown compression must fail")
	}
	if _, err := read(map[string]any{"acks": "some"}); err == nil {
		t.Error("unknown acks must fail")
	}
	if got := len(producerSettings{acks: acksAll, compression: compGzip, idempotent: false}.options()); got != 3 {
		t.Errorf("options = %d, want acks + compression + disable idempotence", got)
	}
}

func TestProduceRecordBuilding(t *testing.T) {
	p := NewProducer(nil, ProduceLimits{MaxRecordBytes: 64})
	rec := func(params map[string]any) (*kgo.Record, error) {
		return p.record(context.Background(), nodetest.New(p.Describe(), nodetest.Items(map[string]any{}), params), 0)
	}
	r, err := rec(map[string]any{"topic": "t", "key": "k", "format": "json", "value": map[string]any{"a": 1.0},
		"headers": map[string]any{"b": "2", "a": 1.0}})
	if err != nil {
		t.Fatal(err)
	}
	if string(r.Value) != `{"a":1}` || string(r.Key) != "k" || len(r.Headers) != 2 || r.Headers[0].Key != "a" || string(r.Headers[0].Value) != "1" {
		t.Errorf("record = %+v", r)
	}
	if r, err = rec(map[string]any{"topic": "t", "format": "text", "value": "plain"}); err != nil || string(r.Value) != "plain" || r.Key != nil {
		t.Errorf("text record = %+v, %v", r, err)
	}
	if r, err = rec(map[string]any{"topic": "t", "format": "json", "value": "s"}); err != nil || string(r.Value) != `"s"` {
		t.Errorf("json string = %+v, %v", r, err)
	}
	if _, err = rec(map[string]any{"topic": "", "value": "x"}); err == nil {
		t.Error("empty topic must fail")
	}
	if _, err = rec(map[string]any{"topic": "t", "format": "text", "value": strings.Repeat("x", 65)}); err == nil || !strings.Contains(err.Error(), "maxRecordBytes") {
		t.Errorf("oversized record: %v", err)
	}
	if _, err = rec(map[string]any{"topic": "t", "value": "x", "headers": 5.0}); err == nil {
		t.Error("non-object headers must fail")
	}
}

func TestExecuteWithoutClientsIsUnavailable(t *testing.T) {
	p := NewProducer(nil, ProduceLimits{})
	in := nodetest.New(p.Describe(), nodetest.Items(map[string]any{}), map[string]any{"topic": "t"})
	if _, err := p.Execute(context.Background(), in); !errors.Is(err, nodesdk.ErrUnavailable) {
		t.Fatalf("producer: %v", err)
	}
	tr := NewTrigger(nil, testLimits)
	err := tr.Listen(context.Background(), nodesdk.ListenInput{Params: map[string]any{"topics": "a"}}, nil)
	if !errors.Is(err, nodesdk.ErrUnavailable) {
		t.Fatalf("trigger: %v", err)
	}
}
