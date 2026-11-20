//go:build integration

package kafka_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/redpanda"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/engi-hq/engi/internal/adapter/kafkapool"
	credkafka "github.com/engi-hq/engi/internal/credtype/kafka"
	kafkanode "github.com/engi-hq/engi/internal/node/kafka"
	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

const redpandaImage = "docker.redpanda.com/redpandadata/redpanda:v24.3.1"

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

var testLimits = kafkanode.TriggerLimits{
	BatchMaxMessages: 500, BatchMaxWait: 10 * time.Second, SessionTimeout: 10 * time.Second,
	RebalanceTimeout: 20 * time.Second, MaxPollRecords: 100, PollInterval: 100 * time.Millisecond,
}

// rig is one Redpanda broker with a connection pool.
type rig struct {
	broker string
	pool   *kafkapool.Manager
	cred   nodesdk.Credential
	admin  *kadm.Client
	raw    *kgo.Client
}

func newRig(t *testing.T) *rig {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	c, err := redpanda.Run(ctx, redpandaImage)
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("starting redpanda: %v", err)
	}
	broker, err := c.KafkaSeedBroker(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pool := kafkapool.New(kafkapool.Config{ConnectTimeout: 10 * time.Second, FlushTimeout: 10 * time.Second}, quiet)
	t.Cleanup(func() { _ = pool.Close() })
	raw, err := kgo.NewClient(kgo.SeedBrokers(broker))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(raw.Close)
	return &rig{
		broker: broker, pool: pool, raw: raw, admin: kadm.NewClient(raw),
		cred: nodesdk.Credential{Type: credkafka.Name, ID: "cred1", Version: 1, Data: map[string]any{"brokers": broker}},
	}
}

func (r *rig) topic(t *testing.T, name string, partitions int32) {
	t.Helper()
	if _, err := r.admin.CreateTopic(context.Background(), partitions, 1, nil, name); err != nil {
		t.Fatalf("creating topic %s: %v", name, err)
	}
}

func (r *rig) produce(t *testing.T, recs ...*kgo.Record) {
	t.Helper()
	if err := r.raw.ProduceSync(context.Background(), recs...).FirstErr(); err != nil {
		t.Fatalf("producing: %v", err)
	}
}

func rec(topic, key, value string) *kgo.Record {
	return &kgo.Record{Topic: topic, Key: []byte(key), Value: []byte(value)}
}

// executions mimics execution.Service.Start: one execution per dedup key.
type executions struct {
	mu       sync.Mutex
	byKey    map[string][]nodesdk.Item
	attempts int
	failOnce map[string]bool
	order    []string
}

func newExecutions() *executions {
	return &executions{byKey: map[string][]nodesdk.Item{}, failOnce: map[string]bool{}}
}

func (e *executions) emit(_ context.Context, ev nodesdk.ListenEvent) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.attempts++
	if e.failOnce[ev.DedupKey] {
		delete(e.failOnce, ev.DedupKey)
		return fmt.Errorf("simulated crash before the execution was stored")
	}
	if _, dup := e.byKey[ev.DedupKey]; dup {
		return nil
	}
	e.byKey[ev.DedupKey] = ev.Items
	e.order = append(e.order, ev.DedupKey)
	return nil
}

func (e *executions) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.byKey)
}

