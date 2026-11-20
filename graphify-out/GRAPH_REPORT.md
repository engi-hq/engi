# Graph Report - engi-hq-engi  (2026-11-20)

## Corpus Check
- 844 files · ~531,930 words
- Verdict: corpus is large enough that graph structure adds value.
- Unclassified: 19 file(s) not represented in the graph (top: (none) 14, .css 4, .tpl 1)

## Summary
- 8545 nodes · 47189 edges · 315 communities (271 shown, 44 thin omitted)
- Extraction: 97% EXTRACTED · 3% INFERRED · 0% AMBIGUOUS · INFERRED: 1497 edges (avg confidence: 0.85)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `68c50031`
- Run `git rev-parse HEAD` and compare to check if the graph is stale.
- Run `graphify update .` after code changes (no API cost).

## Community Hubs (Navigation)
- golden_test.go
- Evaluator
- Default
- EditorPage.tsx
- Event
- PageResult
- newEnv
- New
- DecodeWorkflow
- t
- Stamp
- Queue
- Descriptor
- Task
- newRuntime
- types.ts
- store_suite.go
- .add
- mime.go
- server
- LLMMessage
- Executions
- package.json
- Provider
- github.com/engi-hq/engi
- Variable
- newEnv
- aiagent_test.go
- TypeName
- AuthConfig
- NewConn
- newEnv
- compilerOptions
- .New
- database/sql.DB
- subports.test.tsx
- testing.T
- client.ts
- Tool
- devDependencies
- UserID
- cronexpr.go
- sseTransport
- NodeDescriptor
- appendPositive
- Store
- runner
- TagID
- State
- Worker
- Client
- Item
- time.Duration
- result.go
- useRunner.ts
- Insert
- loadtest/main.go
- Runtime
- BlobStore
- template.go
- propertyDTO
- ExecuteParams
- WorkflowSettings.tsx
- encoding/json.RawMessage
- .run
- context.Context
- Input
- server_api_test.go
- CredentialType
- startStdio
- API
- conn
- database/sql.Result
- cache.go
- schema.d.ts
- Tracer
- net/http.ResponseWriter
- redis/run.go
- dependencies
- runner.py
- io.Reader
- newEnv
- LLM
- supervisor_test.go
- Context
- net/http.Request
- queueEnv
- .oauthData
- Run
- CredentialID
- Timeline.tsx
- subports.test.tsx
- plainHasher
- runner
- NoteCard.tsx
- runWF
- ExecutionID
- Note
- parse
- User
- rig
- ExecutionStatus
- ToolInfo
- go_pkg_context
- github.com/engi-hq/engi/web
- plugin.py
- ChatMemoryStore
- LLMRequest
- FakeHost
- App
- Deps
- Item
- net/http.Response
- runner
- newFixture
- Route
- connectors.go
- imaptrigger/settings.go
- WorkflowID
- poller
- widget.js
- BlobRef
- ui/ui_test.go
- RenderCredentialTypes
- .fetch
- PortKind
- net/http/httptest.Server
- Recursive
- Plugin
- check.sh
- Credential
- Workflow
- layout.ts
- github.com/dop251/goja.Value
- io.Writer
- StdioSession
- mcp/client_test.go
- setting
- EmbedResult
- JsonNode
- _HostLogHandler
- ArmedTestWebhook
- Engi.Plugin
- nopSpan
- JsonObject
- Execution
- Supervisor
- smoke.sh
- Listener
- src/types.ts
- anthropic_test.go
- .streamExecution
- Plugin
- fakeAPI
- NewServer
- server
- index.ts
- run.sh
- time.Time
- WorkerConfig
- StdioTest
- protocol.go
- ParamString
- Item
- .Execute
- build_plugin
- sync.Mutex
- nodeInput
- FromDescriptor
- Store
- logSink
- RpcError
- input
- WebhookRegistration
- HTTPClient
- kafka/settings.go
- ServerConfig
- stdio.test.ts
- context.ts
- Options
- Send
- ResponseFormat
- typescript/package.json
- WebhookSpec
- engi/main_test.go
- NopObserver
- New
- .ServeHTTP
- engi-loadtest
- .RunSubworkflow
- Executions
- FormatTime
- Property
- lineLogger
- Node
- .insert
- pollEnv
- Engi.Plugin.Tests.csproj
- itemDTO
- ToItems
- Session
- PluginLogger
- FromDB
- Metrics
- Node
- build
- consumer
- compilerOptions
- LeaderCallbacks
- Manager
- Registry
- ExecutionMode
- http.ts
- PropertyType
- FakeHost
- Service
- .Run
- BinaryRef
- Deps
- Cache
- Result
- _compact
- ParseTime
- Manager
- input
- ToolDefinition
- Store
- BlobAccess
- Deps
- context.CancelFunc
- listenEnv
- Subscription
- Handler
- Triggers
- Job
- stdio.ts
- client
- schedule
- BinaryRef
- ToFloat
- ToString
- memStore
- log/slog.Logger
- ExecutionInput
- BinaryRef
- JobID
- Credential
- .toDomain
- buildGraph
- arrayOperators
- ToolResult
- triggerAdapter
- BuildReport
- Source
- Tracer
- NewRegistry
- AuthConfig
- NodeException
- Manager
- Coerce
- Service
- .saveWired
- ReadToolMeta
- recorder
- mustPrincipal
- Generator
- .buildBody
- Span
- Service
- Service
- Pools
- Caller
- .serviceAccountToken
- workflowDTO
- Compare
- ListenTrigger
- paramInput
- Deps
- loginLimiter
- sessions
- .Enqueue
- TestWebhooks
- BlobConfig
- ExpressionConfig
- Workflows
- Codec
- fakeServer
- Limits
- main.mjs
- Node
- newSSEReader
- engi-plugin
- client
- Node
- SupplyFrom
- .Call
- options
- Node
- Node
- Node
- .Execute
- Node
- upperNode
- Node
- noTx

## God Nodes (most connected - your core abstractions)
1. `Input` - 181 edges
2. `WorkflowID` - 155 edges
3. `ExecutionID` - 151 edges
4. `Default()` - 132 edges
5. `New()` - 122 edges
6. `Descriptor` - 108 edges
7. `Execution` - 107 edges
8. `ParamString()` - 98 edges
9. `t()` - 97 edges
10. `Workflow` - 94 edges

## Surprising Connections (you probably didn't know these)
- `Type()` --calls--> `opts()`  [INFERRED]
  internal/credtype/postgres/postgres.go → pkg/pluginproto/conn_test.go
- `newApp()` --references--> `App`  [EXTRACTED]
  cmd/engi/cli.go → internal/app/app.go
- `appOptions()` --references--> `Options`  [EXTRACTED]
  cmd/engi/cli.go → internal/app/app.go
- `closePlugins()` --references--> `App`  [EXTRACTED]
  cmd/engi/cli.go → internal/app/app.go
- `loadWorkflow()` --calls--> `DecodeWorkflow()`  [EXTRACTED]
  cmd/engi/cli.go → internal/adapter/jsoncodec/workflow.go

