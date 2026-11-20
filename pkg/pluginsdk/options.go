package pluginsdk

import (
	"log/slog"
	"time"

	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Defaults applied by Options for zero fields.
const (
	// DefaultMaxMessageBytes caps one protocol message in either direction.
	DefaultMaxMessageBytes = 32 << 20
	// DefaultMaxConcurrent caps the requests a plugin handles at once.
	DefaultMaxConcurrent = 16
	// DefaultHTTPTimeout bounds a request made through Input.HTTP when
	// Options.HTTPClient is not set.
	DefaultHTTPTimeout = 30 * time.Second
)

// Options tunes a plugin server. Zero fields take the defaults above.
type Options struct {
	// MaxMessageBytes caps one protocol message.
	MaxMessageBytes int
	// MaxConcurrent caps requests handled at once; more are answered with a
	// busy error.
	MaxConcurrent int
	// HTTPClient backs Input.HTTP. Nil means a client with DefaultHTTPTimeout.
	HTTPClient nodesdk.HTTPClient
	// Logger receives Input.Logger output when no host is listening (HTTP
	// transport). Nil discards it. Over stdio, logs go to the host.
	Logger *slog.Logger
}

func (o Options) withDefaults() Options {
	if o.MaxMessageBytes <= 0 {
		o.MaxMessageBytes = DefaultMaxMessageBytes
	}
	if o.MaxConcurrent <= 0 {
		o.MaxConcurrent = DefaultMaxConcurrent
	}
	return o
}
