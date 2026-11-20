package kafka

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Headers added to a record copied to the dead-letter topic.
const (
	dlqErrorHeader  = "engi-error"
	dlqSourceHeader = "engi-source"
	sourceFormat    = "%s/%d/%d"
)

// consumer is one running Kafka listener: it polls records, turns them into
// events, and commits offsets only after emit returned.
type consumer struct {
	cl   *kgo.Client
	s    settings
	lim  TriggerLimits
	in   nodesdk.ListenInput
	emit func(context.Context, nodesdk.ListenEvent) error
	now  func() time.Time

	paused bool
	// latest is the newest record seen per partition, for batch commits.
	latest map[partitionKey]*kgo.Record
}

// run polls until ctx is done or a delivery fails. Whatever was delivered is
// committed first, so a failure only repeats the messages that were not.
func (c *consumer) run(ctx context.Context) error {
	var bt *batcher
	if c.s.mode == modeBatch {
		bt = newBatcher(c.s.batchSize, c.s.batchWait)
	}
	for ctx.Err() == nil {
		c.applyBackpressure(ctx)
		pollCtx, cancel := context.WithTimeout(ctx, c.pollWait(bt))
		fetches := c.cl.PollRecords(pollCtx, c.lim.MaxPollRecords)
		cancel()
		if fetches.IsClientClosed() {
			return nil
		}
		c.logFetchErrors(ctx, fetches)

		var err error
		if bt != nil {
			err = c.processBatches(ctx, bt, fetches)
		} else {
			err = c.processMessages(ctx, fetches)
		}
		// A rebalance waits for open batches, so they are never left behind a revoke.
		if bt == nil || bt.empty() {
			c.cl.AllowRebalance()
		}
		if err != nil {
			c.cl.AllowRebalance()
			return err
		}
	}
	return nil
}

// pollWait is the longest a poll may block: until the oldest open batch is due,
// and never longer than the backpressure sampling interval.
func (c *consumer) pollWait(bt *batcher) time.Duration {
	wait := c.lim.PollInterval
	if bt != nil {
		if at, ok := bt.nextDue(); ok {
			if d := at.Sub(c.now()); d < wait {
				wait = d
			}
		}
	}
	return max(wait, time.Millisecond)
}

// applyBackpressure pauses fetching while the host reports overload and resumes
// it afterwards. Records already fetched are still processed.
func (c *consumer) applyBackpressure(ctx context.Context) {
	if c.in.Overloaded == nil {
		return
	}
	over := c.in.Overloaded(ctx)
	switch {
	case over && !c.paused:
		c.cl.PauseFetchTopics(c.s.topics...)
		c.paused = true
		c.in.Logger.Warn("kafka consumption paused: job queue is deep", "group", c.s.group)
	case !over && c.paused:
		c.cl.ResumeFetchTopics(c.s.topics...)
		c.paused = false
		c.in.Logger.Info("kafka consumption resumed", "group", c.s.group)
	}
}

func (c *consumer) logFetchErrors(ctx context.Context, f kgo.Fetches) {
	for _, fe := range f.Errors() {
		if ctx.Err() != nil || errors.Is(fe.Err, context.DeadlineExceeded) || errors.Is(fe.Err, context.Canceled) {
			continue
		}
		c.in.Logger.Warn("kafka fetch error", "topic", fe.Topic, "partition", fe.Partition, "error", fe.Err)
	}
}

// processMessages handles perMessage and ordered mode: one event per record, in
// fetch order. perMessage commits once per poll, ordered after every record.
func (c *consumer) processMessages(ctx context.Context, f kgo.Fetches) error {
	ordered := c.s.mode == modeOrdered
	last := map[partitionKey]*kgo.Record{}
	var failure error
	f.EachRecord(func(r *kgo.Record) {
		if failure != nil {
			return
		}
		m := messageOf(r)
		item, skip, err := c.prepare(ctx, m)
		if err == nil && !skip {
			err = c.emit(ctx, nodesdk.ListenEvent{Items: []nodesdk.Item{item}, DedupKey: m.dedupKey(), AwaitCompletion: ordered})
		}
		if err != nil {
			failure = fmt.Errorf("delivering %s: %w", m.dedupKey(), err)
			return
		}
		last[partitionKey{r.Topic, r.Partition}] = r
		if ordered {
			failure = c.commit(ctx, r)
		}
	})
	if !ordered {
		if err := c.commit(ctx, recordsOf(last)...); err != nil && failure == nil {
			failure = err
		}
	}
	return failure
}

