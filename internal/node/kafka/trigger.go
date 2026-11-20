package kafka

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	credkafka "github.com/engi-hq/engi/internal/credtype/kafka"
	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// TriggerType is the trigger node type id.
const TriggerType = "engi.trigger.kafka"

// Trigger is the Kafka trigger node.
type Trigger struct {
	clients Clients
	lim     TriggerLimits
}

var _ nodesdk.ListenTrigger = (*Trigger)(nil)

// NewTrigger returns the Kafka trigger. Without clients Listen fails with
// nodesdk.ErrUnavailable.
func NewTrigger(clients Clients, lim TriggerLimits) *Trigger {
	return &Trigger{clients: clients, lim: lim}
}

// Describe implements nodesdk.Node.
func (t *Trigger) Describe() nodesdk.Descriptor {
	batchOnly := nodekit.Show("mode", modeBatch)
	return nodesdk.Descriptor{
		Type: TriggerType, Version: 1, DisplayName: "Kafka Trigger", Group: nodesdk.GroupTrigger, Icon: "database",
		Description: "Starts the workflow for messages (or batches) of Kafka topics. Runs on every trigger instance; Kafka balances the partitions.",
		Outputs:     []nodesdk.PortSpec{nodekit.Main("main", "")},
		Credentials: []nodesdk.CredentialRef{{Name: credkafka.Name, Type: credkafka.Name, Required: true}},
		Properties: []nodesdk.Property{
			{Name: "topics", DisplayName: "Topics", Type: nodesdk.PropString, Required: true, Placeholder: "orders, payments",
				Description: "Comma-separated topic names."},
			{Name: "groupId", DisplayName: "Consumer group", Type: nodesdk.PropString,
				Description: "Empty uses engi-<workflowId>-<node name>. Instances sharing a group split the partitions."},
			{Name: "startOffset", DisplayName: "Start from", Type: nodesdk.PropOptions, Default: defaultStartOffset,
				Description: "Only used while the group has no committed offset.",
				Options:     []nodesdk.Option{nodekit.Opt(offsetLatest, "Latest"), nodekit.Opt(offsetEarliest, "Earliest")}},
			{Name: "mode", DisplayName: "Mode", Type: nodesdk.PropOptions, Default: defaultTriggerModes, Options: []nodesdk.Option{
				nodekit.Opt(modePerMessage, "One execution per message"), nodekit.Opt(modeBatch, "One execution per batch"),
				nodekit.Opt(modeOrdered, "Ordered (one at a time, waits for each execution)")}},
			{Name: "batchSize", DisplayName: "Max messages per batch", Type: nodesdk.PropNumber, Default: float64(defaultBatchSize), DisplayOptions: batchOnly},
			{Name: "batchWaitMs", DisplayName: "Max wait per batch (ms)", Type: nodesdk.PropNumber, Default: float64(defaultBatchWaitMs), DisplayOptions: batchOnly},
			{Name: "valueFormat", DisplayName: "Value format", Type: nodesdk.PropOptions, Default: defaultValueFormat,
				Options: []nodesdk.Option{nodekit.Opt(formatJSON, "JSON"), nodekit.Opt(formatText, "Text")}},
			{Name: "deadLetterTopic", DisplayName: "Dead-letter topic", Type: nodesdk.PropString,
				Description: "Messages whose value cannot be decoded are copied here and skipped. Empty keeps them: the item carries the raw text and valueError."},
		},
	}
}

// Execute implements nodesdk.Node: the host supplies the message items.
func (*Trigger) Execute(_ context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	items := in.Items(0)
	out := make([]nodesdk.Item, len(items))
	for i, it := range items {
		out[i] = nodekit.Paired(it, i)
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}

// CheckListen implements nodesdk.ListenTrigger.
func (t *Trigger) CheckListen(params map[string]any) error {
	_, err := parseSettings(params, t.lim, "", "")
	return err
}

// Listen implements nodesdk.ListenTrigger.
func (t *Trigger) Listen(ctx context.Context, in nodesdk.ListenInput, emit func(context.Context, nodesdk.ListenEvent) error) error {
	if t.clients == nil {
		return fmt.Errorf("kafka clients: %w", nodesdk.ErrUnavailable)
	}
	s, err := parseSettings(in.Params, t.lim, in.WorkflowID, in.NodeName)
	if err != nil {
		return err
	}
	cred, ok := in.Credentials[credkafka.Name]
	if !ok {
		return fmt.Errorf("credential %q is not bound to the node", credkafka.Name)
	}
	reset := kgo.NewOffset().AtEnd()
	if s.startOffset == offsetEarliest {
		reset = kgo.NewOffset().AtStart()
	}
	cl, err := t.clients.Consumer(cred,
		kgo.ConsumerGroup(s.group), kgo.ConsumeTopics(s.topics...), kgo.ConsumeResetOffset(reset),
		kgo.DisableAutoCommit(), kgo.BlockRebalanceOnPoll(), kgo.FetchIsolationLevel(kgo.ReadCommitted()),
		kgo.SessionTimeout(t.lim.SessionTimeout), kgo.RebalanceTimeout(t.lim.RebalanceTimeout),
	)
	if err != nil {
		return fmt.Errorf("creating consumer: %w", err)
	}
	// Close leaves the group, so the others take over its partitions at once.
	defer cl.Close()
	if err := cl.Ping(ctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("connecting to brokers: %w", err)
	}
	c := &consumer{cl: cl, s: s, lim: t.lim, in: in, emit: emit, now: time.Now, latest: map[partitionKey]*kgo.Record{}}
	if err := c.run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
