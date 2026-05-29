# S.C.R.A.P. V2 project Makefile.
# Run `make help` to list common targets and selected overridable variables.
# Override variables with environment values or `make VAR=value <target>`.
.DEFAULT_GOAL := help

##@ Tool Variables

##? GO Go command used by all Go targets.
##? TOOLS_MODFILE Go tool module file used by Go-managed tools.

GO ?= go
TOOLS_MODFILE ?= tools.go.mod
GO_TOOL = $(GO) tool -modfile=$(TOOLS_MODFILE)

BUF ?= $(GO_TOOL) buf
GOLANGCI_LINT ?= $(GO_TOOL) golangci-lint
GOTESTSUM ?= $(GO_TOOL) gotestsum
GOVULNCHECK ?= $(GO_TOOL) govulncheck
KUSTOMIZE ?= $(GO_TOOL) kustomize

##? DOCKER Docker CLI used by local image targets.
##? KIND kind command used by local cluster targets.
##? KIND_VERSION kind version used by the default KIND command.
##? KUBECTL kubectl CLI used by local cluster targets.

DOCKER ?= docker
KIND_VERSION ?= v0.31.0
KIND ?= $(GO) run sigs.k8s.io/kind@$(KIND_VERSION)
KUBECTL ?= kubectl

##@ Verification Variables

##? TEST_PACKAGES Go packages used by test and race targets.
##? COVER_EXCLUDE_PATTERN Extended regex for packages excluded from coverage instrumentation.

TEST_PACKAGES ?= ./...
COVER_TEST_PACKAGES ?= $(shell $(GO) list $(TEST_PACKAGES) | grep -v '/gen/')
COVER_EXCLUDE_PATTERN ?= (/internal/spike/)
COVER_PACKAGES ?= $(shell printf '%s\n' $(COVER_TEST_PACKAGES) | grep -Ev '$(COVER_EXCLUDE_PATTERN)')

##? LINT_TIMEOUT Timeout passed to golangci-lint run.
LINT_TIMEOUT ?= 5m

##? COVERMODE Coverage mode passed to go test.
##? COVERPROFILE Coverage profile output path.
##? JUNIT_REPORT JUnit XML report output path.
##? TEST_RESULTS_DIR Directory used for JUnit and coverage artifacts.

COVERMODE ?= atomic
COVERPROFILE ?= coverage.out
TEST_RESULTS_DIR ?= test-results
JUNIT_REPORT ?= $(TEST_RESULTS_DIR)/junit.xml
comma := ,
empty :=
space := $(empty) $(empty)
COVERPKG ?= $(subst $(space),$(comma),$(strip $(COVER_PACKAGES)))

CHECK_TARGETS := \
	static \
	tests \
	build
STATIC_TARGETS := \
	manifests-check \
	fmt-check \
	package-boundaries \
	proto-check \
	lint
TEST_TARGETS := \
	test \
	test-race \
	integration

##@ Release Metadata Variables

##? BUILD_TIME Release build timestamp embedded in local artifacts.
##? DIRTY_TREE Clean or dirty release metadata flag.
##? RELEASE_SHA Release commit SHA embedded in local artifacts.
##? RELEASE_VERSION Release version embedded in local artifacts.

BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
DIRTY_TREE ?= $(shell git diff --quiet && git diff --cached --quiet && echo clean || echo dirty)
RELEASE_SHA ?= $(shell git rev-parse HEAD)
RELEASE_VERSION ?= dev
SCRAPD_LDFLAGS = -X main.version=$(RELEASE_VERSION) -X main.buildSHA=$(RELEASE_SHA) -X main.buildTime=$(BUILD_TIME)

##@ Container Variables

##? IMAGE_GOARCH GOARCH used by local container builds.
##? IMAGE_GOOS GOOS used by local container builds.
##? IMAGE_NAME Local container image tag for image and local-kind targets.
##? IMAGE_PLATFORM Container platform passed to docker build.
##? SCRAPD_IMAGE_BINARY Cross-compiled scrapd binary path used by image.

