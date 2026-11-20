package config

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"
)

// Schedule missed-run policies.
const (
	// MissedSkip drops runs missed during downtime or a leader change.
	MissedSkip = "skip"
	// MissedRunOnce catches up with a single run.
	MissedRunOnce = "runOnce"
)

// Lease provider types.
const (
	// LeaseLocal is always the leader (standalone mode).
	LeaseLocal = "local"
	// LeasePostgres elects the leader with a Postgres advisory lock.
	LeasePostgres = "postgres"
	// LeaseKubernetes elects the leader with a coordination.k8s.io/v1 Lease.
	LeaseKubernetes = "kubernetes"
)

// TriggerConfig groups the settings of webhooks, schedules and leader election.
type TriggerConfig struct {
	Webhook  WebhookConfig      `yaml:"webhook"`
	Schedule ScheduleConfig     `yaml:"schedule"`
	Lease    LeaseConfig        `yaml:"lease"`
	Listen   ListenConfig       `yaml:"listen"`
	Kafka    KafkaTriggerConfig `yaml:"kafka"`
	Poll     PollConfig         `yaml:"poll"`
	Chat     ChatTriggerConfig  `yaml:"chat"`
	// MCPServer tunes the endpoints of MCP server triggers.
	MCPServer MCPServerConfig `yaml:"mcpServer"`
}

// ListenConfig tunes the host of group-based listener triggers (Kafka).
type ListenConfig struct {
	// RetryDelay is how long a listener that failed waits before it restarts.
	RetryDelay time.Duration `yaml:"retryDelay"`
}

// KafkaTriggerConfig tunes the Kafka trigger. The backpressure thresholds are
// read by the listener host and apply to every listener.
type KafkaTriggerConfig struct {
	// PauseAboveDepth pauses fetching while the job queue holds more jobs.
	PauseAboveDepth int `yaml:"pauseAboveDepth"`
	// ResumeBelowDepth resumes fetching once the queue is below this depth
	// (lower than PauseAboveDepth, so it does not flap).
	ResumeBelowDepth int `yaml:"resumeBelowDepth"`
	// DepthCheckInterval is the longest time between queue depth samples.
	DepthCheckInterval time.Duration `yaml:"depthCheckInterval"`
	// BatchMaxMessages caps the batch size a node may ask for.
	BatchMaxMessages int `yaml:"batchMaxMessages"`
	// BatchMaxWait caps how long a node may hold a batch open. It must be
	// shorter than RebalanceTimeout because a rebalance waits for open batches.
	BatchMaxWait time.Duration `yaml:"batchMaxWait"`
	// SessionTimeout is the consumer group session timeout.
	SessionTimeout time.Duration `yaml:"sessionTimeout"`
	// RebalanceTimeout is how long the group waits for members to rejoin.
	RebalanceTimeout time.Duration `yaml:"rebalanceTimeout"`
	// MaxPollRecords caps the records taken from the client per poll.
	MaxPollRecords int `yaml:"maxPollRecords"`
}

// WebhookConfig tunes the webhook endpoints.
type WebhookConfig struct {
	// BasePath is the URL prefix of production webhooks.
	BasePath string `yaml:"basePath"`
	// TestBasePath is the URL prefix of test webhooks.
	TestBasePath string `yaml:"testBasePath"`
	// WaitBasePath is the URL prefix of wait-node resume calls.
	WaitBasePath string `yaml:"waitBasePath"`
	// MaxBodyBytes caps the request body; larger requests get 413.
	MaxBodyBytes int64 `yaml:"maxBodyBytes"`
	// ResponseTimeout bounds how long a call waits for the workflow's answer
	// (last node or Respond to Webhook node) before replying 504.
	ResponseTimeout time.Duration `yaml:"responseTimeout"`
	// TestTimeout is how long an armed test webhook listens for one call.
	TestTimeout time.Duration `yaml:"testTimeout"`
	// IdempotencyHeader names the request header that deduplicates calls.
	// Empty disables deduplication.
	IdempotencyHeader string `yaml:"idempotencyHeader"`
	// ActivationPageSize is how many active workflows are loaded per page when
	// triggers are re-registered at startup.
	ActivationPageSize int `yaml:"activationPageSize"`
}

