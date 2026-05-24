.DEFAULT_GOAL := help

.PHONY: help
.PHONY: proto proto-check
.PHONY: fmt fmt-check lint vuln
.PHONY: test test-compat test-crashfault-catalog test-race test-junit cover build check
.PHONY: image manifests-render manifests-check local-kind-create local-kind-delete local-kind-load local-kind-deploy local-kind-smoke local-kind-evidence
.PHONY: release-check crash-fault-evidence capacity-sample openbao-smoke-evidence local-soak-evidence local-dr-drill-evidence
.PHONY: spike-write-path spike-write-path-raft spike-write-path-raft-durable spike-write-path-raft-cluster

GO ?= go
BUF ?= buf
DOCKER ?= docker
KIND ?= kind
KUBECTL ?= kubectl
KUSTOMIZE ?= go run sigs.k8s.io/kustomize/kustomize/v5@v5.7.1
TEST_PACKAGES ?= ./...
COMPAT_PACKAGES ?= ./internal/compat ./internal/metastore
COVER_PACKAGES ?= $(shell $(GO) list $(TEST_PACKAGES) | grep -v '/internal/gen/')
COVERPROFILE ?= coverage.out
COVERMODE ?= atomic
TEST_RESULTS_DIR ?= test-results
JUNIT_REPORT ?= $(TEST_RESULTS_DIR)/junit.xml
GOTESTSUM_VERSION ?= v1.13.0
GOTESTSUM ?= $(GO) run gotest.tools/gotestsum@$(GOTESTSUM_VERSION)
LINT_TIMEOUT ?= 5m
GOLANGCI_LINT_VERSION ?= v2.10.1
GOLANGCI_LINT ?= $(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
GOVULNCHECK_VERSION ?= v1.3.0
PROTO_BREAKING_REF ?= main
PROTO_BREAKING_AGAINST ?= .git#branch=$(PROTO_BREAKING_REF)
SCRAP_BINS := ./cmd/scrapd ./cmd/scrap-spike ./cmd/scrapctl ./cmd/scrap-release-gate ./cmd/scrap-crash-fault-evidence ./cmd/scrap-openbao-smoke ./cmd/scrap-local-soak-evidence ./cmd/scrap-local-dr-drill-evidence
RELEASE_SHA ?= $(shell git rev-parse HEAD)
RELEASE_VERSION ?= dev
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
DIRTY_TREE ?= $(shell git diff --quiet && git diff --cached --quiet && echo clean || echo dirty)
IMAGE_NAME ?= localhost/scrapd:local
IMAGE_GOOS ?= linux
IMAGE_GOARCH ?= $(shell $(GO) env GOARCH)
IMAGE_PLATFORM ?= $(IMAGE_GOOS)/$(IMAGE_GOARCH)
SCRAPD_IMAGE_BINARY ?= bin/scrapd-$(IMAGE_GOOS)-$(IMAGE_GOARCH)
KIND_CLUSTER ?= scrap-local
LOCAL_KIND_OVERLAY ?= deploy/kustomize/overlays/local-kind
LOCAL_KIND_EVIDENCE_REPORT ?= local-kind-evidence.json
PROFILE_ID ?= scrap-prod-v1
SCRAP_ADMIN_ADDR ?= 127.0.0.1:18081
SCRAP_WORKLOAD_IDENTITY ?= local-operator
SCRAP_PUBLIC_ADDR ?= 127.0.0.1:18080
SCRAP_PUBLIC_WORKLOAD_IDENTITY ?= local-public-client
CAPACITY_SAMPLE_BACKEND_URL ?= http://127.0.0.1:4566/scrap-local
CAPACITY_SAMPLE_BACKEND_REGION ?= us-east-1
CAPACITY_SAMPLE_OPENBAO_ADDR ?= http://127.0.0.1:8200
CAPACITY_SAMPLE_OPENBAO_KEY_PATH ?= transit/keys/scrap-backend
CAPACITY_SAMPLE_REPORT ?= capacity-sample-advisory.json
OPENBAO_SMOKE_REPORT ?= openbao-transit-smoke-evidence.json
OPENBAO_SMOKE_OUTAGE_ADDR ?= http://127.0.0.1:1
LOCAL_SOAK_REPORT ?= local-soak-evidence.json
LOCAL_SOAK_RUNNER ?= local-kind
LOCAL_SOAK_IMAGE_IDENTITY ?= $(IMAGE_NAME)
LOCAL_DR_DRILL_REPORT ?= local-dr-drill-evidence.json
LOCAL_DR_DRILL_RUNNER ?= local-kind
LOCAL_DR_DRILL_IMAGE_IDENTITY ?= $(IMAGE_NAME)
LOCAL_DR_DRILL_OPERATOR_OWNER ?= @cotocisternas

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

test-crashfault-catalog: ## Verify crash/fault catalog patterns match real tests.
	GO="$(GO)" $(GO) test -tags=integration ./internal/crashfault

test-race: ## Run package tests with the Go race detector.
	$(GO) test -race $(TEST_PACKAGES)

test-junit: ## Run package tests and write a JUnit XML report.
	mkdir -p "$(TEST_RESULTS_DIR)"
	$(GOTESTSUM) --junitfile "$(JUNIT_REPORT)" --format testname -- $(TEST_PACKAGES)

cover: ## Run package tests and write a coverage profile.
	$(GO) test -covermode=$(COVERMODE) -coverprofile=$(COVERPROFILE) $(COVER_PACKAGES)
	$(GO) tool cover -func="$(COVERPROFILE)" | tail -n 1

build: ## Build all supported command binaries.
	$(GO) build $(SCRAP_BINS)

check: manifests-check fmt-check proto-check test-compat lint test test-crashfault-catalog test-race build ## Run the full local verification gate.

##@ Release Artifacts

image: ## Build the local scrapd container image.
	mkdir -p "$(dir $(SCRAPD_IMAGE_BINARY))"
	CGO_ENABLED=0 GOOS=$(IMAGE_GOOS) GOARCH=$(IMAGE_GOARCH) $(GO) build -trimpath -ldflags "-s -w" -o "$(SCRAPD_IMAGE_BINARY)" ./cmd/scrapd
	$(DOCKER) build \
		--platform="$(IMAGE_PLATFORM)" \
		--build-arg SCRAP_RELEASE_SHA="$(RELEASE_SHA)" \
		--build-arg SCRAP_VERSION="$(RELEASE_VERSION)" \
		--build-arg SCRAP_BUILD_TIME="$(BUILD_TIME)" \
		--build-arg SCRAP_DIRTY_TREE="$(DIRTY_TREE)" \
		--build-arg SCRAPD_IMAGE_BINARY="$(SCRAPD_IMAGE_BINARY)" \
		-t "$(IMAGE_NAME)" .

manifests-render: ## Render the local-kind GitOps manifests.
	@$(KUSTOMIZE) build "$(LOCAL_KIND_OVERLAY)"

manifests-check: ## Validate rendered GitOps manifests and deployment hardening invariants.
	@KUSTOMIZE_CMD='$(KUSTOMIZE)' LOCAL_KIND_OVERLAY='$(LOCAL_KIND_OVERLAY)' sh ./scripts/check-kustomize-manifests.sh

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

capacity-sample: ## Emit advisory local capacity sample evidence.
	@AWS_ACCESS_KEY_ID="$${AWS_ACCESS_KEY_ID:-test}" \
	AWS_SECRET_ACCESS_KEY="$${AWS_SECRET_ACCESS_KEY:-test}" \
	BAO_TOKEN="$${BAO_TOKEN:-local-root}" \
	$(GO) run ./cmd/scrapctl \
		--admin-addr "$(SCRAP_ADMIN_ADDR)" \
		--workload-identity "$(SCRAP_WORKLOAD_IDENTITY)" \
		capacity sample \
		--profile-id "$(PROFILE_ID)" \
		--backend-url "$(CAPACITY_SAMPLE_BACKEND_URL)" \
		--backend-region "$(CAPACITY_SAMPLE_BACKEND_REGION)" \
		--openbao-addr "$(CAPACITY_SAMPLE_OPENBAO_ADDR)" \
		--openbao-transit-key-path "$(CAPACITY_SAMPLE_OPENBAO_KEY_PATH)" \
		--release-sha "$(RELEASE_SHA)" \
		--dirty-tree "$(DIRTY_TREE)" \
		> "$(CAPACITY_SAMPLE_REPORT)"

openbao-smoke-evidence: ## Emit local OpenBao Transit smoke evidence.
	@BAO_TOKEN="$${BAO_TOKEN:-local-root}" \
	BAO_KUBERNETES_JWT="$${BAO_KUBERNETES_JWT:-$$($(KUBECTL) -n scrap-local create token openbao-transit-smoke --duration=10m)}" \
	$(GO) run ./cmd/scrap-openbao-smoke \
		--out "$(OPENBAO_SMOKE_REPORT)" \
		--release-sha "$(RELEASE_SHA)" \
		--dirty-tree "$(DIRTY_TREE)" \
		--profile-id "$(PROFILE_ID)" \
		--environment-id "local-kind" \
		--namespace "scrap-local" \
		--deployment "openbao" \
		--openbao-addr "$(CAPACITY_SAMPLE_OPENBAO_ADDR)" \
		--outage-addr "$(OPENBAO_SMOKE_OUTAGE_ADDR)" \
		--transit-key-path "$(CAPACITY_SAMPLE_OPENBAO_KEY_PATH)"

local-soak-evidence: ## Emit local release soak and capacity rehearsal evidence.
	$(GO) run ./cmd/scrap-local-soak-evidence \
		--out "$(LOCAL_SOAK_REPORT)" \
		--release-sha "$(RELEASE_SHA)" \
		--dirty-tree "$(DIRTY_TREE)" \
		--profile-id "$(PROFILE_ID)" \
		--environment-id "local-kind" \
		--runner-id "$(LOCAL_SOAK_RUNNER)" \
		--image-identity "$(LOCAL_SOAK_IMAGE_IDENTITY)" \
		--public-addr "$(SCRAP_PUBLIC_ADDR)" \
		--admin-addr "$(SCRAP_ADMIN_ADDR)" \
		--public-workload-identity "$(SCRAP_PUBLIC_WORKLOAD_IDENTITY)" \
		--admin-workload-identity "$(SCRAP_WORKLOAD_IDENTITY)" \
		--capacity-sample-report "$(CAPACITY_SAMPLE_REPORT)"

local-dr-drill-evidence: capacity-sample openbao-smoke-evidence ## Emit local release-artifact DR drill evidence.
	$(GO) run ./cmd/scrap-local-dr-drill-evidence \
		--out "$(LOCAL_DR_DRILL_REPORT)" \
		--release-sha "$(RELEASE_SHA)" \
		--dirty-tree "$(DIRTY_TREE)" \
		--profile-id "$(PROFILE_ID)" \
		--environment-id "local-kind" \
		--runner-id "$(LOCAL_DR_DRILL_RUNNER)" \
		--image-identity "$(LOCAL_DR_DRILL_IMAGE_IDENTITY)" \
		--public-addr "$(SCRAP_PUBLIC_ADDR)" \
		--admin-addr "$(SCRAP_ADMIN_ADDR)" \
		--public-workload-identity "$(SCRAP_PUBLIC_WORKLOAD_IDENTITY)" \
		--admin-workload-identity "$(SCRAP_WORKLOAD_IDENTITY)" \
		--capacity-sample-report "$(CAPACITY_SAMPLE_REPORT)" \
		--openbao-smoke-report "$(OPENBAO_SMOKE_REPORT)" \
		--operator-owner "$(LOCAL_DR_DRILL_OPERATOR_OWNER)"

##@ Spikes

spike-write-path: ## Run the local write-path spike.
	$(GO) run ./cmd/scrap-spike

spike-write-path-raft: ## Run the write-path spike with Raft commit barriers.
	$(GO) run ./cmd/scrap-spike -raft-barrier

spike-write-path-raft-durable: ## Run the write-path spike with durable Raft barriers.
	$(GO) run ./cmd/scrap-spike -raft-durable-barrier

spike-write-path-raft-cluster: ## Run the write-path spike with clustered Raft barriers.
	$(GO) run ./cmd/scrap-spike -raft-cluster-barrier