## Import Cycles
- 3-file cycle: `internal/node/postgres/postgres.go -> internal/node/sqlnode/node_test.go -> internal/node/postgres/postgres_integration_test.go -> internal/node/postgres/postgres.go`
- 3-file cycle: `internal/node/postgres/postgres_integration_test.go -> internal/node/sqlnode/sqlsuite/suite.go -> internal/node/sqlnode/node_test.go -> internal/node/postgres/postgres_integration_test.go`
- 3-file cycle: `internal/service/engine/engine_test.go -> internal/service/engine/raw_param_test.go -> internal/service/servicetest/servicetest.go -> internal/service/engine/engine_test.go`
- 3-file cycle: `internal/service/engine/engine_test.go -> internal/service/engine/resume_url_test.go -> internal/service/servicetest/servicetest.go -> internal/service/engine/engine_test.go`
- 3-file cycle: `internal/service/engine/engine_test.go -> internal/service/engine/resume_url_test.go -> internal/service/waiter/waiter.go -> internal/service/engine/engine_test.go`
- 3-file cycle: `internal/service/engine/engine_test.go -> internal/service/engine/resume_url_test.go -> internal/service/waiter/waiter_test.go -> internal/service/engine/engine_test.go`
- 3-file cycle: `internal/service/engine/raw_param_test.go -> internal/service/engine/resume_url_test.go -> internal/service/waiter/waiter.go -> internal/service/engine/raw_param_test.go`
- 3-file cycle: `internal/service/engine/raw_param_test.go -> internal/service/engine/resume_url_test.go -> internal/service/waiter/waiter_test.go -> internal/service/engine/raw_param_test.go`
- 3-file cycle: `internal/service/engine/behaviour_test.go -> internal/service/engine/raw_param_test.go -> internal/service/servicetest/servicetest.go -> internal/service/engine/behaviour_test.go`
- 3-file cycle: `internal/service/engine/golden_test.go -> internal/service/engine/raw_param_test.go -> internal/service/servicetest/servicetest.go -> internal/service/engine/golden_test.go`
- 3-file cycle: `internal/service/engine/behaviour_test.go -> internal/service/engine/resume_url_test.go -> internal/service/servicetest/servicetest.go -> internal/service/engine/behaviour_test.go`
- 3-file cycle: `internal/service/engine/golden_test.go -> internal/service/engine/resume_url_test.go -> internal/service/servicetest/servicetest.go -> internal/service/engine/golden_test.go`
- 3-file cycle: `internal/service/engine/behaviour_test.go -> internal/service/engine/resume_url_test.go -> internal/service/waiter/waiter.go -> internal/service/engine/behaviour_test.go`
- 3-file cycle: `internal/service/engine/behaviour_test.go -> internal/service/engine/resume_url_test.go -> internal/service/waiter/waiter_test.go -> internal/service/engine/behaviour_test.go`
- 3-file cycle: `internal/service/engine/golden_test.go -> internal/service/engine/resume_url_test.go -> internal/service/waiter/waiter.go -> internal/service/engine/golden_test.go`
- 3-file cycle: `internal/service/engine/golden_test.go -> internal/service/engine/resume_url_test.go -> internal/service/waiter/waiter_test.go -> internal/service/engine/golden_test.go`
- 4-file cycle: `internal/service/engine/engine_test.go -> internal/service/engine/golden_test.go -> internal/service/engine/raw_param_test.go -> internal/service/servicetest/servicetest.go -> internal/service/engine/engine_test.go`
- 4-file cycle: `internal/service/engine/engine_test.go -> internal/service/engine/golden_test.go -> internal/service/engine/resume_url_test.go -> internal/service/servicetest/servicetest.go -> internal/service/engine/engine_test.go`
- 4-file cycle: `internal/service/engine/engine_test.go -> internal/service/engine/golden_test.go -> internal/service/engine/resume_url_test.go -> internal/service/waiter/waiter.go -> internal/service/engine/engine_test.go`
- 4-file cycle: `internal/service/engine/engine_test.go -> internal/service/engine/golden_test.go -> internal/service/engine/resume_url_test.go -> internal/service/waiter/waiter_test.go -> internal/service/engine/engine_test.go`

## Communities (315 total, 44 thin omitted)

### Community 0 - "golden_test.go"
Cohesion: 0.15
Nodes (19): authorizeDTO, catalogDTO, credentialRequest, ctxKey, loginRequest, newUserRequest, pluginDTO, previewDTO (+11 more)

### Community 1 - "Evaluator"
Cohesion: 0.09
Nodes (13): envView, sandbox, ExpressionError, ExpressionEvaluator, ExpressionScope, NodeData, checkResult(), normalize() (+5 more)

### Community 2 - "Default"
Cohesion: 0.08
Nodes (60): MetricBuckets, Deps, New(), TestHistogramBucketsComeFromConfig(), TestLLMCall(), TestRecordsMetricsInAPrivateRegistry(), TestNewRejectsInvalidConfig(), newProviderFactory() (+52 more)

### Community 3 - "EditorPage.tsx"
Cohesion: 0.05
Nodes (92): vitest, @xyflow/react, fetchExecution(), fetchWorkflowVersion(), saveWorkflow(), useNodeTypes(), useWorkflow(), WorkflowDoc (+84 more)

### Community 4 - "Event"
Cohesion: 0.15
Nodes (7): buildCache, buildResult, Event, Bus, subscription, subscription, busSub

### Community 5 - "PageResult"
Cohesion: 0.13
Nodes (14): decodeCursor(), encodeCursor(), likeEscape(), page(), whereClause(), decodeCursor(), encodeCursor(), likeEscape() (+6 more)

### Community 6 - "newEnv"
Cohesion: 0.03
Nodes (72): syncBuffer, syncWriter, tokenServer, Client, TestCredentialsUseStaticKeysOrTheDefaultChain(), credentialsFor(), TestContractOnMinIO(), New() (+64 more)

### Community 7 - "New"
Cohesion: 0.10
Nodes (26): newBus(), recv(), TestEventsCrossBusInstances(), TestLargeEventsGoThroughTheTable(), TestListenerReconnectsAfterConnectionLoss(), eventually(), startReplica(), TestLeaderReacquiresAfterConnectionLoss() (+18 more)

### Community 8 - "DecodeWorkflow"
Cohesion: 0.10
Nodes (17): toCredentialDTO(), statusParams(), toTagDTO(), toVariableDTO(), readWorkflow(), badRequest(), boolParam(), decodeJSON() (+9 more)

### Community 9 - "t"
Cohesion: 0.06
Nodes (93): react, ApiKey, CreatedApiKey, NewApiKey, NewUser, SECRET_SENTINEL, Tag, useApiKeyMutations() (+85 more)

### Community 10 - "Stamp"
Cohesion: 0.06
Nodes (11): Stamp(), isPrimaryKeyViolation(), isUniqueViolation(), affectedOrNotFound(), decodeWorkflow(), APIKeys, Credentials, Sessions (+3 more)

### Community 11 - "Queue"
Cohesion: 0.19
Nodes (6): JobStatus, rowScanner, prefixed(), scanJob(), ReclaimedJob, Queue

### Community 12 - "Descriptor"
Cohesion: 0.05
Nodes (31): Node, NodeCatalog, upperNode, goReverse, echoNode, Node, Node, Node (+23 more)

### Community 13 - "Task"
Cohesion: 0.11
Nodes (7): FakeHost, OnRequest, RawLines, Serve, HttpTests, StdioTests, TestPlugins

### Community 14 - "newRuntime"
Cohesion: 0.06
Nodes (63): podPair, TestAgentWithoutModelFailsBeforeRunning(), TestAgentWorkflowRejectsASecondModel(), TestAgentWorkflowValidates(), newApp(), TestCodeNodeSeesTheWorkflowScope(), TestFindStartNode(), TestReportEncodes() (+55 more)

### Community 15 - "types.ts"
Cohesion: 0.05
Nodes (81): CredentialSlot, CredentialSummary, CredentialType, DisplayOptions, DisplayRule, Endpoint, ErrorInfo, Execution (+73 more)

### Community 16 - "store_suite.go"
Cohesion: 0.11
Nodes (50): TestAuthStoreContract(), TestStoreContract(), TestTestWebhookContract(), RunAuthSuite(), RunQueueSuite(), ids(), mustCreate(), mustSave() (+42 more)

### Community 17 - ".add"
Cohesion: 0.11
Nodes (15): issueDTO, problemDTO, requestError, verdict, Issue, validator, classify(), firstIssue() (+7 more)

### Community 18 - "mime.go"
Cohesion: 0.12
Nodes (24): Attachment, connConfig, Message, attachmentHeader(), bodyOf(), Build(), checkHeaderValue(), joinAddresses() (+16 more)

### Community 19 - "server"
Cohesion: 0.15
Nodes (27): credential(), TestAuthCredentialResolvedOncePerRun(), TestAuthentication(), TestAuthenticationErrors(), TestBinaryBodyIsSentWithItsMIME(), TestBinaryBodyNeedsTheField(), TestBinaryResponseFailureIsAnError(), TestBinaryResponseIsStoredAsBinary() (+19 more)

### Community 20 - "LLMMessage"
Cohesion: 0.06
Nodes (22): fakeMemory, loop, Outcome, Step, fakeStore, memory, chatMemoryBridge, DecodeToolCalls() (+14 more)

### Community 21 - "Executions"
Cohesion: 0.15
Nodes (8): rowScanner, scanExecution(), isForeignKeyViolation(), isPrimaryKeyViolation(), isUniqueViolation(), sqliteCode(), timeArg(), Executions

### Community 22 - "package.json"
Cohesion: 0.04
Nodes (52): @codemirror/autocomplete, @codemirror/commands, @codemirror/lang-javascript, @codemirror/lang-json, @codemirror/language, @codemirror/state, @codemirror/view, eslint (+44 more)

### Community 23 - "Provider"
Cohesion: 0.19
Nodes (9): New(), LeaseConfig, Options, Provider, record, Request, noHandler(), LogParams (+1 more)

### Community 25 - "Variable"
Cohesion: 0.07
Nodes (22): variableDTO, scanVariable(), variableConflict(), scanVariable(), variableConflict(), VariableID, Variable, ValidateVariableKey() (+14 more)

