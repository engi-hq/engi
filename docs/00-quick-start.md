# Quick Start

Get Engi running, open the editor, and run your first workflow.

!!! note "Pre-release"
    Engi is under active development and not yet tagged for production use. APIs and node
    parameters may still change. See the [roadmap](12-roadmap.md).

## Run from source

You need Go 1.27+ (and Node.js 24 if you want the editor UI).

```bash
git clone https://github.com/engi-hq/engi.git && cd engi
make build WEB=1                      # builds ./bin/engi with the editor embedded

export ENGI_ENCRYPTION_KEYS="k1:$(openssl rand -base64 32)"   # keep this key: it encrypts your credentials
export ENGI_ENCRYPTION_ACTIVE_KEY_ID=k1
export ENGI_AUTH_SESSION_COOKIE_SECURE=false                  # local HTTP only
./bin/engi serve
```

Open <http://localhost:8080>, create the owner account, and build your first workflow.

Engi stores everything in a local SQLite file and runs its job queue in the same process, so
there is nothing else to install. Every setting can be changed with an `ENGI_*` environment
variable, a YAML file or a flag: see [Deployment & Configuration](11-deployment.md).

## Run with Docker Compose (Engi + Postgres)

No prebuilt image is published yet, so build it locally first:

```bash
make image IMAGE=engi:dev
cat > deploy/compose/.env <<ENV
POSTGRES_PASSWORD=change-me
ENGI_ENCRYPTION_KEYS=k1:$(openssl rand -base64 32)
ENV
docker compose -f deploy/compose/docker-compose.yml up -d
```

For Kubernetes (Helm + KEDA) and Docker Swarm, see [Deployment & Configuration](11-deployment.md).

## Run a workflow from the command line

No server needed:

```bash
./bin/engi validate examples/switch-routing.workflow.json
./bin/engi run examples/switch-routing.workflow.json --input examples/switch-routing.input.json
```

## Your first workflow

The `examples/` folder in the repository has small workflows you can import from the editor
(*Workflows → Import*) or run with `engi run`. A good first one is
`sum-check-email.workflow.json`: it adds two numbers, checks the result with an IF node, and
sends an email only when it matches.

```
Manual Trigger → Set values → Add → IF (sum = 12?) ──true──→ Send Email
```

## Where to go next

- [Expressions](05-expressions.md): use `{{ $json.total * 1.2 }}` in any field.
- [Triggers & Webhooks](08-triggers.md): start workflows from webhooks, schedules, Kafka or email.
- [Node Reference](nodes/README.md): every built-in node's parameters, ports and credentials.
- [HTTP API](09-api.md): automate Engi itself over REST.

**← Previous** [Index](README.md) · **Index** [README](README.md) · **Next →** [1. Vision & Principles](01-vision.md)