IMAGE_GOARCH ?= $(shell $(GO) env GOARCH)
IMAGE_GOOS ?= linux
IMAGE_NAME ?= localhost/scrapd:local
IMAGE_PLATFORM ?= $(IMAGE_GOOS)/$(IMAGE_GOARCH)
SCRAPD_IMAGE_BINARY ?= bin/scrapd-$(IMAGE_GOOS)-$(IMAGE_GOARCH)

CROSS_BUILD_ENV = CGO_ENABLED=0 GOOS=$(IMAGE_GOOS) GOARCH=$(IMAGE_GOARCH)

##@ Local Kind Variables

##? KIND_CLUSTER Local kind cluster name used by local-kind targets.
##? LOCAL_KIND_OVERLAY Kustomize overlay used for local kind manifests.

KIND_CLUSTER ?= scrap-local
LOCAL_KIND_OVERLAY ?= deploy/kustomize/overlays/local-kind

##@ Local Dev Variables

##? LOCAL_DEV_SCRIPT Script used by local-dev targets.

LOCAL_DEV_SCRIPT ?= scripts/local-dev-env.sh

##@ E2E Variables

##? SCRAP_E2E_ADDR gRPC address used by E2E tests.
##? SCRAP_E2E_METRICS_URL HTTP metrics URL used by E2E tests.
##? SCRAP_E2E_NAMESPACE Kubernetes namespace used by E2E tests.
##? SCRAP_E2E_S3_BUCKET LocalStack S3 bucket used by upload E2E tests.
##? E2E_TEST_RUN Go test -run pattern used by the default E2E target.
##? SCRUB_E2E_TEST_RUN Go test -run pattern used by the scrub E2E target.

SCRAP_E2E_ADDR ?= 127.0.0.1:18090
SCRAP_E2E_METRICS_URL ?= http://127.0.0.1:18100/metrics
SCRAP_E2E_NAMESPACE ?= scrap
SCRAP_E2E_S3_BUCKET ?= scrap-e2e
E2E_TEST_RUN ?= TestE2E(WriteAndRead|LeaderFailover|BackendUpload)
SCRUB_E2E_TEST_RUN ?= TestE2E(DeepScrub|LightScrub)

##@ Stress Variables

##? STRESS_KIND_CLUSTER Kind cluster name used by stress targets.
##? STRESS_KIND_CONFIG Kind cluster config with Grafana NodePort.
##? STRESS_OVERLAY Kustomize overlay used for stress manifests.
##? EVIDENCE_OVERLAY Kustomize overlay for OTel evidence stack (stress runs).
##? STRESS_ADDR gRPC address for the stress test binary.
##? STRESS_SCENARIO Stress scenario to run: throughput, mixed, pressure.
##? STRESS_WORKERS Concurrent worker goroutines for the stress test.
##? STRESS_DURATION Duration of the stress test run.
##? STRESS_DOC_SIZE Document payload size in bytes.
##? EVIDENCE_BASELINE_SAMPLING Baseline % of normal traces the gateway keeps; errors + slow are always kept.
##? EVIDENCE_LOWRATE_SAMPLING Baseline % used by the stress-setup-lowrate capture scenario.

STRESS_KIND_CLUSTER ?= scrap-stress
STRESS_KIND_CONFIG ?= deploy/kind/cluster-stress.yaml
STRESS_OVERLAY ?= deploy/kustomize/overlays/local-stress
EVIDENCE_OVERLAY ?= deploy/kustomize/overlays/evidence
STRESS_ADDR ?= 127.0.0.1:18090
STRESS_SCENARIO ?= throughput
STRESS_WORKERS ?= 8
STRESS_DURATION ?= 60s
STRESS_DOC_SIZE ?= 16384
EVIDENCE_BASELINE_SAMPLING ?= 100
EVIDENCE_LOWRATE_SAMPLING ?= 10

##@ Help

.PHONY: help
help: ## Show this help.
	@awk -f scripts/make-help.awk $(MAKEFILE_LIST)