### Community 26 - "newEnv"
Cohesion: 0.07
Nodes (47): RetentionMaxAge, TestRetentionPrunesSQLite(), defaultRetention(), RetentionConfig, chainWorkflow(), runUntilDone(), TestResumeURLFromScopeResumesWaitingExecution(), TestResumeURLIsStableAcrossWaitNodes() (+39 more)

### Community 27 - "aiagent_test.go"
Cohesion: 0.25
Nodes (25): rig, call(), mcpSub(), newRig(), okTool(), say(), tc(), TestAnswerWithoutTools() (+17 more)

### Community 28 - "TypeName"
Cohesion: 0.09
Nodes (29): allData(), individualFields(), separators(), objectParam(), readMatchFields(), ToTime(), ClonePaired(), ParamSlice() (+21 more)

### Community 29 - "AuthConfig"
Cohesion: 0.25
Nodes (10): Hasher, parsed, New(), parse(), fast(), TestConcurrencyLimitStillCompletesAndRehashIgnoresIt(), TestHashVerify(), TestNeedsRehash() (+2 more)

### Community 30 - "NewConn"
Cohesion: 0.09
Nodes (23): newLineLogger(), spawn(), newStderrTail(), TestStderrTail(), TestWithStderr(), withStderr(), resolveProgram(), Conn (+15 more)

### Community 31 - "newEnv"
Cohesion: 0.12
Nodes (44): Eventually(), gatedRunner(), TestFreshRunCountsOneAttempt(), TestLateRequeueFromAnEarlierClaimIsIgnored(), TestReclaimedJobTakesOverItsRun(), TestReclaimRecordsRecoveryAndAttempts(), TestStaleJobReclaimDoesNotResetAnotherJobsRun(), TestTwoJobsForOneExecutionRunItOnce() (+36 more)

### Community 32 - "compilerOptions"
Cohesion: 0.10
Nodes (20): compilerOptions, exactOptionalPropertyTypes, isolatedModules, jsx, lib, module, moduleResolution, noEmit (+12 more)

### Community 33 - ".New"
Cohesion: 0.02
Nodes (142): fakeFactory, fakeFactory, env, fakeRunner, poll(), Type(), Type(), TestAllItemData() (+134 more)

### Community 34 - "database/sql.DB"
Cohesion: 0.04
Nodes (76): fakePools, credentialStack, database, migrateCommand(), assets(), assets(), New(), New() (+68 more)

### Community 35 - "subports.test.tsx"
Cohesion: 0.09
Nodes (32): NodeType, MAX_RECENT_NODES, CardData, CardNode, groupVar(), kindLabel(), NodeCard, NodeCardImpl() (+24 more)

### Community 36 - "testing.T"
Cohesion: 0.01
Nodes (242): echoPlugin(), TestPluginCommands(), TestDocsUpToDate(), env, InlineRunner, Subworkflows, New(), TestContract() (+234 more)

### Community 37 - "client.ts"
Cohesion: 0.06
Nodes (58): react-dom, react-router-dom, @tanstack/react-query, useUserMutations(), useUsers(), api, ApiError, listAll() (+50 more)

### Community 38 - "Tool"
Cohesion: 0.12
Nodes (8): toolPrefixer, toolProv, Tools, provider, prefixOf(), Tool, ToolProvider, Toolset

### Community 39 - "devDependencies"
Cohesion: 0.11
Nodes (19): devDependencies, eslint, @eslint/js, eslint-plugin-react-hooks, globals, jsdom, openapi-typescript, @testing-library/dom (+11 more)

### Community 40 - "UserID"
Cohesion: 0.06
Nodes (16): apiKeyDTO, createdAPIKeyDTO, reqInfo, apiKeys, IDGenerator, memStore, users, toAPIKeyDTO() (+8 more)

### Community 41 - "cronexpr.go"
Cohesion: 0.21
Nodes (14): fieldSpec, Expr, instant(), isAny(), Parse(), parseField(), parsePart(), mustLoc() (+6 more)

### Community 42 - "sseTransport"
Cohesion: 0.13
Nodes (9): ClientConfig, Doer, copyHeader(), newStreamable(), statusError(), newSSE(), HTTPStatusError, sseTransport (+1 more)

### Community 43 - "NodeDescriptor"
Cohesion: 0.04
Nodes (45): CredentialRef, Name, Required, Type, NodeDescriptor, Credentials, Description, DisplayName (+37 more)

### Community 44 - "appendPositive"
Cohesion: 0.05
Nodes (43): AIAgentConfig, AirtableNodeConfig, AIToolConfig, ChatNodeConfig, CodeNodeConfig, DatabaseNodeConfig, EmailNodeConfig, GoogleNodeConfig (+35 more)

### Community 45 - "Store"
Cohesion: 0.06
Nodes (42): PluginLister, pluginCaller, pluginEnv, pluginHost, PluginState, Failure, Installed, manifestOf() (+34 more)

### Community 46 - "runner"
Cohesion: 0.17
Nodes (13): runner, sheetState, cellText(), cellValue(), columnLetters(), copyMap(), emptyRow(), indexOf() (+5 more)

### Community 47 - "TagID"
Cohesion: 0.13
Nodes (11): tagDTO, TagID, Tag, ValidateTagName(), TagStore, Service, New(), Tags (+3 more)

### Community 48 - "State"
Cohesion: 0.19
Nodes (7): Destination, Request, resumeURL(), State, StateCodec, tokenMatches(), Runner

### Community 49 - "Worker"
Cohesion: 0.18
Nodes (3): Worker, holderOf(), sleep()

### Community 50 - "Client"
Cohesion: 0.14
Nodes (10): connectWith(), Client, TestClientAgainstEveryTransport(), newNotification(), newRequest(), LatestVersion(), negotiate(), SupportedVersion() (+2 more)

### Community 51 - "Item"
Cohesion: 0.04
Nodes (50): DisplayOptions, Hide, Show, NodeResult, Outputs, Wait, Credential, Data (+42 more)

### Community 52 - "time.Duration"
Cohesion: 0.03
Nodes (49): recMetrics, ListenConfig, PollConfig, ScheduleConfig, Backoff, RateLimitedError, recMetrics, fakeClock (+41 more)

### Community 53 - "result.go"
Cohesion: 0.25
Nodes (11): listOf(), coerceArg(), TestCoerceArg(), builder, Condition, Dialect, Pair, Sort (+3 more)

### Community 54 - "useRunner.ts"
Cohesion: 0.15
Nodes (17): RunBody, runWorkflow(), NAMES, StreamEventName, subscribeExecution(), Subscription, StreamEvent, API_BASE (+9 more)

### Community 55 - "Insert"
Cohesion: 0.07
Nodes (46): chunkRef, liveDSN(), openLive(), TestPgvectorLive(), TestReplaceDocumentLive(), checkIdent(), CreateIndexSQL(), CreateTableSQL() (+38 more)

### Community 56 - "loadtest/main.go"
Cohesion: 0.18
Nodes (26): New(), New(), env, hookNode(), newEnv(), newEnvWith(), schedNode(), TestActivateAllRestoresActiveWorkflowsAndDeactivatesBrokenOnes() (+18 more)

### Community 57 - "Runtime"
Cohesion: 0.10
Nodes (13): Runtime, storeSet, Deps, Deps, InputEncoder, ExecutionStateStore, TxManager, WorkflowStore (+5 more)

### Community 58 - "BlobStore"
Cohesion: 0.22
Nodes (18): BlobStore, blobCanceled(), blobDelete(), blobDeleteExecution(), blobDeleteNone(), blobEmpty(), blobExecutions(), blobFailedReader() (+10 more)

### Community 59 - "template.go"
Cohesion: 0.48
Nodes (6): segment, TestParseTemplate(), findBlockEnd(), parseTemplate(), skipString(), skipUntil()

### Community 60 - "propertyDTO"
Cohesion: 0.18
Nodes (11): credentialFieldDTO, credentialRefDTO, credentialTypeDTO, displayOptionsDTO, nodeTypeDTO, optionDTO, portDTO, propertyDTO (+3 more)

### Community 61 - "ExecuteParams"
Cohesion: 0.12
Nodes (18): Caller, New(), desc(), newNode(), okResult(), TestCallbacks(), TestExecuteBuildsParams(), TestExecuteResults() (+10 more)

### Community 62 - "WorkflowSettings.tsx"
Cohesion: 0.09
Nodes (32): fetchWorkflow(), previewExpressionRemote(), WfNode, chatPath(), chatWidgetUrl(), OpenChatButton(), OpenChatView(), fetchWorkflow (+24 more)

### Community 63 - "encoding/json.RawMessage"
Cohesion: 0.09
Nodes (22): Schema, previewRequest, runDestination, runRequest, versionDTO, caseOptions, tokenBody, Message (+14 more)

