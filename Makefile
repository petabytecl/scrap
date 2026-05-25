# S.C.R.A.P. project Makefile.
# Run `make help` to list common targets and selected overridable variables.
# Override variables with environment values or `make VAR=value <target>`.
.DEFAULT_GOAL := help

# Toolchain.
BUF ?= $(GO) tool -modfile=$(TOOLS_MODFILE) buf
DOCKER ?= docker
GO ?= go
GOLANGCI_LINT ?= $(GO) tool -modfile=$(TOOLS_MODFILE) golangci-lint
GOTESTSUM ?= $(GO) tool -modfile=$(TOOLS_MODFILE) gotestsum
GOVULNCHECK ?= $(GO) tool -modfile=$(TOOLS_MODFILE) govulncheck
KIND ?= kind
KUBECTL ?= kubectl
KUSTOMIZE ?= $(GO) tool -modfile=$(TOOLS_MODFILE) kustomize
TOOLS_MODFILE ?= tools.go.mod

##@ Variables
##? TEST_PACKAGES Go packages used by test and race targets.
##? IMAGE_NAME Local container image tag for image and local-kind targets.
##? KIND_CLUSTER Local kind cluster name used by local-kind targets.
##? RELEASE_EVIDENCE_MANIFEST Manifest path consumed by release-check.

# Package sets.
COMPAT_PACKAGES ?= ./internal/compat ./internal/metastore
COVER_PACKAGES ?= $(shell $(GO) list $(TEST_PACKAGES) | grep -v '/internal/gen/')
SCRAP_BINS := \
	./cmd/scrapd \
	./cmd/scrap-spike \
	./cmd/scrapctl \
	./cmd/scrap-release-gate \
	./cmd/scrap-crash-fault-evidence \
	./cmd/scrap-openbao-smoke \
	./cmd/scrap-local-soak-evidence \
	./cmd/scrap-local-dr-drill-evidence \
	./cmd/scrap-write-pipeline-evidence
TEST_PACKAGES ?= ./...

# Aggregate target dependencies.
CHECK_TARGETS := \
	static \
	tests \
	build
STATIC_TARGETS := \
	manifests-check \
	fmt-check \
	proto-check \
	lint
TEST_TARGETS := \
	test-compat \
	test-crashfault-catalog \
	test \
	test-race

# Static analysis, protobuf, and test reporting.
COVERMODE ?= atomic
COVERPROFILE ?= coverage.out
JUNIT_REPORT ?= $(TEST_RESULTS_DIR)/junit.xml
LINT_TIMEOUT ?= 5m
PROTO_BREAKING_AGAINST ?= .git#branch=$(PROTO_BREAKING_REF)
PROTO_BREAKING_REF ?= main
TEST_RESULTS_DIR ?= test-results

# Release and build metadata.
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
DIRTY_TREE ?= $(shell git diff --quiet && git diff --cached --quiet && echo clean || echo dirty)
RELEASE_SHA ?= $(shell git rev-parse HEAD)
RELEASE_VERSION ?= dev

# Container image build.
IMAGE_GOARCH ?= $(shell $(GO) env GOARCH)
IMAGE_GOOS ?= linux
IMAGE_NAME ?= localhost/scrapd:local
IMAGE_PLATFORM ?= $(IMAGE_GOOS)/$(IMAGE_GOARCH)
SCRAPD_IMAGE_BINARY ?= bin/scrapd-$(IMAGE_GOOS)-$(IMAGE_GOARCH)

# Local kind release rehearsal.
KIND_CLUSTER ?= scrap-local
LOCAL_KIND_OVERLAY ?= deploy/kustomize/overlays/local-kind
LOCAL_KIND_EVIDENCE_REPORT ?= local-kind-evidence.json

# Local workload identities and endpoints.
PROFILE_ID ?= scrap-prod-v1
SCRAP_ADMIN_ADDR ?= 127.0.0.1:18081
SCRAP_PUBLIC_ADDR ?= 127.0.0.1:18080
SCRAP_PUBLIC_WORKLOAD_IDENTITY ?= local-public-client
SCRAP_WORKLOAD_IDENTITY ?= local-operator

# Capacity and OpenBao evidence.
CAPACITY_SAMPLE_BACKEND_REGION ?= us-east-1
CAPACITY_SAMPLE_BACKEND_URL ?= http://127.0.0.1:4566/scrap-local
CAPACITY_SAMPLE_OPENBAO_ADDR ?= http://127.0.0.1:8200
CAPACITY_SAMPLE_OPENBAO_KEY_PATH ?= transit/keys/scrap-backend
CAPACITY_SAMPLE_REPORT ?= capacity-sample-advisory.json
OPENBAO_SMOKE_JWT_CMD ?= $(KUBECTL) -n scrap-local create token openbao-transit-smoke --duration=10m
OPENBAO_SMOKE_OUTAGE_ADDR ?= http://127.0.0.1:1
OPENBAO_SMOKE_REPORT ?= openbao-transit-smoke-evidence.json