func (e *executions) wait(t *testing.T, want int) {
	t.Helper()
	waitFor(t, fmt.Sprintf("%d executions", want), func() bool { return e.count() >= want })
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// listener is a running Kafka trigger.
type listener struct {
	cancel context.CancelFunc
	done   chan error
}

func (r *rig) listen(t *testing.T, params map[string]any, emit func(context.Context, nodesdk.ListenEvent) error, over func(context.Context) bool) *listener {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	l := &listener{cancel: cancel, done: make(chan error, 1)}
	node := kafkanode.NewTrigger(r.pool, testLimits)
	in := nodesdk.ListenInput{
		WorkflowID: "wf1", NodeName: "Kafka", Params: params, Logger: quiet, Overloaded: over,
		Credentials: map[string]nodesdk.Credential{credkafka.Name: r.cred},
	}
	go func() { l.done <- node.Listen(ctx, in, emit) }()
	t.Cleanup(func() { l.stop(t) })
	return l
}

// stop ends the listener and checks it left cleanly.
func (l *listener) stop(t *testing.T) {
	t.Helper()
	l.cancel()
	select {
	case err := <-l.done:
		if err != nil {
			t.Errorf("Listen returned %v", err)
		}
		l.done <- nil
	case <-time.After(30 * time.Second):
		t.Error("listener did not stop")
	}
}

func (r *rig) groupMembers(t *testing.T, group string) int {
	t.Helper()
	d, err := r.admin.DescribeGroups(context.Background(), group)
	if err != nil {
		return 0
	}
	return len(d[group].Members)
}

func TestKafkaIntegration(t *testing.T) {
	r := newRig(t)
	t.Run("produce creates executions and a restart creates no duplicates", func(t *testing.T) { testRestartDedup(t, r) })
	t.Run("two instances in one group split the partitions", func(t *testing.T) { testGroupSplit(t, r) })
	t.Run("batch mode emits batches keyed by offset range", func(t *testing.T) { testBatch(t, r) })
	t.Run("undecodable messages go to the dead-letter topic", func(t *testing.T) { testDeadLetter(t, r) })
	t.Run("ordered mode waits for each execution", func(t *testing.T) { testOrdered(t, r) })
	t.Run("backpressure pauses and resumes consumption", func(t *testing.T) { testBackpressure(t, r) })
	t.Run("producer node writes readable messages", func(t *testing.T) { testProducerNode(t, r) })
}

func testRestartDedup(t *testing.T, r *rig) {
	r.topic(t, "orders", 3)
	params := map[string]any{"topics": "orders", "startOffset": "earliest", "groupId": "g-restart"}
	for i := range 10 {
		r.produce(t, rec("orders", fmt.Sprintf("k%d", i), fmt.Sprintf(`{"n":%d}`, i)))
	}
	ex := newExecutions()
	l := r.listen(t, params, ex.emit, nil)
	ex.wait(t, 10)
	for key, items := range ex.byKey {
		if len(items) != 1 || items[0].JSON["topic"] != "orders" || items[0].JSON["value"] == nil || items[0].JSON["key"] == nil {
			t.Fatalf("item of %s = %+v", key, items)
		}
	}
	l.stop(t)

	// Restart: committed offsets mean nothing is delivered twice.
	before := ex.attempts
	l = r.listen(t, params, ex.emit, nil)
	for i := 10; i < 15; i++ {
		r.produce(t, rec("orders", fmt.Sprintf("k%d", i), fmt.Sprintf(`{"n":%d}`, i)))
	}
	ex.wait(t, 15)
	if got := ex.attempts - before; got != 5 {
		t.Errorf("after restart %d deliveries, want exactly the 5 new messages", got)
	}
	l.stop(t)

	// A crash before the execution is stored keeps the offset uncommitted: the
	// message is redelivered with the same dedup key, so it counts once.
	r.topic(t, "crashy", 1)
	crashParams := map[string]any{"topics": "crashy", "startOffset": "earliest", "groupId": "g-crash"}
	ex2 := newExecutions()
	ex2.failOnce["crashy:0:2"] = true
	for i := range 5 {
		r.produce(t, rec("crashy", "k", fmt.Sprintf(`{"n":%d}`, i)))
	}
	crashCtx, stopCrash := context.WithTimeout(context.Background(), 60*time.Second)
	defer stopCrash()
	node := kafkanode.NewTrigger(r.pool, testLimits)
	in := nodesdk.ListenInput{
		WorkflowID: "wf1", NodeName: "Kafka", Params: crashParams, Logger: quiet,
		Credentials: map[string]nodesdk.Credential{credkafka.Name: r.cred},
	}
	if err := node.Listen(crashCtx, in, ex2.emit); err == nil {
		t.Fatal("Listen must fail when the execution could not be stored")
	}
	if ex2.count() != 2 {
		t.Fatalf("executions before restart = %d, want the 2 delivered before the crash", ex2.count())
	}
	r.listen(t, crashParams, ex2.emit, nil)
	ex2.wait(t, 5)
	time.Sleep(time.Second)
	if ex2.count() != 5 {
		t.Errorf("executions = %d, want 5 (no duplicates)", ex2.count())
	}
}

func testGroupSplit(t *testing.T, r *rig) {
	r.topic(t, "split", 4)
	params := map[string]any{"topics": "split", "startOffset": "earliest", "groupId": "g-split"}
	type seen struct {
		mu    sync.Mutex
		parts map[int]map[int]bool // partition -> instances
		total int
	}
	s := &seen{parts: map[int]map[int]bool{}}
	emitFor := func(inst int) func(context.Context, nodesdk.ListenEvent) error {
		return func(_ context.Context, ev nodesdk.ListenEvent) error {
			s.mu.Lock()
			defer s.mu.Unlock()
			p := int(ev.Items[0].JSON["partition"].(float64))
			if s.parts[p] == nil {
				s.parts[p] = map[int]bool{}
			}
			s.parts[p][inst] = true
			s.total++
			return nil
		}
	}
	r.listen(t, params, emitFor(1), nil)
	r.listen(t, params, emitFor(2), nil)
	waitFor(t, "both members to join", func() bool { return r.groupMembers(t, "g-split") == 2 })
	// Give the rebalance time to settle, then spread messages over every partition.
	time.Sleep(3 * time.Second)
	manual, err := kgo.NewClient(kgo.SeedBrokers(r.broker), kgo.RecordPartitioner(kgo.ManualPartitioner()))
	if err != nil {
		t.Fatal(err)
	}
	defer manual.Close()
	for i := range 40 {
		if err := manual.ProduceSync(context.Background(), &kgo.Record{Topic: "split", Partition: int32(i % 4), Value: []byte(`{}`)}).FirstErr(); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, "40 messages", func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.total >= 40 })
	s.mu.Lock()
	defer s.mu.Unlock()
	instances := map[int]bool{}
	for p, who := range s.parts {
		if len(who) != 1 {
			t.Errorf("partition %d was read by %d instances, want 1", p, len(who))
		}
		for i := range who {
			instances[i] = true
		}
	}
	if len(instances) != 2 || len(s.parts) != 4 {
		t.Errorf("instances reading = %v over %d partitions, want both instances over all 4", instances, len(s.parts))
	}
}