### Community 64 - ".run"
Cohesion: 0.21
Nodes (17): fixture, recorded, base64URL(), decodePart(), headerThen(), newFixture(), reply(), TestAPIErrorBecomesNodeError() (+9 more)

### Community 65 - "context.Context"
Cohesion: 0.06
Nodes (22): funcNode, supplyNode, duplicateToolError(), joinSystem(), memoryOf(), modelOf(), toolsOf(), Secret() (+14 more)

### Community 66 - "Input"
Cohesion: 0.11
Nodes (8): storedInput, storedInput, Input, RawParamSource, storedInput, Supplied, storedInput, secretInput

### Community 67 - "server_api_test.go"
Cohesion: 0.07
Nodes (52): req, resp, session, sseFrame, heapInuseMiB(), retainedMiB(), TestMemoryStaysBounded(), TestMetricsEndpointOnMainServerBypassesAuth() (+44 more)

### Community 68 - "CredentialType"
Cohesion: 0.09
Nodes (31): CredentialTypeCatalog, load(), Type(), Type(), Type(), TestAICredentialTypes(), Types(), TestAirtableAndServiceAccountTypes() (+23 more)

### Community 69 - "startStdio"
Cohesion: 0.09
Nodes (43): plugin, sdkCase, assertReversed(), checkNodes(), execCtx(), forEachSDK(), newNode(), processCount() (+35 more)

### Community 70 - "API"
Cohesion: 0.08
Nodes (11): access, handlerFunc, route, API, New(), requestIDFrom(), validRequestID(), New() (+3 more)

### Community 71 - "conn"
Cohesion: 0.10
Nodes (6): DB, Open(), Call, conn, rows, tx

### Community 72 - "database/sql.Result"
Cohesion: 0.11
Nodes (9): Rebind(), TestRebindSkipsLiterals(), Runner, DBTX, Runner, Run(), binder, DBTX (+1 more)

### Community 73 - "cache.go"
Cohesion: 0.17
Nodes (9): cacheEntry, cacheKey, compiledSegment, compiledTemplate, programCache, keyFor(), newProgramCache(), TestProgramCacheEvictsLeastRecentlyUsed() (+1 more)

### Community 74 - "schema.d.ts"
Cohesion: 0.25
Nodes (7): RFC-3339, components, $defs, operations, paths, RFC-9457, webhooks

### Community 75 - "Tracer"
Cohesion: 0.18
Nodes (3): kv(), span, Tracer

### Community 76 - "net/http.ResponseWriter"
Cohesion: 0.11
Nodes (9): listDTO, statusRecorder, toNodeTypeDTO(), allowParams(), listOf(), writeJSON(), statusWriter, ChatReservedSegment() (+1 more)

### Community 77 - "redis/run.go"
Cohesion: 0.53
Nodes (6): count(), hashGet(), hashSet(), run(), scan(), valueResult()

### Community 78 - "dependencies"
Cohesion: 0.15
Nodes (13): dependencies, @codemirror/autocomplete, @codemirror/commands, @codemirror/lang-javascript, @codemirror/lang-json, @codemirror/language, @codemirror/state, @codemirror/view (+5 more)

### Community 79 - "runner.py"
Cohesion: 0.06
Nodes (25): analyze(), Analyzer, compile_user_code(), ExprError, handle(), HostLink, import_allowed(), json_default() (+17 more)

### Community 80 - "io.Reader"
Cohesion: 0.10
Nodes (10): Cipher, limitReader, ctxReader, NewWithRandom(), ReadNDJSON(), newLineReader(), TestReadNDJSON(), newUUID() (+2 more)

### Community 81 - "newEnv"
Cohesion: 0.14
Nodes (35): chatRoute(), lastNodeAnswer(), newChatEnv(), TestChatAuthModes(), TestChatCORS(), TestChatFailureModes(), TestChatIsOffWithoutDeps(), TestChatMessageStartsTheWorkflowAndReplies() (+27 more)

### Community 82 - "LLM"
Cohesion: 0.14
Nodes (14): modelProv, modelProvider, liveModel, providerFactory, field(), orDefault(), wrap(), WithMetrics() (+6 more)

### Community 83 - "supervisor_test.go"
Cohesion: 0.17
Nodes (25): io_(), newSup(), params(), run(), serverLogs(), stringsReader(), TestCallTimeoutSendsCancel(), TestCloseKillsStubbornPlugin() (+17 more)

### Community 84 - "Context"
Cohesion: 0.11
Nodes (4): Blob, Context, CredentialError, ParamError

### Community 85 - "net/http.Request"
Cohesion: 0.06
Nodes (23): bearerToken(), contextWithPrincipal(), isSafe(), readsOnly(), toErrorInfoDTO(), workflowDoc(), sameSite(), secureCookie() (+15 more)

### Community 86 - "queueEnv"
Cohesion: 0.09
Nodes (41): testAPIKeyLifecycle(), testAPIKeyListAndCount(), testAPIKeyNotFound(), testAPIKeysFollowUser(), testAPIKeyUniquePrefix(), testSessionExpiry(), testSessionLifecycle(), testSessionsFollowUser() (+33 more)

### Community 87 - ".oauthData"
Cohesion: 0.21
Nodes (6): pkceState, token, parseToken(), withAccess(), OAuth2Grant, OAuth2Spec

### Community 88 - "Run"
Cohesion: 0.24
Nodes (9): TestMySQLIntegration(), TestPostgresIntegration(), execute(), inTx(), equal(), Run(), planned, Env (+1 more)

### Community 89 - "CredentialID"
Cohesion: 0.12
Nodes (17): authorizedDTO, Input, keyLock, lockEntry, RotationResult, CredentialID, CredentialData, newKeyLock() (+9 more)

### Community 90 - "Timeline.tsx"
Cohesion: 0.27
Nodes (8): NodeRun, TIMELINE_MIN_BAR_PCT, countItems(), ms(), Timeline(), TimelineRow, timelineRows(), formatDuration()

### Community 91 - "subports.test.tsx"
Cohesion: 0.13
Nodes (26): buildUrl(), Item, BINARY_PREVIEW_MAX_BYTES, binaryUrl(), BinaryValue, fetchBinaryText(), humanSize(), IMAGE_TYPES (+18 more)

### Community 93 - "runner"
Cohesion: 0.22
Nodes (6): pending, runner, target, objectParam(), records(), ItemFailure()

### Community 94 - "NoteCard.tsx"
Cohesion: 0.13
Nodes (25): Connection, WfNote, Block, blocks(), inline(), isWordChar(), Markdown(), render() (+17 more)

### Community 95 - "runWF"
Cohesion: 0.14
Nodes (40): OnError, PortKind, Connection, Endpoint, NodeSettings, joinWF(), lineageWF(), nItems() (+32 more)

### Community 96 - "ExecutionID"
Cohesion: 0.07
Nodes (15): startedDTO, scoped, DuplicateError, BlobCleaner, IDGenerator, ExecutionID, Service, newID() (+7 more)

### Community 97 - "Note"
Cohesion: 0.17
Nodes (13): NoteColor, NoteID, Position, Size, finite(), Note, NoteColors(), conn() (+5 more)

### Community 98 - "parse"
Cohesion: 0.19
Nodes (15): TestPlainUsesExtendedJSON(), plain(), document(), insertDocs(), isBlank(), parse(), rawJSON(), stages() (+7 more)

### Community 99 - "User"
Cohesion: 0.09
Nodes (16): editorSettingsDTO, meDTO, sessionDTO, userDTO, env, NewUser, UserPatch, principalFrom() (+8 more)

### Community 100 - "rig"
Cohesion: 0.18
Nodes (22): newExecutions(), newRig(), rec(), testBackpressure(), testBatch(), testDeadLetter(), testGroupSplit(), TestKafkaIntegration() (+14 more)

### Community 101 - "ExecutionStatus"
Cohesion: 0.08
Nodes (21): errorInfoDTO, executionDTO, streamEvent, WorkflowError, RunRef, inlineFake, decode(), toExecutionDetail() (+13 more)

### Community 102 - "ToolInfo"
Cohesion: 0.15
Nodes (10): labelledMCP, labelled, provider, tools, ToolInfo, describe(), sanitize(), schemaOf() (+2 more)

### Community 103 - "go_pkg_context"
Cohesion: 0.07
Nodes (56): zeros, statusDTO, MemStats, ollamaRequest, ollamaResponse, openAIRequest, openAIResponse, errReader (+48 more)

### Community 105 - "plugin.py"
Cohesion: 0.07
Nodes (18): check_descriptor(), CredentialRef, Descriptor, DisplayOptions, Option, Port, Property, CancelledError (+10 more)

### Community 106 - "ChatMemoryStore"
Cohesion: 0.21
Nodes (23): Config, Node, New(), newNode(), supply(), TestDescriptor(), TestSupplyErrors(), TestWindowStartsAtUserMessage() (+15 more)

