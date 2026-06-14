# S.C.R.A.P. project Makefile.
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

##? ACT act CLI used by local GitHub Actions validation targets.
ACT ?= act

##? DOCKER Docker CLI used by local image targets.
##? KIND kind command used by local cluster targets.
##? KIND_VERSION kind version used by the default KIND command.
##? HELM_VERSION Helm version used by the default HELM command.
##? KUBECTL kubectl CLI used by local cluster targets.

DOCKER ?= docker
HELM_VERSION ?= v3.21.0
HELM ?= $(GO) run helm.sh/helm/v3/cmd/helm@$(HELM_VERSION)
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
	gates-check \
	kind-cilium-check \
	manifests-check \
	fmt-check \
	package-boundaries \
	proto-check \
	lint
TEST_TARGETS := \
	test \
	test-race \
	integration

##@ GitHub Actions Variables

##? ACT_ARGS Extra arguments appended to act commands.
##? ACT_CI_WORKFLOW GitHub Actions workflow file used by act CI targets.
##? ACT_EVENT GitHub event used by act CI targets.
##? ACT_JOB Single job run by act-ci-job.
##? ACT_CONCURRENT_JOBS Maximum concurrent act jobs for local CI runs.
##? ACT_CLEANUP Whether act-ci-run removes local act CI resources on exit.
##? ACT_PULL Whether act pulls mapped runner images before execution.
##? ACT_RUNNER_BASE_IMAGE Base image used for the local act runner image.
##? ACT_RUNNER_DOCKERFILE Dockerfile used for the local act runner image.
##? ACT_RUNNER_IMAGE Local act runner image tag used by .actrc mappings.

ACT_ARGS ?= --pull=$(ACT_PULL) --rm --concurrent-jobs=$(ACT_CONCURRENT_JOBS) --env=ACT=true
ACT_CI_WORKFLOW ?= .github/workflows/ci.yml
ACT_CLEANUP ?= false
ACT_CONCURRENT_JOBS ?= 1
ACT_EVENT ?= workflow_dispatch
ACT_JOB ?= build
ACT_PULL ?= false
ACT_RUNNER_BASE_IMAGE ?= catthehacker/ubuntu:act-24.04
ACT_RUNNER_DOCKERFILE ?= tools/act-runner/Dockerfile
ACT_RUNNER_IMAGE ?= scrap/act-ubuntu:24.04-varrun

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
##? LOCAL_KIND_OVERLAY Kustomize environment used for local kind manifests.

KIND_CLUSTER ?= scrap-local
LOCAL_KIND_OVERLAY ?= deploy/kustomize/environments/local

##@ Prod-like Kind Variables

##? PRODLIKE_KIND_CLUSTER Kind cluster name used by prod-like Cell targets.
##? PRODLIKE_KIND_CONFIG Kind config for the Cilium-backed prod-like Cell.
##? PRODLIKE_OVERLAY Kustomize environment used for the prod-like Cell.
##? PRODLIKE_E2E_OVERLAY Kustomize environment used for prod-like E2E test hooks.
##? PRODLIKE_KUBE_CONTEXT kubectl context used by prod-like Kind targets.
##? PRODUCTION_REHEARSAL_SCRIPT Script used by production-mode readiness rehearsal targets.
##? PRODUCTION_REHEARSAL_DIR Directory for generated production rehearsal assets.
##? PRODUCTION_REHEARSAL_OPENBAO_IMAGE OpenBao image used by production rehearsal.
##? CILIUM_VERSION Cilium chart version used by prod-like Kind targets.
##? CILIUM_CHART_DIR Vendored Cilium chart used by prod-like Kind targets.
##? CILIUM_VALUES Helm values used for prod-like Cilium.
##? CILIUM_SCRIPT Helper script used by prod-like Cilium targets.

PRODLIKE_KIND_CLUSTER ?= scrap-prodlike
PRODLIKE_KIND_CONFIG ?= deploy/kind/cluster-prodlike-cilium.yaml
PRODLIKE_OVERLAY ?= deploy/kustomize/environments/prodlike
PRODLIKE_E2E_OVERLAY ?= deploy/kustomize/environments/prodlike-e2e
PRODLIKE_KUBE_CONTEXT ?= kind-$(PRODLIKE_KIND_CLUSTER)
PRODUCTION_REHEARSAL_SCRIPT ?= scripts/production-rehearsal.sh
PRODUCTION_REHEARSAL_DIR ?= artifacts/production-rehearsal
PRODUCTION_REHEARSAL_OPENBAO_IMAGE ?= openbao/openbao:2.5.4
CILIUM_VERSION ?= 1.19.4
CILIUM_CHART_DIR ?= deploy/cilium/charts/cilium
CILIUM_VALUES ?= deploy/cilium/prodlike-values.yaml
CILIUM_SCRIPT ?= scripts/prodlike-cilium.sh
PRODLIKE_KUBECTL = $(KUBECTL) --context "$(PRODLIKE_KUBE_CONTEXT)"