// ScheduleConfig tunes the scheduler.
type ScheduleConfig struct {
	// DefaultTimezone is the IANA zone for schedules that name none.
	DefaultTimezone string `yaml:"defaultTimezone"`
	// Tick is how often the scheduler looks for due schedules; it is the
	// precision of every schedule.
	Tick time.Duration `yaml:"tick"`
	// MissedPolicy is skip or runOnce. It applies to a run that is overdue by
	// more than MissedGrace.
	MissedPolicy string `yaml:"missedPolicy"`
	// MissedGrace is how late a run may start and still count as on time.
	MissedGrace time.Duration `yaml:"missedGrace"`
}

// LeaseConfig is the leader election timing (Kubernetes Lease semantics).
type LeaseConfig struct {
	// Type selects the provider: auto (postgres when store.driver is postgres,
	// else local), local or postgres.
	Type string `yaml:"type"`
	// LockKey is the Postgres advisory lock the postgres provider campaigns
	// for. Replicas that share a database and lock key elect one leader.
	LockKey int64 `yaml:"lockKey"`
	// Name is the Kubernetes Lease object the kubernetes provider campaigns
	// for. Replicas of one installation share it.
	Name string `yaml:"name"`
	// Namespace is the namespace of that Lease. Empty reads NamespaceFile.
	Namespace string `yaml:"namespace"`
	// Identity names this replica as the lease holder. Empty uses the host
	// name (the pod name in Kubernetes).
	Identity string `yaml:"identity"`
	// APIServer is the Kubernetes API base URL.
	APIServer string `yaml:"apiServer"`
	// TokenFile holds the service-account token (re-read on every request, as
	// projected tokens rotate).
	TokenFile string `yaml:"tokenFile"`
	// CAFile holds the PEM CA bundle that signed the API server certificate.
	CAFile string `yaml:"caFile"`
	// NamespaceFile holds the pod's own namespace.
	NamespaceFile string `yaml:"namespaceFile"`
	// Duration is how long a lease is valid without renewal.
	Duration time.Duration `yaml:"duration"`
	// RenewDeadline is how long the leader keeps trying to renew before it
	// steps down. It must be shorter than Duration.
	RenewDeadline time.Duration `yaml:"renewDeadline"`
	// RetryPeriod is the pause between acquire and renew attempts.
	RetryPeriod time.Duration `yaml:"retryPeriod"`
}

func defaultTrigger() TriggerConfig {
	return TriggerConfig{
		Webhook: WebhookConfig{
			BasePath: "/hook", TestBasePath: "/hook-test", WaitBasePath: "/hook-wait",
			MaxBodyBytes: 16 << 20, ResponseTimeout: 120 * time.Second, TestTimeout: 120 * time.Second,
			IdempotencyHeader: "Idempotency-Key", ActivationPageSize: 100,
		},
		Schedule: ScheduleConfig{DefaultTimezone: "UTC", Tick: time.Second, MissedPolicy: MissedSkip, MissedGrace: time.Minute},
		Lease: LeaseConfig{Type: DriverAuto, //nolint:gosec // G101: file paths of the service-account mount, not credentials
			LockKey: 7313401, Duration: 15 * time.Second, RenewDeadline: 10 * time.Second, RetryPeriod: 2 * time.Second,
			Name: "engi-trigger", APIServer: "https://kubernetes.default.svc",
			TokenFile:     "/var/run/secrets/kubernetes.io/serviceaccount/token",
			CAFile:        "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt",
			NamespaceFile: "/var/run/secrets/kubernetes.io/serviceaccount/namespace"},
		Listen:    ListenConfig{RetryDelay: 5 * time.Second},
		Poll:      defaultPoll(),
		Chat:      defaultChatTrigger(),
		MCPServer: defaultMCPServer(),
		Kafka: KafkaTriggerConfig{
			PauseAboveDepth: 1000, ResumeBelowDepth: 500, DepthCheckInterval: 2 * time.Second,
			BatchMaxMessages: 1000, BatchMaxWait: 30 * time.Second,
			SessionTimeout: 45 * time.Second, RebalanceTimeout: 60 * time.Second, MaxPollRecords: 500,
		},
	}
}

