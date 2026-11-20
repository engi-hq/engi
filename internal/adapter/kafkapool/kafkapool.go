// Package kafkapool builds franz-go clients from Kafka credentials. Producers
// are shared, one per credential version and option set; consumers belong to
// the listener that creates them.
package kafkapool

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"

	"github.com/engi-hq/engi/internal/clientcache"
	credkafka "github.com/engi-hq/engi/internal/credtype/kafka"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

const defaultClientID = "engi"

// Config bounds every client. See config.KafkaNodeConfig.
type Config struct {
	ConnectTimeout time.Duration
	FlushTimeout   time.Duration
}

// Manager is safe for concurrent use.
type Manager struct {
	cfg   Config
	cache *clientcache.Cache[*kgo.Client]
}

// New returns a manager with no clients open.
func New(cfg Config, log *slog.Logger) *Manager {
	m := &Manager{cfg: cfg}
	m.cache = clientcache.New(log, m.closeProducer)
	return m
}

// Producer returns the shared client for the credential and option set. variant
// names the option set (acks, compression ...) so that different settings get
// different clients.
func (m *Manager) Producer(ctx context.Context, cred nodesdk.Credential, variant string, opts ...kgo.Opt) (*kgo.Client, error) {
	return m.cache.Get(ctx, "producer/"+variant, cred, func() (*kgo.Client, error) {
		return m.newClient(cred, opts...)
	})
}

// Consumer builds a client the caller owns and must Close.
func (m *Manager) Consumer(cred nodesdk.Credential, opts ...kgo.Opt) (*kgo.Client, error) {
	return m.newClient(cred, opts...)
}

// Len reports the number of live shared producers.
func (m *Manager) Len() int { return m.cache.Len() }

// Close flushes and closes every shared producer and rejects further requests.
func (m *Manager) Close() error { return m.cache.Close() }

func (m *Manager) closeProducer(c *kgo.Client) error {
	ctx, cancel := context.WithTimeout(context.Background(), m.cfg.FlushTimeout)
	defer cancel()
	err := c.Flush(ctx)
	c.Close()
	if err != nil {
		return fmt.Errorf("flushing producer: %w", err)
	}
	return nil
}

func (m *Manager) newClient(cred nodesdk.Credential, opts ...kgo.Opt) (*kgo.Client, error) {
	base, err := Options(cred.Data)
	if err != nil {
		return nil, fmt.Errorf("building kafka connection: %w", err)
	}
	base = append(base, kgo.DialTimeout(m.cfg.ConnectTimeout))
	cl, err := kgo.NewClient(append(base, opts...)...)
	if err != nil {
		return nil, fmt.Errorf("creating kafka client: %w", err)
	}
	return cl, nil
}

// Options maps credential data to the connection options of a franz-go client.
func Options(data map[string]any) ([]kgo.Opt, error) {
	brokers := splitBrokers(str(data, "brokers"))
	if len(brokers) == 0 {
		return nil, errors.New("brokers must name at least one host:port")
	}
	clientID := str(data, "clientId")
	if clientID == "" {
		clientID = defaultClientID
	}
	opts := []kgo.Opt{kgo.SeedBrokers(brokers...), kgo.ClientID(clientID)}

	mech := str(data, "saslMechanism")
	switch mech {
	case "", credkafka.SASLNone:
	case credkafka.SASLPlain, credkafka.SASLScram256, credkafka.SASLScram512:
		user := str(data, "username")
		pass, _ := data["password"].(string)
		if user == "" {
			return nil, fmt.Errorf("saslMechanism %q needs a username", mech)
		}
		opts = append(opts, kgo.SASL(saslMechanism(mech, user, pass)))
	default:
		return nil, fmt.Errorf("unknown saslMechanism %q", mech)
	}

	if useTLS, _ := data["tls"].(bool); useTLS {
		cfg, err := tlsConfig(data, brokers[0])
		if err != nil {
			return nil, err
		}
		opts = append(opts, kgo.DialTLSConfig(cfg))
	}
	return opts, nil
}

func saslMechanism(mech, user, pass string) sasl.Mechanism {
	switch mech {
	case credkafka.SASLScram256:
		return scram.Auth{User: user, Pass: pass}.AsSha256Mechanism()
	case credkafka.SASLScram512:
		return scram.Auth{User: user, Pass: pass}.AsSha512Mechanism()
	}
	return plain.Auth{User: user, Pass: pass}.AsMechanism()
}

func tlsConfig(data map[string]any, firstBroker string) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if host := hostOf(firstBroker); host != "" {
		cfg.ServerName = host
	}
	if ca := str(data, "caCertificate"); ca != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(ca)) {
			return nil, errors.New("caCertificate holds no PEM certificate")
		}
		cfg.RootCAs = pool
	}
	if skip, _ := data["tlsSkipVerify"].(bool); skip {
		cfg.InsecureSkipVerify = true //nolint:gosec // explicit development-only credential option
	}
	return cfg, nil
}

func splitBrokers(s string) []string {
	var out []string
	for _, b := range strings.Split(s, ",") {
		if b = strings.TrimSpace(b); b != "" {
			out = append(out, b)
		}
	}
	return out
}

func hostOf(broker string) string {
	if i := strings.LastIndex(broker, ":"); i > 0 {
		return strings.Trim(broker[:i], "[]")
	}
	return broker
}

func str(data map[string]any, key string) string {
	s, _ := data[key].(string)
	return strings.TrimSpace(s)
}
