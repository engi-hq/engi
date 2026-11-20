package config

import (
	"errors"
	"strings"
	"time"
)

// NodesConfig tunes built-in nodes.
type NodesConfig struct {
	Code   CodeNodeConfig   `yaml:"code"`
	Switch SwitchNodeConfig `yaml:"switch"`
	Merge  MergeNodeConfig  `yaml:"merge"`
	// Database sizes the connection pools of the SQL nodes.
	Database DatabaseNodeConfig `yaml:"database"`
	// Kafka tunes the producer node and its shared clients.
	Kafka KafkaNodeConfig `yaml:"kafka"`
	// Email bounds the Send Email node and the IMAP trigger.
	Email EmailNodeConfig `yaml:"email"`
	// Chat tunes the Slack, Telegram and Teams nodes.
	Chat ChatNodeConfig `yaml:"chat"`
	// Google tunes the Google Sheets node.
	Google GoogleNodeConfig `yaml:"google"`
	// Airtable tunes the Airtable node.
	Airtable AirtableNodeConfig `yaml:"airtable"`
	// S3 bounds the S3 storage node and its shared clients.
	S3 S3NodeConfig `yaml:"s3"`
	// AI tunes the AI nodes and the model providers behind them.
	AI AINodeConfig `yaml:"ai"`
	// MCP tunes the MCP client tool.
	MCP MCPClientConfig `yaml:"mcp"`
}

// S3NodeConfig bounds the S3 storage node.
type S3NodeConfig struct {
	// MaxKeys caps the objects one list operation returns.
	MaxKeys int `yaml:"maxKeys"`
	// MaxObjectBytes caps an object read into an item as text or JSON.
	MaxObjectBytes int `yaml:"maxObjectBytes"`
	// MaxUploadBytes caps one uploaded body (buffered in memory to sign it).
	MaxUploadBytes int `yaml:"maxUploadBytes"`
	// PresignMaxExpiry caps the lifetime of a presigned URL.
	PresignMaxExpiry time.Duration `yaml:"presignMaxExpiry"`
	// ConnectTimeout bounds establishing one connection.
	ConnectTimeout time.Duration `yaml:"connectTimeout"`
	// RequestTimeout bounds one S3 request.
	RequestTimeout time.Duration `yaml:"requestTimeout"`
}

// KafkaNodeConfig bounds the Kafka producer node and the connections shared
// by the Kafka nodes.
type KafkaNodeConfig struct {
	// ConnectTimeout bounds establishing one broker connection.
	ConnectTimeout time.Duration `yaml:"connectTimeout"`
	// ProduceTimeout bounds producing one batch of records.
	ProduceTimeout time.Duration `yaml:"produceTimeout"`
	// FlushTimeout bounds flushing buffered records when a producer closes.
	FlushTimeout time.Duration `yaml:"flushTimeout"`
	// MaxRecordBytes rejects a produced record whose key plus value is larger.
	MaxRecordBytes int `yaml:"maxRecordBytes"`
}

// DatabaseNodeConfig bounds the per-credential connection pools shared by the
// Postgres and MySQL nodes.
type DatabaseNodeConfig struct {
	// MaxOpenConns caps open connections per pool.
	MaxOpenConns int `yaml:"maxOpenConns"`
	// MaxIdleConns caps idle connections kept per pool.
	MaxIdleConns int `yaml:"maxIdleConns"`
	// IdleTimeout closes connections idle for this long.
	IdleTimeout time.Duration `yaml:"idleTimeout"`
	// MaxLifetime recycles connections older than this.
	MaxLifetime time.Duration `yaml:"maxLifetime"`
	// ConnectTimeout bounds establishing one connection.
	ConnectTimeout time.Duration `yaml:"connectTimeout"`
	// RedisMaxKeys caps the keys the Redis "keys" operation returns.
	RedisMaxKeys int `yaml:"redisMaxKeys"`
	// MongoMaxResults caps the documents one MongoDB find or aggregate returns.
	MongoMaxResults int `yaml:"mongoMaxResults"`
}

// CodeNodeConfig bounds the JavaScript Code node sandbox.
type CodeNodeConfig struct {
	// Timeout bounds one run of user code (all items, or one item).
	Timeout time.Duration `yaml:"timeout"`
	// MaxCallStackDepth bounds JavaScript recursion depth.
	MaxCallStackDepth int `yaml:"maxCallStackDepth"`
	// MaxOutputBytes bounds the serialized size of the returned items.
	MaxOutputBytes int `yaml:"maxOutputBytes"`
	// Python tunes the Python Code node. It also uses Timeout and
	// MaxOutputBytes above.
	Python PythonCodeConfig `yaml:"python"`
}