### Community 107 - "LLMRequest"
Cohesion: 0.11
Nodes (20): scriptLLM, fakeLLM, messageResponse, usage, errorf(), mapStop(), Client, mapDone() (+12 more)

### Community 109 - "App"
Cohesion: 0.18
Nodes (6): metricsServer, Server, ServerOptions, App, cryptoRand(), countWebhooks()

### Community 110 - "Deps"
Cohesion: 0.17
Nodes (8): Deps, EditorSettings, IDGenerator, StartNodeFinder, Bus, Sleeper, Deps, ExecutionIDs

### Community 111 - "Item"
Cohesion: 0.12
Nodes (15): Result, byFields(), fromInput(), matchKey(), mergeParts(), ParamFloat(), Items(), parse() (+7 more)

### Community 112 - "net/http.Response"
Cohesion: 0.07
Nodes (37): seen, doerFunc, doer, decode(), workflows, newFixture(), TestAuthentication(), TestBodyLimit() (+29 more)

### Community 113 - "runner"
Cohesion: 0.11
Nodes (12): runner, TestSpanAttributesAndFailure(), NodeError, SpanAttr, IntAttr(), StrAttr(), cloneMap(), cloneValue() (+4 more)

### Community 114 - "newFixture"
Cohesion: 0.12
Nodes (32): fixture, recorded, compressible(), TestGzip(), Type(), echoRecords(), items(), newFixture() (+24 more)

### Community 115 - "Route"
Cohesion: 0.09
Nodes (13): router, chatRouter, duplicateOf(), Router, router, writeJSON(), hopByHop(), writeResponse() (+5 more)

### Community 116 - "connectors.go"
Cohesion: 0.15
Nodes (12): Client, mysqlConnector(), mysqlTLS(), portOf(), postgresConnector(), TestMySQLTLSModes(), New(), tlsConfig() (+4 more)

### Community 117 - "imaptrigger/settings.go"
Cohesion: 0.09
Nodes (21): client, connConfig, Limits, mailServer, settings, newMailServer(), subjectsOf(), TestSendThenPollOnceThenRestartWithoutDuplicates() (+13 more)

### Community 118 - "WorkflowID"
Cohesion: 0.10
Nodes (7): Activator, WorkflowID, ActivationStore, TriggerStateStore, Triggers, activationStore, stateStore

### Community 119 - "poller"
Cohesion: 0.29
Nodes (4): backoff(), stopPoller(), poller, pollNode

### Community 120 - "widget.js"
Cohesion: 0.48
Nodes (5): add(), problemText(), saveSession(), setBusy(), submit()

### Community 121 - "BlobRef"
Cohesion: 0.12
Nodes (9): BinaryReader, limitedBody, contentType(), isNotFound(), BlobInfo, BlobMeta, BlobRef, ParseBlobRef() (+1 more)

### Community 122 - "ui/ui_test.go"
Cohesion: 0.08
Nodes (47): counterIDs, pgCred(), testManager(), TestRejectsBadCredentials(), TestReuseAndInvalidation(), cred(), factory(), openAIServer() (+39 more)

### Community 123 - "RenderCredentialTypes"
Cohesion: 0.08
Nodes (35): streamBlock, main(), run(), readCapped(), authText(), cell(), cellOrDash(), conditions() (+27 more)

### Community 124 - ".fetch"
Cohesion: 0.09
Nodes (19): cursor, filters, storeAttachments(), TestAllocationFailureFailsThePoll(), TestAttachmentsBecomeBinaryFields(), TestWithoutBlobStorageAttachmentsStayListed(), decodeCursor(), dedupKey() (+11 more)

### Community 125 - "PortKind"
Cohesion: 0.12
Nodes (15): buildRegistry(), desc(), one(), paramsDesc(), port(), two(), registerSubPortFakes(), subDesc() (+7 more)

### Community 128 - "net/http/httptest.Server"
Cohesion: 0.10
Nodes (32): Config, Limits, TestEngiClientToolTalksToEngiServer(), newProvider(), TestCreatesLeaseLeadsAndReleases(), TestStandbyTakesOverAfterRelease(), TestStepsDownWhenRenewalFailsPastDeadline(), TestTakesOverExpiredLeaseOfCrashedHolder() (+24 more)

### Community 129 - "Recursive"
Cohesion: 0.28
Nodes (6): Recursive, cut(), NewRecursive(), TestNewRecursiveValidates(), TestSplit(), TestSplitInvariants()

### Community 130 - "Plugin"
Cohesion: 0.07
Nodes (9): RpcError, Plugin, _answer(), HttpTransport, make_server(), do_GET(), do_POST(), _send() (+1 more)

### Community 131 - "check.sh"
Cohesion: 0.62
Nodes (6): count(), expect(), expect_fail(), expect_not(), render(), check.sh script

### Community 132 - "Credential"
Cohesion: 0.16
Nodes (7): fakePools, countingInput, Manager, Clients, Credential, DBPool, countingPools

### Community 133 - "Workflow"
Cohesion: 0.09
Nodes (12): workflowSummaryDTO, WorkflowSettings, toWorkflowSummary(), EncodeWorkflow(), RunRequest, NoteLimits, Workflow, workflows (+4 more)

### Community 134 - "layout.ts"
Cohesion: 0.39
Nodes (8): autoLayout(), Box, byPosition(), carryNotes(), Edge, overlapArea(), Pos, posOf()

### Community 135 - "github.com/dop251/goja.Value"
Cohesion: 0.10
Nodes (20): readOnlyView, run, workflowScope, nodeView, New(), toTime(), checkSize(), firstLines() (+12 more)

### Community 136 - "io.Writer"
Cohesion: 0.12
Nodes (27): appOptions(), closePlugins(), loadWorkflow(), lookupIn(), newApp(), parseArgs(), runCommand(), runPersisted() (+19 more)

### Community 137 - "StdioSession"
Cohesion: 0.10
Nodes (8): Call, HostError, UnsupportedError, make_logger(), _host_error(), _id_key(), _Pending, StdioSession

### Community 138 - "mcp/client_test.go"
Cohesion: 0.17
Nodes (18): TestMCPServerTriggerEndToEnd(), Connect(), fallbackStatus(), cfg(), connect(), TestConfigValidation(), TestExpiredSession(), TestFallbackToLegacySSE() (+10 more)

### Community 139 - "setting"
Cohesion: 0.18
Nodes (35): setting, aiModelSettings(), aiSettings(), aiAgentSettings(), aiRAGSettings(), airtableSettings(), authSettings(), u32Setting() (+27 more)

### Community 140 - "EmbedResult"
Cohesion: 0.16
Nodes (8): fakeEmbedder, fakeEmbedder, fakeEmbedder, batchClient, batchResult, embedder, metered, EmbedResult

### Community 141 - "JsonNode"
Cohesion: 0.13
Nodes (19): RpcException, Code, Data, UnsupportedException, BlobClient, IHostChannel, NoHostChannel, NodeContext (+11 more)

### Community 144 - "ArmedTestWebhook"
Cohesion: 0.09
Nodes (9): scanTestWebhook(), scanTestWebhook(), ArmedTestWebhook, TestWebhookStore, matchTests(), TestWebhookEnv, TestWebhooks, TestWebhooks (+1 more)

### Community 145 - "Engi.Plugin"
Cohesion: 0.10
Nodes (5): Engi.Plugin, Engi.Plugin.Tests, ErrorCodes, WireJson, Options

### Community 147 - "JsonObject"
Cohesion: 0.14
Nodes (4): CallGate, MessageTooLargeException, RpcMessages, StdioSession

### Community 148 - "Execution"
Cohesion: 0.12
Nodes (6): Execution, RunHolder, ExecutionStore, errorInfo(), failureLog, outcome

### Community 149 - "Supervisor"
Cohesion: 0.07
Nodes (14): Client, snippet(), Callbacks, startChild(), decode(), Supervisor, CheckNodeType(), TestCheckDescriptor() (+6 more)

### Community 151 - "smoke.sh"
Cohesion: 0.83
Nodes (3): call(), json(), smoke.sh script

### Community 152 - "Listener"
Cohesion: 0.12
Nodes (8): Bus, New(), Listener, NewListener(), Target, defaultBus(), BusConfig, NotifyFunc

### Community 153 - "src/types.ts"
Cohesion: 0.11
Nodes (26): ALLOWED_GROUPS, compact(), credentialWire(), descriptorWire(), Dict, MERGE_POLICIES, optionWire(), PORT_MAIN (+18 more)

### Community 154 - "anthropic_test.go"
Cohesion: 0.09
Nodes (38): captured, recorded, headersOf(), New(), newClient(), reply(), sse(), TestChatText() (+30 more)