##@ Local Dev Variables

##? LOCAL_DEV_SCRIPT Script used by local-dev targets.

LOCAL_DEV_SCRIPT ?= scripts/local-dev-env.sh

##@ E2E Variables

##? SCRAP_E2E_ADDR gRPC address used by E2E tests.
##? SCRAP_E2E_CELL_ID Cell ID prefix used by Backend upload E2E assertions.
##? SCRAP_E2E_METRICS_URL HTTP metrics URL used by E2E tests.
##? SCRAP_E2E_NAMESPACE Kubernetes namespace used by E2E tests.
##? SCRAP_E2E_KUBE_CONTEXT kubectl context used by safety-gated E2E fault commands.
##? SCRAP_E2E_S3_BUCKET LocalStack S3 bucket used by upload E2E tests.
##? PRODLIKE_E2E_KUBE_CONTEXT kubectl context used by the prod-like Tier 2 E2E gate.
##? PRODLIKE_SECURITY_ASSET_DIR Directory for generated prod-like E2E test TLS assets.
##? TIER2_SECURITY_EVIDENCE_REPORT Prod-like security report written by the Tier 2 E2E gate.
##? E2E_TEST_RUN Go test -run pattern used by the default E2E target.
##? SCRUB_E2E_TEST_RUN Go test -run pattern used by the scrub E2E target.
##? TIER2_E2E_TEST_RUN Go test -run pattern used by the prod-like Tier 2 E2E gate.

SCRAP_E2E_ADDR ?= 127.0.0.1:18090
SCRAP_E2E_CELL_ID ?= kind-dev
SCRAP_E2E_METRICS_URL ?= http://127.0.0.1:18100/metrics
SCRAP_E2E_NAMESPACE ?= scrap
SCRAP_E2E_KUBE_CONTEXT ?= kind-$(KIND_CLUSTER)
SCRAP_E2E_S3_BUCKET ?= scrap-e2e
ifndef PRODLIKE_E2E_KUBE_CONTEXT
PRODLIKE_E2E_KUBE_CONTEXT := $(PRODLIKE_KUBE_CONTEXT)
endif
PRODLIKE_E2E_KUBECTL = $(KUBECTL) --context "$(PRODLIKE_E2E_KUBE_CONTEXT)"
PRODLIKE_SECURITY_ASSET_DIR ?= artifacts/prodlike-security
TIER2_SECURITY_EVIDENCE_REPORT ?= $(PRODLIKE_SECURITY_ASSET_DIR)/security-evidence.json
E2E_TEST_RUN ?= TestE2E(WriteReadHead|LeaderFailover|BackendUpload)
SCRUB_E2E_TEST_RUN ?= TestE2E(DeepScrub|LightScrub)
TIER2_E2E_TEST_RUN ?= TestE2E(WriteReadHead|LeaderFailover|BackendUploadHappyPath|BackendUploadLeaderChange|BackendUploadAdmissionPressure|MultiShardRestartDeterminism|MultiShardBackendUploadUsesNonZeroShard|LightScrub|ProdlikeSecurityEncryptionEvidence)

##@ Stress Variables

##? STRESS_KIND_CLUSTER Kind cluster name used by stress targets.
##? STRESS_KIND_CONFIG Kind cluster config with Grafana NodePort.
##? STRESS_OVERLAY Kustomize environment used for stress/evidence workload manifests.
##? EVIDENCE_OVERLAY Kustomize component for OTel evidence stack manifests.
##? STRESS_ADDR gRPC address for the stress test binary.
##? STRESS_SCENARIO Stress scenario to run: throughput, mixed, pressure.
##? STRESS_WORKERS Concurrent worker goroutines for the stress test.
##? STRESS_DURATION Duration of the stress test run.
##? STRESS_DOC_SIZE Document payload size in bytes.
##? BUNDLE_DIR Directory where evidence bundles are written.
##? SECURITY_EVIDENCE_REPORT Prod-like security report copied into Tier 3 evidence bundles.
##? EVIDENCE_BASELINE_SAMPLING Baseline % of normal traces the gateway keeps; errors + slow are always kept.
##? EVIDENCE_LOWRATE_SAMPLING Baseline % used by the stress-setup-lowrate capture scenario.

