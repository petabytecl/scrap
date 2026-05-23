.DEFAULT_GOAL := help

.PHONY: help
.PHONY: proto proto-check
.PHONY: fmt fmt-check lint vuln
.PHONY: test test-compat test-race build check
.PHONY: image manifests-render manifests-check local-kind-create local-kind-delete local-kind-load local-kind-deploy local-kind-smoke local-kind-evidence
.PHONY: release-check crash-fault-evidence
.PHONY: spike-write-path spike-write-path-raft spike-write-path-raft-durable spike-write-path-raft-cluster

GO ?= go
BUF ?= buf
DOCKER ?= docker
KIND ?= kind
KUBECTL ?= kubectl
KUSTOMIZE ?= go run sigs.k8s.io/kustomize/kustomize/v5@v5.7.1
TEST_PACKAGES ?= ./...
COMPAT_PACKAGES ?= ./internal/compat
LINT_TIMEOUT ?= 5m
GOLANGCI_LINT_VERSION ?= v2.10.1
GOLANGCI_LINT ?= $(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
GOVULNCHECK_VERSION ?= v1.3.0
PROTO_BREAKING_REF ?= main
PROTO_BREAKING_AGAINST ?= .git#branch=$(PROTO_BREAKING_REF)
SCRAP_BINS := ./cmd/scrapd ./cmd/scrap-spike ./cmd/scrapctl ./cmd/scrap-release-gate ./cmd/scrap-crash-fault-evidence
RELEASE_SHA ?= $(shell git rev-parse HEAD)
RELEASE_VERSION ?= dev
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
DIRTY_TREE ?= $(shell git diff --quiet && git diff --cached --quiet && echo clean || echo dirty)
IMAGE_NAME ?= localhost/scrapd:local
SCRAPD_IMAGE_BINARY ?= bin/scrapd-linux-amd64
KIND_CLUSTER ?= scrap-local
LOCAL_KIND_NAMESPACE ?= scrap-local
LOCAL_KIND_OVERLAY ?= deploy/kustomize/overlays/local-kind
LOCAL_KIND_EVIDENCE_REPORT ?= local-kind-evidence.json

help: ## Show this help.
	@awk 'BEGIN { FS = ":.*##"; printf "\n\033[1mUsage:\033[0m\n  make \033[36m<target>\033[0m\n" } /^[a-zA-Z0-9_.-]+:.*##/ { printf "  \033[36m%-34s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) }' $(MAKEFILE_LIST)

##@ Protobuf

proto: ## Generate protobuf and gRPC code.
	$(BUF) generate

proto-check: ## Lint schemas, check breaking changes, and verify generated code.
	$(BUF) lint
	@if git cat-file -e "$(PROTO_BREAKING_REF):buf.yaml" 2>/dev/null && git cat-file -e "$(PROTO_BREAKING_REF):proto" 2>/dev/null; then \
		$(BUF) breaking --against "$(PROTO_BREAKING_AGAINST)"; \
	else \
		echo "buf breaking skipped: $(PROTO_BREAKING_REF) has no proto module yet"; \
	fi
	$(BUF) generate
	git diff --exit-code -- internal/gen

##@ Development

fmt: ## Format Go source using configured golangci formatters.
	$(GOLANGCI_LINT) fmt

fmt-check: ## Check formatter drift using configured golangci formatters.
	$(GOLANGCI_LINT) fmt --diff

lint: ## Run the golangci-lint baseline.
	$(GOLANGCI_LINT) run --timeout=$(LINT_TIMEOUT)

vuln: ## Run govulncheck against the module graph.
	$(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

test: ## Run package tests.
	$(GO) test $(TEST_PACKAGES)

test-compat: ## Run compatibility tests for stored data and metadata boundaries.
	$(GO) test $(COMPAT_PACKAGES)

test-race: ## Run package tests with the Go race detector.
	$(GO) test -race $(TEST_PACKAGES)

build: ## Build all supported command binaries.
	$(GO) build $(SCRAP_BINS)

check: fmt-check proto-check test-compat lint test test-race build ## Run the full local verification gate.

##@ Release Artifacts

image: ## Build the local scrapd container image.
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags "-s -w" -o "$(SCRAPD_IMAGE_BINARY)" ./cmd/scrapd
	$(DOCKER) build \
		--build-arg SCRAP_RELEASE_SHA="$(RELEASE_SHA)" \
		--build-arg SCRAP_VERSION="$(RELEASE_VERSION)" \
		--build-arg SCRAP_BUILD_TIME="$(BUILD_TIME)" \
		--build-arg SCRAP_DIRTY_TREE="$(DIRTY_TREE)" \
		-t "$(IMAGE_NAME)" .

manifests-render: ## Render the local-kind GitOps manifests.
	@$(KUSTOMIZE) build "$(LOCAL_KIND_OVERLAY)"

manifests-check: ## Validate that the local-kind GitOps manifests render.
	@tmp="$$(mktemp)"; \
		$(KUSTOMIZE) build "$(LOCAL_KIND_OVERLAY)" > "$$tmp"; \
		test -s "$$tmp"; \
		rm -f "$$tmp"

local-kind-create: ## Create the local kind cluster for release rehearsal.
	$(KIND) create cluster --name "$(KIND_CLUSTER)" --config deploy/kind/cluster.yaml

local-kind-delete: ## Delete the local kind cluster.
	$(KIND) delete cluster --name "$(KIND_CLUSTER)"

local-kind-load: image ## Load the scrapd image into the local kind cluster.
	$(KIND) load docker-image "$(IMAGE_NAME)" --name "$(KIND_CLUSTER)"

local-kind-deploy: manifests-check ## Apply the local-kind release rehearsal manifests.
	$(KUSTOMIZE) build "$(LOCAL_KIND_OVERLAY)" | $(KUBECTL) apply -f -

local-kind-smoke: ## Run a local kind admin smoke check.
	@./scripts/local-kind-smoke.sh

local-kind-evidence: manifests-check ## Emit a local kind release rehearsal evidence report.
	@./scripts/local-kind-evidence.sh > "$(LOCAL_KIND_EVIDENCE_REPORT)"

##@ Release Evidence

release-check: ## Verify release evidence from RELEASE_EVIDENCE_MANIFEST.
	@test -n "$(RELEASE_EVIDENCE_MANIFEST)" || (echo "RELEASE_EVIDENCE_MANIFEST is required" >&2; exit 2)
	$(GO) run ./cmd/scrap-release-gate --tier release --manifest "$(RELEASE_EVIDENCE_MANIFEST)"

crash-fault-evidence: ## Emit crash/fault evidence JSON for dedicated runners.
	$(GO) run ./cmd/scrap-crash-fault-evidence --out "$${SCRAP_CRASH_FAULT_EVIDENCE_REPORT:-crash-fault-evidence.json}"

##@ Spikes

spike-write-path: ## Run the local write-path spike.
	$(GO) run ./cmd/scrap-spike

spike-write-path-raft: ## Run the write-path spike with Raft commit barriers.
	$(GO) run ./cmd/scrap-spike -raft-barrier

spike-write-path-raft-durable: ## Run the write-path spike with durable Raft barriers.
	$(GO) run ./cmd/scrap-spike -raft-durable-barrier

spike-write-path-raft-cluster: ## Run the write-path spike with clustered Raft barriers.
	$(GO) run ./cmd/scrap-spike -raft-cluster-barrier