// Validate reports every problem with the trigger settings.
func (c TriggerConfig) Validate() error {
	var errs []error
	w := c.Webhook
	for name, p := range map[string]string{"webhook.basePath": w.BasePath, "webhook.testBasePath": w.TestBasePath, "webhook.waitBasePath": w.WaitBasePath} {
		if len(p) < 2 || p[0] != '/' || p[len(p)-1] == '/' {
			errs = append(errs, fmt.Errorf("%s %q must start with / and not end with /", name, p))
		}
	}
	if w.BasePath == w.TestBasePath || w.BasePath == w.WaitBasePath || w.TestBasePath == w.WaitBasePath {
		errs = append(errs, errors.New("webhook.basePath, testBasePath and waitBasePath must differ"))
	}
	errs = appendPositive(errs, "webhook.maxBodyBytes", w.MaxBodyBytes)
	errs = appendPositive(errs, "webhook.responseTimeout", int64(w.ResponseTimeout))
	errs = appendPositive(errs, "webhook.testTimeout", int64(w.TestTimeout))
	errs = appendPositive(errs, "webhook.activationPageSize", int64(w.ActivationPageSize))
	errs = c.Chat.validate(errs, w)
	errs = c.MCPServer.validate(errs, w, c.Chat)

	s := c.Schedule
	if s.DefaultTimezone == "" {
		errs = append(errs, errors.New("schedule.defaultTimezone must not be empty"))
	} else if _, err := time.LoadLocation(s.DefaultTimezone); err != nil {
		errs = append(errs, fmt.Errorf("schedule.defaultTimezone %q: %w", s.DefaultTimezone, err))
	}
	errs = appendPositive(errs, "schedule.tick", int64(s.Tick))
	if !slices.Contains([]string{MissedSkip, MissedRunOnce}, s.MissedPolicy) {
		errs = append(errs, fmt.Errorf("schedule.missedPolicy %q is not one of %s, %s", s.MissedPolicy, MissedSkip, MissedRunOnce))
	}
	if s.MissedGrace < 0 {
		errs = append(errs, errors.New("schedule.missedGrace must not be negative"))
	}

	l := c.Lease
	if !slices.Contains([]string{DriverAuto, LeaseLocal, LeasePostgres, LeaseKubernetes}, l.Type) {
		errs = append(errs, fmt.Errorf("lease.type %q is not one of auto, %s, %s, %s", l.Type, LeaseLocal, LeasePostgres, LeaseKubernetes))
	}
	if l.Type == LeaseKubernetes {
		for k, v := range map[string]string{"lease.name": l.Name, "lease.apiServer": l.APIServer, "lease.tokenFile": l.TokenFile} {
			if v == "" {
				errs = append(errs, fmt.Errorf("%s must be set for lease.type kubernetes", k))
			}
		}
		if l.Namespace == "" && l.NamespaceFile == "" {
			errs = append(errs, errors.New("lease.namespace or lease.namespaceFile must be set for lease.type kubernetes"))
		}
	}
	errs = appendPositive(errs, "lease.duration", int64(l.Duration))
	errs = appendPositive(errs, "lease.renewDeadline", int64(l.RenewDeadline))
	errs = appendPositive(errs, "lease.retryPeriod", int64(l.RetryPeriod))
	if l.RenewDeadline >= l.Duration {
		errs = append(errs, errors.New("lease.renewDeadline must be shorter than lease.duration"))
	}
	errs = appendPositive(errs, "listen.retryDelay", int64(c.Listen.RetryDelay))
	errs = append(errs, c.Kafka.validate()...)
	errs = append(errs, c.Poll.validate()...)
	return errors.Join(errs...)
}

func (k KafkaTriggerConfig) validate() []error {
	var errs []error
	errs = appendPositive(errs, "kafka.pauseAboveDepth", int64(k.PauseAboveDepth))
	errs = appendPositive(errs, "kafka.resumeBelowDepth", int64(k.ResumeBelowDepth))
	errs = appendPositive(errs, "kafka.depthCheckInterval", int64(k.DepthCheckInterval))
	errs = appendPositive(errs, "kafka.batchMaxMessages", int64(k.BatchMaxMessages))
	errs = appendPositive(errs, "kafka.batchMaxWait", int64(k.BatchMaxWait))
	errs = appendPositive(errs, "kafka.sessionTimeout", int64(k.SessionTimeout))
	errs = appendPositive(errs, "kafka.rebalanceTimeout", int64(k.RebalanceTimeout))
	errs = appendPositive(errs, "kafka.maxPollRecords", int64(k.MaxPollRecords))
	if k.ResumeBelowDepth >= k.PauseAboveDepth {
		errs = append(errs, errors.New("kafka.resumeBelowDepth must be lower than kafka.pauseAboveDepth"))
	}
	if k.BatchMaxWait >= k.RebalanceTimeout {
		errs = append(errs, errors.New("kafka.batchMaxWait must be shorter than kafka.rebalanceTimeout"))
	}
	return errs
}