// PythonCodeConfig tunes the Python Code node, which runs user code in a fresh
// interpreter process per run.
type PythonCodeConfig struct {
	// Enabled registers the Python Code node.
	Enabled bool `yaml:"enabled"`
	// Interpreter is the Python 3 executable, looked up on PATH when it has no
	// directory part.
	Interpreter string `yaml:"interpreter"`
	// AllowedModules lists the modules user code may import. An entry allows
	// the module of that exact dotted name and its submodules ("urllib.parse"
	// allows urllib.parse and urllib.parse.x, not urllib).
	AllowedModules []string `yaml:"allowedModules"`
}

// defaultPythonAllowedModules is a conservative, side-effect-free slice of the
// standard library: no filesystem, process, network or reflection modules.
func defaultPythonAllowedModules() []string {
	return []string{
		"json", "math", "re", "datetime", "time", "random", "statistics", "itertools", "functools",
		"collections", "string", "decimal", "fractions", "uuid", "hashlib", "base64", "textwrap",
		"unicodedata", "operator", "copy", "typing", "dataclasses", "enum", "html", "urllib.parse",
	}
}

// SwitchNodeConfig sizes the Switch node.
type SwitchNodeConfig struct {
	// MaxRules is the number of rule outputs a Switch node declares. The
	// fallback output follows them.
	MaxRules int `yaml:"maxRules"`
}

// MergeNodeConfig sizes the Merge node.
type MergeNodeConfig struct {
	// MaxInputs is the number of inputs a Merge node declares.
	MaxInputs int `yaml:"maxInputs"`
}

func defaultNodes() NodesConfig {
	return NodesConfig{
		Code: CodeNodeConfig{Timeout: 10 * time.Second, MaxCallStackDepth: 256, MaxOutputBytes: 8 << 20,
			Python: PythonCodeConfig{Enabled: true, Interpreter: "python3", AllowedModules: defaultPythonAllowedModules()}},
		Switch: SwitchNodeConfig{MaxRules: 8},
		Merge:  MergeNodeConfig{MaxInputs: 2},
		Database: DatabaseNodeConfig{
			MaxOpenConns: 10, MaxIdleConns: 2, IdleTimeout: 5 * time.Minute, MaxLifetime: 30 * time.Minute, ConnectTimeout: 10 * time.Second,
			RedisMaxKeys: 1000, MongoMaxResults: 1000,
		},
		Kafka: KafkaNodeConfig{ConnectTimeout: 10 * time.Second, ProduceTimeout: 30 * time.Second, FlushTimeout: 10 * time.Second, MaxRecordBytes: 1 << 20},
		Email: defaultEmail(),
		S3: S3NodeConfig{MaxKeys: 1000, MaxObjectBytes: 1 << 20, MaxUploadBytes: 64 << 20, PresignMaxExpiry: 24 * time.Hour,
			ConnectTimeout: 10 * time.Second, RequestTimeout: 60 * time.Second},
		Chat:   defaultChat(),
		Google: defaultGoogle(), Airtable: defaultAirtable(),
		AI:  defaultAI(),
		MCP: defaultMCPClient(),
	}
}