# Local soak and DR drill evidence.
LOCAL_DR_DRILL_IMAGE_IDENTITY ?= $(IMAGE_NAME)
LOCAL_DR_DRILL_OPERATOR_OWNER ?= @cotocisternas
LOCAL_DR_DRILL_REPORT ?= local-dr-drill-evidence.json
LOCAL_DR_DRILL_RUNNER ?= local-kind
LOCAL_SOAK_IMAGE_IDENTITY ?= $(IMAGE_NAME)
LOCAL_SOAK_REPORT ?= local-soak-evidence.json
LOCAL_SOAK_RUNNER ?= local-kind

# Write-pipeline performance smoke evidence.
WRITE_PIPELINE_CONCURRENCY ?= 8
WRITE_PIPELINE_DOCUMENT_SIZE ?= 4096
WRITE_PIPELINE_DURATION ?= 30s
WRITE_PIPELINE_MAX_P99_ACK_LATENCY ?= 0s
WRITE_PIPELINE_MIN_WRITES_PER_SECOND ?= 0
WRITE_PIPELINE_REPORT ?= write-pipeline-performance-evidence.json
WRITE_PIPELINE_RUNNER ?= local-application
WRITE_PIPELINE_SAMPLES ?= 128

# Shared recipe fragments.
ADMIN_CLIENT_FLAGS = \
	--admin-addr "$(SCRAP_ADMIN_ADDR)" \
	--workload-identity "$(SCRAP_WORKLOAD_IDENTITY)"
CROSS_BUILD_ENV = CGO_ENABLED=0 GOOS=$(IMAGE_GOOS) GOARCH=$(IMAGE_GOARCH)
LOCAL_ENDPOINT_FLAGS = \
	--public-addr "$(SCRAP_PUBLIC_ADDR)" \
	--admin-addr "$(SCRAP_ADMIN_ADDR)" \
	--public-workload-identity "$(SCRAP_PUBLIC_WORKLOAD_IDENTITY)" \
	--admin-workload-identity "$(SCRAP_WORKLOAD_IDENTITY)"
RELEASE_EVIDENCE_FLAGS = \
	--release-sha "$(RELEASE_SHA)" \
	--dirty-tree "$(DIRTY_TREE)"
PROFILE_EVIDENCE_FLAGS = \
	$(RELEASE_EVIDENCE_FLAGS) \
	--profile-id "$(PROFILE_ID)"

##@ Help

.PHONY: help
help: ## Show this help.
	@awk -f scripts/make-help.awk $(MAKEFILE_LIST)

##@ Protobuf

.PHONY: proto
proto: ## Generate protobuf and gRPC code.
	$(BUF) generate

.PHONY: proto-check
proto-check: ## Lint schemas, check breaking changes, and verify generated code.
	$(BUF) lint
	@if git cat-file -e "$(PROTO_BREAKING_REF):buf.yaml" 2>/dev/null && \
		git cat-file -e "$(PROTO_BREAKING_REF):proto" 2>/dev/null; then \
		$(BUF) breaking --against "$(PROTO_BREAKING_AGAINST)"; \
	else \
		echo "buf breaking skipped: $(PROTO_BREAKING_REF) has no proto module yet"; \
	fi
	$(BUF) generate
	git diff --exit-code -- internal/gen

##@ Development

.PHONY: fmt
fmt: ## Format Go source using configured golangci formatters.
	$(GOLANGCI_LINT) fmt

.PHONY: fmt-check
fmt-check: ## Check formatter drift using configured golangci formatters.
	$(GOLANGCI_LINT) fmt --diff

.PHONY: lint
lint: ## Run the golangci-lint baseline.
	$(GOLANGCI_LINT) run --timeout=$(LINT_TIMEOUT)

.PHONY: vuln
vuln: ## Run govulncheck against the module graph.
	$(GOVULNCHECK) ./...

.PHONY: test
test: ## Run package tests.
	$(GO) test $(TEST_PACKAGES)

.PHONY: test-compat
test-compat: ## Run compatibility tests for stored data and metadata boundaries.
	$(GO) test $(COMPAT_PACKAGES)

.PHONY: test-crashfault-catalog
test-crashfault-catalog: ## Verify crash/fault catalog patterns match real tests.
	GO="$(GO)" $(GO) test -tags=integration ./internal/crashfault

.PHONY: test-race
test-race: ## Run package tests with the Go race detector.
	$(GO) test -race $(TEST_PACKAGES)