// triggerSettings are the env/flag bindings of the webhook, schedule and lease sections.
func triggerSettings() []setting {
	return slices.Concat(chatTriggerSettings(), []setting{
		{"ENGI_WEBHOOK_BASE_PATH", "webhook-base-path", "URL prefix of production webhooks",
			stringSetting(func(c *Config) *string { return &c.Trigger.Webhook.BasePath })},
		{"ENGI_WEBHOOK_TEST_BASE_PATH", "webhook-test-base-path", "URL prefix of test webhooks",
			stringSetting(func(c *Config) *string { return &c.Trigger.Webhook.TestBasePath })},
		{"ENGI_WEBHOOK_WAIT_BASE_PATH", "webhook-wait-base-path", "URL prefix of wait-node resume calls",
			stringSetting(func(c *Config) *string { return &c.Trigger.Webhook.WaitBasePath })},
		{"ENGI_WEBHOOK_MAX_BODY_BYTES", "webhook-max-body-bytes", "max webhook request body bytes",
			func(c *Config, v string) (err error) {
				c.Trigger.Webhook.MaxBodyBytes, err = strconv.ParseInt(v, 10, 64)
				return
			}},
		{"ENGI_WEBHOOK_RESPONSE_TIMEOUT", "webhook-response-timeout", "how long a webhook call waits for the workflow's answer",
			durationSetting(func(c *Config) *time.Duration { return &c.Trigger.Webhook.ResponseTimeout })},
		{"ENGI_WEBHOOK_TEST_TIMEOUT", "webhook-test-timeout", "how long an armed test webhook listens",
			durationSetting(func(c *Config) *time.Duration { return &c.Trigger.Webhook.TestTimeout })},
		{"ENGI_WEBHOOK_IDEMPOTENCY_HEADER", "webhook-idempotency-header", "request header that deduplicates webhook calls (empty disables)",
			stringSetting(func(c *Config) *string { return &c.Trigger.Webhook.IdempotencyHeader })},
		{"ENGI_WEBHOOK_ACTIVATION_PAGE_SIZE", "webhook-activation-page-size", "active workflows loaded per page at startup",
			intSetting(func(c *Config) *int { return &c.Trigger.Webhook.ActivationPageSize })},
		{"ENGI_SCHEDULE_DEFAULT_TIMEZONE", "schedule-default-timezone", "IANA zone of schedules that name none",
			stringSetting(func(c *Config) *string { return &c.Trigger.Schedule.DefaultTimezone })},
		{"ENGI_SCHEDULE_TICK", "schedule-tick", "how often the scheduler looks for due schedules",
			durationSetting(func(c *Config) *time.Duration { return &c.Trigger.Schedule.Tick })},
		{"ENGI_SCHEDULE_MISSED_POLICY", "schedule-missed-policy", "missed schedule runs: skip or runOnce",
			stringSetting(func(c *Config) *string { return &c.Trigger.Schedule.MissedPolicy })},
		{"ENGI_SCHEDULE_MISSED_GRACE", "schedule-missed-grace", "lateness after which a schedule run counts as missed",
			durationSetting(func(c *Config) *time.Duration { return &c.Trigger.Schedule.MissedGrace })},
		{"ENGI_LEASE_TYPE", "lease-type", "leader election provider: auto, local, postgres, kubernetes",
			stringSetting(func(c *Config) *string { return &c.Trigger.Lease.Type })},
		{"ENGI_LEASE_LOCK_KEY", "lease-lock-key", "Postgres advisory lock key of the leader election",
			func(c *Config, v string) (err error) {
				c.Trigger.Lease.LockKey, err = strconv.ParseInt(v, 10, 64)
				return
			}},
		{"ENGI_LEASE_NAME", "lease-name", "Kubernetes Lease object name",
			stringSetting(func(c *Config) *string { return &c.Trigger.Lease.Name })},
		{"ENGI_LEASE_NAMESPACE", "lease-namespace", "Kubernetes Lease namespace (empty: the pod's namespace file)",
			stringSetting(func(c *Config) *string { return &c.Trigger.Lease.Namespace })},
		{"ENGI_LEASE_IDENTITY", "lease-identity", "holder identity of this replica (empty: host name)",
			stringSetting(func(c *Config) *string { return &c.Trigger.Lease.Identity })},
		{"ENGI_LEASE_API_SERVER", "lease-api-server", "Kubernetes API base URL",
			stringSetting(func(c *Config) *string { return &c.Trigger.Lease.APIServer })},
		{"ENGI_LEASE_TOKEN_FILE", "lease-token-file", "service-account token file",
			stringSetting(func(c *Config) *string { return &c.Trigger.Lease.TokenFile })},
		{"ENGI_LEASE_CA_FILE", "lease-ca-file", "CA bundle of the Kubernetes API server",
			stringSetting(func(c *Config) *string { return &c.Trigger.Lease.CAFile })},
		{"ENGI_LEASE_NAMESPACE_FILE", "lease-namespace-file", "file holding the pod's namespace",
			stringSetting(func(c *Config) *string { return &c.Trigger.Lease.NamespaceFile })},
		{"ENGI_LEASE_DURATION", "lease-duration", "validity of a leader lease",
			durationSetting(func(c *Config) *time.Duration { return &c.Trigger.Lease.Duration })},
		{"ENGI_LEASE_RENEW_DEADLINE", "lease-renew-deadline", "how long the leader retries renewal before stepping down",
			durationSetting(func(c *Config) *time.Duration { return &c.Trigger.Lease.RenewDeadline })},
		{"ENGI_LEASE_RETRY_PERIOD", "lease-retry-period", "pause between lease acquire and renew attempts",
			durationSetting(func(c *Config) *time.Duration { return &c.Trigger.Lease.RetryPeriod })},
		{"ENGI_LISTEN_RETRY_DELAY", "listen-retry-delay", "pause before a failed listener trigger restarts",
			durationSetting(func(c *Config) *time.Duration { return &c.Trigger.Listen.RetryDelay })},
		{"ENGI_KAFKA_PAUSE_ABOVE_DEPTH", "kafka-pause-above-depth", "pause listeners while the job queue is deeper than this",
			intSetting(func(c *Config) *int { return &c.Trigger.Kafka.PauseAboveDepth })},
		{"ENGI_KAFKA_RESUME_BELOW_DEPTH", "kafka-resume-below-depth", "resume listeners once the job queue is shallower than this",
			intSetting(func(c *Config) *int { return &c.Trigger.Kafka.ResumeBelowDepth })},
		{"ENGI_KAFKA_DEPTH_CHECK_INTERVAL", "kafka-depth-check-interval", "longest time between queue depth samples",
			durationSetting(func(c *Config) *time.Duration { return &c.Trigger.Kafka.DepthCheckInterval })},
		{"ENGI_KAFKA_BATCH_MAX_MESSAGES", "kafka-batch-max-messages", "cap on the batch size of a Kafka trigger",
			intSetting(func(c *Config) *int { return &c.Trigger.Kafka.BatchMaxMessages })},
		{"ENGI_KAFKA_BATCH_MAX_WAIT", "kafka-batch-max-wait", "cap on how long a Kafka batch stays open",
			durationSetting(func(c *Config) *time.Duration { return &c.Trigger.Kafka.BatchMaxWait })},
		{"ENGI_KAFKA_SESSION_TIMEOUT", "kafka-session-timeout", "Kafka consumer group session timeout",
			durationSetting(func(c *Config) *time.Duration { return &c.Trigger.Kafka.SessionTimeout })},
		{"ENGI_KAFKA_REBALANCE_TIMEOUT", "kafka-rebalance-timeout", "Kafka consumer group rebalance timeout",
			durationSetting(func(c *Config) *time.Duration { return &c.Trigger.Kafka.RebalanceTimeout })},
		{"ENGI_KAFKA_MAX_POLL_RECORDS", "kafka-max-poll-records", "records taken from the Kafka client per poll",
			intSetting(func(c *Config) *int { return &c.Trigger.Kafka.MaxPollRecords })},
	})
}