### Community 156 - "Plugin"
Cohesion: 0.10
Nodes (10): HttpTransport, Plugin, Name, Nodes, Options, Version, PluginOptions, BearerToken (+2 more)

### Community 157 - "fakeAPI"
Cohesion: 0.50
Nodes (3): fakeAPI, leaseObject, leaseSpec

### Community 158 - "NewServer"
Cohesion: 0.33
Nodes (5): errorResponse(), Reply, Server, invalid(), ToolHandler

### Community 159 - "server"
Cohesion: 0.07
Nodes (33): response, startGoHTTP(), Config, New(), cfg(), newServer(), params(), TestFailures() (+25 more)

### Community 160 - "index.ts"
Cohesion: 0.15
Nodes (13): normalizeDefinition(), Code, CredentialError, DescriptorError, NodeError, NodeErrorOptions, ParamError, DEFAULT_MAX_CONCURRENT_CALLS (+5 more)

### Community 161 - "run.sh"
Cohesion: 0.83
Nodes (3): call(), json(), run.sh script

### Community 162 - "time.Time"
Cohesion: 0.06
Nodes (21): credentialDTO, credentialSummaryDTO, testWebhookDTO, versionSummaryDTO, fixed, fixedClock, fixedClock, fixedClock (+13 more)

### Community 163 - "WorkerConfig"
Cohesion: 0.22
Nodes (8): defaultWorker(), WorkerConfig, New(), TestNewRejectsMissingDependencies(), New(), TestNewRejectsMissingDependencies(), New(), TestNewRejectsMissingDependencies()

### Community 164 - "StdioTest"
Cohesion: 0.17
Nodes (3): execute_params(), StdioTest, on_request()

### Community 165 - "protocol.go"
Cohesion: 0.12
Nodes (17): CallToolResult, TextResult(), WorkflowTool, ParseWorkflowTools(), schemaOf(), TestParseWorkflowTools(), text(), argumentsObject() (+9 more)

### Community 166 - "ParamString"
Cohesion: 0.13
Nodes (15): Node, ReadFormat(), Node, readProducerSettings(), ParseConditions(), ReadGroup(), StoredParam(), storedConditions() (+7 more)

### Community 167 - "Item"
Cohesion: 0.11
Nodes (20): loadItems(), wrapPlainObjects(), fakeNodes, jsonItems(), chatReply(), Resumer, resumer, TestDecodeItems() (+12 more)

### Community 168 - ".Execute"
Cohesion: 0.43
Nodes (3): session, dial(), toAny()

### Community 170 - "sync.Mutex"
Cohesion: 0.10
Nodes (10): keyedLocker, execs, starter, StartRequest, execs, starter, fakeDepth, execs (+2 more)

### Community 171 - "nodeInput"
Cohesion: 0.12
Nodes (8): delivery, edge, nodeInfo, nodeInput, subKey, Task, firstItemOnly(), newNodeInput()

### Community 172 - "FromDescriptor"
Cohesion: 0.17
Nodes (9): TestMain(), descriptor(), runPlugin(), runRaw(), TestMain(), longLine(), Group, FromDescriptor() (+1 more)

### Community 173 - "Store"
Cohesion: 0.12
Nodes (3): Executions, Store, notFound()

### Community 174 - "logSink"
Cohesion: 0.12
Nodes (8): recordLog, addAttr(), levelName(), qualify(), hostLogHandler, notifier, attrHandler, logSink

### Community 175 - "RpcError"
Cohesion: 0.19
Nodes (6): Call, MessageTooLargeError, RpcError, normalizeOutput(), StdioSession, toRpc()

### Community 176 - "input"
Cohesion: 0.44
Nodes (7): fakeSMTP, input(), newFakeSMTP(), testNode(), TestRefusedRecipientIsReported(), TestRejectsBadInputBeforeConnecting(), TestSendsEveryItemOverOneConnection()

### Community 177 - "WebhookRegistration"
Cohesion: 0.11
Nodes (5): scanWebhook(), WebhookRegistration, WebhookStore, literals(), webhookStore

### Community 178 - "HTTPClient"
Cohesion: 0.11
Nodes (13): fakeFactory, field(), orDefault(), New(), Embedder, EmbedderSettings, AuthKind, AuthSpec (+5 more)

### Community 179 - "kafka/settings.go"
Cohesion: 0.13
Nodes (12): TestParseSettings(), paramInt(), paramString(), splitList(), defaultGroup(), parseSettings(), settings, Trigger (+4 more)

### Community 180 - "ServerConfig"
Cohesion: 0.22
Nodes (8): CORSConfig, PaginationConfig, UIConfig, AllRoles(), defaultServer(), ServerConfig, TestValidatePublicURL(), ValidatePublicURL()

### Community 181 - "stdio.test.ts"
Cohesion: 0.24
Nodes (6): checkDescriptor(), definePlugin(), PluginInfo, buildPlugin(), executeParams(), Msg

### Community 182 - "context.ts"
Cohesion: 0.16
Nodes (10): Blob, Context, Transport, CancelledError, HttpTransport, BinaryRef, ExecuteParams, ExecutionInfo (+2 more)

### Community 183 - "Options"
Cohesion: 0.25
Nodes (4): Options(), saslMechanism(), splitBrokers(), producerSettings

### Community 184 - "Send"
Cohesion: 0.08
Nodes (25): Config, requestBody, wireBlock, wireMessage, wireTool, wireToolChoice, ollamaClient, openAIClient (+17 more)

### Community 185 - "ResponseFormat"
Cohesion: 0.22
Nodes (8): Config, Node, settings, limitFailure(), New(), TestDescriptorIsValid(), ResponseFormat, ResponseFormatKind

### Community 186 - "typescript/package.json"
Cohesion: 0.10
Nodes (19): description, devDependencies, @types/node, typescript, engines, node, exports, files (+11 more)

### Community 187 - "WebhookSpec"
Cohesion: 0.20
Nodes (6): Node, Node, ChatTrigger, ChatSpec, WebhookSpec, WebhookTrigger

### Community 188 - "engi/main_test.go"
Cohesion: 0.15
Nodes (19): execute(), run(), main(), jsonOf(), runCLI(), TestConfigFromFileAndEnvironment(), TestExampleCodeAggregate(), TestExampleHTTPFetchIf() (+11 more)

### Community 190 - "New"
Cohesion: 0.13
Nodes (22): RegistryOption, registryOptions, Option, WithTracer(), New(), WithChatMemory(), WithDBPools(), WithEmbedderFactory() (+14 more)

### Community 191 - ".ServeHTTP"
Cohesion: 0.38
Nodes (3): problem(), writeBody(), Handler

### Community 193 - ".RunSubworkflow"
Cohesion: 0.14
Nodes (11): fakeRunner, subworkflowBridge, unavailableSubworkflows, fakeRunner, Node, New(), fromSDK(), toSDK() (+3 more)

### Community 194 - "Executions"
Cohesion: 0.18
Nodes (7): encode(), EncodeErrorInfo(), EncodeRunData(), rowScanner, scanExecution(), timeArg(), Executions

### Community 195 - "FormatTime"
Cohesion: 0.20
Nodes (7): EncodeToolCalls(), expiredOwners(), rowScanner, scanJob(), FormatTime(), TestTimeRoundTripAndOrdering(), Queue

### Community 196 - "Property"
Cohesion: 0.18
Nodes (14): SchemaProperty(), ToolProperties(), ConditionProperties(), DistanceProperty(), FilterProperty(), PostgresCredential(), TableProperties(), TopKProperty() (+6 more)

### Community 198 - "Node"
Cohesion: 0.08
Nodes (21): Limits, Node, Runner, New(), New(), New(), New(), New() (+13 more)

### Community 199 - ".insert"
Cohesion: 0.16
Nodes (12): Node, Node, setDefault(), DB(), Embedder(), JSONObject(), OpenDB(), ReadDistance() (+4 more)

### Community 200 - "pollEnv"
Cohesion: 0.14
Nodes (19): env(), listAll(), main(), rid(), run(), chatEnv(), chatNode(), TestChatPathsAreUniqueAndSeparateFromWebhooks() (+11 more)

### Community 201 - "Engi.Plugin.Tests.csproj"
Cohesion: 0.14
Nodes (9): net10.0, Microsoft.NET.Test.Sdk (17.14.1), xunit (2.9.3), xunit.runner.visualstudio (3.1.4), net8.0, Microsoft.NET.Sdk, net8.0, Microsoft.NET.Sdk (+1 more)

### Community 202 - "itemDTO"
Cohesion: 0.21
Nodes (20): errorToDTO(), itemsToDTO(), EncodeState(), portDataToDomain(), portDataToDTO(), portsToDTO(), runToDTO(), sourcesToDTO() (+12 more)

