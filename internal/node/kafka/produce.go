package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	credkafka "github.com/engi-hq/engi/internal/credtype/kafka"
	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// ProduceType is the producer node type id.
const ProduceType = "engi.kafka.produce"

// Acknowledgement levels.
const (
	acksAll    = "all"
	acksLeader = "leader"
	acksNone   = "none"
)

// Compression codecs.
const (
	compNone   = "none"
	compGzip   = "gzip"
	compSnappy = "snappy"
	compLZ4    = "lz4"
	compZstd   = "zstd"
)

// ProduceLimits are the host settings of the producer (nodes.kafka.*).
type ProduceLimits struct {
	// MaxRecordBytes rejects a record whose key plus value is larger.
	MaxRecordBytes int
	// Timeout bounds producing the records of one run.
	Timeout time.Duration
}

// Producer is the Kafka producer node.
type Producer struct {
	clients Clients
	lim     ProduceLimits
}

// NewProducer returns the producer node. Without clients Execute fails with
// nodesdk.ErrUnavailable.
func NewProducer(clients Clients, lim ProduceLimits) *Producer {
	return &Producer{clients: clients, lim: lim}
}

// Describe implements nodesdk.Node.
func (*Producer) Describe() nodesdk.Descriptor {
	return nodesdk.Descriptor{
		Type: ProduceType, Version: 1, DisplayName: "Kafka", Group: nodesdk.GroupAction, Icon: "database",
		Description: "Produce a message to a Kafka topic for every input item.",
		Inputs:      []nodesdk.PortSpec{nodekit.Main("main", "")},
		Outputs:     []nodesdk.PortSpec{nodekit.Main("main", "")},
		Credentials: []nodesdk.CredentialRef{{Name: credkafka.Name, Type: credkafka.Name, Required: true}},
		Properties: []nodesdk.Property{
			{Name: "topic", DisplayName: "Topic", Type: nodesdk.PropString, Required: true},
			{Name: "key", DisplayName: "Key", Type: nodesdk.PropString, Description: "Messages with the same key go to the same partition, in order."},
			{Name: "value", DisplayName: "Value", Type: nodesdk.PropString,
				Description: "JSON format encodes whatever the expression returns; text format sends strings as they are and encodes other values as JSON."},
			{Name: "format", DisplayName: "Value format", Type: nodesdk.PropOptions, Default: formatJSON,
				Options: []nodesdk.Option{nodekit.Opt(formatJSON, "JSON"), nodekit.Opt(formatText, "Text")}},
			{Name: "headers", DisplayName: "Headers", Type: nodesdk.PropJSON, Description: "Object of header names to string values."},
			{Name: "acks", DisplayName: "Acknowledgements", Type: nodesdk.PropOptions, Default: acksAll, Options: []nodesdk.Option{
				nodekit.Opt(acksAll, "All in-sync replicas"), nodekit.Opt(acksLeader, "Leader only"), nodekit.Opt(acksNone, "None")}},
			{Name: "compression", DisplayName: "Compression", Type: nodesdk.PropOptions, Default: compNone, Options: []nodesdk.Option{
				nodekit.Opt(compNone, "None"), nodekit.Opt(compGzip, "gzip"), nodekit.Opt(compSnappy, "Snappy"), nodekit.Opt(compLZ4, "LZ4"), nodekit.Opt(compZstd, "Zstandard")}},
			{Name: "idempotent", DisplayName: "Idempotent producer", Type: nodesdk.PropBoolean, Default: true,
				Description: "Retries never duplicate a message. Needs acknowledgements from all in-sync replicas."},
		},
	}
}

// producerSettings are the client-level options; they pick the shared client.
type producerSettings struct {
	acks, compression string
	idempotent        bool
}

func (p producerSettings) variant() string {
	return fmt.Sprintf("acks=%s,compression=%s,idempotent=%t", p.acks, p.compression, p.idempotent)
}

func (p producerSettings) options() []kgo.Opt {
	var opts []kgo.Opt
	switch p.acks {
	case acksLeader:
		opts = append(opts, kgo.RequiredAcks(kgo.LeaderAck()))
	case acksNone:
		opts = append(opts, kgo.RequiredAcks(kgo.NoAck()))
	default:
		opts = append(opts, kgo.RequiredAcks(kgo.AllISRAcks()))
	}
	switch p.compression {
	case compGzip:
		opts = append(opts, kgo.ProducerBatchCompression(kgo.GzipCompression()))
	case compSnappy:
		opts = append(opts, kgo.ProducerBatchCompression(kgo.SnappyCompression()))
	case compLZ4:
		opts = append(opts, kgo.ProducerBatchCompression(kgo.Lz4Compression()))
	case compZstd:
		opts = append(opts, kgo.ProducerBatchCompression(kgo.ZstdCompression()))
	default:
		opts = append(opts, kgo.ProducerBatchCompression(kgo.NoCompression()))
	}
	if !p.idempotent {
		opts = append(opts, kgo.DisableIdempotentWrite())
	}
	return opts
}