.PHONY: test-cover
test-cover: ## Run tests producing both coverage profile and JUnit XML in one pass.
	mkdir -p "$(TEST_RESULTS_DIR)"
	$(GOTESTSUM) \
		--junitfile "$(JUNIT_REPORT)" \
		--format testname \
		-- \
		-covermode=$(COVERMODE) \
		-coverprofile=$(COVERPROFILE) \
		$(COVER_PACKAGES)
	$(GO) tool cover -func="$(COVERPROFILE)" | tail -n 1

.PHONY: build
build: ## Build all supported command binaries.
	$(GO) build $(SCRAP_BINS)

.PHONY: static
static: $(STATIC_TARGETS) ## Run all static analysis and format checks.

.PHONY: tests
tests: $(TEST_TARGETS) ## Run all test suites including race detector.

.PHONY: check
check: $(CHECK_TARGETS) ## Run the full local verification gate.

##@ Release Artifacts

.PHONY: image
image: ## Build the local scrapd container image.
	mkdir -p "$(dir $(SCRAPD_IMAGE_BINARY))"
	$(CROSS_BUILD_ENV) $(GO) build \
		-trimpath \
		-ldflags "-s -w" \
		-o "$(SCRAPD_IMAGE_BINARY)" \
		./cmd/scrapd
	$(DOCKER) build \
		--platform="$(IMAGE_PLATFORM)" \
		--build-arg SCRAP_RELEASE_SHA="$(RELEASE_SHA)" \
		--build-arg SCRAP_VERSION="$(RELEASE_VERSION)" \
		--build-arg SCRAP_BUILD_TIME="$(BUILD_TIME)" \
		--build-arg SCRAP_DIRTY_TREE="$(DIRTY_TREE)" \
		--build-arg SCRAPD_IMAGE_BINARY="$(SCRAPD_IMAGE_BINARY)" \
		-t "$(IMAGE_NAME)" .

.PHONY: manifests-render
manifests-render: ## Render the local-kind GitOps manifests.
	@$(KUSTOMIZE) build "$(LOCAL_KIND_OVERLAY)"

.PHONY: manifests-check
manifests-check: ## Validate rendered GitOps manifests and deployment hardening invariants.
	@KUSTOMIZE_CMD='$(KUSTOMIZE)' \
		LOCAL_KIND_OVERLAY='$(LOCAL_KIND_OVERLAY)' \
		sh ./scripts/check-kustomize-manifests.sh

.PHONY: local-kind-create
local-kind-create: ## Create the local kind cluster for release rehearsal.
	$(KIND) create cluster --name "$(KIND_CLUSTER)" --config deploy/kind/cluster.yaml

.PHONY: local-kind-delete
local-kind-delete: ## Delete the local kind cluster.
	$(KIND) delete cluster --name "$(KIND_CLUSTER)"

.PHONY: local-kind-load
local-kind-load: image ## Load the scrapd image into the local kind cluster.
	$(KIND) load docker-image "$(IMAGE_NAME)" --name "$(KIND_CLUSTER)"

.PHONY: local-kind-deploy
local-kind-deploy: manifests-check ## Apply the local-kind release rehearsal manifests.
	$(KUSTOMIZE) build "$(LOCAL_KIND_OVERLAY)" | $(KUBECTL) apply -f -

.PHONY: local-kind-smoke
local-kind-smoke: ## Run a local kind admin smoke check.
	@./scripts/local-kind-smoke.sh

.PHONY: local-kind-evidence
local-kind-evidence: manifests-check ## Emit a local kind release rehearsal evidence report.
	@./scripts/local-kind-evidence.sh > "$(LOCAL_KIND_EVIDENCE_REPORT)"

##@ Release Evidence

.PHONY: release-check
release-check: ## Verify release evidence from RELEASE_EVIDENCE_MANIFEST.
	@test -n "$(RELEASE_EVIDENCE_MANIFEST)" || \
		(echo "RELEASE_EVIDENCE_MANIFEST is required" >&2; exit 2)
	$(GO) run ./cmd/scrap-release-gate --tier release --manifest "$(RELEASE_EVIDENCE_MANIFEST)"

.PHONY: crash-fault-evidence
crash-fault-evidence: ## Emit crash/fault evidence JSON for dedicated runners.
	$(GO) run ./cmd/scrap-crash-fault-evidence \
		--out "$${SCRAP_CRASH_FAULT_EVIDENCE_REPORT:-crash-fault-evidence.json}"

.PHONY: capacity-sample
capacity-sample: ## Emit advisory local capacity sample evidence.
	@AWS_ACCESS_KEY_ID="$${AWS_ACCESS_KEY_ID:-test}" \
	AWS_SECRET_ACCESS_KEY="$${AWS_SECRET_ACCESS_KEY:-test}" \
	BAO_TOKEN="$${BAO_TOKEN:-local-root}" \
	$(GO) run ./cmd/scrapctl \
		$(ADMIN_CLIENT_FLAGS) \
		capacity sample \
		--profile-id "$(PROFILE_ID)" \
		--backend-url "$(CAPACITY_SAMPLE_BACKEND_URL)" \
		--backend-region "$(CAPACITY_SAMPLE_BACKEND_REGION)" \
		--openbao-addr "$(CAPACITY_SAMPLE_OPENBAO_ADDR)" \
		--openbao-transit-key-path "$(CAPACITY_SAMPLE_OPENBAO_KEY_PATH)" \
		$(RELEASE_EVIDENCE_FLAGS) \
		> "$(CAPACITY_SAMPLE_REPORT)"

