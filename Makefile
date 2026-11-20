# Engi build tooling. Run `make help` for targets.

BINARY  := bin/engi
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.DEFAULT_GOAL := help
.PHONY: help build run test test-integration lint fmt vet check tidy web web-check image helm-check docs-nodes docs-check docs-site docs-serve

# WEB=1 builds the editor first and embeds it (needs Node.js 22 or newer). Without it the
# binary serves a small placeholder page and `go build` needs no Node.
WEB     ?= 0
GOTAGS  := $(if $(filter 1,$(WEB)),-tags webui,)
UI_DIST := internal/adapter/http/ui/dist

help: ## List targets
	@grep -E '^[a-z-]+:.*##' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "%-10s %s\n", $$1, $$2}'

web: ## Install and build the editor, then copy it where go:embed finds it
	cd web && npm ci && npm run build
	rm -rf $(UI_DIST)
	cp -R web/dist $(UI_DIST)

web-check: ## Lint, typecheck and test the editor
	cd web && npm ci && npm run lint && npm run typecheck && npm test

build: $(if $(filter 1,$(WEB)),web) ## Build ./bin/engi (WEB=1 embeds the editor)
	CGO_ENABLED=0 go build $(GOTAGS) -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/engi

# Local standalone server. Uses engi.yaml when it exists (gitignored, local);
# otherwise the binary's defaults. Data paths stay overridable by ENGI_* env vars.
run: build ## Build, then run the standalone server (uses engi.yaml when present)
	@if [ -z "$$ENGI_ENCRYPTION_KEYS" ] && [ -z "$$ENGI_ENCRYPTION_KEYS_FILE" ] && [ ! -f engi.yaml ]; then \
		echo 'engi: set a credential key first: export ENGI_ENCRYPTION_KEYS="k1:$$(openssl rand -base64 32)" ENGI_ENCRYPTION_ACTIVE_KEY_ID=k1' >&2; \
		exit 1; \
	fi
	./$(BINARY) serve $(if $(wildcard engi.yaml),--config engi.yaml)

IMAGE ?= engi:$(VERSION)

image: ## Build the container image (IMAGE=repo:tag; PLATFORM=linux/amd64,linux/arm64 for multi-arch via buildx)
	docker buildx build $(if $(PLATFORM),--platform $(PLATFORM)) --build-arg VERSION=$(VERSION) -t $(IMAGE) $(if $(PLATFORM),,--load) .

helm-check: ## Render the Helm chart for every supported setup and check it (needs helm)
	deploy/helm/check.sh

test: ## Run all tests with the race detector
	go test -race ./...

test-integration: ## Run integration tests (build tag integration; needs Docker)
	go test -race -tags integration ./...

lint: ## Run golangci-lint
	golangci-lint run

fmt: ## Format all Go code
	gofmt -s -w cmd internal pkg

vet: ## Run go vet
	go vet ./...

tidy: ## Sync go.mod and go.sum
	go mod tidy

check: fmt vet lint test ## fmt + vet + lint + test

docs-nodes: ## Regenerate the Node Reference tables in docs/nodes
	go run ./cmd/nodedocs

docs-check: ## Fail if docs/nodes is stale or a node lacks a section
	go run ./cmd/nodedocs -check

DOCS_VENV := .venv-docs
DOCS_CFG  := docs/website/mkdocs.yml

$(DOCS_VENV): docs/website/requirements.txt
	python3 -m venv $(DOCS_VENV)
	$(DOCS_VENV)/bin/pip install -q -r docs/website/requirements.txt
	touch $(DOCS_VENV)

docs-site: $(DOCS_VENV) ## Build the docs website into ./site (needs Python 3)
	$(DOCS_VENV)/bin/mkdocs build -f $(DOCS_CFG)

docs-serve: $(DOCS_VENV) ## Preview the docs website with live reload
	$(DOCS_VENV)/bin/mkdocs serve -f $(DOCS_CFG)
