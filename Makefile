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
##? PROTO_BREAKING_REF Git ref used by buf breaking checks.

LINT_TIMEOUT ?= 5m
PROTO_BREAKING_REF ?= main
PROTO_BREAKING_AGAINST ?= .git#branch=$(PROTO_BREAKING_REF)

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

SCRAP_E2E_ADDR ?= 127.0.0.1:18090

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
	$(GO) build ./cmd/scrapd

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
e2e-setup: local-kind-load local-kind-deploy ## Build image, load into Kind, and deploy manifests.
	$(KUBECTL) -n scrap wait --for=condition=Ready pod -l app=scrap --timeout=120s

.PHONY: e2e
e2e: e2e-setup ## Run E2E tests against a Kind cluster.
	SCRAP_E2E=1 SCRAP_E2E_ADDR="$(SCRAP_E2E_ADDR)" $(GO) test ./test/e2e/ -v -timeout 300s
