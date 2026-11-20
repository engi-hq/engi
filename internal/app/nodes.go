package app

import (
	"crypto/rand"
	"fmt"
	"io"
	"time"

	"github.com/engi-hq/engi/internal/adapter/plugin/pyrunner"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/node/aggregate"
	"github.com/engi-hq/engi/internal/node/aiagent"
	"github.com/engi-hq/engi/internal/node/aichat"
	"github.com/engi-hq/engi/internal/node/aiembeddings"
	"github.com/engi-hq/engi/internal/node/aimemorywindow"
	"github.com/engi-hq/engi/internal/node/aimodel"
	"github.com/engi-hq/engi/internal/node/airtable"
	"github.com/engi-hq/engi/internal/node/aitextsplitter"
	"github.com/engi-hq/engi/internal/node/aitoolcode"
	"github.com/engi-hq/engi/internal/node/aitoolhttp"
	"github.com/engi-hq/engi/internal/node/aitoolmcp"
	"github.com/engi-hq/engi/internal/node/aitoolvectorsearch"
	"github.com/engi-hq/engi/internal/node/aitoolworkflow"
	"github.com/engi-hq/engi/internal/node/aivectorstore"
	"github.com/engi-hq/engi/internal/node/chatkit"
	"github.com/engi-hq/engi/internal/node/chattrigger"
	"github.com/engi-hq/engi/internal/node/code"
	"github.com/engi-hq/engi/internal/node/codepython"
	"github.com/engi-hq/engi/internal/node/emailsend"
	"github.com/engi-hq/engi/internal/node/errortrigger"
	"github.com/engi-hq/engi/internal/node/executeworkflow"
	"github.com/engi-hq/engi/internal/node/executeworkflowtrigger"
	"github.com/engi-hq/engi/internal/node/filter"
	"github.com/engi-hq/engi/internal/node/googlesheets"
	"github.com/engi-hq/engi/internal/node/httprequest"
	"github.com/engi-hq/engi/internal/node/ifnode"
	"github.com/engi-hq/engi/internal/node/imaptrigger"
	kafkanode "github.com/engi-hq/engi/internal/node/kafka"
	"github.com/engi-hq/engi/internal/node/limit"
	"github.com/engi-hq/engi/internal/node/manualtrigger"
	"github.com/engi-hq/engi/internal/node/mcpservertrigger"
	"github.com/engi-hq/engi/internal/node/merge"
	"github.com/engi-hq/engi/internal/node/mongodb"
	"github.com/engi-hq/engi/internal/node/mysql"
	"github.com/engi-hq/engi/internal/node/noop"
	"github.com/engi-hq/engi/internal/node/postgres"
	"github.com/engi-hq/engi/internal/node/ragkit"
	"github.com/engi-hq/engi/internal/node/redis"
	"github.com/engi-hq/engi/internal/node/registry"
	"github.com/engi-hq/engi/internal/node/respondtowebhook"
	s3node "github.com/engi-hq/engi/internal/node/s3"
	"github.com/engi-hq/engi/internal/node/scheduletrigger"
	"github.com/engi-hq/engi/internal/node/set"
	"github.com/engi-hq/engi/internal/node/slack"
	"github.com/engi-hq/engi/internal/node/sortnode"
	"github.com/engi-hq/engi/internal/node/splitout"
	"github.com/engi-hq/engi/internal/node/sqlnode"
	"github.com/engi-hq/engi/internal/node/switchnode"
	"github.com/engi-hq/engi/internal/node/teams"
	"github.com/engi-hq/engi/internal/node/telegram"
	"github.com/engi-hq/engi/internal/node/wait"
	"github.com/engi-hq/engi/internal/node/webhooktrigger"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// nowFunc is the clock the nodes need.
type nowFunc interface{ Now() time.Time }

// RegistryOption customizes NewRegistry.
type RegistryOption func(*registryOptions)

type registryOptions struct {
	subworkflows nodesdk.SubworkflowRunner
	maxDepth     int
	timezone     string
	dbPools      sqlnode.Pools
	redisClients redis.Clients
	mongoClients mongodb.Clients
	kafkaClients kafkanode.Clients
	s3Clients    s3node.Clients
	kafkaTrigger config.KafkaTriggerConfig
	llmFactory   port.LLMFactory
	chatMemory   port.ChatMemoryStore
	embedFactory port.EmbedderFactory
}

// defaultScheduleTimezone applies when WithScheduleTimezone is not used.
const defaultScheduleTimezone = "UTC"

// WithScheduleTimezone sets the timezone the Schedule trigger defaults to
// (schedule.defaultTimezone).
func WithScheduleTimezone(tz string) RegistryOption {
	return func(o *registryOptions) { o.timezone = tz }
}

// WithDBPools gives the Postgres and MySQL nodes the connection pools they run
// on. Without it they fail at run time with nodesdk.ErrUnavailable.
func WithDBPools(p sqlnode.Pools) RegistryOption {
	return func(o *registryOptions) { o.dbPools = p }
}

// WithRedisClients gives the Redis node its shared clients. Without it the node
// fails at run time with nodesdk.ErrUnavailable.
func WithRedisClients(c redis.Clients) RegistryOption {
	return func(o *registryOptions) { o.redisClients = c }
}

// WithMongoClients gives the MongoDB node its shared clients. Without it the
// node fails at run time with nodesdk.ErrUnavailable.
func WithMongoClients(c mongodb.Clients) RegistryOption {
	return func(o *registryOptions) { o.mongoClients = c }
}

// WithS3Clients gives the S3 node its client source.
// Without it the node fails at run time with nodesdk.ErrUnavailable.
func WithS3Clients(c s3node.Clients) RegistryOption {
	return func(o *registryOptions) { o.s3Clients = c }
}

// WithKafkaClients gives the Kafka trigger and producer their client source.
// Without it they fail at run time with nodesdk.ErrUnavailable.
func WithKafkaClients(c kafkanode.Clients) RegistryOption {
	return func(o *registryOptions) { o.kafkaClients = c }
}

// WithKafkaTrigger sets the Kafka trigger limits (the top-level kafka config section). The
// defaults apply without it.
func WithKafkaTrigger(c config.KafkaTriggerConfig) RegistryOption {
	return func(o *registryOptions) { o.kafkaTrigger = c }
}

// WithChatMemory gives the Window Memory node the store that keeps chat
// messages. Without it the node fails at run time with nodesdk.ErrUnavailable.
func WithChatMemory(s port.ChatMemoryStore) RegistryOption {
	return func(o *registryOptions) { o.chatMemory = s }
}

// WithLLMFactory gives the AI nodes the factory that builds model clients from
// credentials. Without it they fail at run time with nodesdk.ErrUnavailable.
func WithLLMFactory(f port.LLMFactory) RegistryOption {
	return func(o *registryOptions) { o.llmFactory = f }
}

// WithEmbedderFactory gives the Embeddings node the factory that builds
// embedding clients from credentials. Without it the node fails at run time
// with nodesdk.ErrUnavailable.
func WithEmbedderFactory(f port.EmbedderFactory) RegistryOption {
	return func(o *registryOptions) { o.embedFactory = f }
}

// WithSubworkflows gives the Execute Workflow node the runner it starts child
// executions with, and the nesting limit (engine.maxSubworkflowDepth). Without
// it the node fails at run time with nodesdk.ErrUnavailable.
func WithSubworkflows(r nodesdk.SubworkflowRunner, maxDepth int) RegistryOption {
	return func(o *registryOptions) { o.subworkflows, o.maxDepth = r, maxDepth }
}

// NewRegistry registers every built-in node. The list is explicit on purpose:
// adding a node is one line here, and nothing registers itself.
func NewRegistry(cfg config.NodesConfig, clock nowFunc, opts ...RegistryOption) (*registry.Registry, error) {
	o := registryOptions{subworkflows: unavailableSubworkflows{}, timezone: defaultScheduleTimezone, kafkaTrigger: config.Default().Trigger.Kafka}
	for _, opt := range opts {
		opt(&o)
	}
	chatCaller := chatkit.Caller{Limits: chatkit.Limits{MaxRetryWait: cfg.Chat.MaxRetryWait, MaxRetries: cfg.Chat.MaxRetries}}
	factories := []registry.Factory{
		manualtrigger.New,
		noop.New,
		set.New,
		ifnode.New,
		func() nodesdk.Node { return switchnode.New(cfg.Switch.MaxRules) },
		func() nodesdk.Node { return merge.New(cfg.Merge.MaxInputs) },
		filter.New,
		limit.New,
		sortnode.New,
		splitout.New,
		aggregate.New,
		func() nodesdk.Node { return wait.New(clock) },
		func() nodesdk.Node {
			return code.New(code.Limits{
				Timeout: cfg.Code.Timeout, MaxCallStackDepth: cfg.Code.MaxCallStackDepth, MaxOutputBytes: cfg.Code.MaxOutputBytes,
			}, clock)
		},
		httprequest.New,
		func() nodesdk.Node { return executeworkflow.New(o.subworkflows, o.maxDepth) },
		executeworkflowtrigger.New,
		errortrigger.New,
		webhooktrigger.New,
		mcpservertrigger.New,
		func() nodesdk.Node {
			m := cfg.MCP
			return aitoolmcp.New(aitoolmcp.Config{Limits: aitoolmcp.Limits{
				RequestTimeout: m.RequestTimeout, MaxResponseBytes: m.MaxResponseBytes, MaxListPages: m.MaxListPages,
				MaxTools: m.MaxTools, MaxResultBytes: m.MaxResultBytes,
			}})
		},
		chattrigger.New,
		respondtowebhook.New,
		func() nodesdk.Node { return scheduletrigger.New(o.timezone) },
		func() nodesdk.Node { return postgres.New(o.dbPools) },
		func() nodesdk.Node { return mysql.New(o.dbPools) },
		func() nodesdk.Node { return redis.New(o.redisClients, cfg.Database.RedisMaxKeys) },
		func() nodesdk.Node { return mongodb.New(o.mongoClients, cfg.Database.MongoMaxResults) },
		func() nodesdk.Node {
			return s3node.New(o.s3Clients, s3node.Limits{
				MaxKeys: cfg.S3.MaxKeys, MaxObjectBytes: int64(cfg.S3.MaxObjectBytes), MaxUploadBytes: int64(cfg.S3.MaxUploadBytes),
				PresignMaxExpiry: cfg.S3.PresignMaxExpiry, RequestTimeout: cfg.S3.RequestTimeout,
			})
		},
		func() nodesdk.Node {
			return kafkanode.NewTrigger(o.kafkaClients, kafkanode.TriggerLimits{
				BatchMaxMessages: o.kafkaTrigger.BatchMaxMessages, BatchMaxWait: o.kafkaTrigger.BatchMaxWait,
				SessionTimeout: o.kafkaTrigger.SessionTimeout, RebalanceTimeout: o.kafkaTrigger.RebalanceTimeout,
				MaxPollRecords: o.kafkaTrigger.MaxPollRecords, PollInterval: o.kafkaTrigger.DepthCheckInterval,
			})
		},
		func() nodesdk.Node {
			e := cfg.Email
			return emailsend.New(emailsend.Limits{ConnectTimeout: e.ConnectTimeout, SendTimeout: e.SendTimeout, MaxRecipients: e.MaxRecipients, MaxMessageBytes: e.MaxMessageBytes},
				clock, cryptoRand())
		},
		func() nodesdk.Node {
			e := cfg.Email
			return imaptrigger.New(imaptrigger.Limits{ConnectTimeout: e.ConnectTimeout, Timeout: e.ImapTimeout, MaxMessages: e.ImapMaxMessages, MaxMessageBytes: e.ImapMaxMessageBytes})
		},
		func() nodesdk.Node {
			return kafkanode.NewProducer(o.kafkaClients, kafkanode.ProduceLimits{MaxRecordBytes: cfg.Kafka.MaxRecordBytes, Timeout: cfg.Kafka.ProduceTimeout})
		},
		func() nodesdk.Node {
			return slack.New(slack.Config{BaseURL: cfg.Chat.SlackBaseURL, MaxListResults: cfg.Chat.MaxListResults, PageSize: cfg.Chat.PageSize, Caller: chatCaller})
		},
		func() nodesdk.Node {
			return telegram.New(telegram.Config{BaseURL: cfg.Chat.TelegramBaseURL, Caller: chatCaller})
		},
		func() nodesdk.Node {
			g := cfg.Google
			return googlesheets.New(googlesheets.Config{
				BaseURL: g.SheetsBaseURL, MaxReadRows: g.MaxReadRows, Now: clock.Now,
				Caller: chatkit.Caller{Limits: chatkit.Limits{MaxRetryWait: g.MaxRetryWait, MaxRetries: g.MaxRetries}},
			})
		},
		func() nodesdk.Node {
			a := cfg.Airtable
			return airtable.New(airtable.Config{
				BaseURL: a.BaseURL, MaxListResults: a.MaxListResults, PageSize: a.PageSize, BatchSize: a.BatchSize,
				Caller: chatkit.Caller{Limits: chatkit.Limits{MaxRetryWait: a.MaxRetryWait, MaxRetries: a.MaxRetries}},
			})
		},
		func() nodesdk.Node {
			return teams.New(teams.Config{GraphBaseURL: cfg.Chat.GraphBaseURL, Caller: chatCaller})
		},
		func() nodesdk.Node {
			return aimodel.New(aimodel.Config{Factory: o.llmFactory, DefaultModels: defaultModels(cfg.AI)})
		},
		func() nodesdk.Node {
			m := cfg.AI.Memory
			return aimemorywindow.New(aimemorywindow.Config{Store: o.chatMemory, DefaultWindow: m.DefaultWindow, MaxWindow: m.MaxWindow})
		},
		func() nodesdk.Node {
			a := cfg.AI.Agent
			return aiagent.New(aiagent.Config{Clock: clock, Limits: aiagent.Limits{
				MaxIterations: a.MaxIterations, MaxTotalTokens: a.MaxTotalTokens, Timeout: a.Timeout,
				ToolConcurrency: a.ToolConcurrency, MaxToolResultBytes: a.MaxToolResultBytes,
			}})
		},
		func() nodesdk.Node {
			return aitoolworkflow.New(aitoolworkflow.Config{Runner: o.subworkflows, MaxDepth: o.maxDepth, Timeout: cfg.AI.Tool.WorkflowTimeout})
		},
		func() nodesdk.Node {
			return aitoolhttp.New(aitoolhttp.Config{Timeout: cfg.AI.Tool.HTTPTimeout, MaxResponseBytes: cfg.AI.Tool.HTTPMaxResponseBytes})
		},
		func() nodesdk.Node {
			return aitoolcode.New(aitoolcode.Config{Limits: code.Limits{
				Timeout: cfg.Code.Timeout, MaxCallStackDepth: cfg.Code.MaxCallStackDepth, MaxOutputBytes: cfg.Code.MaxOutputBytes,
			}})
		},
		func() nodesdk.Node {
			return aichat.New(aichat.Config{Factory: o.llmFactory, DefaultModels: map[string]string{
				port.LLMProviderAnthropic: cfg.AI.AnthropicDefaultModel, port.LLMProviderOpenAI: cfg.AI.OpenAIDefaultModel,
				port.LLMProviderOllama: cfg.AI.OllamaDefaultModel,
			}})
		},
		aitextsplitter.New,
		func() nodesdk.Node {
			return aiembeddings.New(aiembeddings.Config{Factory: o.embedFactory, DefaultModels: map[string]string{
				port.LLMProviderOpenAI: cfg.AI.RAG.OpenAIEmbeddingModel, port.LLMProviderOllama: cfg.AI.RAG.OllamaEmbeddingModel,
			}})
		},
		func() nodesdk.Node {
			return aivectorstore.New(aivectorstore.Config{Pools: o.dbPools, Limits: ragLimits(cfg.AI.RAG)})
		},
		func() nodesdk.Node {
			return aitoolvectorsearch.New(aitoolvectorsearch.Config{Pools: o.dbPools, Limits: ragLimits(cfg.AI.RAG)})
		},
	}
	if py := cfg.Code.Python; py.Enabled {
		runner := pyrunner.New(pyrunner.Config{Interpreter: py.Interpreter})
		limits := codepython.Limits{Timeout: cfg.Code.Timeout, MaxOutputBytes: cfg.Code.MaxOutputBytes, AllowedModules: py.AllowedModules}
		factories = append(factories, func() nodesdk.Node { return codepython.New(runner, limits) })
	}
	reg := registry.New()
	for _, f := range factories {
		if err := reg.Register(f); err != nil {
			return nil, fmt.Errorf("registering built-in nodes: %w", err)
		}
	}
	return reg, nil
}

func cryptoRand() io.Reader { return rand.Reader }

// ragLimits maps the nodes.ai.rag settings to the retrieval nodes' limits.
func ragLimits(c config.AIRAGConfig) ragkit.Limits {
	return ragkit.Limits{MaxTopK: c.MaxTopK, InsertBatchSize: c.InsertBatchSize, MaxDeleteIDs: c.MaxDeleteIDs}
}

// defaultModels maps each provider to the model used when a node sets none.
func defaultModels(c config.AINodeConfig) map[string]string {
	return map[string]string{
		port.LLMProviderAnthropic: c.AnthropicDefaultModel, port.LLMProviderOpenAI: c.OpenAIDefaultModel,
		port.LLMProviderOllama: c.OllamaDefaultModel,
	}
}