STRESS_KIND_CLUSTER ?= scrap-stress
STRESS_KIND_CONFIG ?= deploy/kind/cluster-stress.yaml
STRESS_OVERLAY ?= deploy/kustomize/environments/evidence
EVIDENCE_OVERLAY ?= deploy/kustomize/components/evidence-stack
STRESS_ADDR ?= 127.0.0.1:18090
STRESS_SCENARIO ?= throughput
STRESS_WORKERS ?= 8
STRESS_DURATION ?= 60s
STRESS_DOC_SIZE ?= 16384
BUNDLE_DIR ?= evidence
SECURITY_EVIDENCE_REPORT ?= $(TIER2_SECURITY_EVIDENCE_REPORT)
EVIDENCE_BASELINE_SAMPLING ?= 100
EVIDENCE_LOWRATE_SAMPLING ?= 10

##@ Help

.PHONY: help
help: ## Show public targets.
	@awk -f scripts/make-help.awk $(MAKEFILE_LIST)

.PHONY: help-vars
help-vars: ## Show overridable Make variables.
	@MAKE_HELP_MODE=variables awk -f scripts/make-help.awk $(MAKEFILE_LIST)

.PHONY: help-all
help-all: ## Show public targets and overridable Make variables.
	@MAKE_HELP_MODE=all awk -f scripts/make-help.awk $(MAKEFILE_LIST)

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
fmt-check:
	$(GOLANGCI_LINT) fmt --diff

.PHONY: lint
lint: ## Run the golangci-lint baseline.
	$(GOLANGCI_LINT) run --timeout=$(LINT_TIMEOUT)

.PHONY: package-boundaries
package-boundaries:
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
	$(GO) test -tags=integration ./test/integration/... -v -timeout 5m

.PHONY: build
build: ## Build all supported command binaries.
	$(GO) build -ldflags "$(SCRAPD_LDFLAGS)" ./cmd/scrapd
	$(GO) build ./cmd/scrapctl

.PHONY: static
static: $(STATIC_TARGETS) ## Run all static analysis and format checks.

.PHONY: tests
tests: $(TEST_TARGETS) ## Run all test suites including race detector.

.PHONY: check
check: $(CHECK_TARGETS) ## Run the full local verification gate.

##@ GitHub Actions

.PHONY: act-runner-image
act-runner-image: ## Build the local act runner image used by .actrc.
	$(DOCKER) build \
		--build-arg ACT_RUNNER_BASE_IMAGE="$(ACT_RUNNER_BASE_IMAGE)" \
		-f "$(ACT_RUNNER_DOCKERFILE)" \
		-t "$(ACT_RUNNER_IMAGE)" \
		.

.PHONY: act-ci-list
act-ci-list: ## List jobs in the CI workflow through act.
	$(ACT) --list -W "$(ACT_CI_WORKFLOW)"

.PHONY: act-ci-validate
act-ci-validate: ## Validate the CI workflow through act.
	$(ACT) --validate -W "$(ACT_CI_WORKFLOW)"

.PHONY: act-ci-dry-run
act-ci-dry-run: act-runner-image ## Dry-run the CI workflow_dispatch path through act.
	@$(MAKE) --no-print-directory act-ci-run ACT_RUN_ARGS="-n"

.PHONY: act-ci
act-ci: ACT_CLEANUP=true
act-ci: act-runner-image ## Run the CI workflow_dispatch path through act, including Tier 2 E2E.
	@$(MAKE) --no-print-directory act-ci-run

.PHONY: act-ci-job
act-ci-job: act-runner-image ## Run one CI job through act, e.g. make act-ci-job ACT_JOB=build.
	@test -n "$(ACT_JOB)" || { printf 'ACT_JOB is required\n' >&2; exit 1; }
	@$(MAKE) --no-print-directory act-ci-run ACT_RUN_ARGS="-j $(ACT_JOB)"

.PHONY: act-ci-e2e
act-ci-e2e: ACT_JOB=e2e
act-ci-e2e: ACT_CLEANUP=true
act-ci-e2e: act-ci-job ## Run only the Tier 2 E2E CI job through act.

.PHONY: act-ci-clean
act-ci-clean: ## Remove local act CI containers and the prod-like Kind Cell.
	@containers="$$( $(DOCKER) ps -aq --filter 'name=act-ci-' )"; \
	if [ -n "$$containers" ]; then \
		$(DOCKER) rm -f $$containers; \
	else \
		printf 'no act CI containers found\n'; \
	fi
	@$(MAKE) --no-print-directory prodlike-kind-delete || true

