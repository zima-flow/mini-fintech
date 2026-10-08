SHELL := /bin/bash
GOBIN := $(shell go env GOPATH)/bin
BUF_VERSION := v1.47.2
PROTOC_GEN_GO_VERSION := v1.36.6
PROTOC_GEN_GO_GRPC_VERSION := v1.5.1
GOLANGCI_VERSION := v2.14.0
GOLANGCI_VERSION_NUM := 2.14.0
GOLANGCI := $(GOBIN)/golangci-lint
GOLANGCI_CONFIG := $(CURDIR)/.golangci.yml
COMPOSE := docker compose -f deploy/docker-compose.yml --env-file .env
AUTH_KEY := deploy/keys/auth_ed25519_private.pem

MODULE_DIRS := $(shell go list -m -f '{{.Dir}}')

.PHONY: help tools generate fmt lint test test-integration migrate e2e e2e-stage1 auth-keygen up down

help: ## List targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  %-18s %s\n", $$1, $$2}'

tools: ## Install pinned codegen tools
	@test -x "$(GOBIN)/buf" || { echo "installing buf $(BUF_VERSION)"; \
		go install github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION); }
	@test -x "$(GOBIN)/protoc-gen-go" || go install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)
	@test -x "$(GOBIN)/protoc-gen-go-grpc" || go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GO_GRPC_VERSION)

generate: tools ## Regenerate Go from .proto
	cd contracts && PATH="$(GOBIN):$$PATH" buf generate
	go work sync

fmt: ## Format the workspace
	gofmt -w $$(git ls-files '*.go')

lint: ## Lint every workspace module (golangci-lint v2)
	@test "$$($(GOLANGCI) version 2>/dev/null | grep -o '$(GOLANGCI_VERSION_NUM)')" = "$(GOLANGCI_VERSION_NUM)" || { \
		echo "installing golangci-lint $(GOLANGCI_VERSION)"; \
		go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION); }
	@set -e; for d in $(MODULE_DIRS); do \
		rel="$${d#$(CURDIR)/}"; \
		if [ -z "$$(cd "$$d" && go list ./... 2>/dev/null)" ]; then \
			echo ">> lint $$rel (no packages, skipped)"; \
			continue; \
		fi; \
		echo ">> lint $$rel"; \
		"$(GOLANGCI)" run --config "$(GOLANGCI_CONFIG)" "./$$rel/..."; \
	done

test: ## Fast unit tests for every module (no Docker; integration is build-tag gated)
	@set -e; for d in $(MODULE_DIRS); do \
		rel="$${d#$(CURDIR)/}"; \
		if [ -z "$$(cd "$$d" && go list ./... 2>/dev/null)" ]; then \
			echo ">> test $$rel (no packages, skipped)"; \
			continue; \
		fi; \
		echo ">> test $$rel"; \
		(cd "$$d" && go test ./... -race -count=1); \
	done

test-integration: ## Integration tests for every module (testcontainers, needs Docker)
	@set -e; for d in $(MODULE_DIRS); do \
		rel="$${d#$(CURDIR)/}"; \
		if [ -z "$$(cd "$$d" && go list ./... 2>/dev/null)" ]; then \
			echo ">> test-integration $$rel (no packages, skipped)"; \
			continue; \
		fi; \
		echo ">> test-integration $$rel"; \
		(cd "$$d" && go test -tags=integration ./... -count=1); \
	done

migrate: ## Apply migrations for every service (runs each cmd/* with -migrate)
	@set -e; for d in $(MODULE_DIRS); do \
		rel="$${d#$(CURDIR)/}"; \
		cmds="$$(cd "$$d" && go list ./cmd/... 2>/dev/null || true)"; \
		if [ -z "$$cmds" ]; then echo ">> migrate $$rel (no cmd, skipped)"; continue; fi; \
		for pkg in $$cmds; do \
			echo ">> migrate $$rel:$$pkg"; \
			(cd "$$d" && go run "$$pkg" -migrate); \
		done; \
	done

e2e: ## End-to-end scenario over docker-compose
	./scripts/e2e.sh

e2e-stage1: ## Stage-1 REST smoke test (register -> login -> profile -> RBAC) over docker-compose
	./scripts/e2e-stage1.sh

auth-keygen: ## Generate the auth Ed25519 dev key (PKCS#8 PEM, idempotent)
	@mkdir -p $(dir $(AUTH_KEY))
	@if [ -f "$(AUTH_KEY)" ]; then \
		echo "auth key already present: $(AUTH_KEY)"; \
	else \
		openssl genpkey -algorithm ED25519 -out "$(AUTH_KEY)"; \
		chmod 600 "$(AUTH_KEY)"; \
		echo "generated auth key: $(AUTH_KEY)"; \
	fi
	@openssl pkey -in "$(AUTH_KEY)" -noout && echo "key is loadable: $(AUTH_KEY)"

up: ## Start local infrastructure and wait until healthy
	@$(COMPOSE) up -d --wait || { \
		echo "make up failed; removing partially started containers" >&2; \
		$(COMPOSE) down >/dev/null 2>&1 || true; \
		exit 1; }
	$(COMPOSE) run --rm createbuckets

down: ## Stop local infrastructure
	$(COMPOSE) down