// Validate reports every problem with the node settings.
func (c NodesConfig) Validate() error {
	var errs []error
	errs = appendPositive(errs, "nodes.code.timeout", int64(c.Code.Timeout))
	errs = appendPositive(errs, "nodes.code.maxCallStackDepth", int64(c.Code.MaxCallStackDepth))
	errs = appendPositive(errs, "nodes.code.maxOutputBytes", int64(c.Code.MaxOutputBytes))
	if c.Code.Python.Enabled && strings.TrimSpace(c.Code.Python.Interpreter) == "" {
		errs = append(errs, errors.New("nodes.code.python.interpreter must not be empty when nodes.code.python.enabled is true"))
	}
	for _, m := range c.Code.Python.AllowedModules {
		if strings.TrimSpace(m) == "" {
			errs = append(errs, errors.New("nodes.code.python.allowedModules must not contain empty names"))
			break
		}
	}
	errs = appendPositive(errs, "nodes.switch.maxRules", int64(c.Switch.MaxRules))
	errs = appendPositive(errs, "nodes.merge.maxInputs", int64(c.Merge.MaxInputs))
	errs = appendPositive(errs, "nodes.database.maxOpenConns", int64(c.Database.MaxOpenConns))
	errs = appendPositive(errs, "nodes.database.maxIdleConns", int64(c.Database.MaxIdleConns))
	errs = appendPositive(errs, "nodes.database.idleTimeout", int64(c.Database.IdleTimeout))
	errs = appendPositive(errs, "nodes.database.maxLifetime", int64(c.Database.MaxLifetime))
	errs = appendPositive(errs, "nodes.database.connectTimeout", int64(c.Database.ConnectTimeout))
	errs = appendPositive(errs, "nodes.database.redisMaxKeys", int64(c.Database.RedisMaxKeys))
	errs = appendPositive(errs, "nodes.database.mongoMaxResults", int64(c.Database.MongoMaxResults))
	errs = appendPositive(errs, "nodes.kafka.connectTimeout", int64(c.Kafka.ConnectTimeout))
	errs = appendPositive(errs, "nodes.kafka.produceTimeout", int64(c.Kafka.ProduceTimeout))
	errs = appendPositive(errs, "nodes.kafka.flushTimeout", int64(c.Kafka.FlushTimeout))
	errs = appendPositive(errs, "nodes.kafka.maxRecordBytes", int64(c.Kafka.MaxRecordBytes))
	errs = appendPositive(errs, "nodes.s3.maxKeys", int64(c.S3.MaxKeys))
	errs = appendPositive(errs, "nodes.s3.maxObjectBytes", int64(c.S3.MaxObjectBytes))
	errs = appendPositive(errs, "nodes.s3.maxUploadBytes", int64(c.S3.MaxUploadBytes))
	errs = appendPositive(errs, "nodes.s3.presignMaxExpiry", int64(c.S3.PresignMaxExpiry))
	errs = appendPositive(errs, "nodes.s3.connectTimeout", int64(c.S3.ConnectTimeout))
	errs = appendPositive(errs, "nodes.s3.requestTimeout", int64(c.S3.RequestTimeout))
	errs = c.Email.validate(errs)
	if c.Database.MaxIdleConns > c.Database.MaxOpenConns {
		errs = append(errs, errors.New("nodes.database.maxIdleConns must not exceed nodes.database.maxOpenConns"))
	}
	if c.Merge.MaxInputs == 1 {
		errs = append(errs, errors.New("nodes.merge.maxInputs must be at least 2"))
	}
	errs = append(errs, c.Chat.Validate(), c.AI.Validate(), c.Google.Validate(), c.Airtable.Validate())
	errs = c.MCP.validate(errs)
	return errors.Join(errs...)
}