.PHONY: act-ci-run
act-ci-run:
	@tmp="$$(mktemp)"; \
	trap 'status="$$?"; rm -f "$$tmp"; if [ "$(ACT_CLEANUP)" = "true" ]; then $(MAKE) --no-print-directory act-ci-clean || true; fi; exit "$$status"' EXIT; \
	if command -v gh >/dev/null 2>&1 && token="$$(gh auth token 2>/dev/null)" && [ -n "$$token" ]; then \
		printf 'GITHUB_TOKEN=%s\nGH_TOKEN=%s\n' "$$token" "$$token" > "$$tmp"; \
		secret_args="--secret-file $$tmp"; \
	else \
		printf 'warning: gh auth token unavailable; running act without GITHUB_TOKEN/GH_TOKEN\n' >&2; \
		secret_args=""; \
	fi; \
	$(ACT) "$(ACT_EVENT)" -W "$(ACT_CI_WORKFLOW)" $(ACT_ARGS) $(ACT_RUN_ARGS) $$secret_args

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

.PHONY: gates-check
gates-check:
	@bash ./scripts/check-e2e-gates.sh

.PHONY: e2e-gates-check
e2e-gates-check: gates-check

.PHONY: kind-cilium-check
kind-cilium-check:
	@PRODLIKE_KIND_CONFIG='$(PRODLIKE_KIND_CONFIG)' \
		STRESS_KIND_CONFIG='$(STRESS_KIND_CONFIG)' \
		PRODLIKE_OVERLAY='$(PRODLIKE_OVERLAY)' \
		PRODLIKE_E2E_OVERLAY='$(PRODLIKE_E2E_OVERLAY)' \
		CILIUM_VALUES='$(CILIUM_VALUES)' \
		CILIUM_CHART_DIR='$(CILIUM_CHART_DIR)' \
		CILIUM_SCRIPT='$(CILIUM_SCRIPT)' \
		sh ./scripts/check-kind-cilium.sh

# Internal Local Kind helpers. Public local E2E workflows live under `##@ E2E`.

.PHONY: local-kind-create
local-kind-create:
	$(KIND) create cluster --name "$(KIND_CLUSTER)" --config deploy/kind/cluster.yaml

.PHONY: local-kind-ensure
local-kind-ensure:
	@if $(KIND) get clusters 2>/dev/null | grep -Fx "$(KIND_CLUSTER)" >/dev/null 2>&1; then \
		printf 'kind cluster already exists: %s\n' "$(KIND_CLUSTER)"; \
	else \
		$(KIND) create cluster --name "$(KIND_CLUSTER)" --config deploy/kind/cluster.yaml; \
	fi
	$(KIND) export kubeconfig --name "$(KIND_CLUSTER)" >/dev/null

.PHONY: local-kind-delete
local-kind-delete:
	$(KIND) delete cluster --name "$(KIND_CLUSTER)"

.PHONY: local-kind-load
local-kind-load: image
	$(KIND) load docker-image "$(IMAGE_NAME)" --name "$(KIND_CLUSTER)"

.PHONY: local-kind-deploy
local-kind-deploy: manifests-check
	$(KUSTOMIZE) build "$(LOCAL_KIND_OVERLAY)" | $(KUBECTL) apply -f -

##@ Prod-like

.PHONY: prodlike-kind-create
prodlike-kind-create:
	$(KIND) create cluster --name "$(PRODLIKE_KIND_CLUSTER)" --config "$(PRODLIKE_KIND_CONFIG)"

.PHONY: prodlike-kind-ensure
prodlike-kind-ensure:
	@if $(KIND) get clusters 2>/dev/null | grep -Fx "$(PRODLIKE_KIND_CLUSTER)" >/dev/null 2>&1; then \
		printf 'kind cluster already exists: %s\n' "$(PRODLIKE_KIND_CLUSTER)"; \
	else \
		$(KIND) create cluster --name "$(PRODLIKE_KIND_CLUSTER)" --config "$(PRODLIKE_KIND_CONFIG)"; \
	fi
	$(KIND) export kubeconfig --name "$(PRODLIKE_KIND_CLUSTER)" >/dev/null
	$(MAKE) prodlike-cilium-install
	$(MAKE) prodlike-cilium-wait

.PHONY: prodlike-kind-delete
prodlike-kind-delete:
	$(KIND) delete cluster --name "$(PRODLIKE_KIND_CLUSTER)"