.PHONY: openbao-smoke-evidence
openbao-smoke-evidence: ## Emit local OpenBao Transit smoke evidence.
	@BAO_TOKEN="$${BAO_TOKEN:-local-root}" \
	BAO_KUBERNETES_JWT="$${BAO_KUBERNETES_JWT:-$$($(OPENBAO_SMOKE_JWT_CMD))}" \
	$(GO) run ./cmd/scrap-openbao-smoke \
		--out "$(OPENBAO_SMOKE_REPORT)" \
		$(PROFILE_EVIDENCE_FLAGS) \
		--environment-id "local-kind" \
		--namespace "scrap-local" \
		--deployment "openbao" \
		--openbao-addr "$(CAPACITY_SAMPLE_OPENBAO_ADDR)" \
		--outage-addr "$(OPENBAO_SMOKE_OUTAGE_ADDR)" \
		--transit-key-path "$(CAPACITY_SAMPLE_OPENBAO_KEY_PATH)"

.PHONY: local-soak-evidence
local-soak-evidence: ## Emit local release soak and capacity rehearsal evidence.
	$(GO) run ./cmd/scrap-local-soak-evidence \
		--out "$(LOCAL_SOAK_REPORT)" \
		$(PROFILE_EVIDENCE_FLAGS) \
		--environment-id "local-kind" \
		--runner-id "$(LOCAL_SOAK_RUNNER)" \
		--image-identity "$(LOCAL_SOAK_IMAGE_IDENTITY)" \
		$(LOCAL_ENDPOINT_FLAGS) \
		--capacity-sample-report "$(CAPACITY_SAMPLE_REPORT)"

.PHONY: local-dr-drill-evidence
local-dr-drill-evidence: capacity-sample openbao-smoke-evidence ## Emit local release-artifact DR drill evidence.
	$(GO) run ./cmd/scrap-local-dr-drill-evidence \
		--out "$(LOCAL_DR_DRILL_REPORT)" \
		$(PROFILE_EVIDENCE_FLAGS) \
		--environment-id "local-kind" \
		--runner-id "$(LOCAL_DR_DRILL_RUNNER)" \
		--image-identity "$(LOCAL_DR_DRILL_IMAGE_IDENTITY)" \
		$(LOCAL_ENDPOINT_FLAGS) \
		--capacity-sample-report "$(CAPACITY_SAMPLE_REPORT)" \
		--openbao-smoke-report "$(OPENBAO_SMOKE_REPORT)" \
		--operator-owner "$(LOCAL_DR_DRILL_OPERATOR_OWNER)"

.PHONY: write-pipeline-evidence
write-pipeline-evidence: ## Emit local write-pipeline performance-smoke evidence.
	$(GO) run ./cmd/scrap-write-pipeline-evidence \
		--out "$(WRITE_PIPELINE_REPORT)" \
		$(RELEASE_EVIDENCE_FLAGS) \
		--runner-id "$(WRITE_PIPELINE_RUNNER)" \
		--samples "$(WRITE_PIPELINE_SAMPLES)" \
		--concurrency "$(WRITE_PIPELINE_CONCURRENCY)" \
		--document-size "$(WRITE_PIPELINE_DOCUMENT_SIZE)" \
		--duration "$(WRITE_PIPELINE_DURATION)" \
		--min-writes-per-second "$(WRITE_PIPELINE_MIN_WRITES_PER_SECOND)" \
		--max-p99-ack-latency "$(WRITE_PIPELINE_MAX_P99_ACK_LATENCY)"

##@ Spikes

.PHONY: spike-write-path
spike-write-path: ## Run the local write-path spike.
	$(GO) run ./cmd/scrap-spike

.PHONY: spike-write-path-raft
spike-write-path-raft: ## Run the write-path spike with Raft commit barriers.
	$(GO) run ./cmd/scrap-spike -raft-barrier

.PHONY: spike-write-path-raft-durable
spike-write-path-raft-durable: ## Run the write-path spike with durable Raft barriers.
	$(GO) run ./cmd/scrap-spike -raft-durable-barrier

.PHONY: spike-write-path-raft-cluster
spike-write-path-raft-cluster: ## Run the write-path spike with clustered Raft barriers.
	$(GO) run ./cmd/scrap-spike -raft-cluster-barrier