### Community 203 - "ToItems"
Cohesion: 0.12
Nodes (10): toResult(), ItemError, FromItems(), FromOutput(), ToItems(), TestConversionsRoundTrip(), Item, ItemError (+2 more)

### Community 204 - "Session"
Cohesion: 0.12
Nodes (4): Session, SessionStore, AuthStores, Sessions

### Community 205 - "PluginLogger"
Cohesion: 0.25
Nodes (6): LogLevel, Debug, Error, Info, Warn, PluginLogger

### Community 206 - "FromDB"
Cohesion: 0.13
Nodes (9): newAPIKeyRequest, CreatedAPIKey, NewAPIKey, FromDB(), scanAPIKey(), scanUser(), notFound(), optTime() (+1 more)

### Community 207 - "Metrics"
Cohesion: 0.12
Nodes (4): Metrics, QueueStats, nextOverloaded(), gate

### Community 208 - "Node"
Cohesion: 0.16
Nodes (7): CanvasLess(), Node, checkPath(), defaultPath(), shapeOf(), plan, webhookNode

### Community 209 - "build"
Cohesion: 0.15
Nodes (17): settings, binaryFieldParam(), binaryResponse(), headerMap(), bodyItems(), buildRequest(), do(), readBody() (+9 more)

### Community 210 - "consumer"
Cohesion: 0.11
Nodes (15): messageOf(), recordsOf(), msgs(), TestBatcherFlushesBySizeAndByAge(), TestDedupKeys(), TestPollWaitFollowsOpenBatches(), headerMap(), newBatcher() (+7 more)

### Community 211 - "compilerOptions"
Cohesion: 0.13
Nodes (14): compilerOptions, declaration, lib, module, moduleResolution, noUncheckedIndexedAccess, outDir, rootDir (+6 more)

### Community 212 - "LeaderCallbacks"
Cohesion: 0.20
Nodes (7): New(), startLeading(), LeaderCallbacks, Provider, leadership, Provider, lease

### Community 213 - "Manager"
Cohesion: 0.17
Nodes (9): Manager, New(), Clients, TestMongoDBIntegration(), New(), failingClients, Node, Config (+1 more)

### Community 214 - "Registry"
Cohesion: 0.17
Nodes (10): Factory, Registry, New(), factory(), TestLookupUnknownIsNotFound(), TestRegisterAndLookup(), TestRegisterRejectsBadInput(), entry (+2 more)

### Community 215 - "ExecutionMode"
Cohesion: 0.14
Nodes (8): ExpressionPreviewer, Notifier, ExecutionMode, TestEnums(), Preview, failureItem(), notifies(), ExecutionMeta

### Community 216 - "http.ts"
Cohesion: 0.20
Nodes (5): sameToken(), startHttp(), Plugin, ServeHttpOptions, Descriptor

### Community 217 - "PropertyType"
Cohesion: 0.14
Nodes (14): PropertyType, Boolean, Code, Collection, CredentialsSelect, DateTime, FixedCollection, Json (+6 more)

### Community 219 - "Service"
Cohesion: 0.21
Nodes (4): decoyHash, LoginResult, Service, hashToken()

### Community 220 - ".Run"
Cohesion: 0.17
Nodes (8): hostHandler(), New(), nonNil(), Job, ExecutionInfo, FromExecutionInfo(), Config, Runner

### Community 221 - "BinaryRef"
Cohesion: 0.17
Nodes (9): addValue(), parseMultipart(), queryOf(), textOf(), uniqueKey(), BinaryRef, binaryRefs(), authResult (+1 more)

### Community 222 - "Deps"
Cohesion: 0.19
Nodes (10): cluster, jobQueue, JobDispatcher, JobQueue, JobReclaimer, VarsResolver, LeaseProvider, QueueEnv (+2 more)

### Community 223 - "Cache"
Cohesion: 0.15
Nodes (9): entry, Config, connectorFunc, Manager, New(), New(), Cache, New() (+1 more)

### Community 224 - "Result"
Cohesion: 0.20
Nodes (5): PendingWait, recordingObserver, ExecutionInfo, Result, busObserver

### Community 226 - "ParseTime"
Cohesion: 0.19
Nodes (8): ParseTime(), decodeVersion(), scanAPIKey(), scanUser(), notFound(), optTime(), decodeVersion(), WorkflowVersion

### Community 227 - "Manager"
Cohesion: 0.16
Nodes (8): Manager, New(), Clients, TestRedisIntegration(), New(), failingClients, Node, Config

### Community 228 - "input"
Cohesion: 0.19
Nodes (5): FromBinaryRef(), Unsupported(), blobAccess, hostCaller, input

### Community 229 - "ToolDefinition"
Cohesion: 0.19
Nodes (10): tool, tool, searchTool, Config, tool, ToolMeta, TestSnippet(), Snippet (+2 more)

### Community 231 - "Store"
Cohesion: 0.22
Nodes (3): Store, writeAtomic(), BlobScope

### Community 232 - "BlobAccess"
Cohesion: 0.15
Nodes (3): BlobScopes, BlobAccess, blobAllocator

### Community 233 - "Deps"
Cohesion: 0.18
Nodes (9): Deps, IDGenerator, parse(), KeyLocker, OAuth2Client, OAuth2Token, OAuth2TokenRequest, TypeRegistry (+1 more)

### Community 234 - "context.CancelFunc"
Cohesion: 0.22
Nodes (4): stopListener(), listener, listener, listenNode

### Community 235 - "listenEnv"
Cohesion: 0.29
Nodes (12): listenEnv(), listenNode(), newRecorder(), TestActivateValidatesListeners(), TestBackpressureHasHysteresisAndSamplesAtAnInterval(), TestListenerRestartsAfterFailure(), TestListenerRunsWithoutLeadershipAndDedupsEvents(), TestShutdownStopsListeners() (+4 more)

### Community 236 - "Subscription"
Cohesion: 0.18
Nodes (5): Bus, EventFilter, EventKind, Subscription, wireEvent

### Community 237 - "Handler"
Cohesion: 0.20
Nodes (6): loadChatAssets(), widgetCSP(), Handler, problem(), chatAsset, ExecutionIDs

### Community 240 - "stdio.ts"
Cohesion: 0.19
Nodes (9): HostError, UnsupportedError, hostError(), Id, Message, Pending, redirectStdout(), runStdio() (+1 more)

### Community 241 - "client"
Cohesion: 0.22
Nodes (5): Redact(), blocksParam(), nextCursor(), snippet(), client

### Community 242 - "schedule"
Cohesion: 0.24
Nodes (4): isValidation(), dueRun, schedule, scheduleNode

### Community 243 - "BinaryRef"
Cohesion: 0.16
Nodes (4): BinaryRef, inputCallbacks, BlobReadParams, fakeCB

### Community 244 - "ToFloat"
Cohesion: 0.24
Nodes (9): Config, Node, optFloat(), New(), optFloat(), ParamMap(), StringList(), ToFloat() (+1 more)

### Community 245 - "ToString"
Cohesion: 0.21
Nodes (9): tool, expandJSON(), expandURL(), interpolate(), placeholders(), text(), recordHeaders(), ToString() (+1 more)

### Community 246 - "memStore"
Cohesion: 0.24
Nodes (9): memStore, newMem(), service(), TestExecutionLimit(), TestItemLimit(), TestOpenRejectsMalformedReferences(), TestPutAndOpen(), TestScopedAccessStoresUnderTheExecution() (+1 more)

### Community 247 - "log/slog.Logger"
Cohesion: 0.21
Nodes (7): nodeLogInput, Server, New(), Deps, ApplyRuntime(), CredentialResolver, Deps

### Community 248 - "ExecutionInput"
Cohesion: 0.22
Nodes (7): inputToDomain(), inputToDTO(), ExecutionInput, Codec, inputDTO, savedProbe, envelope

### Community 249 - "BinaryRef"
Cohesion: 0.19
Nodes (6): allocator, binaryFrom(), MemoryBlobs, BinaryRef, blobInput, memBlob

### Community 250 - "JobID"
Cohesion: 0.22
Nodes (5): leaseResult(), leaseResult(), JobID, ackLostQueue, aliveQueue

### Community 251 - "Credential"
Cohesion: 0.21
Nodes (3): Credential, CredentialStore, Credentials

### Community 252 - ".toDomain"
Cohesion: 0.17
Nodes (3): NodeID, validateID(), TestIDValidate()

### Community 253 - "buildGraph"
Cohesion: 0.26
Nodes (5): graph, NodeRegistry, buildGraph(), backfillResumeSecret(), NewResumeSecret()