##@ Protobuf

.PHONY: proto
proto: ## Generate protobuf and gRPC code.
	$(BUF) generate

.PHONY: proto-check
proto-check: ## Lint schemas and verify generated code.
	$(BUF) lint
	$(BUF) generate
	git diff --exit-code -- gen

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

.PHONY: package-boundaries
package-boundaries: ## Check package dependency boundaries.
	GO="$(GO)" scripts/check-package-boundaries.sh

.PHONY: vuln
vuln: ## Run govulncheck against the module graph.
	$(GOVULNCHECK) ./...

.PHONY: test
test: ## Run package tests.
	$(GO) test $(TEST_PACKAGES)

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
		-coverpkg=$(COVERPKG) \
		-coverprofile=$(COVERPROFILE) \
		$(COVER_TEST_PACKAGES)
	$(GO) tool cover -func="$(COVERPROFILE)" | tail -n 1

.PHONY: integration
integration: ## Run integration tests.
	$(GO) test ./test/integration/ -v -timeout 120s

.PHONY: build
build: ## Build all supported command binaries.
	$(GO) build -ldflags "$(SCRAPD_LDFLAGS)" ./cmd/scrapd

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
		-ldflags "-s -w $(SCRAPD_LDFLAGS)" \
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
manifests-render: ## Render the local-kind kustomize manifests.
	@$(KUSTOMIZE) build "$(LOCAL_KIND_OVERLAY)"

.PHONY: manifests-check
manifests-check: ## Validate rendered manifests and deployment hardening invariants.
	@KUSTOMIZE_CMD='$(KUSTOMIZE)' \
		LOCAL_KIND_OVERLAY='$(LOCAL_KIND_OVERLAY)' \
		sh ./scripts/check-kustomize-manifests.sh

##@ Local Kind

.PHONY: local-kind-create
local-kind-create: ## Create the local kind cluster.
	$(KIND) create cluster --name "$(KIND_CLUSTER)" --config deploy/kind/cluster.yaml

.PHONY: local-kind-ensure
local-kind-ensure: ## Create the local kind cluster if it does not exist.
	@if $(KIND) get clusters 2>/dev/null | grep -Fx "$(KIND_CLUSTER)" >/dev/null 2>&1; then \
		printf 'kind cluster already exists: %s\n' "$(KIND_CLUSTER)"; \
	else \
		$(KIND) create cluster --name "$(KIND_CLUSTER)" --config deploy/kind/cluster.yaml; \
	fi
	$(KIND) export kubeconfig --name "$(KIND_CLUSTER)" >/dev/null

.PHONY: local-kind-delete
local-kind-delete: ## Delete the local kind cluster.
	$(KIND) delete cluster --name "$(KIND_CLUSTER)"

.PHONY: local-kind-load
local-kind-load: image ## Load the scrapd image into the local kind cluster.
	$(KIND) load docker-image "$(IMAGE_NAME)" --name "$(KIND_CLUSTER)"

.PHONY: local-kind-deploy
local-kind-deploy: manifests-check ## Apply the local-kind manifests.
	$(KUSTOMIZE) build "$(LOCAL_KIND_OVERLAY)" | $(KUBECTL) apply -f -

##@ Local Dev

.PHONY: local-dev-up
local-dev-up: ## Start the local dev environment.
	@$(LOCAL_DEV_SCRIPT) up

.PHONY: local-dev-down
local-dev-down: ## Stop the local dev environment and delete its kind cluster.
	@$(LOCAL_DEV_SCRIPT) down

.PHONY: local-dev-status
local-dev-status: ## Show local dev Kubernetes resources and port-forwards.
	@$(LOCAL_DEV_SCRIPT) status

.PHONY: local-dev-stop-forwards
local-dev-stop-forwards: ## Stop only local dev port-forwards.
	@$(LOCAL_DEV_SCRIPT) stop-forwards

##@ E2E