.PHONY: prodlike-cilium-install
prodlike-cilium-install:
	PRODLIKE_KIND_CLUSTER="$(PRODLIKE_KIND_CLUSTER)" \
		CILIUM_VERSION="$(CILIUM_VERSION)" \
		CILIUM_VALUES="$(CILIUM_VALUES)" \
		DOCKER="$(DOCKER)" \
		HELM="$(HELM)" \
		KUBECTL="$(KUBECTL)" \
		PRODLIKE_KUBE_CONTEXT="$(PRODLIKE_KUBE_CONTEXT)" \
		CILIUM_CHART_DIR="$(CILIUM_CHART_DIR)" \
		"$(CILIUM_SCRIPT)" install

.PHONY: prodlike-cilium-wait
prodlike-cilium-wait:
	PRODLIKE_KIND_CLUSTER="$(PRODLIKE_KIND_CLUSTER)" \
		KUBECTL="$(KUBECTL)" \
		PRODLIKE_KUBE_CONTEXT="$(PRODLIKE_KUBE_CONTEXT)" \
		"$(CILIUM_SCRIPT)" wait

.PHONY: prodlike-kind-load
prodlike-kind-load: image
	$(KIND) load docker-image "$(IMAGE_NAME)" --name "$(PRODLIKE_KIND_CLUSTER)"

.PHONY: prodlike-kind-deploy
prodlike-kind-deploy: LOCAL_KIND_OVERLAY=$(PRODLIKE_OVERLAY)
prodlike-kind-deploy: manifests-check
	$(KUSTOMIZE) build "$(PRODLIKE_OVERLAY)" | $(PRODLIKE_KUBECTL) apply -f -
	$(PRODLIKE_KUBECTL) -n scrap rollout status deployment/localstack --timeout=180s
	$(PRODLIKE_KUBECTL) -n scrap wait --for=condition=Ready pod -l app=localstack --timeout=120s
	$(PRODLIKE_KUBECTL) -n scrap exec deploy/localstack -- sh -c 'awslocal s3api head-bucket --bucket "$(SCRAP_E2E_S3_BUCKET)" >/dev/null 2>&1 || awslocal s3api create-bucket --bucket "$(SCRAP_E2E_S3_BUCKET)" >/dev/null'
	$(PRODLIKE_KUBECTL) -n scrap rollout status statefulset/scrapd --timeout=180s
	$(PRODLIKE_KUBECTL) -n scrap wait --for=condition=Ready pod -l app=scrap --timeout=120s

.PHONY: prodlike-kind-deploy-e2e
prodlike-kind-deploy-e2e: prodlike-test-security-assets manifests-check
	$(KUSTOMIZE) build "$(PRODLIKE_E2E_OVERLAY)" | $(PRODLIKE_E2E_KUBECTL) apply -f -
	$(PRODLIKE_E2E_KUBECTL) -n scrap rollout status deployment/localstack --timeout=180s
	$(PRODLIKE_E2E_KUBECTL) -n scrap wait --for=condition=Ready pod -l app=localstack --timeout=120s
	$(PRODLIKE_E2E_KUBECTL) -n scrap exec deploy/localstack -- sh -c 'awslocal s3api head-bucket --bucket "$(SCRAP_E2E_S3_BUCKET)" >/dev/null 2>&1 || awslocal s3api create-bucket --bucket "$(SCRAP_E2E_S3_BUCKET)" >/dev/null'
	$(PRODLIKE_E2E_KUBECTL) -n scrap rollout restart statefulset/scrapd
	$(PRODLIKE_E2E_KUBECTL) -n scrap rollout status statefulset/scrapd --timeout=180s
	$(PRODLIKE_E2E_KUBECTL) -n scrap wait --for=condition=Ready pod -l app=scrap --timeout=120s

.PHONY: prodlike-test-security-assets
prodlike-test-security-assets:
	PRODLIKE_E2E_KUBE_CONTEXT="$(PRODLIKE_E2E_KUBE_CONTEXT)" \
		SCRAP_E2E_NAMESPACE="$(SCRAP_E2E_NAMESPACE)" \
		KUBECTL="$(KUBECTL)" \
		scripts/prodlike-test-security-assets.sh "$(PRODLIKE_SECURITY_ASSET_DIR)"

.PHONY: prodlike-test-security-rollout
prodlike-test-security-rollout: prodlike-doctor prodlike-test-security-assets
	$(PRODLIKE_E2E_KUBECTL) -n scrap rollout restart statefulset/scrapd
	$(PRODLIKE_E2E_KUBECTL) -n scrap rollout status statefulset/scrapd --timeout=180s
	$(PRODLIKE_E2E_KUBECTL) -n scrap wait --for=condition=Ready pod -l app=scrap --timeout=120s

