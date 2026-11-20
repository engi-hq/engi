# 11. Deployment & Configuration

## 11.1 Install modes

| Mode | Command / artifact | Store / queue |
|---|---|---|
| Standalone binary | `engi serve` | SQLite file, in-process queue and bus |
| Docker, single container | `docker run -v engi-data:/data -p 8080:8080 engi:dev` (`make image`) | same, data on a volume |
| docker-compose | `deploy/compose/docker-compose.yml`: engi alone, pointed at your infrastructure by `.env` | SQLite on a volume, or your Postgres and bucket; `docker-compose.postgres.yml` adds a local Postgres to try it |
| Docker Swarm | `deploy/swarm/docker-stack.yml` : one service per role | Postgres (optional service or external), optional MinIO |
| Kubernetes | `helm install engi deploy/helm/engi` | Standalone (SQLite on a PVC) with no values, cluster mode on your Postgres and S3 bucket |
| Air-gapped | Helm chart + image tarball pushed to a private registry | as above; no outbound calls |

Engi never phones home. The binary is static (no cgo). Release images and binaries are signed with cosign (keyless) and ship with an SBOM; see [Verifying images and binaries](#verifying-images-and-binaries).

### Container image

`Dockerfile` has three stages: the editor (`web/`, Node 22), the Go binary built with `-tags webui`
(`CGO_ENABLED=0`, `TARGETOS`/`TARGETARCH` cross-compilation, so `docker buildx build --platform
linux/amd64,linux/arm64` works without emulation), and `gcr.io/distroless/static-debian12:nonroot`
(uid 65532, no shell). `make image` builds `engi:$(VERSION)` (`IMAGE=repo:tag`, `PLATFORM=...` for
multi-arch; a multi-platform build is not loaded locally, add `--push` via your own buildx call).
Defaults: `ENGI_STORE_PATH=/data/engi.db`, `ENGI_BLOB_FS_DIR=/data/blobs`, `VOLUME /data`, port 8080,
`ENTRYPOINT ["/engi"]`, `CMD ["serve"]`. The image has no curl, so its `HEALTHCHECK` (and the
Compose/Swarm ones) run `engi healthcheck [/path]`: it calls `http://127.0.0.1:<server.addr port><path>`
(default `/readyz`) and exits 0 on 2xx.

**Image size.** The final image holds the stripped static binary (about 59 MB uncompressed; it links ~890 packages for every node integration, the web editor is about 1 MB of it) plus the distroless base (CA certificates, tzdata). Measured on `make image` (linux/arm64, Docker 28): **20.7 MB compressed**, about 64 MB unpacked, 14 layers. linux/amd64 is 22.7 MB compressed. Those are the numbers that matter for pulls. Measure with `docker image inspect --format '{{.Size}}' engi:<tag>` (compressed layers, what a registry transfers), `docker history engi:<tag>` (per-layer uncompressed size), or `docker export <container> | wc -c` (unpacked filesystem). Do not compare against `docker images`: on Docker Desktop's containerd image store it adds the unpacked snapshot to the compressed size (about 85 MB here), and a single-platform build that was not unpacked on the host shows only the compressed size. The two readings differ by that, not by a build bug.

### Verifying images and binaries

`.github/workflows/release.yml` runs on every `v*` tag. It builds `linux/amd64` and `linux/arm64`
from the `Dockerfile`, pushes `ghcr.io/<owner>/engi` (tags `X.Y.Z`, `X.Y`, and `latest` for
non-prerelease tags only), and attaches BuildKit SBOM and max-mode provenance attestations. The
image digest is then signed with keyless cosign (GitHub OIDC, no keys to manage) and an SPDX SBOM
is attached as a signed attestation. The GitHub release carries static binaries for
linux/darwin x amd64/arm64 (with the editor embedded), the SPDX SBOM, `SHA256SUMS` and its cosign bundle
`SHA256SUMS.sigstore.json`. The workflow has not run yet (no tag has been cut), so treat these
commands as unproven until the first release. Replace `<owner>` and `<tag>`:

```bash
# Image signature
cosign verify ghcr.io/<owner>/engi:<tag> \
  --certificate-identity-regexp '^https://github.com/<owner>/engi/.github/workflows/release.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com

# SBOM attestation (SPDX)
cosign verify-attestation ghcr.io/<owner>/engi:<tag> --type spdxjson \
  --certificate-identity-regexp '^https://github.com/<owner>/engi/.github/workflows/release.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com

# BuildKit SBOM stored in the image index
docker buildx imagetools inspect ghcr.io/<owner>/engi:<tag> --format '{{ json .SBOM }}'

# Binaries: verify the signed checksum file, then the files
cosign verify-blob SHA256SUMS --bundle SHA256SUMS.sigstore.json \
  --certificate-identity-regexp '^https://github.com/<owner>/engi/.github/workflows/release.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
sha256sum --check --ignore-missing SHA256SUMS
```

For a stricter policy, pin `--certificate-identity` to the exact tag. A run started with
`workflow_dispatch` from a branch is signed with a branch identity and will not match the regexp
above; run it from the tag.

### Bring your own infrastructure

Engi needs only two things from outside, and only in cluster mode: a **Postgres** (store, job
queue, event bus, leader lease) and an **S3-compatible bucket** (binary data shared by pods). Both
are yours: a managed service, an operator in your cluster, anything that speaks the protocol. The
chart and the compose file bundle neither; they take connection details and the names of Secrets
you already have. Everything else a workflow talks to (Kafka, Redis, MongoDB, MySQL, SMTP, IMAP,
Slack...) is a **credential** created in the editor, pointing at your existing service; it is not
deployment configuration.

| Setup | Store | Binary data | You provide |
|---|---|---|---|
| Standalone (default) | SQLite on the volume | the volume | nothing (Docker: the encryption key) |
| Cluster | your Postgres | your bucket | Postgres host and a password Secret (or a DSN Secret); bucket, region, endpoint and a key Secret (or the pods' cloud identity) |

Recovery: a job whose worker died is retried after `queue.leaseDuration` plus at most
`worker.reclaimInterval`. The chart sets 30s and 10s (the binary defaults are 2m and 30s); a
killed worker pod's execution finished 40s after the kill in the test below.

Verified on a single-node kubeadm cluster (Docker Desktop, Kubernetes 1.34) and Docker 28 with
`deploy/smoke.sh` (owner setup, a webhook workflow activated through the API pod, called through
the webhook pod, stored execution): standalone `helm install` with no values; restart and upgrade
keep data and the generated key; cluster mode on an existing Postgres 17 and MinIO (password with
URL special characters, binary data landing in the bucket); a worker pod force-killed mid-run;
Docker standalone, with the Postgres overlay, and against the same existing Postgres and MinIO.

### Load test

`deploy/loadtest/` holds a stdlib-only Go program that drives a deployment through its public HTTP
API. It logs in, creates and activates a webhook workflow (`responseMode: immediately`, then a Code
node that busy-waits `-work`, default 200 ms, and copies a unique request id into its output), fires
`-requests` calls at `-rate` per second, polls the executions API until all are terminal and
reports accepted/success/missing/duplicate counts, throughput and latency (request sent to
`finishedAt`, so it assumes the load generator and the cluster share a clock). It exits non-zero
unless every accepted request has exactly one `success` execution.

```bash
docker build -t engi-loadtest:dev deploy/loadtest       # 9 MB image, or: cd deploy/loadtest && go run . -h
kubectl -n engi create -f deploy/loadtest/job.yaml      # edit args/env first
kubectl -n engi logs -f job/<name>
# locally: LOADTEST_PASSWORD=... go run ./deploy/loadtest -api http://localhost:8080 -requests 2000 -rate 50
```

Setup of the numbers below: single-node Docker Desktop Kubernetes 1.34 (12 CPUs and 7.6 GiB shared
with everything else on the laptop), Postgres 17 and MinIO as in "Bring your own infrastructure",
1 API, 1 webhook, 2 trigger and N worker pods at `worker.concurrency=4`, load generator as a Job in
the same namespace. It is a smoke-level capacity check, not a benchmark: the Code node burns CPU, so
the figures say more about the laptop's cores than about Engi.

| Run | Requests | Accepted | Success | Missing / duplicate | Throughput | p50 / p95 / p99 |
|---|---|---|---|---|---|---|
| 4 workers, 50 req/s | 2000 | 2000 | 2000 | 0 / 0 | 49.8 exec/s | 0.21 / 0.22 / 0.24 s |
| 4 workers, 5 worker pods force-deleted (SIGTERM, drains) + trigger leader + webhook pod, 25 req/s | 3000 | 2991 (9 connection errors while the webhook pod restarted, none queued) | 2991 | 0 / 0 | 24.8 exec/s | 0.20 / 0.21 / 2.89 s (max 3.8 s) |
| 4 workers, 5 worker, the leader and the webhook container SIGKILLed (`docker kill`), 25 req/s | 3000 | 2987 (13 connection errors) | 2987 | 0 / 0 | 24.2 exec/s | 0.20 / 0.21 / 3.64 s (max 30.7 s) |
| Saturation, 1 worker, 400 req/s offered | 3000 | 3000 | 3000 | 0 / 0 | 19.5 exec/s | queueing |
| Saturation, 2 workers | 3000 | 3000 | 3000 | 0 / 0 | 38.6 exec/s | queueing |
| Saturation, 4 workers | 3000 | 3000 | 3000 | 0 / 0 | 67.4 exec/s | queueing |

Findings: throughput scales close to linearly with workers (4 slots per worker at 200 ms is about
20 exec/s each) until the node's CPUs are the limit; Postgres stayed at 52 to 57 of 100 connections
through all runs; no job was left in the queue and no execution stuck in `running`. A SIGKILLed
worker's in-flight executions finish only after the queue lease runs out (30 s lease plus the
reclaim interval, the 30.7 s maximum above); a `kubectl delete pod --grace-period=0 --force` still
delivers SIGTERM, so the worker drains and nothing is delayed. While the single webhook pod
restarted, callers got connection errors rather than accepted-then-lost calls; run two webhook
replicas (and retry on the caller side) if that matters. No duplicate execution was observed after
a kill, but the window (worker dies after finishing, before acking) is narrow and was not forced.

### docker-compose (single host)

`deploy/compose/docker-compose.yml` runs one engi container (all roles, `server.autoMigrate=true`)
and reads every setting from `.env`. Copy `.env.example`, set `ENGI_ENCRYPTION_KEYS`
(`k1:$(openssl rand -base64 32)`), and start: it runs standalone on SQLite in the `engi-data`
volume. Uncomment the `ENGI_STORE_*` lines for your Postgres and the `ENGI_BLOB_*` lines for your
bucket (leave the S3 keys empty to use the host's AWS identity). To try Postgres without one, add
the overlay: `docker compose -f docker-compose.yml -f docker-compose.postgres.yml up -d`. Secrets
are environment variables here; use Swarm or Kubernetes for file-mounted secrets.

### Docker Swarm

`deploy/swarm/docker-stack.yml` has one service per role (`api`, `webhook`, `trigger`, `worker`), a
one-shot `migrate` service (`engi migrate up`, restarts on failure only), and optional `postgres` and
`minio` services (drop them to use external ones). All roles read `/run/secrets/*` through
`ENGI_ENCRYPTION_KEYS_FILE`, `ENGI_STORE_POSTGRES_DSN_FILE` and `ENGI_BLOB_S3_SECRET_ACCESS_KEY_FILE`;
create the external secrets with the commands in the file header. `lease.type=postgres`,
`healthcheck: engi healthcheck`, `stop_grace_period` above the shutdown grace of each role, and
`update_config: {order: start-first}` for the stateless roles. Traefik labels route the UI/API host to
`api` and `PathPrefix(/hook)` on a separate host to `webhook` (both on the external `edge` overlay).
`docker stack config -c deploy/swarm/docker-stack.yml` validates the file.

## 11.2 Roles

`engi serve --roles=api,webhook,trigger,worker,runner` (default: all, which is standalone mode).
Role semantics are in [page 2, section 2.2](02-architecture.md#22-runtime-roles). In Kubernetes
each role is its own Deployment from the same image, differing only in `--roles`.
`engi migrate up|down` applies pending migrations or reverts the latest one and exits; `engi healthcheck [/path]` is the container health probe; `engi worker` is shorthand for `serve --roles=worker`.

## 11.3 Kubernetes contract

| Concern | Contract |
|---|---|
| Probes | `/healthz` liveness (process alive); `/readyz` readiness (store, queue, bus reachable; worker ready once subscribed). Separate startup probe for slow migrations |
| Shutdown | On SIGTERM: fail readiness, stop claiming jobs, drain in-flight work within `shutdown.gracePeriod`, release leases. Set `terminationGracePeriodSeconds` > `shutdown.gracePeriod` |
| Security context | `runAsNonRoot`, `readOnlyRootFilesystem`, `allowPrivilegeEscalation: false`, drop all capabilities, `seccompProfile: RuntimeDefault`. Writable paths are explicit `emptyDir`s |
| NetworkPolicy | Default deny. Allow: ingress to `api`/`webhook` from the ingress controller; egress to Postgres/queue/S3/DNS; `runner` gets **no** egress by default |
| Workers | KEDA `ScaledObject` on queue depth (Postgres scaler query on queued executions, or the Redis/NATS scaler for those adapters); `minReplicaCount` may be 1 |
| Disruption | `PodDisruptionBudget` per role (`api`, `webhook`, `worker`: `maxUnavailable: 1`; `trigger`: `minAvailable: 1`) |
| Leader election | `lease.type=kubernetes` (chart `lease.type`): Role + RoleBinding for `coordination.k8s.io` `leases` (create; get, update on the one Lease name) in the release namespace, bound to the `trigger` ServiceAccount only. Default `postgres` needs no RBAC |
| Scheduling | `topologySpreadConstraints` across zones; `trigger` runs 2 replicas, one active |
| Config | Env vars and mounted files only; ConfigMap for YAML, Secret for secrets |
| Observability | `/metrics` scrape annotation or `ServiceMonitor`; OTLP endpoint via config |
| Runner isolation | Optional `runtimeClassName` (gVisor/Kata) for the `runner` pool |

## 11.4 Helm values overview

Chart: `deploy/helm/engi`. `make helm-check` renders it for every supported setup and checks each
result, and that unworkable values fail with a clear message (cluster mode without Postgres, file
blobs across pods, S3 without a bucket...). `values.yaml` is commented key by key; a cluster-mode
example on existing services is `deploy/helm/examples/existing-infra.yaml`.

| Key | Purpose |
|---|---|
| `mode` | `standalone` (one pod, all roles, `Recreate` rollouts, SQLite and blobs on a PVC) or `cluster` (one Deployment per role). Empty: cluster when `postgres.host` or `postgres.existingSecret` is set |
| `postgres.host/port/database/user/sslmode` + `postgres.passwordSecret.name/key` | Your Postgres. The DSN carries no password; it comes from your Secret as `PGPASSWORD`, so any characters work and operator-managed Secrets (CloudNativePG, Bitnami, RDS) plug in as they are |
| `postgres.existingSecret` + `dsnKey` | Or a Secret holding the whole connection string, mounted as a file (`ENGI_STORE_POSTGRES_DSN_FILE`) |
| `blobs.driver` | `fs` or `s3`; default `fs` standalone, `s3` cluster |
| `blobs.s3.bucket/region/endpoint/forcePathStyle/prefix` | Your bucket (AWS S3, MinIO, R2, Ceph...); the bucket must exist |
| `blobs.s3.existingSecret` + `accessKeyIdKey/secretAccessKeyKey` | Access key pair from your Secret (secret key mounted as a file). Leave empty to use the pods' cloud identity (IRSA, Pod Identity, workload identity federation; annotate `serviceAccount`), which mounts the ServiceAccount token |
| `encryption.existingSecret/key/activeKeyId` | Your Secret with `keyId:base64` lines (needed for rotation). Without it the chart generates one key on first install, keeps it across upgrades (`lookup`) and leaves it on uninstall (`helm.sh/resource-policy: keep`); NOTES print the backup command |
| `standalone.persistence.*` | PVC for standalone mode (`existingClaim`, `storageClass`, `size`, kept on uninstall); `standalone.resources/extraEnv/...` size the pod |
| `config.*` | Extra `ENGI_*` settings for every pod (section 11.5). Defaults: metrics on, `queue.leaseDuration` 30s, `queue.heartbeatInterval` 10s, `worker.reclaimInterval` 10s |
| `lease.type/name/duration/renewDeadline/retryPeriod` | Cluster mode: `postgres` (default, no RBAC) or `kubernetes` (trigger pods get `ENGI_LEASE_*`, their own ServiceAccount and a Role/RoleBinding for Leases). Standalone uses `local` |
| `roles.<role>.enabled/replicas/resources/extraEnv/nodeSelector/tolerations/affinity/topologySpreadConstraints/terminationGracePeriodSeconds/pdb` | Cluster mode per-role sizing and scheduling; `pdb` takes `maxUnavailable` or `minAvailable` |
| `probes.liveness/readiness/startup` | `/healthz`, `/readyz` and a `/healthz` startup probe |
| `ingress.api.*` and `ingress.webhook.*` | Two Ingresses (both modes: in standalone the `-api` and `-webhook` Services select the one pod) |
| `migrate.*` | Cluster mode: `pre-install,pre-upgrade` Job running `engi migrate up`, with only the Postgres settings (nothing else of the release exists yet). Standalone migrates on start |
| `podSecurityContext`, `containerSecurityContext` | Non-root (65532), `RuntimeDefault` seccomp, read-only root filesystem, no privilege escalation, all capabilities dropped; `/tmp` is an `emptyDir`, `/data` the PVC (standalone) or an `emptyDir` |
| `networkPolicy.*` | One policy per Deployment: ingress to the HTTP port from `ingressFrom` (api, webhook, standalone) or `metricsFrom`; egress to DNS and `egressPorts` (add your S3 port if not 443), plus `extraEgress`; workers and the standalone pod may reach anything (`workerEgressAny`) |
| `serviceMonitor.*` | Optional Prometheus Operator `ServiceMonitor` on a headless metrics Service |
| `keda.worker.*`, `keda.trigger.*` | Cluster mode: optional `ScaledObject`s (Prometheus `max(engi_queue_depth)` for workers, Kafka lag for trigger pods) |

Not in the chart, by design: a bundled Postgres, S3 or message broker (bring yours), and Gateway API
routes. With Helm 4, change replica counts through values, not `kubectl scale`: server-side apply
refuses to take the field back (`--force-conflicts` does).

## 11.5 Configuration

Load order, later wins: **defaults → YAML file → `ENGI_*` environment → CLI flags**.

- Defaults are declared once in `internal/config` and mirrored in the table below.
- The file is `--config <path>` or `ENGI_CONFIG`; it is optional.
- Env names are `ENGI_` + the key path upper-cased, `.` and camelCase boundaries become `_` (`httpClient.maxRedirects` → `ENGI_HTTP_CLIENT_MAX_REDIRECTS`). Secrets that can come from a mounted file have an explicit `_FILE` key (`ENGI_ENCRYPTION_KEYS_FILE`, `ENGI_STORE_POSTGRES_DSN_FILE`, `ENGI_BLOB_S3_SECRET_ACCESS_KEY_FILE`).
- Validation runs at startup and fails fast with all errors listed. Planned: `engi config print` to show the effective config with secrets redacted.
- Packages never read the environment themselves.
- Unknown `ENGI_*` variables are ignored, so a typo has no effect; `engi serve` logs one WARN naming them (never their values) and starts anyway. Compose's `ENGI_IMAGE` and `ENGI_PORT` are not reported.

### Reference

This table mirrors `internal/config` (defaults live in `Default()` and the `default*` functions
there). Every key also has a flag, the env name in kebab-case (`ENGI_STORE_PATH` → `--store-path`).
Durations use Go syntax (`30s`, `5m`, `1h`). YAML keys are the key column; unknown keys in the file are rejected.

| Key | Env | Default | Meaning |
|---|---|---|---|
| (file) | `ENGI_CONFIG` / `--config` | none | Path of the optional YAML file |
| `engine.maxNodeRuns` | `ENGI_ENGINE_MAX_NODE_RUNS` | `1000` | Loop guard: runs of one node per execution |
| `engine.defaultNodeTimeout` | `ENGI_ENGINE_DEFAULT_NODE_TIMEOUT` | `5m` | Node timeout unless the node sets its own |
| `engine.executionTimeout` | `ENGI_ENGINE_EXECUTION_TIMEOUT` | `1h` | Execution timeout unless the workflow sets its own |
| `engine.maxSubworkflowDepth` | `ENGI_ENGINE_MAX_SUBWORKFLOW_DEPTH` | `10` | Sub-workflow nesting limit |
| `engine.maxRetryDelay` | `ENGI_ENGINE_MAX_RETRY_DELAY` | `5m` | Cap on the delay between retries |
| `expression.evalTimeout` | `ENGI_EXPRESSION_EVAL_TIMEOUT` | `2s` | Run-time limit of one expression |
| `expression.maxTemplateLength` | `ENGI_EXPRESSION_MAX_TEMPLATE_LENGTH` | `65536` | Largest expression template in bytes |
| `expression.maxOutputBytes` | `ENGI_EXPRESSION_MAX_OUTPUT_BYTES` | `1048576` | Largest serialized expression result |
| `expression.maxCallStackDepth` | `ENGI_EXPRESSION_MAX_CALL_STACK_DEPTH` | `128` | JavaScript recursion limit |
| `expression.programCacheSize` | `ENGI_EXPRESSION_PROGRAM_CACHE_SIZE` | `512` | Compiled templates kept in the LRU cache |
| `expression.envAllowList` | `ENGI_EXPRESSION_ENV_ALLOW_LIST` | empty | Comma-separated env vars readable as `$env.NAME` |
| `log.level` | `ENGI_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `log.format` | `ENGI_LOG_FORMAT` | `json` | `json` or `text` |
| `store.path` | `ENGI_STORE_PATH` | `engi.db` | SQLite file (the queue shares it; in-memory is not supported) |
| `store.journalMode` | `ENGI_STORE_JOURNAL_MODE` | `wal` | `wal` or `delete` |
| `store.synchronous` | `ENGI_STORE_SYNCHRONOUS` | `normal` | `off`, `normal`, `full` |
| `store.busyTimeout` | `ENGI_STORE_BUSY_TIMEOUT` | `5s` | Wait for a lock held by another connection |
| `store.maxOpenConns` / `maxIdleConns` | `ENGI_STORE_MAX_OPEN_CONNS` / `ENGI_STORE_MAX_IDLE_CONNS` | `8` / `4` | Connection pool size |
| `store.connMaxLifetime` | `ENGI_STORE_CONN_MAX_LIFETIME` | `0` (forever) | Recycle pooled connections |
| `store.defaultPageSize` / `maxPageSize` | `ENGI_STORE_DEFAULT_PAGE_SIZE` / `ENGI_STORE_MAX_PAGE_SIZE` | `50` / `500` | List page sizes |
| `queue.leaseDuration` | `ENGI_QUEUE_LEASE_DURATION` | `2m` | How long a claimed job stays owned without a heartbeat |
| `queue.heartbeatInterval` | `ENGI_QUEUE_HEARTBEAT_INTERVAL` | `30s` | Lease extension interval (must be shorter than the lease) |
| `queue.pollInterval` | `ENGI_QUEUE_POLL_INTERVAL` | `1s` | Fallback wait between claim attempts |
| `queue.maxAttempts` | `ENGI_QUEUE_MAX_ATTEMPTS` | `3` | Claims of one job before it is parked as failed |
| `queue.retryDelay` | `ENGI_QUEUE_RETRY_DELAY` | `5s` | Delay before a failed job can be claimed again |
| `worker.concurrency` | `ENGI_WORKER_CONCURRENCY` | `4` | Executions one worker runs in parallel |
| `worker.reclaimInterval` | `ENGI_WORKER_RECLAIM_INTERVAL` | `30s` | How often jobs with an expired lease are recovered |
| `worker.shutdownGrace` | `ENGI_WORKER_SHUTDOWN_GRACE` | `30s` | Time running executions get to finish on shutdown before they are released |
| `worker.persistTimeout` | `ENGI_WORKER_PERSIST_TIMEOUT` | `30s` | Time allowed for the final writes of an execution after its context ended |
| `worker.waiterInterval` | `ENGI_WORKER_WAITER_INTERVAL` | `1s` | How often due waiting executions are looked up |
| `worker.waiterBatchSize` | `ENGI_WORKER_WAITER_BATCH_SIZE` | `100` | Waiting executions resumed per tick |
| `worker.syncRunTimeout` | `ENGI_WORKER_SYNC_RUN_TIMEOUT` | `5m` | How long a synchronous run waits for its result |
| `worker.syncPollInterval` | `ENGI_WORKER_SYNC_POLL_INTERVAL` | `1s` | Fallback store check of a synchronous run |
| `worker.eventBuffer` | `ENGI_WORKER_EVENT_BUFFER` | `256` | Events an in-process bus subscriber may lag behind before events are dropped for it |
| `httpClient.timeout` | `ENGI_HTTP_CLIENT_TIMEOUT` | `30s` | Timeout of one outbound request incl. reading the body (a node may set its own deadline) |
| `httpClient.dialTimeout` | `ENGI_HTTP_CLIENT_DIAL_TIMEOUT` | `10s` | TCP connect timeout |
| `httpClient.tlsHandshakeTimeout` | `ENGI_HTTP_CLIENT_TLS_HANDSHAKE_TIMEOUT` | `10s` | TLS handshake timeout |
| `httpClient.idleConnTimeout` | `ENGI_HTTP_CLIENT_IDLE_CONN_TIMEOUT` | `90s` | Idle connection lifetime |
| `httpClient.maxIdleConns` | `ENGI_HTTP_CLIENT_MAX_IDLE_CONNS` | `100` | Idle connection pool size |
| `httpClient.maxRedirects` | `ENGI_HTTP_CLIENT_MAX_REDIRECTS` | `10` | Redirects followed per request |
| `httpClient.maxResponseBytes` | `ENGI_HTTP_CLIENT_MAX_RESPONSE_BYTES` | `10485760` | Largest response body a node can read |
| `httpClient.proxyUrl` | `ENGI_HTTP_CLIENT_PROXY_URL` | none | Proxy for all outbound requests (the process environment is not consulted) |
| `httpClient.tlsInsecureSkipVerify` | `ENGI_HTTP_CLIENT_TLS_INSECURE_SKIP_VERIFY` | `false` | Skip certificate verification (development only) |
| `httpClient.tlsCaFile` | `ENGI_HTTP_CLIENT_TLS_CA_FILE` | none | PEM bundle of extra trusted CAs |
| `httpClient.userAgent` | `ENGI_HTTP_CLIENT_USER_AGENT` | `engi` | Default `User-Agent` |
| `nodes.code.timeout` | `ENGI_NODES_CODE_TIMEOUT` | `10s` | Run-time limit of one Code node run |
| `nodes.code.maxCallStackDepth` | `ENGI_NODES_CODE_MAX_CALL_STACK_DEPTH` | `256` | Code node recursion limit |
| `nodes.code.maxOutputBytes` | `ENGI_NODES_CODE_MAX_OUTPUT_BYTES` | `8388608` | Largest serialized Code node result |
| `nodes.code.python.enabled` | `ENGI_NODES_CODE_PYTHON_ENABLED` | `true` | Register the Python Code node (it also uses `nodes.code.timeout` and `nodes.code.maxOutputBytes`) |
| `nodes.code.python.interpreter` | `ENGI_NODES_CODE_PYTHON_INTERPRETER` | `python3` | Python 3 executable, looked up on `PATH`; started fresh for every run with an empty environment |
| `nodes.code.python.allowedModules` | `ENGI_NODES_CODE_PYTHON_ALLOWED_MODULES` | `json,math,re,datetime,time,random,statistics,itertools,functools,collections,string,decimal,fractions,uuid,hashlib,base64,textwrap,unicodedata,operator,copy,typing,dataclasses,enum,html,urllib.parse` | Comma-separated modules user code may import; an entry allows that module and its submodules |
| `nodes.switch.maxRules` | `ENGI_NODES_SWITCH_MAX_RULES` | `8` | Rule outputs of the Switch node (a fallback output follows them) |
| `nodes.merge.maxInputs` | `ENGI_NODES_MERGE_MAX_INPUTS` | `2` | Inputs of the Merge node |
| `nodes.database.maxOpenConns` | `ENGI_NODES_DATABASE_MAX_OPEN_CONNS` | `10` | Max open connections per database pool (one pool per credential); also the Redis pool size and MongoDB `maxPoolSize` |
| `nodes.database.maxIdleConns` | `ENGI_NODES_DATABASE_MAX_IDLE_CONNS` | `2` | Idle connections kept per pool; must not exceed `maxOpenConns` |
| `nodes.database.idleTimeout` | `ENGI_NODES_DATABASE_IDLE_TIMEOUT` | `5m` | Idle connections are closed after this time |
| `nodes.database.maxLifetime` | `ENGI_NODES_DATABASE_MAX_LIFETIME` | `30m` | Connections are recycled after this time |
| `nodes.database.connectTimeout` | `ENGI_NODES_DATABASE_CONNECT_TIMEOUT` | `10s` | Timeout for establishing one database connection |
| `nodes.database.redisMaxKeys` | `ENGI_NODES_DATABASE_REDIS_MAX_KEYS` | `1000` | Max keys the Redis node's `keys` operation returns |
| `nodes.database.mongoMaxResults` | `ENGI_NODES_DATABASE_MONGO_MAX_RESULTS` | `1000` | Max documents one MongoDB find or aggregate returns |
| `nodes.kafka.connectTimeout` | `ENGI_NODES_KAFKA_CONNECT_TIMEOUT` | `10s` | Timeout for connecting to a broker |
| `nodes.kafka.produceTimeout` | `ENGI_NODES_KAFKA_PRODUCE_TIMEOUT` | `30s` | Time limit for producing the records of one Kafka node run |
| `nodes.kafka.flushTimeout` | `ENGI_NODES_KAFKA_FLUSH_TIMEOUT` | `10s` | Time to flush a producer when it closes |
| `nodes.kafka.maxRecordBytes` | `ENGI_NODES_KAFKA_MAX_RECORD_BYTES` | `1048576` | Largest key plus value the Kafka node sends |
| `nodes.email.connectTimeout` | `ENGI_NODES_EMAIL_CONNECT_TIMEOUT` | `15s` | SMTP and IMAP connect timeout |
| `nodes.email.sendTimeout` | `ENGI_NODES_EMAIL_SEND_TIMEOUT` | `2m` | Time limit of one Send Email run (all items) |
| `nodes.email.maxRecipients` | `ENGI_NODES_EMAIL_MAX_RECIPIENTS` | `100` | to + cc + bcc per message |
| `nodes.email.maxMessageBytes` | `ENGI_NODES_EMAIL_MAX_MESSAGE_BYTES` | `26214400` | Largest outgoing message, attachments included |
| `nodes.email.imapTimeout` | `ENGI_NODES_EMAIL_IMAP_TIMEOUT` | `1m` | Time limit of one IMAP poll |
| `nodes.email.imapMaxMessages` | `ENGI_NODES_EMAIL_IMAP_MAX_MESSAGES` | `50` | Mails one poll emits; the rest follow on the next polls |
| `nodes.email.imapMaxMessageBytes` | `ENGI_NODES_EMAIL_IMAP_MAX_MESSAGE_BYTES` | `10485760` | Larger mails are emitted with headers only (`truncated: true`); text bodies are cut at this size |
| `nodes.s3.maxKeys` | `ENGI_NODES_S3_MAX_KEYS` | `1000` | Most objects one S3 list returns |
| `nodes.s3.maxObjectBytes` | `ENGI_NODES_S3_MAX_OBJECT_BYTES` | `1048576` | Largest object read into an item as text or JSON (use binary for larger) |
| `nodes.s3.maxUploadBytes` | `ENGI_NODES_S3_MAX_UPLOAD_BYTES` | `67108864` | Largest body the S3 node uploads (buffered in memory to sign it) |
| `nodes.s3.presignMaxExpiry` | `ENGI_NODES_S3_PRESIGN_MAX_EXPIRY` | `24h` | Longest lifetime of a presigned URL |
| `nodes.s3.connectTimeout` | `ENGI_NODES_S3_CONNECT_TIMEOUT` | `10s` | Timeout for connecting to the S3 endpoint |
| `nodes.s3.requestTimeout` | `ENGI_NODES_S3_REQUEST_TIMEOUT` | `60s` | Time limit for one S3 request |
| `nodes.chat.slackBaseUrl` | `ENGI_NODES_CHAT_SLACK_BASE_URL` | `https://slack.com/api` | Slack Web API root |
| `nodes.chat.telegramBaseUrl` | `ENGI_NODES_CHAT_TELEGRAM_BASE_URL` | `https://api.telegram.org` | Telegram Bot API root; point it at a local Bot API server |
| `nodes.chat.graphBaseUrl` | `ENGI_NODES_CHAT_GRAPH_BASE_URL` | `https://graph.microsoft.com/v1.0` | Microsoft Graph root |
| `nodes.chat.maxRetryWait` | `ENGI_NODES_CHAT_MAX_RETRY_WAIT` | `30s` | Longest `Retry-After` the chat nodes wait out; a longer one fails the call |
| `nodes.chat.maxRetries` | `ENGI_NODES_CHAT_MAX_RETRIES` | `2` | Retries of a rate-limited (429) chat call |
| `nodes.chat.maxListResults` | `ENGI_NODES_CHAT_MAX_LIST_RESULTS` | `1000` | Channels one Slack "list channels" run returns |
| `nodes.chat.pageSize` | `ENGI_NODES_CHAT_PAGE_SIZE` | `200` | Page size of paginated chat list calls |
| `nodes.google.sheetsBaseUrl` | `ENGI_NODES_GOOGLE_SHEETS_BASE_URL` | `https://sheets.googleapis.com/v4` | Google Sheets API root |
| `nodes.google.maxRetryWait` | `ENGI_NODES_GOOGLE_MAX_RETRY_WAIT` | `30s` | Longest rate-limit (429) wait the Google Sheets node accepts |
| `nodes.google.maxRetries` | `ENGI_NODES_GOOGLE_MAX_RETRIES` | `2` | Retries of a rate-limited Google Sheets call |
| `nodes.google.maxReadRows` | `ENGI_NODES_GOOGLE_MAX_READ_ROWS` | `10000` | Rows one Google Sheets read returns per input item |
| `nodes.airtable.baseUrl` | `ENGI_NODES_AIRTABLE_BASE_URL` | `https://api.airtable.com/v0` | Airtable API root |
| `nodes.airtable.maxRetryWait` | `ENGI_NODES_AIRTABLE_MAX_RETRY_WAIT` | `30s` | Longest rate-limit (429) wait the Airtable node accepts |
| `nodes.airtable.maxRetries` | `ENGI_NODES_AIRTABLE_MAX_RETRIES` | `2` | Retries of a rate-limited Airtable call |
| `nodes.airtable.maxListResults` | `ENGI_NODES_AIRTABLE_MAX_LIST_RESULTS` | `1000` | Records one Airtable list returns per input item |
| `nodes.airtable.pageSize` | `ENGI_NODES_AIRTABLE_PAGE_SIZE` | `100` | Page size of Airtable list calls |
| `nodes.airtable.batchSize` | `ENGI_NODES_AIRTABLE_BATCH_SIZE` | `10` | Records per Airtable write request (Airtable's maximum is 10) |
| `nodes.ai.anthropicBaseUrl` | `ENGI_NODES_AI_ANTHROPIC_BASE_URL` | `https://api.anthropic.com` | Anthropic API root (an `engi.anthropic` credential may override it) |
| `nodes.ai.anthropicVersion` | `ENGI_NODES_AI_ANTHROPIC_VERSION` | `2023-06-01` | `anthropic-version` header value |
| `nodes.ai.openaiBaseUrl` | `ENGI_NODES_AI_OPENAI_BASE_URL` | `https://api.openai.com/v1` | OpenAI API root (an `engi.openai` credential may override it) |
| `nodes.ai.ollamaBaseUrl` | `ENGI_NODES_AI_OLLAMA_BASE_URL` | `http://localhost:11434` | Ollama root used when an `engi.ollama` credential sets no base URL |
| `nodes.ai.azureApiVersion` | `ENGI_NODES_AI_AZURE_API_VERSION` | `2024-10-21` | Azure OpenAI `api-version` when the credential sets none |
| `nodes.ai.anthropicDefaultModel` | `ENGI_NODES_AI_ANTHROPIC_DEFAULT_MODEL` | `claude-sonnet-5-5` | Model used when an AI node leaves the model empty |
| `nodes.ai.openaiDefaultModel` | `ENGI_NODES_AI_OPENAI_DEFAULT_MODEL` | `gpt-4o-mini` | Same, for OpenAI |
| `nodes.ai.ollamaDefaultModel` | `ENGI_NODES_AI_OLLAMA_DEFAULT_MODEL` | `llama3.2:3b` | Same, for Ollama |
| `nodes.ai.defaultMaxTokens` | `ENGI_NODES_AI_DEFAULT_MAX_TOKENS` | `4096` | Reply token cap when a node sets none (Anthropic requires one) |
| `nodes.ai.requestTimeout` | `ENGI_NODES_AI_REQUEST_TIMEOUT` | `2m` | Time limit of one model call, streaming included |
| `nodes.ai.rag.openaiEmbeddingModel` | `ENGI_NODES_AI_RAG_OPENAI_EMBEDDING_MODEL` | `text-embedding-3-small` | Embedding model used when an Embeddings node leaves the model empty (OpenAI) |
| `nodes.ai.rag.ollamaEmbeddingModel` | `ENGI_NODES_AI_RAG_OLLAMA_EMBEDDING_MODEL` | `nomic-embed-text` | Same, for Ollama (you must have pulled the model; Engi never pulls) |
| `nodes.ai.rag.embeddingBatchSize` | `ENGI_NODES_AI_RAG_EMBEDDING_BATCH_SIZE` | `64` | Texts per embeddings request; larger inputs are sent as several sequential requests |
| `nodes.ai.rag.embeddingRequestTimeout` | `ENGI_NODES_AI_RAG_EMBEDDING_REQUEST_TIMEOUT` | `1m` | Time limit of one embeddings request (one batch) |
| `nodes.ai.rag.maxTopK` | `ENGI_NODES_AI_RAG_MAX_TOP_K` | `100` | Largest `topK` of a vector search (Vector Store node and Vector Search tool) |
| `nodes.ai.rag.insertBatchSize` | `ENGI_NODES_AI_RAG_INSERT_BATCH_SIZE` | `100` | Rows per vector store `INSERT` statement |
| `nodes.ai.rag.maxDeleteIds` | `ENGI_NODES_AI_RAG_MAX_DELETE_IDS` | `1000` | Most chunk ids one vector store delete accepts |
| `nodes.ai.agent.maxIterations` | `ENGI_NODES_AI_AGENT_MAX_ITERATIONS` | `10` | AI Agent: model calls per item (a node may override) |
| `nodes.ai.agent.maxTotalTokens` | `ENGI_NODES_AI_AGENT_MAX_TOTAL_TOKENS` | `200000` | AI Agent: input plus output tokens per item over all calls (a node may override) |
| `nodes.ai.agent.timeout` | `ENGI_NODES_AI_AGENT_TIMEOUT` | `5m` | AI Agent: time limit per item, tool runs included (a node may override) |
| `nodes.ai.agent.toolConcurrency` | `ENGI_NODES_AI_AGENT_TOOL_CONCURRENCY` | `4` | AI Agent: tool calls running at once within one turn |
| `nodes.ai.agent.maxToolResultBytes` | `ENGI_NODES_AI_AGENT_MAX_TOOL_RESULT_BYTES` | `32768` | AI Agent: tool results longer than this are cut before going back to the model |
| `nodes.ai.memory.defaultWindow` | `ENGI_NODES_AI_MEMORY_DEFAULT_WINDOW` | `10` | Window Memory: messages loaded when the node sets no window |
| `nodes.ai.memory.maxWindow` | `ENGI_NODES_AI_MEMORY_MAX_WINDOW` | `200` | Window Memory: largest window a node may ask for |
| `nodes.ai.memory.maxMessages` | `ENGI_NODES_AI_MEMORY_MAX_MESSAGES` | `1000` | Chat memory: messages kept per session (oldest dropped on append) |
| `nodes.ai.memory.retention` | `ENGI_NODES_AI_MEMORY_RETENTION` | `720h` | Chat memory: messages older than this are deleted on every append |
| `nodes.ai.tool.httpTimeout` | `ENGI_NODES_AI_TOOL_HTTP_TIMEOUT` | `30s` | HTTP tool: time limit of one request |
| `nodes.ai.tool.httpMaxResponseBytes` | `ENGI_NODES_AI_TOOL_HTTP_MAX_RESPONSE_BYTES` | `1048576` | HTTP tool: response bytes returned to the model (the rest is cut) |
| `nodes.ai.tool.workflowTimeout` | `ENGI_NODES_AI_TOOL_WORKFLOW_TIMEOUT` | `2m` | Workflow tool: time limit of one child run |
| `nodes.mcp.requestTimeout` | `ENGI_NODES_MCP_REQUEST_TIMEOUT` | `30s` | Time limit of one request/response exchange of the MCP Client tool (also the SSE handshake) |
| `nodes.mcp.maxResponseBytes` | `ENGI_NODES_MCP_MAX_RESPONSE_BYTES` | `4MiB` | Largest MCP server response, event or stream line the client reads |
| `nodes.mcp.maxListPages` / `maxTools` | `ENGI_NODES_MCP_MAX_LIST_PAGES`, `ENGI_NODES_MCP_MAX_TOOLS` | `20` / `200` | `tools/list` pages followed and tools accepted; a server beyond either fails the node instead of being truncated |
| `nodes.mcp.maxResultBytes` | `ENGI_NODES_MCP_MAX_RESULT_BYTES` | `1MiB` | Text one MCP tool call returns to the model (longer output is cut and marked) |
| `credentials.activeKeyId` | `ENGI_ENCRYPTION_ACTIVE_KEY_ID` | none, required with keys | Key ID that encrypts new and updated credentials |
| `credentials.keys` | `ENGI_ENCRYPTION_KEYS` | none, required in server mode (secret) | Comma-separated `keyId:base64key` entries (32-byte keys). No CLI flag, to keep keys out of `argv`. Redacted in logs |
| `credentials.keysFile` | `ENGI_ENCRYPTION_KEYS_FILE`, `--encryption-keys-file` | none | File with one `keyId:base64key` per line (`#` comments allowed), merged with `keys`. Use this for mounted Secrets |
| `credentials.rotationBatchSize` | `ENGI_CREDENTIALS_ROTATION_BATCH_SIZE` | `100` | Credentials re-encrypted per batch by key rotation |
| `credentials.oauth2RefreshSkew` | `ENGI_CREDENTIALS_OAUTH2_REFRESH_SKEW` | `1m` | Refresh an access token this long before it expires |
| `credentials.oauth2StateTtl` | `ENGI_CREDENTIALS_OAUTH2_STATE_TTL` | `10m` | Lifetime of an authorization-code `state` |
| `credentials.oauth2RedirectUrl` | `ENGI_CREDENTIALS_OAUTH2_REDIRECT_URL` | none | Exact callback URL registered with OAuth2 providers (required to start an authorization-code flow) |
| `credentials.oauth2RequestTimeout` | `ENGI_CREDENTIALS_OAUTH2_REQUEST_TIMEOUT` | `15s` | Timeout of one token endpoint call |
| `credentials.oauth2RefreshLockTimeout` | `ENGI_CREDENTIALS_OAUTH2_REFRESH_LOCK_TIMEOUT` | `30s` | Cluster mode: longest wait for another replica that is refreshing the same credential's token. Only the Postgres lock uses it |
| `credentials.testTimeout` | `ENGI_CREDENTIALS_TEST_TIMEOUT` | `15s` | Timeout of one credential test |

| `roles` | `ENGI_ROLES` / `--roles` | all four | Comma-separated roles this process runs: `api`, `webhook`, `trigger`, `worker` |
| `server.addr` | `ENGI_SERVER_ADDR` | `:8080` | HTTP listen address |
| `server.autoMigrate` | `ENGI_SERVER_AUTO_MIGRATE` | `true` | Apply pending migrations on start; when `false` the server refuses to start with pending ones (run `engi migrate up`) |
| `server.readHeaderTimeout` / `readTimeout` / `writeTimeout` / `idleTimeout` | `ENGI_SERVER_READ_HEADER_TIMEOUT` etc. | `10s` / `30s` / `60s` / `120s` | net/http server timeouts (streams lift their write deadline) |
| `server.requestTimeout` | `ENGI_SERVER_REQUEST_TIMEOUT` | `30s` | Deadline of one API request (not streams) |
| `server.shutdownGrace` | `ENGI_SERVER_SHUTDOWN_GRACE` | `30s` | Time in-flight HTTP requests get on shutdown |
| `server.maxBodyBytes` | `ENGI_SERVER_MAX_BODY_BYTES` | `4194304` | Largest API request body (413 above) |
| `server.healthTimeout` | `ENGI_SERVER_HEALTH_TIMEOUT` | `2s` | Timeout of one readiness probe |
| `server.sseHeartbeat` | `ENGI_SERVER_SSE_HEARTBEAT` | `15s` | Keep-alive comment interval on event streams |
| `server.trustForwardedFor` | `ENGI_SERVER_TRUST_FORWARDED_FOR` | `false` | Take the client address (login limiting, access log) from the last `X-Forwarded-For` entry; only behind a proxy you control |
| `server.problemTypeBase` | `ENGI_SERVER_PROBLEM_TYPE_BASE` | `https://engi.dev/problems/` | Prefix of error `type` URIs |
| `server.publicUrl` | `ENGI_SERVER_PUBLIC_URL` | `http://localhost:8080` | Absolute base URL of this server, as clients reach it (scheme and host, optional path, no trailing `/`, no query). Absolute links Engi hands out are built on it. Set it in production; a value that is not an absolute http(s) URL fails startup |
| `server.oauth2CompleteRedirect` | `ENGI_SERVER_OAUTH2_COMPLETE_REDIRECT` | none | Where the browser goes after an OAuth2 authorization (`?credentialId=` is added); empty answers JSON |
| `server.cors.allowedOrigins` / `allowedMethods` / `allowedHeaders` | `ENGI_SERVER_CORS_ALLOWED_ORIGINS` etc. | none (CORS off) / common verbs / API headers | CORS allow-list (comma-separated) |
| `server.cors.allowCredentials` / `maxAge` | `ENGI_SERVER_CORS_ALLOW_CREDENTIALS` / `_MAX_AGE` | `false` / `10m` | Credentials header (not with `*`) and preflight cache |
| `server.ui.enabled` | `ENGI_SERVER_UI_ENABLED` | `true` | Serve the embedded editor at `/` (api role). Turn off for an API-only deployment |
| `server.ui.contentSecurityPolicy` | `ENGI_SERVER_UI_CONTENT_SECURITY_POLICY` | built-in strict policy (`default-src 'self'`, inline styles allowed) | `Content-Security-Policy` header of the editor's files |
| `server.pagination.defaultLimit` / `maxLimit` | `ENGI_SERVER_PAGINATION_DEFAULT_LIMIT` / `_MAX_LIMIT` | `50` / `200` | API page sizes |
| `auth.argon2.iterations` / `memoryKib` / `threads` | `ENGI_AUTH_ARGON2_*` | `2` / `19456` / `1` | Password hash cost (stored per hash). Default is OWASP's first-choice setting (m=19 MiB, t=2, p=1); every hash allocates `memoryKib`, so this is the main driver of login-time memory. Raising it also raises peak RSS |
| `auth.argon2.maxConcurrent` | `ENGI_AUTH_ARGON2_MAX_CONCURRENT` | `2` | Hashes allowed at once; extra logins wait. Bounds the spike to `maxConcurrent x memoryKib` |
| `auth.argon2.releaseMemory` | `ENGI_AUTH_ARGON2_RELEASE_MEMORY` | `true` | Returns hashing memory to the OS once no hash is running (`debug.FreeOSMemory`) |
| `runtime.memoryLimitMib` | `ENGI_RUNTIME_MEMORY_LIMIT_MIB` | `0` (off) | Soft Go memory limit (`debug.SetMemoryLimit`); the GC works harder near it and releases memory sooner. Ignored when `GOMEMLIMIT` is set. In a container use about 80-90% of the container limit; it is soft, so a real working set above it still grows |
| `runtime.pprofAddr` | `ENGI_RUNTIME_PPROF_ADDR` | none (off) | Serves `/debug/pprof/*` and `/debug/memstats` on this separate address; bind to loopback, never expose it |
| `auth.minPasswordLength` | `ENGI_AUTH_MIN_PASSWORD_LENGTH` | `10` | Shortest accepted password (max `auth.maxPasswordLength`, YAML only, `256`) |
| `auth.session.idleTtl` / `absoluteTtl` | `ENGI_AUTH_SESSION_IDLE_TTL` / `_ABSOLUTE_TTL` | `24h` / `168h` | Session expiry when unused / since login |
| `auth.session.cookieName` / `cookieDomain` | `ENGI_AUTH_SESSION_COOKIE_NAME` / `_DOMAIN` | `engi_session` / none | Session cookie |
| `auth.session.cookieSecure` | `ENGI_AUTH_SESSION_COOKIE_SECURE` | `always` | `Secure` attribute: `always`, `auto` (`Secure` on TLS, or on `X-Forwarded-Proto: https` from a proxy trusted by `server.trustForwardedFor`), `never` (plain HTTP and in-cluster clients that keep the cookie; they should use an API key, see [9.2](09-api.md#92-authentication)). `true`/`false` still work. `none` SameSite needs `always` or `auto` |
| `auth.session.cookieSameSite` | `ENGI_AUTH_SESSION_COOKIE_SAME_SITE` | `lax` | `lax`, `strict`, `none` (needs secure) |
| `auth.session.csrfHeader` | YAML only | `X-CSRF-Token` | Header carrying the CSRF token |
| `auth.session.tokenBytes` / `touchInterval` / `purgeInterval` | YAML only | `32` / `1m` / `10m` | Token size, last-use write throttle, expired-session purge |
| `auth.apiKey.prefix` / `maxPerUser` | `ENGI_AUTH_API_KEY_PREFIX` / `_MAX_PER_USER` | `engi` / `50` | API key prefix and active keys per user (also YAML: `lookupBytes`, `secretBytes`, `touchInterval`) |
| `auth.loginLimit.enabled` / `maxFailures` / `window` | `ENGI_AUTH_LOGIN_LIMIT_*` | `true` / `5` / `15m` | Failed-login throttle per account (YAML: `maxFailuresPerAddress` `20`, `maxTrackedKeys` `10000`) |
| `observability.metrics.enabled` | `ENGI_METRICS_ENABLED` | `false` | Serves Prometheus metrics. Off by default |
| `observability.metrics.path` | `ENGI_METRICS_PATH` | `/metrics` | URL path of the endpoint |
| `observability.metrics.listen` | `ENGI_METRICS_LISTEN` | none | Separate listener address for metrics. Empty serves them on the main server. Either way the endpoint is **outside API authentication**: bind it privately or restrict it in the network (NetworkPolicy, ingress) |
| `observability.metrics.buckets.execution` / `node` / `claim` / `http` / `llm` | YAML only | see below | Histogram upper bounds in seconds, strictly ascending |
| (metric) `engi_queue_jobs_reclaimed_total{outcome}` | none | n/a | Counts jobs taken back from a worker whose lease expired (the worker died or hung): `requeued` (retried by another worker) or `failed` (attempts exhausted, execution marked crashed). Each one is also logged by the reclaiming worker: `WARN job lease expired, job requeued` or `ERROR job lost its worker too often...`, with `job`, `execution`, `previous_owner` (worker id) and `attempts`. Alert on `increase(...[10m]) > 0`; a steady trickle points at OOM kills or lease/heartbeat settings |
| (log) execution lifecycle | none | n/a | The process that starts an execution logs `INFO execution queued` (`execution`, `workflow`, `mode`). The worker that runs it logs `INFO execution finished` (or `execution waiting`) with `execution`, `workflow`, `mode`, `status`, `nodes` (nodes that ran), `durationMs` and `worker`; a failed run logs `WARN execution finished` with `errorNode` and `error`. Follow one execution across pods with `kubectl logs ... \| grep <execution id>` |
| `observability.tracing.enabled` | `ENGI_TRACING_ENABLED` | `false` | Exports OpenTelemetry traces over OTLP/HTTP. Off by default |
| `observability.tracing.endpoint` | `ENGI_TRACING_ENDPOINT` | `localhost:4318` | OTLP/HTTP collector `host:port` |
| `observability.tracing.insecure` | `ENGI_TRACING_INSECURE` | `false` | Plain HTTP to the collector (local Jaeger or Tempo) |
| `observability.tracing.samplingRatio` | `ENGI_TRACING_SAMPLING_RATIO` | `1` | Fraction of new traces recorded (0 to 1); a trace continued from an incoming `traceparent` follows its parent's decision |
| `observability.tracing.serviceName` | `ENGI_TRACING_SERVICE_NAME` | `engi` | `service.name` resource attribute |

Default buckets (seconds): execution `0.01 0.05 0.1 0.5 1 5 10 30 60 300 900`; node `0.001 0.005 0.01 0.05 0.1 0.5 1 5 10 30 60`;
claim `0.001 0.005 0.01 0.05 0.1 0.5 1 5 30 60`; http `0.001 0.005 0.01 0.05 0.1 0.25 0.5 1 2.5 5 10`; llm `0.25 0.5 1 2 5 10 20 40 80 160`.

### Binary data (blob store)

Items carry references (`blob://<executionId>/<id>`); the bytes live in the blob store (page 3, section 3.4). Standalone uses a directory; a cluster uses an S3-compatible bucket (AWS, MinIO). The S3 credentials are server settings, not a user credential, and are secrets: supply them by environment or file, never commit them.

| Key | Env | Default | Meaning |
|---|---|---|---|
| `blob.driver` | `ENGI_BLOB_DRIVER` | `fs` | `fs` or `s3` |
| `blob.maxItemBytes` | `ENGI_BLOB_MAX_ITEM_BYTES` | `268435456` | Largest single binary value; larger uploads fail (webhook uploads answer 413) |
| `blob.maxExecutionBytes` | `ENGI_BLOB_MAX_EXECUTION_BYTES` | `1073741824` | Total binary data of one execution |
| `blob.orphanGrace` | `ENGI_BLOB_ORPHAN_GRACE` | `1h` | Age before blobs of an execution that does not exist are removed by the sweep after pruning (an upload precedes its execution record) |
| `blob.fs.dir` | `ENGI_BLOB_FS_DIR` | `blobs` | `fs` base directory, created on the first write. Put it on a persistent volume and include it in backups |
| `blob.s3.bucket` | `ENGI_BLOB_S3_BUCKET` | none | Required for `s3`; the bucket must exist |
| `blob.s3.region` | `ENGI_BLOB_S3_REGION` | `us-east-1` | |
| `blob.s3.endpoint` | `ENGI_BLOB_S3_ENDPOINT` | none | Custom endpoint (MinIO); empty uses AWS |
| `blob.s3.forcePathStyle` | `ENGI_BLOB_S3_FORCE_PATH_STYLE` | `false` | Path-style addressing (MinIO) |
| `blob.s3.prefix` | `ENGI_BLOB_S3_PREFIX` | none | Prepended to every object key |
| `blob.s3.accessKeyId` / `secretAccessKey` | `ENGI_BLOB_S3_ACCESS_KEY_ID` / `ENGI_BLOB_S3_SECRET_ACCESS_KEY` | none | Secrets; set both, or neither to use the AWS default credential chain (environment, web identity token as with IRSA or workload identity federation, shared config, instance metadata) |
| `blob.s3.secretAccessKeyFile` | `ENGI_BLOB_S3_SECRET_ACCESS_KEY_FILE` | none | File holding the secret access key (Docker secret, mounted Secret); wins over `secretAccessKey` |
| `blob.s3.partSize` | `ENGI_BLOB_S3_PART_SIZE` | `8388608` | Multipart part size (min 5 MiB); smaller values are one `PutObject` |
| `blob.s3.connectTimeout` | `ENGI_BLOB_S3_CONNECT_TIMEOUT` (YAML only) | `10s` | |

### Cluster mode (Postgres)

`store.driver: postgres` switches the store, job queue, event bus and leader election to Postgres
(`internal/adapter/{pgdb,store/postgres,queue/postgres,bus/postgres,lease/postgres}`). Postgres is the only
dependency. Run several replicas against one database: jobs are claimed with `FOR UPDATE SKIP LOCKED` (each
job runs on exactly one pod), events and queue wake-ups travel over `LISTEN/NOTIFY` between pods, and one
replica at a time holds the leader advisory lock (schedules and pollers run only there).

**Connections per pod.** Every pod opens one shared pool, capped by `store.maxOpenConns` (default `8`, it
opens connections on demand and keeps up to `maxIdleConns` idle), plus 3 dedicated connections outside the
pool: the queue `LISTEN`, the event-bus `LISTEN` and the leader-lease advisory lock. So a pod holds at most
`maxOpenConns + 3` (11 by default) and typically 5 to 7 when idle. Size Postgres `max_connections` to at
least `pods x (maxOpenConns + 3)` plus headroom for migrations, admin sessions and `nodes.database` pools
(Postgres nodes use their own pools, see the data sources page), or lower `store.maxOpenConns`.

| Key | Env | Default | Meaning |
|---|---|---|---|
| `store.driver` | `ENGI_STORE_DRIVER` | `sqlite` | `sqlite` or `postgres` |
| `store.postgres.dsn` | `ENGI_STORE_POSTGRES_DSN` | none (secret) | Connection string. Prefer the env var or the file; never commit it. Errors never echo it |
| `store.postgres.dsnFile` | `ENGI_STORE_POSTGRES_DSN_FILE` | none | File holding the DSN (a mounted Secret); wins over `dsn` |
| `store.postgres.connectTimeout` | `ENGI_STORE_POSTGRES_CONNECT_TIMEOUT` | `10s` | Bound on opening a connection |
| `store.postgres.connMaxIdleTime` | `ENGI_STORE_POSTGRES_CONN_MAX_IDLE_TIME` | `5m` | Idle pooled connections are closed after this |
| `store.postgres.applicationName` | `ENGI_STORE_POSTGRES_APPLICATION_NAME` | `engi` | Shown in `pg_stat_activity` |
| `store.postgres.listenReconnectDelay` | `ENGI_STORE_POSTGRES_LISTEN_RECONNECT_DELAY` | `2s` | Pause before a lost LISTEN connection (queue, bus) is re-established |
| `store.maxOpenConns` / `maxIdleConns` / `connMaxLifetime` | `ENGI_STORE_*` | `8` / `4` / `0` | Pool sizes (shared with SQLite). Budget `maxOpenConns` + 3 dedicated connections (queue and bus LISTEN, lease) per replica against `max_connections` |
| `queue.driver` | `ENGI_QUEUE_DRIVER` | `auto` | `auto` follows `store.driver`; the queue lives in the store's database, so it must match |
| `queue.pollInterval` | `ENGI_QUEUE_POLL_INTERVAL` | `1s` | With Postgres only the fallback when a NOTIFY is missed |
| `bus.driver` | `ENGI_BUS_DRIVER` | `auto` | `auto`: `memory` for sqlite, `postgres` for postgres |
| `bus.maxInlineBytes` | `ENGI_BUS_MAX_INLINE_BYTES` | `7000` | Largest event sent inside a NOTIFY (Postgres allows just under 8000 bytes). Larger events are stored in `bus_events` and only the id is notified |
| `bus.eventRetention` | `ENGI_BUS_EVENT_RETENTION` | `5m` | How long stored large events are kept |
| `lease.type` | `ENGI_LEASE_TYPE` | `auto` | `auto`: `postgres` for a postgres store, else `local`; or `local` / `postgres` / `kubernetes` explicitly |
| `lease.lockKey` | `ENGI_LEASE_LOCK_KEY` | `7313401` | Advisory lock key; replicas sharing database and key elect one leader |

Migrations come from `migrations/postgres` (the four SQLite migrations translated to `timestamptz`, `jsonb`,
`boolean`, `bytea`, plus `0005` for `bus_events`). `engi migrate up|down` and `server.autoMigrate` work as for
SQLite; concurrent replicas are serialized by an advisory lock. The lease check pings its connection every
`lease.retryPeriod`; after a network partition the server may free the lock before the old leader notices, so
leader work must stay idempotent. Bus delivery is best-effort: events published while a pod's listener
connection is down are lost to that pod (durable state lives in the tables).

### Leader election (Kubernetes Lease)

`lease.type: kubernetes` (`internal/adapter/lease/kubernetes`) elects the leader with a
`coordination.k8s.io/v1` Lease through the in-cluster REST API; it is a small get/create/update client, so
the binary does not link client-go. A candidate takes over after it has seen the record unchanged for
`lease.duration` on its own clock (no clock-skew dependence); writes carry the `resourceVersion`, so only
one candidate wins; the leader renews every `retryPeriod`, steps down after failing for `renewDeadline`,
and clears the holder on shutdown. RBAC: `create` on `leases`, `get`/`update` on the one name.

| Key | Env | Default | Meaning |
|---|---|---|---|
| `lease.duration` | `ENGI_LEASE_DURATION` | `15s` | Validity of the lease without renewal |
| `lease.renewDeadline` | `ENGI_LEASE_RENEW_DEADLINE` | `10s` | How long the leader retries renewal before stepping down; shorter than `duration` |
| `lease.retryPeriod` | `ENGI_LEASE_RETRY_PERIOD` | `2s` | Pause between acquire and renew attempts (also the Postgres connection check) |
| `lease.name` | `ENGI_LEASE_NAME` | `engi-trigger` | Lease object name (kubernetes) |
| `lease.namespace` | `ENGI_LEASE_NAMESPACE` | pod namespace | Empty reads `lease.namespaceFile` |
| `lease.identity` | `ENGI_LEASE_IDENTITY` | host name | Holder identity (the pod name) |
| `lease.apiServer` | `ENGI_LEASE_API_SERVER` | `https://kubernetes.default.svc` | API server URL |
| `lease.tokenFile` / `caFile` / `namespaceFile` | `ENGI_LEASE_TOKEN_FILE` / `_CA_FILE` / `_NAMESPACE_FILE` | `/var/run/secrets/kubernetes.io/serviceaccount/{token,ca.crt,namespace}` | Service-account files (the token is re-read per request) |
| `listen.retryDelay` | `ENGI_LISTEN_RETRY_DELAY` | `5s` | Pause before a failed listener trigger (Kafka) restarts |
| `kafka.pauseAboveDepth` / `resumeBelowDepth` / `depthCheckInterval` | `ENGI_KAFKA_PAUSE_ABOVE_DEPTH`, `_RESUME_BELOW_DEPTH`, `_DEPTH_CHECK_INTERVAL` | `1000` / `500` / `2s` | Backpressure (top-level YAML key `kafka`, like `webhook` and `lease`): listeners stop fetching while the job queue holds more than `pauseAboveDepth` jobs and resume below `resumeBelowDepth`; the depth is sampled at most every interval. Resume must be lower than pause |
| `kafka.batchMaxMessages` / `batchMaxWait` | `ENGI_KAFKA_BATCH_MAX_MESSAGES`, `_BATCH_MAX_WAIT` | `1000` / `30s` | Caps for the `batchSize` and `batchWaitMs` a Kafka trigger node may ask for (activation fails above them). `batchMaxWait` must be shorter than `rebalanceTimeout` |
| `kafka.sessionTimeout` / `rebalanceTimeout` | `ENGI_KAFKA_SESSION_TIMEOUT`, `_REBALANCE_TIMEOUT` | `45s` / `60s` | Consumer group session and rebalance timeouts |
| `kafka.maxPollRecords` | `ENGI_KAFKA_MAX_POLL_RECORDS` | `500` | Records taken from the Kafka client per poll |
| `poll.minInterval` | `ENGI_POLL_MIN_INTERVAL` | `15s` | Shortest interval a polling trigger (IMAP) may ask for; activation fails below it |
| `poll.maxFailures` | `ENGI_POLL_MAX_FAILURES` | `10` | Failed polls in a row before the workflow is deactivated with the last error recorded |
| `poll.maxBackoff` | `ENGI_POLL_MAX_BACKOFF` | `10m` | Longest wait after a failed poll (doubles from the trigger's interval) |
| `retention.enabled` | `ENGI_RETENTION_ENABLED` | `false` | Turns on the pruning job. It runs on the leader only, so one replica prunes |
| `retention.interval` / `batchSize` | `ENGI_RETENTION_INTERVAL`, `_BATCH_SIZE` | `1h` / `500` | Pause between passes; executions deleted per round (a pass repeats rounds until none are left) |
| `workflow.maxNotes` | `ENGI_WORKFLOW_MAX_NOTES` | `200` | Most sticky notes one workflow may hold; a save over the limit is a 422 |
| `workflow.maxNoteTextBytes` | `ENGI_WORKFLOW_MAX_NOTE_TEXT_BYTES` | `16384` | Largest markdown text of one sticky note, in bytes |
| `retention.maxAge.success` / `error` / `canceled` / `crashed` | `ENGI_RETENTION_MAX_AGE_SUCCESS`, `_ERROR`, `_CANCELED`, `_CRASHED` | `336h` / `720h` / `336h` / `720h` | How long finished executions are kept, counted from their start time; `0` keeps that status forever. Binary data goes with the execution; a final sweep removes orphaned blobs. Unfinished executions are never pruned |
| `plugins.enabled` / `dir` | `ENGI_PLUGINS_ENABLED`, `ENGI_PLUGINS_DIR` | `true` / `plugins` | Load the active version of every plugin installed in `dir` at start ([page 15](15-plugins.md)); a plugin that fails is logged and skipped |
| `plugins.inheritEnv` | `ENGI_PLUGINS_INHERIT_ENV` | `PATH,HOME,LANG,TZ,TMPDIR,SYSTEMROOT` | The only server variables a stdio plugin process gets, besides its manifest's `env`. `ENGI_*` names are refused |
| `plugins.startTimeout` / `callTimeout` / `shutdownGrace` | `ENGI_PLUGINS_START_TIMEOUT`, `_CALL_TIMEOUT`, `_SHUTDOWN_GRACE` | `10s` / `5m` / `5s` | Starting a plugin and its handshake; one `execute` call; time to exit before a kill |
| `plugins.idleTimeout` / `restartBackoff` | `ENGI_PLUGINS_IDLE_TIMEOUT`, `_RESTART_BACKOFF` | `0` / `1s` | Stop a stdio plugin after this long without calls (`0` never; restarted on demand); least time between two starts |
| `plugins.maxConcurrentCalls` | `ENGI_PLUGINS_MAX_CONCURRENT_CALLS` | `16` | Calls in flight per plugin; more wait for a slot |
| `plugins.maxMessageBytes` / `maxBlobBytes` / `maxLogLineBytes` | `ENGI_PLUGINS_MAX_MESSAGE_BYTES`, `_MAX_BLOB_BYTES`, `_MAX_LOG_LINE_BYTES` | `32MiB` / `16MiB` / `4KiB` | One protocol message; one `blob.read`/`blob.write` (must fit a message once base64-encoded); plugin stderr and log lines are cut here |
| `plugins.startErrorLines` | `ENGI_PLUGINS_START_ERROR_LINES` | `20` | Last stderr lines of a plugin that fails to start, appended to its start error and its status `lastError` (each cut at `maxLogLineBytes`); `0` shows none |
| `webhook.basePath` / `testBasePath` / `waitBasePath` | `ENGI_WEBHOOK_BASE_PATH`, `_TEST_BASE_PATH`, `_WAIT_BASE_PATH` | `/hook` / `/hook-test` / `/hook-wait` | URL prefixes of production, test and wait-resume webhooks |
| `webhook.maxBodyBytes` / `responseTimeout` / `testTimeout` | `ENGI_WEBHOOK_*` | `16MiB` / `120s` / `120s` | Body cap (413), how long a call waits for the workflow's answer (504), how long an armed test webhook listens |
| `webhook.idempotencyHeader` | `ENGI_WEBHOOK_IDEMPOTENCY_HEADER` | `Idempotency-Key` | Header that deduplicates calls; empty disables |
| `webhook.activationPageSize` | `ENGI_WEBHOOK_ACTIVATION_PAGE_SIZE` | `100` | Active workflows loaded per page when triggers are restored at start |
| `chat.basePath` | `ENGI_CHAT_BASE_PATH` | `/chat` | URL prefix of Chat trigger endpoints and widgets (top-level YAML key `chat`); must differ from the webhook prefixes. Route it to the `webhook` role in an Ingress |
| `chat.maxBodyBytes` / `maxSessionIdLength` | `ENGI_CHAT_MAX_BODY_BYTES`, `ENGI_CHAT_MAX_SESSION_ID_LENGTH` | `64KiB` / `128` | Chat request body cap (413), longest client-supplied session id (400) |
| `chat.responseTimeout` / `preflightMaxAge` | `ENGI_CHAT_RESPONSE_TIMEOUT`, `ENGI_CHAT_PREFLIGHT_MAX_AGE` | `50s` / `10m` | How long a chat message waits for the reply (504; keep below `server.writeTimeout`), how long browsers cache a CORS preflight |
| `mcpServer.basePath` | `ENGI_MCP_SERVER_BASE_PATH` | `/mcp` | URL prefix of MCP Server trigger endpoints (YAML section `mcpServer`); must differ from the webhook and chat prefixes. Route it to the `webhook` role in an Ingress |
| `mcpServer.maxBodyBytes` | `ENGI_MCP_SERVER_MAX_BODY_BYTES` | `1MiB` | MCP request body cap (413) |
| `mcpServer.callTimeout` / `maxConcurrentCalls` | `ENGI_MCP_SERVER_CALL_TIMEOUT`, `ENGI_MCP_SERVER_MAX_CONCURRENT_CALLS` | `50s` / `32` | How long one `tools/call` waits for its workflow (keep below `server.writeTimeout`), and how many run at once per process (more get a JSON-RPC "server busy" error) |
| `mcpServer.allowedOrigins` / `serverName` | `ENGI_MCP_SERVER_ALLOWED_ORIGINS`, `ENGI_MCP_SERVER_NAME` | none / `engi` | Browser origins (comma-separated) allowed to call MCP endpoints; requests without an `Origin` header always pass. Name sent in `serverInfo` |
| `schedule.defaultTimezone` / `tick` | `ENGI_SCHEDULE_DEFAULT_TIMEZONE`, `_TICK` | `UTC` / `1s` | Zone of schedules that name none; how often due schedules are looked for (the precision of every schedule) |
| `schedule.missedPolicy` / `missedGrace` | `ENGI_SCHEDULE_MISSED_POLICY`, `_MISSED_GRACE` | `skip` / `1m` | `skip` or `runOnce` for a run overdue by more than the grace |

### Planned keys (not in `internal/config` yet)

These belong to later phases. Each will join the table above when its section is implemented.

| Key | Env | Default | Meaning |
|---|---|---|---|
| `api.rateLimit.*` | `ENGI_API_RATE_LIMIT_*` | `true` / `600` / `100` | API rate limiting (body size and pagination are `server.*` above) |
| `datasource.pool.maxOpen/maxIdle/idleTimeout/maxPools` | `ENGI_DATASOURCE_POOL_*` | `10` / `2` / `5m` / `100` | DB data source pools |

## 11.6 Secrets handling

- Required secrets: encryption key, DB password/DSN, optional S3 keys and OAuth client secrets. None have defaults and none are generated per pod.
- Provide them as env vars or `_FILE` mounts from a Kubernetes Secret, External Secrets Operator or Vault Agent. Never in `values.yaml` or the YAML config committed to Git.
- Losing the encryption key makes stored credentials unrecoverable. Back it up separately from the database.

### Credential encryption key

Credentials are encrypted with AES-256-GCM ([page 7](07-data-sources.md#72-encryption-at-rest)). The key is a 32-byte random value, base64 encoded, and has a short ID of your choosing.

```bash
# 1. generate a key
openssl rand -base64 32

# 2. supply it (environment) ...
export ENGI_ENCRYPTION_KEYS="k1:<base64 key>"
export ENGI_ENCRYPTION_ACTIVE_KEY_ID=k1

# ... or mount a file with one "keyId:base64key" per line and point at it
export ENGI_ENCRYPTION_KEYS_FILE=/run/secrets/engi-keys
export ENGI_ENCRYPTION_ACTIVE_KEY_ID=k1
```

- There is no default and no generated key. Server roles fail at startup with an explanatory message when no key is configured; the in-memory `engi run` works without one (it has no credential store).
- **Rotation:** add a second `keyId:base64key`, make it active, restart, then re-encrypt the stored rows (`credential.Service.RotateKeys`, exposed as `engi credentials reencrypt` with the API phase). Remove the old key only after the run reports no rows left on it.

## 11.7 Backup and restore

| Mode | Backup |
|---|---|
| Standalone | `engi backup --out file` (SQLite online backup + blobs) or stop and copy `data/` |
| Postgres | `pg_dump` / CNPG backups / PITR, plus the blob bucket versioning |
| Always | The encryption key, stored separately |

Restore: restore database and blobs, provide the **same** encryption key (or keyring), start
`engi migrate`, then `serve`. Executions that were `running` at backup time are marked
`crashed` on startup recovery and may be retried.

## 11.8 Upgrades

- Migrations are forward-only SQL in `migrations/<driver>/`. A binary refuses to start if the schema is newer than it understands, and (cluster) if it is older than required.
- In Helm, a `migrate` Job runs as a `pre-install,pre-upgrade` hook before any role rolls out; roles wait on schema version via readiness.
- Rolling upgrades are supported across one minor version: migrations are expand-then-contract, so old and new pods can overlap.
- Standalone: stop, replace the binary or image, start; migrations run on boot (`server.autoMigrate`, default `true`; set `false` in cluster mode and run `engi migrate up` as a Job).

---
**← Previous** [10. Editor UI](10-editor-ui.md) · **Index** [README](README.md) · **Next →** [12. Feature Plan & Roadmap](12-roadmap.md)