// nodeSettings are the env/flag bindings of the nodes section.
func nodeSettings() []setting {
	return []setting{
		{"ENGI_NODES_CODE_TIMEOUT", "nodes-code-timeout", "Code node run time limit",
			durationSetting(func(c *Config) *time.Duration { return &c.Nodes.Code.Timeout })},
		{"ENGI_NODES_CODE_MAX_CALL_STACK_DEPTH", "nodes-code-max-call-stack-depth", "Code node recursion limit",
			intSetting(func(c *Config) *int { return &c.Nodes.Code.MaxCallStackDepth })},
		{"ENGI_NODES_CODE_MAX_OUTPUT_BYTES", "nodes-code-max-output-bytes", "Code node max result bytes",
			intSetting(func(c *Config) *int { return &c.Nodes.Code.MaxOutputBytes })},
		{"ENGI_NODES_CODE_PYTHON_ENABLED", "nodes-code-python-enabled", "enable the Python Code node",
			boolSetting(func(c *Config) *bool { return &c.Nodes.Code.Python.Enabled })},
		{"ENGI_NODES_CODE_PYTHON_INTERPRETER", "nodes-code-python-interpreter", "Python 3 executable for the Python Code node",
			stringSetting(func(c *Config) *string { return &c.Nodes.Code.Python.Interpreter })},
		{"ENGI_NODES_CODE_PYTHON_ALLOWED_MODULES", "nodes-code-python-allowed-modules", "comma-separated modules the Python Code node may import",
			listSetting(func(c *Config) *[]string { return &c.Nodes.Code.Python.AllowedModules })},
		{"ENGI_NODES_SWITCH_MAX_RULES", "nodes-switch-max-rules", "rule outputs of the Switch node",
			intSetting(func(c *Config) *int { return &c.Nodes.Switch.MaxRules })},
		{"ENGI_NODES_MERGE_MAX_INPUTS", "nodes-merge-max-inputs", "inputs of the Merge node",
			intSetting(func(c *Config) *int { return &c.Nodes.Merge.MaxInputs })},
		{"ENGI_NODES_DATABASE_MAX_OPEN_CONNS", "nodes-database-max-open-conns", "max open connections per database pool",
			intSetting(func(c *Config) *int { return &c.Nodes.Database.MaxOpenConns })},
		{"ENGI_NODES_DATABASE_MAX_IDLE_CONNS", "nodes-database-max-idle-conns", "max idle connections per database pool",
			intSetting(func(c *Config) *int { return &c.Nodes.Database.MaxIdleConns })},
		{"ENGI_NODES_DATABASE_IDLE_TIMEOUT", "nodes-database-idle-timeout", "close database connections idle this long",
			durationSetting(func(c *Config) *time.Duration { return &c.Nodes.Database.IdleTimeout })},
		{"ENGI_NODES_DATABASE_MAX_LIFETIME", "nodes-database-max-lifetime", "recycle database connections after this long",
			durationSetting(func(c *Config) *time.Duration { return &c.Nodes.Database.MaxLifetime })},
		{"ENGI_NODES_DATABASE_CONNECT_TIMEOUT", "nodes-database-connect-timeout", "database connect timeout",
			durationSetting(func(c *Config) *time.Duration { return &c.Nodes.Database.ConnectTimeout })},
		{"ENGI_NODES_DATABASE_REDIS_MAX_KEYS", "nodes-database-redis-max-keys", "max keys the Redis keys operation returns",
			intSetting(func(c *Config) *int { return &c.Nodes.Database.RedisMaxKeys })},
		{"ENGI_NODES_DATABASE_MONGO_MAX_RESULTS", "nodes-database-mongo-max-results", "max documents one MongoDB find or aggregate returns",
			intSetting(func(c *Config) *int { return &c.Nodes.Database.MongoMaxResults })},
		{"ENGI_NODES_KAFKA_CONNECT_TIMEOUT", "nodes-kafka-connect-timeout", "Kafka broker connect timeout",
			durationSetting(func(c *Config) *time.Duration { return &c.Nodes.Kafka.ConnectTimeout })},
		{"ENGI_NODES_KAFKA_PRODUCE_TIMEOUT", "nodes-kafka-produce-timeout", "Kafka producer node time limit per run",
			durationSetting(func(c *Config) *time.Duration { return &c.Nodes.Kafka.ProduceTimeout })},
		{"ENGI_NODES_KAFKA_FLUSH_TIMEOUT", "nodes-kafka-flush-timeout", "time to flush a Kafka producer on shutdown",
			durationSetting(func(c *Config) *time.Duration { return &c.Nodes.Kafka.FlushTimeout })},
		{"ENGI_NODES_KAFKA_MAX_RECORD_BYTES", "nodes-kafka-max-record-bytes", "largest Kafka record the producer node sends",
			intSetting(func(c *Config) *int { return &c.Nodes.Kafka.MaxRecordBytes })},
		{"ENGI_NODES_S3_MAX_KEYS", "nodes-s3-max-keys", "max objects one S3 list returns",
			intSetting(func(c *Config) *int { return &c.Nodes.S3.MaxKeys })},
		{"ENGI_NODES_S3_MAX_OBJECT_BYTES", "nodes-s3-max-object-bytes", "largest S3 object read into an item as text or JSON",
			intSetting(func(c *Config) *int { return &c.Nodes.S3.MaxObjectBytes })},
		{"ENGI_NODES_S3_MAX_UPLOAD_BYTES", "nodes-s3-max-upload-bytes", "largest body the S3 node uploads",
			intSetting(func(c *Config) *int { return &c.Nodes.S3.MaxUploadBytes })},
		{"ENGI_NODES_S3_PRESIGN_MAX_EXPIRY", "nodes-s3-presign-max-expiry", "longest lifetime of an S3 presigned URL",
			durationSetting(func(c *Config) *time.Duration { return &c.Nodes.S3.PresignMaxExpiry })},
		{"ENGI_NODES_S3_CONNECT_TIMEOUT", "nodes-s3-connect-timeout", "S3 connect timeout",
			durationSetting(func(c *Config) *time.Duration { return &c.Nodes.S3.ConnectTimeout })},
		{"ENGI_NODES_S3_REQUEST_TIMEOUT", "nodes-s3-request-timeout", "S3 request time limit",
			durationSetting(func(c *Config) *time.Duration { return &c.Nodes.S3.RequestTimeout })},
	}
}