.PHONY: prodlike-cell-doctor
prodlike-cell-doctor:
	PRODLIKE_KIND_CLUSTER="$(PRODLIKE_KIND_CLUSTER)" \
		SCRAP_PRODLIKE_NAMESPACE="$(SCRAP_E2E_NAMESPACE)" \
		DOCKER="$(DOCKER)" \
		KUBECTL="$(KUBECTL)" \
		PRODLIKE_KUBE_CONTEXT="$(PRODLIKE_KUBE_CONTEXT)" \
		"$(CILIUM_SCRIPT)" doctor

.PHONY: prodlike-cell-up
prodlike-cell-up: prodlike-kind-ensure prodlike-kind-load prodlike-kind-deploy prodlike-cell-doctor

.PHONY: prodlike-e2e-cell-up
prodlike-e2e-cell-up: PRODLIKE_KUBE_CONTEXT=$(PRODLIKE_E2E_KUBE_CONTEXT)
prodlike-e2e-cell-up: prodlike-kind-ensure prodlike-kind-load prodlike-kind-deploy-e2e prodlike-cell-doctor

.PHONY: prodlike-up
prodlike-up: prodlike-cell-up ## Bring up and verify the prod-like Kind Cell.

.PHONY: prodlike-down
prodlike-down: prodlike-kind-delete ## Delete the prod-like Kind Cell.

.PHONY: prodlike-doctor
prodlike-doctor: prodlike-cell-doctor ## Check the prod-like Cell without creating or deleting infrastructure.

.PHONY: cell-doctor
cell-doctor: prodlike-cell-doctor

.PHONY: tier2-e2e-hooks-check
tier2-e2e-hooks-check:
	@hooks="$$( $(PRODLIKE_E2E_KUBECTL) -n "$(SCRAP_E2E_NAMESPACE)" get statefulset scrapd -o jsonpath='{.spec.template.spec.containers[?(@.name=="scrapd")].env[?(@.name=="SCRAP_TEST_HOOKS")].value}' )"; \
	if [ "$$hooks" != "1" ]; then \
		printf 'Tier 2 E2E requires SCRAP_TEST_HOOKS=1; run make tier2-e2e-up or deploy %s\n' "$(PRODLIKE_E2E_OVERLAY)" >&2; \
		exit 1; \
	fi

.PHONY: prodlike-e2e-smoke
prodlike-e2e-smoke: tier2-e2e

.PHONY: production-rehearsal-security
production-rehearsal-security: build ## Run local production-mode security rehearsal with real OpenBao Transit and FS Backend.
	SCRAP_PROD_REHEARSAL_BACKEND=fs \
		SCRAP_PROD_REHEARSAL_DIR="$(PRODUCTION_REHEARSAL_DIR)" \
		SCRAP_PROD_REHEARSAL_OPENBAO_IMAGE="$(PRODUCTION_REHEARSAL_OPENBAO_IMAGE)" \
		SCRAPD_BIN="$(abspath scrapd)" \
		SCRAPCTL_BIN="$(abspath scrapctl)" \
		"$(PRODUCTION_REHEARSAL_SCRIPT)" run

.PHONY: production-rehearsal
production-rehearsal: build ## Run production-mode rehearsal with real OpenBao Transit and real S3/IAM-style Backend config.
	SCRAP_PROD_REHEARSAL_BACKEND=s3 \
		SCRAP_PROD_REHEARSAL_DIR="$(PRODUCTION_REHEARSAL_DIR)" \
		SCRAP_PROD_REHEARSAL_OPENBAO_IMAGE="$(PRODUCTION_REHEARSAL_OPENBAO_IMAGE)" \
		SCRAPD_BIN="$(abspath scrapd)" \
		SCRAPCTL_BIN="$(abspath scrapctl)" \
		"$(PRODUCTION_REHEARSAL_SCRIPT)" run

.PHONY: production-rehearsal-down
production-rehearsal-down: ## Stop production rehearsal processes.
	SCRAP_PROD_REHEARSAL_DIR="$(PRODUCTION_REHEARSAL_DIR)" \
		"$(PRODUCTION_REHEARSAL_SCRIPT)" down

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
e2e: ## Run E2E tests against an existing Cell.
	SCRAP_E2E=1 \
		SCRAP_E2E_ADDR="$(SCRAP_E2E_ADDR)" \
		SCRAP_E2E_CELL_ID="$(SCRAP_E2E_CELL_ID)" \
		SCRAP_E2E_METRICS_URL="$(SCRAP_E2E_METRICS_URL)" \
		SCRAP_E2E_NAMESPACE="$(SCRAP_E2E_NAMESPACE)" \
		SCRAP_E2E_KUBE_CONTEXT="$(SCRAP_E2E_KUBE_CONTEXT)" \
		SCRAP_E2E_S3_BUCKET="$(SCRAP_E2E_S3_BUCKET)" \
		SCRAP_E2E_KUBECTL="$(KUBECTL)" \
		KIND_CLUSTER="$(KIND_CLUSTER)" \
		$(GO) test ./test/e2e/ -run '$(E2E_TEST_RUN)' -count=1 -v -timeout 300s