func readProducerSettings(ctx context.Context, in nodesdk.Input) (producerSettings, error) {
	acks, err := nodesdk.ParamString(ctx, in, "acks", 0)
	if err != nil {
		return producerSettings{}, err
	}
	comp, err := nodesdk.ParamString(ctx, in, "compression", 0)
	if err != nil {
		return producerSettings{}, err
	}
	idem, err := nodesdk.ParamBool(ctx, in, "idempotent", 0)
	if err != nil {
		return producerSettings{}, err
	}
	switch acks {
	case acksAll, acksLeader, acksNone:
	default:
		return producerSettings{}, fmt.Errorf("acks %q is not %s, %s or %s", acks, acksAll, acksLeader, acksNone)
	}
	switch comp {
	case compNone, compGzip, compSnappy, compLZ4, compZstd:
	default:
		return producerSettings{}, fmt.Errorf("compression %q is not supported", comp)
	}
	if idem && acks != acksAll {
		return producerSettings{}, errors.New("an idempotent producer needs acks \"all\"; choose acks \"all\" or turn idempotent off")
	}
	return producerSettings{acks: acks, compression: comp, idempotent: idem}, nil
}

// Execute implements nodesdk.Node. Every item is turned into a record before
// the first one is sent.
func (p *Producer) Execute(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	items := in.Items(0)
	if len(items) == 0 {
		return nodesdk.Output{Ports: [][]nodesdk.Item{nil}}, nil
	}
	if p.clients == nil {
		return nodesdk.Output{}, fmt.Errorf("kafka clients: %w", nodesdk.ErrUnavailable)
	}
	ps, err := readProducerSettings(ctx, in)
	if err != nil {
		return nodesdk.Output{}, err
	}
	recs := make([]*kgo.Record, len(items))
	for i := range items {
		if recs[i], err = p.record(ctx, in, i); err != nil {
			return nodesdk.Output{}, nodekit.ItemError(i, err)
		}
	}
	cred, err := in.Credential(ctx, credkafka.Name)
	if err != nil {
		return nodesdk.Output{}, fmt.Errorf("loading credential %q: %w", credkafka.Name, err)
	}
	cl, err := p.clients.Producer(ctx, cred, ps.variant(), ps.options()...)
	if err != nil {
		return nodesdk.Output{}, fmt.Errorf("opening kafka producer: %w", err)
	}
	pctx, cancel := context.WithTimeout(ctx, p.lim.Timeout)
	defer cancel()
	res := cl.ProduceSync(pctx, recs...)
	out := make([]nodesdk.Item, len(items))
	for i, r := range res {
		if r.Err != nil {
			return nodesdk.Output{}, nodekit.ItemError(i, fmt.Errorf("producing to %q: %w", r.Record.Topic, r.Err))
		}
		out[i] = nodekit.Paired(nodesdk.Item{JSON: map[string]any{
			"topic": r.Record.Topic, "partition": float64(r.Record.Partition), "offset": float64(r.Record.Offset),
			"timestamp": r.Record.Timestamp.UTC().Format(time.RFC3339Nano),
		}}, i)
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}

// record builds the record of item i.
func (p *Producer) record(ctx context.Context, in nodesdk.Input, i int) (*kgo.Record, error) {
	topic, err := nodesdk.ParamString(ctx, in, "topic", i)
	if err != nil {
		return nil, err
	}
	if topic == "" {
		return nil, errors.New("topic must not be empty")
	}
	format, err := nodesdk.ParamString(ctx, in, "format", i)
	if err != nil {
		return nil, err
	}
	raw, err := in.Param(ctx, "value", i)
	if err != nil {
		return nil, err
	}
	value, err := encodeValue(format, raw)
	if err != nil {
		return nil, err
	}
	rec := &kgo.Record{Topic: topic, Value: value}
	key, err := nodesdk.ParamString(ctx, in, "key", i)
	if err != nil {
		return nil, err
	}
	if key != "" {
		rec.Key = []byte(key)
	}
	if size := len(rec.Key) + len(rec.Value); size > p.lim.MaxRecordBytes {
		return nil, fmt.Errorf("record is %d bytes; the limit is %d (nodes.kafka.maxRecordBytes)", size, p.lim.MaxRecordBytes)
	}
	hv, err := in.Param(ctx, "headers", i)
	if err != nil {
		return nil, err
	}
	if rec.Headers, err = recordHeaders(hv); err != nil {
		return nil, err
	}
	return rec, nil
}

// encodeValue serializes a value for the wire.
func encodeValue(format string, v any) ([]byte, error) {
	if s, ok := v.(string); ok && format == formatText {
		return []byte(s), nil
	}
	if v == nil && format == formatText {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encoding value as JSON: %w", err)
	}
	return b, nil
}

// recordHeaders turns an object of header names to values into record headers,
// in name order so the record is the same on every run.
func recordHeaders(v any) ([]kgo.RecordHeader, error) {
	switch t := v.(type) {
	case nil:
		return nil, nil
	case string:
		if t == "" {
			return nil, nil
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(t), &m); err != nil {
			return nil, fmt.Errorf("headers must be an object: %w", err)
		}
		return recordHeaders(m)
	case map[string]any:
		names := make([]string, 0, len(t))
		for k := range t {
			names = append(names, k)
		}
		sort.Strings(names)
		out := make([]kgo.RecordHeader, 0, len(names))
		for _, k := range names {
			out = append(out, kgo.RecordHeader{Key: k, Value: []byte(nodekit.ToString(t[k]))})
		}
		return out, nil
	}
	return nil, errors.New("headers must be an object of names to values")
}