.PHONY: e2e-setup
e2e-setup: local-kind-ensure local-kind-load local-kind-deploy ## Create Kind cluster, load image, and deploy manifests.
	$(KUBECTL) -n scrap rollout status deployment/localstack --timeout=180s
	$(KUBECTL) -n scrap wait --for=condition=Ready pod -l app=localstack --timeout=120s
	$(KUBECTL) -n scrap exec deploy/localstack -- sh -c 'awslocal s3api head-bucket --bucket "$(SCRAP_E2E_S3_BUCKET)" >/dev/null 2>&1 || awslocal s3api create-bucket --bucket "$(SCRAP_E2E_S3_BUCKET)" >/dev/null'
	$(KUBECTL) -n scrap rollout status statefulset/scrapd --timeout=180s
	$(KUBECTL) -n scrap wait --for=condition=Ready pod -l app=scrap --timeout=120s

.PHONY: e2e
e2e: e2e-setup ## Run E2E tests against a Kind cluster.
	SCRAP_E2E=1 \
		SCRAP_E2E_ADDR="$(SCRAP_E2E_ADDR)" \
		SCRAP_E2E_METRICS_URL="$(SCRAP_E2E_METRICS_URL)" \
		SCRAP_E2E_NAMESPACE="$(SCRAP_E2E_NAMESPACE)" \
		SCRAP_E2E_S3_BUCKET="$(SCRAP_E2E_S3_BUCKET)" \
		SCRAP_E2E_KUBECTL="$(KUBECTL)" \
		KIND_CLUSTER="$(KIND_CLUSTER)" \
		$(GO) test ./test/e2e/ -run '$(E2E_TEST_RUN)' -v -timeout 300s

.PHONY: e2e-scrub
e2e-scrub: LOCAL_KIND_OVERLAY=deploy/kustomize/overlays/local-kind-scrub
e2e-scrub: e2e-setup ## Run scrub E2E tests with the local Kind scrub overlay and cleanup.
	SCRAP_E2E=1 \
		SCRAP_E2E_CLEANUP=1 \
		SCRAP_E2E_ADDR="$(SCRAP_E2E_ADDR)" \
		SCRAP_E2E_METRICS_URL="$(SCRAP_E2E_METRICS_URL)" \
		SCRAP_E2E_NAMESPACE="$(SCRAP_E2E_NAMESPACE)" \
		SCRAP_E2E_S3_BUCKET="$(SCRAP_E2E_S3_BUCKET)" \
		SCRAP_E2E_KUBECTL="$(KUBECTL)" \
		KIND_CLUSTER="$(KIND_CLUSTER)" \
		$(GO) test ./test/e2e/ -run '$(SCRUB_E2E_TEST_RUN)' -v -timeout 600s

##@ Stress