.PHONY: e2e-up
e2e-up: e2e-setup e2e ## Create/update local Kind, then run E2E tests.

.PHONY: e2e-down
e2e-down: local-kind-delete ## Delete the local Kind E2E Cell.

.PHONY: e2e-scrub
e2e-scrub: ## Run scrub E2E tests against an existing scrub-enabled Cell.
	SCRAP_E2E=1 \
		SCRAP_E2E_ADDR="$(SCRAP_E2E_ADDR)" \
		SCRAP_E2E_CELL_ID="$(SCRAP_E2E_CELL_ID)" \
		SCRAP_E2E_METRICS_URL="$(SCRAP_E2E_METRICS_URL)" \
		SCRAP_E2E_NAMESPACE="$(SCRAP_E2E_NAMESPACE)" \
		SCRAP_E2E_KUBE_CONTEXT="$(SCRAP_E2E_KUBE_CONTEXT)" \
		SCRAP_E2E_S3_BUCKET="$(SCRAP_E2E_S3_BUCKET)" \
		SCRAP_E2E_KUBECTL="$(KUBECTL)" \
		KIND_CLUSTER="$(KIND_CLUSTER)" \
		$(GO) test ./test/e2e/ -run '$(SCRUB_E2E_TEST_RUN)' -count=1 -v -timeout 600s

.PHONY: e2e-scrub-up
e2e-scrub-up: LOCAL_KIND_OVERLAY=deploy/kustomize/environments/local-scrub
e2e-scrub-up: e2e-setup e2e-scrub ## Create/update scrub-enabled local Kind, then run scrub E2E tests.

##@ Gates

.PHONY: tier1-check
tier1-check: check vuln ## Run the Tier 1 commit gate.

.PHONY: tier2-e2e
tier2-e2e: PRODLIKE_KUBE_CONTEXT=$(PRODLIKE_E2E_KUBE_CONTEXT)
tier2-e2e: prodlike-test-security-assets prodlike-test-security-rollout tier2-e2e-hooks-check ## Run the Tier 2 prod-like E2E gate against an existing E2E Cell.
	@printf 'TIER2_E2E_STATUS=running\n'
	SCRAP_E2E=1 \
		SCRAP_E2E_ADDR="$(SCRAP_E2E_ADDR)" \
		SCRAP_E2E_CELL_ID="kind-prodlike" \
		SCRAP_E2E_METRICS_URL="$(SCRAP_E2E_METRICS_URL)" \
		SCRAP_E2E_NAMESPACE="$(SCRAP_E2E_NAMESPACE)" \
		SCRAP_E2E_KUBE_CONTEXT="$(PRODLIKE_E2E_KUBE_CONTEXT)" \
		SCRAP_E2E_S3_BUCKET="$(SCRAP_E2E_S3_BUCKET)" \
		SCRAP_E2E_KUBECTL="$(KUBECTL)" \
		SCRAP_SECURITY_MODE="test" \
		SCRAP_TLS_SCRAPCTL_CERT="$(abspath $(PRODLIKE_SECURITY_ASSET_DIR)/scrap.pem)" \
		SCRAP_TLS_SCRAPCTL_KEY="$(abspath $(PRODLIKE_SECURITY_ASSET_DIR)/scrap.key)" \
		SCRAP_TLS_SCRAPCTL_CLIENT_CA="$(abspath $(PRODLIKE_SECURITY_ASSET_DIR)/ca.pem)" \
		SCRAP_TLS_SCRAPCTL_SERVER_NAME="scrap.local" \
		SCRAP_E2E_TLS_CERT="$(abspath $(PRODLIKE_SECURITY_ASSET_DIR)/scrap.pem)" \
		SCRAP_E2E_TLS_KEY="$(abspath $(PRODLIKE_SECURITY_ASSET_DIR)/scrap.key)" \
		SCRAP_E2E_TLS_CA="$(abspath $(PRODLIKE_SECURITY_ASSET_DIR)/ca.pem)" \
		SCRAP_E2E_TLS_SERVER_NAME="scrap.local" \
		SCRAP_E2E_SECURITY_REPORT="$(abspath $(TIER2_SECURITY_EVIDENCE_REPORT))" \
		KIND_CLUSTER="$(PRODLIKE_KIND_CLUSTER)" \
		$(GO) test ./test/e2e/ -run '$(TIER2_E2E_TEST_RUN)' -count=1 -v -timeout 600s
	@printf 'TIER2_E2E_STATUS=passed\n'