func testBatch(t *testing.T, r *rig) {
	r.topic(t, "batches", 1)
	for i := range 25 {
		r.produce(t, rec("batches", "k", fmt.Sprintf(`{"n":%d}`, i)))
	}
	ex := newExecutions()
	r.listen(t, map[string]any{"topics": "batches", "startOffset": "earliest", "groupId": "g-batch",
		"mode": "batch", "batchSize": 10.0, "batchWaitMs": 500.0}, ex.emit, nil)
	ex.wait(t, 3)
	ex.mu.Lock()
	defer ex.mu.Unlock()
	want := map[string]int{"batches:0:0-9": 10, "batches:0:10-19": 10, "batches:0:20-24": 5}
	for key, n := range want {
		if got := len(ex.byKey[key]); got != n {
			t.Errorf("batch %s has %d items, want %d (all: %v)", key, got, n, ex.order)
		}
	}
}

func testDeadLetter(t *testing.T, r *rig) {
	r.topic(t, "events", 1)
	r.topic(t, "events-dlq", 1)
	r.produce(t, rec("events", "k", `{"ok":true}`), rec("events", "k", `{broken`), rec("events", "k", `{"ok":2}`))
	ex := newExecutions()
	r.listen(t, map[string]any{"topics": "events", "startOffset": "earliest", "groupId": "g-dlq", "deadLetterTopic": "events-dlq"}, ex.emit, nil)
	ex.wait(t, 2)
	time.Sleep(time.Second)
	if ex.count() != 2 {
		t.Errorf("executions = %d, want 2 (the broken message is skipped)", ex.count())
	}
	cl, err := kgo.NewClient(kgo.SeedBrokers(r.broker), kgo.ConsumeTopics("events-dlq"), kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	f := cl.PollFetches(ctx)
	recs := f.Records()
	if len(recs) != 1 || string(recs[0].Value) != `{broken` {
		t.Fatalf("dead-letter records = %+v", recs)
	}
	var hasErr bool
	for _, h := range recs[0].Headers {
		hasErr = hasErr || h.Key == "engi-error"
	}
	if !hasErr {
		t.Error("dead-letter record carries no engi-error header")
	}
}

func testOrdered(t *testing.T, r *rig) {
	r.topic(t, "seq", 1)
	for i := range 5 {
		r.produce(t, rec("seq", "same", fmt.Sprintf(`{"n":%d}`, i)))
	}
	var mu sync.Mutex
	var offsets []float64
	var awaits int
	emit := func(_ context.Context, ev nodesdk.ListenEvent) error {
		mu.Lock()
		defer mu.Unlock()
		if ev.AwaitCompletion {
			awaits++
		}
		offsets = append(offsets, ev.Items[0].JSON["offset"].(float64))
		return nil
	}
	r.listen(t, map[string]any{"topics": "seq", "startOffset": "earliest", "groupId": "g-seq", "mode": "ordered"}, emit, nil)
	waitFor(t, "5 ordered events", func() bool { mu.Lock(); defer mu.Unlock(); return len(offsets) >= 5 })
	mu.Lock()
	defer mu.Unlock()
	for i, o := range offsets {
		if int(o) != i {
			t.Fatalf("offsets = %v, want 0..4 in order", offsets)
		}
	}
	if awaits != 5 {
		t.Errorf("%d events asked to await completion, want 5", awaits)
	}
}

func testBackpressure(t *testing.T, r *rig) {
	r.topic(t, "pressure", 1)
	var mu sync.Mutex
	paused := true
	over := func(context.Context) bool { mu.Lock(); defer mu.Unlock(); return paused }
	ex := newExecutions()
	r.listen(t, map[string]any{"topics": "pressure", "startOffset": "earliest", "groupId": "g-pressure"}, ex.emit, over)
	waitFor(t, "the member to join", func() bool { return r.groupMembers(t, "g-pressure") == 1 })
	time.Sleep(time.Second) // the pause is applied before the first poll
	for i := range 5 {
		r.produce(t, rec("pressure", "k", fmt.Sprintf(`{"n":%d}`, i)))
	}
	time.Sleep(3 * time.Second)
	if n := ex.count(); n != 0 {
		t.Fatalf("%d executions while paused, want 0", n)
	}
	mu.Lock()
	paused = false
	mu.Unlock()
	ex.wait(t, 5)
}

func testProducerNode(t *testing.T, r *rig) {
	r.topic(t, "out", 1)
	n := kafkanode.NewProducer(r.pool, kafkanode.ProduceLimits{MaxRecordBytes: 1 << 20, Timeout: 30 * time.Second})
	in := nodetest.New(n.Describe(), nodetest.Items(map[string]any{"a": 1.0}, map[string]any{"a": 2.0}), map[string]any{
		"topic": "out", "key": "user-1", "format": "json", "value": map[string]any{"hello": "world"},
		"headers": map[string]any{"source": "engi"}, "acks": "all", "compression": "gzip", "idempotent": true,
	})
	in.Credentials = map[string]nodesdk.Credential{credkafka.Name: r.cred}
	out, err := n.Execute(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got := out.Ports[0]; len(got) != 2 || got[0].JSON["topic"] != "out" || got[1].JSON["offset"] != 1.0 {
		t.Fatalf("output = %+v", got)
	}
	cl, err := kgo.NewClient(kgo.SeedBrokers(r.broker), kgo.ConsumeTopics("out"), kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var recs []*kgo.Record
	for len(recs) < 2 {
		recs = append(recs, cl.PollFetches(ctx).Records()...)
		if ctx.Err() != nil {
			t.Fatalf("read %d records", len(recs))
		}
	}
	for _, rc := range recs {
		if string(rc.Key) != "user-1" || string(rc.Value) != `{"hello":"world"}` || len(rc.Headers) != 1 || rc.Headers[0].Key != "source" || string(rc.Headers[0].Value) != "engi" {
			t.Errorf("record = key %q value %q headers %v", rc.Key, rc.Value, rc.Headers)
		}
	}
	if r.pool.Len() != 1 {
		t.Errorf("producers = %d, want one shared client for the credential", r.pool.Len())
	}
	// A second run reuses the client; a new credential version builds another.
	if _, err := n.Execute(context.Background(), in); err != nil || r.pool.Len() != 1 {
		t.Errorf("second run: err %v, producers %d", err, r.pool.Len())
	}
	v2 := r.cred
	v2.Version = 2
	in.Credentials = map[string]nodesdk.Credential{credkafka.Name: v2}
	if _, err := n.Execute(context.Background(), in); err != nil || r.pool.Len() != 1 {
		t.Errorf("new version: err %v, producers %d (the old one must be closed)", err, r.pool.Len())
	}
}