.PHONY: stress-setup
stress-setup: ## Create Kind cluster with stress overlay, monitoring stack, and LocalStack.
	@if $(KIND) get clusters 2>/dev/null | grep -Fx "$(STRESS_KIND_CLUSTER)" >/dev/null 2>&1; then \
		printf 'kind cluster already exists: %s\n' "$(STRESS_KIND_CLUSTER)"; \
	else \
		$(KIND) create cluster --name "$(STRESS_KIND_CLUSTER)" --config "$(STRESS_KIND_CONFIG)"; \
	fi
	$(KIND) export kubeconfig --name "$(STRESS_KIND_CLUSTER)" >/dev/null
	$(MAKE) image
	$(KIND) load docker-image "$(IMAGE_NAME)" --name "$(STRESS_KIND_CLUSTER)"
	@printf '\n== Phase 1: observability stack (log pipeline up before any app starts) ==\n'
	# Ensure the scrap namespace exists so the evidence overlay's scrap-namespace
	# NetworkPolicy applies; the app workload (Phase 2) re-applies it idempotently.
	$(KUBECTL) create namespace scrap --dry-run=client -o yaml | $(KUBECTL) apply -f -
	# Render the evidence stack, overriding only the baseline trace-sampling rate.
	# Default 100 keeps the sed a no-op; stress-setup-lowrate lowers it to model a
	# production capture where errors + slow traces are still kept (ADR 0013).
	$(KUSTOMIZE) build "$(EVIDENCE_OVERLAY)" \
		| sed 's/sampling_percentage: 100/sampling_percentage: $(EVIDENCE_BASELINE_SAMPLING)/' \
		| $(KUBECTL) apply -f -
	# Gate on the log pipeline first (Loki sink, gateway, per-node agent) so that
	# when apps deploy in Phase 2 their logs are captured from the first line.
	$(KUBECTL) -n monitoring rollout status deployment/loki --timeout=120s
	$(KUBECTL) -n monitoring rollout status deployment/otel-collector --timeout=120s
	$(KUBECTL) -n monitoring rollout status daemonset/otel-agent --timeout=120s
	$(KUBECTL) -n monitoring rollout status deployment/mimir --timeout=120s
	$(KUBECTL) -n monitoring rollout status deployment/tempo --timeout=120s
	$(KUBECTL) -n monitoring rollout status deployment/pyroscope --timeout=120s
	$(KUBECTL) -n monitoring rollout status deployment/alloy --timeout=120s
	$(KUBECTL) -n monitoring rollout status deployment/grafana --timeout=120s
	@printf '\n== Phase 2: application workload (logs captured from startup) ==\n'
	$(KUSTOMIZE) build "$(STRESS_OVERLAY)" | $(KUBECTL) apply -f -
	$(KUBECTL) -n scrap rollout status deployment/localstack --timeout=180s
	$(KUBECTL) -n scrap wait --for=condition=Ready pod -l app=localstack --timeout=120s
	$(KUBECTL) -n scrap exec deploy/localstack -- sh -c 'awslocal s3api head-bucket --bucket "$(SCRAP_E2E_S3_BUCKET)" >/dev/null 2>&1 || awslocal s3api create-bucket --bucket "$(SCRAP_E2E_S3_BUCKET)" >/dev/null'
	$(KUBECTL) -n scrap rollout status statefulset/scrapd --timeout=180s
	$(KUBECTL) -n scrap wait --for=condition=Ready pod -l app=scrap --timeout=120s
	@printf '\nStress environment ready.\n'
	@printf '  gRPC:       127.0.0.1:18090\n'
	@printf '  Grafana:    http://127.0.0.1:13000\n'
	@printf '  Collector:  127.0.0.1:14317 (OTLP gRPC)\n'
	@printf '  LocalStack: http://127.0.0.1:18566 (S3, e.g. aws --endpoint-url http://127.0.0.1:18566 s3 ls)\n\n'

.PHONY: stress-setup-lowrate
stress-setup-lowrate: ## Bring up the stress env with a production-like low baseline trace-sampling rate.
	$(MAKE) stress-setup EVIDENCE_BASELINE_SAMPLING=$(EVIDENCE_LOWRATE_SAMPLING)

.PHONY: stress
stress: ## Run the stress test binary against the Kind cluster.
	$(GO) run ./test/stress/ \
		-addr="$(STRESS_ADDR)" \
		-scenario="$(STRESS_SCENARIO)" \
		-workers=$(STRESS_WORKERS) \
		-duration=$(STRESS_DURATION) \
		-doc-size=$(STRESS_DOC_SIZE)

.PHONY: evidence-bundle
evidence-bundle: ## Generate a timestamped evidence bundle from a stress run.
	STRESS_ADDR="$(STRESS_ADDR)" \
	STRESS_WORKERS="$(STRESS_WORKERS)" \
	STRESS_DURATION="$(STRESS_DURATION)" \
	STRESS_DOC_SIZE="$(STRESS_DOC_SIZE)" \
	scripts/evidence-bundle.sh "$(STRESS_SCENARIO)"

.PHONY: stress-teardown
stress-teardown: ## Delete the stress Kind cluster.
	$(KIND) delete cluster --name "$(STRESS_KIND_CLUSTER)"