.PHONY: tier2-e2e-up
tier2-e2e-up: prodlike-e2e-cell-up tier2-e2e ## Bring up prod-like Kind with E2E hooks, then run the Tier 2 gate.

.PHONY: tier3-evidence
tier3-evidence: evidence-bundle ## Run the Tier 3 evidence gate against an existing evidence Cell.

.PHONY: tier3-evidence-up
tier3-evidence-up: evidence-up tier3-evidence ## Bring up the evidence Cell, then run the Tier 3 evidence gate.

##@ Evidence / Stress

.PHONY: evidence-up
evidence-up: stress-setup ## Bring up the evidence Cell.

.PHONY: evidence-up-lowrate
evidence-up-lowrate: stress-setup-lowrate ## Bring up the evidence Cell with production-like low trace sampling.

.PHONY: evidence-down
evidence-down: stress-teardown ## Delete the evidence Kind Cell.

.PHONY: stress-setup
stress-setup:
	@if $(KIND) get clusters 2>/dev/null | grep -Fx "$(STRESS_KIND_CLUSTER)" >/dev/null 2>&1; then \
		printf 'kind cluster already exists: %s\n' "$(STRESS_KIND_CLUSTER)"; \
	else \
		$(KIND) create cluster --name "$(STRESS_KIND_CLUSTER)" --config "$(STRESS_KIND_CONFIG)"; \
	fi
	$(KIND) export kubeconfig --name "$(STRESS_KIND_CLUSTER)" >/dev/null
	PRODLIKE_KIND_CLUSTER="$(STRESS_KIND_CLUSTER)" \
		KUBECTL="$(KUBECTL)" \
		"$(CILIUM_SCRIPT)" baseline
	PRODLIKE_KIND_CLUSTER="$(STRESS_KIND_CLUSTER)" \
		CILIUM_VERSION="$(CILIUM_VERSION)" \
		CILIUM_VALUES="$(CILIUM_VALUES)" \
		DOCKER="$(DOCKER)" \
		HELM="$(HELM)" \
		KUBECTL="$(KUBECTL)" \
		CILIUM_CHART_DIR="$(CILIUM_CHART_DIR)" \
		"$(CILIUM_SCRIPT)" install
	PRODLIKE_KIND_CLUSTER="$(STRESS_KIND_CLUSTER)" \
		KUBECTL="$(KUBECTL)" \
		"$(CILIUM_SCRIPT)" wait
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
	# A reused cluster keeps the previously-running otel-collector pod, whose
	# tail_sampling rate is read from config only at process start. kubectl apply
	# updates the ConfigMap in place but does not roll the Deployment, so without
	# this the collector would keep its old in-memory sampling (e.g. 100%) while
	# the rendered config claims the low rate — a misleading low-rate scenario.
	# Force the collector to reload the freshly-applied config before gating on it
	# (no-op-cost first rollout on a fresh cluster; the status gate below waits).
	$(KUBECTL) -n monitoring rollout restart deployment/otel-collector
	# Gate on the log pipeline first (Loki sink, gateway, per-node agent) so that
	# when apps deploy in Phase 2 their logs are captured from the first line.
	$(KUBECTL) -n monitoring rollout status deployment/loki --timeout=120s
	$(KUBECTL) -n monitoring rollout status deployment/otel-collector --timeout=120s
	$(KUBECTL) -n monitoring rollout status daemonset/otel-agent --timeout=120s
	$(KUBECTL) -n monitoring rollout status deployment/kube-state-metrics --timeout=120s
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
stress-setup-lowrate:
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
	BUNDLE_DIR="$(BUNDLE_DIR)" \
	STRESS_ADDR="$(STRESS_ADDR)" \
	STRESS_WORKERS="$(STRESS_WORKERS)" \
	STRESS_DURATION="$(STRESS_DURATION)" \
	STRESS_DOC_SIZE="$(STRESS_DOC_SIZE)" \
	SECURITY_EVIDENCE_REPORT="$(SECURITY_EVIDENCE_REPORT)" \
	scripts/evidence-bundle.sh "$(STRESS_SCENARIO)"

.PHONY: stress-teardown
stress-teardown:
	$(KIND) delete cluster --name "$(STRESS_KIND_CLUSTER)"