### Community 254 - "arrayOperators"
Cohesion: 0.32
Nodes (11): arrayOperators(), buildOperators(), dateOperators(), numberOperators(), stringOperators(), textOf(), textPair(), toArray() (+3 more)

### Community 255 - "ToolResult"
Cohesion: 0.29
Nodes (5): fakeTool, ErrorResult(), truncate(), result(), ToolResult

### Community 256 - "triggerAdapter"
Cohesion: 0.18
Nodes (4): TestArmer, UnavailableActivator, triggerAdapter, TestWebhook

### Community 257 - "BuildReport"
Cohesion: 0.29
Nodes (10): OutputReport, Report, ReportError, ReportItem, ReportPending, RunReport, BuildReport(), reportError() (+2 more)

### Community 258 - "Source"
Cohesion: 0.38
Nodes (6): found, itemKey, lineage, resolved, WaitingInputs, Source

### Community 259 - "Tracer"
Cohesion: 0.20
Nodes (6): observability, New(), Options, buildObservability(), TracingConfig, Tracer

### Community 260 - "NewRegistry"
Cohesion: 0.20
Nodes (8): nowFunc, TestRegistryHasEveryBuiltin(), NewRegistry(), New(), New(), New(), Clock, Node

### Community 261 - "AuthConfig"
Cohesion: 0.22
Nodes (7): APIKeyConfig, TestCookieSecureValidate(), defaultAuth(), AuthConfig, CookieSecureMode, SessionConfig, ParseCookieSecureMode()

### Community 262 - "NodeException"
Cohesion: 0.25
Nodes (6): NodeException, Description, HttpStatus, ItemIndex, ParamException, ParamName

### Community 263 - "Manager"
Cohesion: 0.24
Nodes (6): Manager, New(), nodeHTTPGet(), TestMinIO(), credential(), Config

### Community 264 - "Coerce"
Cohesion: 0.22
Nodes (8): Coerce(), coerceLiteral(), CoerceLiterals(), literalTypes(), TestConversions(), ToBool(), Condition, Group

### Community 265 - "Service"
Cohesion: 0.29
Nodes (3): Blobs, Leader, Service

### Community 266 - ".saveWired"
Cohesion: 0.36
Nodes (9): env, link(), passNode(), respondEnv(), respondNode(), TestArmTestNeedsAReachableRespondToWebhook(), TestChatResponseNodeNeedsAReachableRespondToWebhook(), TestResponseNodeNeedsAReachableRespondToWebhook() (+1 more)

### Community 267 - "ReadToolMeta"
Cohesion: 0.22
Nodes (5): Node, Node, ReadToolMeta(), schemaParam(), ValidToolName()

### Community 269 - "mustPrincipal"
Cohesion: 0.36
Nodes (3): mustPrincipal(), toUserDTO(), userID()

### Community 271 - ".buildBody"
Cohesion: 0.28
Nodes (8): mapMessage(), mentionsJSON(), strp(), requestBody, wireCall, wireMessage, wireResponseFormat, wireTool

### Community 275 - "Pools"
Cohesion: 0.39
Nodes (8): Config, Config, New(), Limits, Pools, New(), Node, Spec

### Community 276 - "Caller"
Cohesion: 0.32
Nodes (6): Limits, Response, Caller, New(), Config, Node

### Community 277 - ".serviceAccountToken"
Cohesion: 0.29
Nodes (4): client, apiMessage(), parseKey(), signJWT()

### Community 278 - "workflowDTO"
Cohesion: 0.25
Nodes (8): workflowToDTO(), nodeDTO, nodeSettingsDTO, noteDTO, retryDTO, settingsDTO, sizeDTO, workflowDTO

### Community 279 - "Compare"
Cohesion: 0.29
Nodes (7): TestKeyAndEqual(), Compare(), Equal(), Key(), numberOf(), compareSort(), sortValue

### Community 280 - "ListenTrigger"
Cohesion: 0.25
Nodes (4): ScheduleTrigger, WebhookResponder, ListenTrigger, ScheduleSpec

### Community 282 - "Deps"
Cohesion: 0.29
Nodes (4): Deps, PasswordHasher, New(), TestNewRequiresDependencies()

### Community 283 - "loginLimiter"
Cohesion: 0.52
Nodes (3): loginLimiter, LoginLimitConfig, newLoginLimiter()

### Community 285 - ".Enqueue"
Cohesion: 0.33
Nodes (5): IsForeignKeyViolation(), IsPrimaryKeyViolation(), IsUniqueViolation(), pgErr(), isForeignKeyViolation()

### Community 286 - "TestWebhooks"
Cohesion: 0.33
Nodes (3): TestWebhooks, NewTestWebhooks(), testKey()

### Community 288 - "BlobConfig"
Cohesion: 0.40
Nodes (5): BlobFSConfig, buildBlobs(), defaultBlob(), BlobConfig, New()

### Community 289 - "ExpressionConfig"
Cohesion: 0.33
Nodes (4): clockFunc, ExpressionConfig, defaultCfgForTest(), TestCompiledIsCached()

### Community 293 - "Limits"
Cohesion: 0.40
Nodes (4): Config, Clock, Limits, New()

### Community 295 - "Node"
Cohesion: 0.50
Nodes (4): Clock, Limits, Node, New()

### Community 296 - "newSSEReader"
Cohesion: 0.40
Nodes (3): newSSEReader(), sseEvent, sseReader

### Community 301 - "Node"
Cohesion: 0.67
Nodes (4): Config, Node, New(), TestDescriptor()

### Community 306 - "Node"
Cohesion: 1.00
Nodes (3): Config, Node, New()

### Community 307 - "Node"
Cohesion: 1.00
Nodes (3): Config, Node, New()

### Community 308 - "Node"
Cohesion: 1.00
Nodes (3): Config, Node, New()

### Community 310 - "Node"
Cohesion: 1.00
Nodes (3): Config, Node, New()

### Community 312 - "Node"
Cohesion: 1.00
Nodes (3): New(), Config, Node

## Knowledge Gaps
- **146 isolated node(s):** `engi-loadtest`, `github.com/engi-hq/engi`, `runtimeFixture`, `env`, `env` (+141 more)
  These have ≤1 connection - possible missing edges. (Counts symbols only; 1005 node(s) total have ≤1 connection when file, concept and rationale nodes are included.)
- **44 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `Descriptor` connect `Descriptor` to `Supervisor`, `server`, `.New`, `nodeInput`, `FromDescriptor`, `kafka/settings.go`, `upperNode`, `WebhookSpec`, `ExecuteParams`, `context.Context`, `Input`, `Property`, `CredentialType`, `startStdio`, `net/http.ResponseWriter`, `Registry`, `go_pkg_context`, `ChatMemoryStore`, `imaptrigger/settings.go`, `RenderCredentialTypes`, `PortKind`?**
  _High betweenness centrality (0.012) - this node is a cross-community bridge._
- **What connects `engi-loadtest`, `github.com/engi-hq/engi`, `runtimeFixture` to the rest of the system?**
  _146 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `Evaluator` be split into smaller, more focused modules?**
  _Cohesion score 0.09047619047619047 - nodes in this community are weakly interconnected._
- **Why does `State` connect `State` to `golden_test.go`, `BuildReport`, `Source`, `Workflow`, `Service`, `Execution`, `newEnv`, `newEnv`, `time.Time`, `Codec`, `nodeInput`, `time.Duration`, `itemDTO`, `runWF`, `Result`, `ExecutionID`, `ExecutionStatus`, `runner`, `ExecutionInput`, `buildGraph`?**
  _High betweenness centrality (0.011) - this node is a cross-community bridge._
- **Should `Default` be split into smaller, more focused modules?**
  _Cohesion score 0.07834101382488479 - nodes in this community are weakly interconnected._
- **Why does `ExecutionID` connect `ExecutionID` to `golden_test.go`, `Event`, `Service`, `Stamp`, `Queue`, `newRuntime`, `Generator`, `store_suite.go`, `Service`, `Execution`, `Executions`, `newEnv`, `newEnv`, `testing.T`, `UserID`, `sync.Mutex`, `Store`, `State`, `Worker`, `Runtime`, `encoding/json.RawMessage`, `.RunSubworkflow`, `Executions`, `FormatTime`, `server_api_test.go`, `FromDB`, `net/http.Request`, `queueEnv`, `ExecutionMode`, `BinaryRef`, `Result`, `ParseTime`, `ExecutionStatus`, `BlobAccess`, `Subscription`, `Handler`, `Deps`, `Job`, `Route`, `poller`, `.toDomain`?**
  _High betweenness centrality (0.011) - this node is a cross-community bridge._
- **Should `EditorPage.tsx` be split into smaller, more focused modules?**
  _Cohesion score 0.05232383808095952 - nodes in this community are weakly interconnected._