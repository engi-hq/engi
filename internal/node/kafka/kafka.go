// Package kafka implements the Kafka trigger (engi.trigger.kafka) and the Kafka
// producer node (engi.kafka.produce) on franz-go.
package kafka

import (
	"context"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Clients builds franz-go clients from a Kafka credential (satisfied by
// kafkapool.Manager).
type Clients interface {
	// Producer returns the shared client for the credential and variant, which
	// names the option set the client was built with.
	Producer(ctx context.Context, cred nodesdk.Credential, variant string, opts ...kgo.Opt) (*kgo.Client, error)
	// Consumer builds a client the caller owns and must Close.
	Consumer(cred nodesdk.Credential, opts ...kgo.Opt) (*kgo.Client, error)
}