// processBatches buffers records per partition and emits full or expired
// batches, committing each after it was delivered.
func (c *consumer) processBatches(ctx context.Context, bt *batcher, f kgo.Fetches) error {
	var failure error
	deliver := func(b batch) {
		if failure == nil {
			failure = c.deliverBatch(ctx, b)
		}
	}
	f.EachRecord(func(r *kgo.Record) {
		if failure != nil {
			return
		}
		// Batches are per partition and hold every pending message of it, so a
		// batch always ends at the latest record seen of its partition.
		c.latest[partitionKey{r.Topic, r.Partition}] = r
		if b, full := bt.add(messageOf(r), c.now()); full {
			deliver(b)
		}
	})
	for _, b := range bt.due(c.now()) {
		deliver(b)
	}
	return failure
}

func (c *consumer) deliverBatch(ctx context.Context, b batch) error {
	items := make([]nodesdk.Item, 0, len(b.msgs))
	for _, m := range b.msgs {
		item, skip, err := c.prepare(ctx, m)
		if err != nil {
			return fmt.Errorf("preparing %s: %w", m.dedupKey(), err)
		}
		if !skip {
			items = append(items, item)
		}
	}
	if len(items) > 0 {
		if err := c.emit(ctx, nodesdk.ListenEvent{Items: items, DedupKey: b.dedupKey()}); err != nil {
			return fmt.Errorf("delivering batch %s: %w", b.dedupKey(), err)
		}
	}
	return c.commit(ctx, c.latest[b.partitionKey])
}

// prepare decodes a message. When decoding fails the message goes to the
// dead-letter topic and is skipped, or without one it is kept with its raw text.
func (c *consumer) prepare(ctx context.Context, m message) (item nodesdk.Item, skip bool, err error) {
	v, derr := decodeValue(c.s.format, m.Value)
	if derr == nil {
		return m.item(v, nil), false, nil
	}
	if c.s.deadLetter == "" {
		return m.item(nil, derr), false, nil
	}
	rec := &kgo.Record{Topic: c.s.deadLetter, Key: m.Key, Value: m.Value, Headers: []kgo.RecordHeader{
		{Key: dlqErrorHeader, Value: []byte(derr.Error())},
		{Key: dlqSourceHeader, Value: []byte(fmt.Sprintf(sourceFormat, m.Topic, m.Partition, m.Offset))},
	}}
	for _, h := range m.Headers {
		rec.Headers = append(rec.Headers, kgo.RecordHeader{Key: h.Key, Value: h.Value})
	}
	if err := c.cl.ProduceSync(ctx, rec).FirstErr(); err != nil {
		return nodesdk.Item{}, false, fmt.Errorf("copying to dead-letter topic %q: %w", c.s.deadLetter, err)
	}
	c.in.Logger.Warn("kafka message could not be decoded; sent to the dead-letter topic",
		"topic", m.Topic, "partition", strconv.Itoa(int(m.Partition)), "offset", m.Offset, "error", derr)
	return nodesdk.Item{}, true, nil
}

// commit stores the offsets after the given records. It outlives a canceled ctx
// so a clean shutdown still records what it delivered.
func (c *consumer) commit(ctx context.Context, recs ...*kgo.Record) error {
	if len(recs) == 0 {
		return nil
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.lim.RebalanceTimeout)
	defer cancel()
	if err := c.cl.CommitRecords(cctx, recs...); err != nil {
		return fmt.Errorf("committing offsets: %w", err)
	}
	return nil
}

func recordsOf(m map[partitionKey]*kgo.Record) []*kgo.Record {
	out := make([]*kgo.Record, 0, len(m))
	for _, r := range m {
		out = append(out, r)
	}
	return out
}

func messageOf(r *kgo.Record) message {
	m := message{Topic: r.Topic, Partition: r.Partition, Offset: r.Offset, Timestamp: r.Timestamp, Key: r.Key, Value: r.Value}
	for _, h := range r.Headers {
		m.Headers = append(m.Headers, header{Key: h.Key, Value: h.Value})
	}
	return m
}
