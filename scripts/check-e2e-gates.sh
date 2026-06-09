#!/usr/bin/env bash
set -euo pipefail

MAKEFILE=${MAKEFILE:-Makefile}
WORKFLOW=${WORKFLOW:-.github/workflows/ci.yml}
PRODLIKE_WORKFLOW=${PRODLIKE_WORKFLOW:-.github/workflows/prodlike-e2e.yml}
EVIDENCE_WORKFLOW=${EVIDENCE_WORKFLOW:-.github/workflows/evidence-gate.yml}
SCRUB_E2E=${SCRUB_E2E:-test/e2e/scrub_e2e_test.go}
PRODLIKE_OVERLAY=${PRODLIKE_OVERLAY:-deploy/kustomize/environments/prodlike}
PRODLIKE_E2E_OVERLAY=${PRODLIKE_E2E_OVERLAY:-deploy/kustomize/environments/prodlike-e2e}
PRD_CLOSURE_POLICY=${PRD_CLOSURE_POLICY:-docs/prd-closure-policy.md}

fail() {
	echo "e2e gate check failed: $*" >&2
	exit 1
}

require_target() {
	local target=$1
	grep -Eq "^${target}:" "$MAKEFILE" || fail "missing make target ${target}"
}

target_header() {
	local target=$1
	grep -E "^${target}:" "$MAKEFILE" | grep -v '=' | head -n 1
}

target_must_not_depend_on() {
	local target=$1
	local dependency=$2
	local header

	header=$(target_header "$target")
	[ -n "$header" ] || fail "missing make target ${target}"
	if grep -Eq "^${target}:.*(^|[[:space:]])${dependency}([[:space:]]|$$)" <<<"$header"; then
		fail "${target} must not depend on ${dependency}; E2E execution must target an existing Cell"
	fi
}

target_must_depend_on() {
	local target=$1
	local dependency=$2
	local header

	header=$(target_header "$target")
	[ -n "$header" ] || fail "missing make target ${target}"
	if ! grep -Eq "^${target}:.*(^|[[:space:]])${dependency}([[:space:]]|$$)" <<<"$header"; then
		fail "${target} must depend on ${dependency}"
	fi
}

require_pattern() {
	local pattern=$1
	local file=$2
	local description=$3

	grep -Eq "$pattern" "$file" || fail "missing ${description} in ${file}"
}

reject_pattern() {
	local pattern=$1
	local file=$2
	local description=$3

	if grep -Eq "$pattern" "$file"; then
		fail "found ${description} in ${file}"
	fi
}

require_target e2e
require_target e2e-up
require_target e2e-scrub
require_target e2e-scrub-up
require_target gates-check
require_target tier1-check
require_target prodlike-up
require_target prodlike-doctor
require_target prodlike-kind-deploy-e2e
require_target prodlike-test-security-assets
require_target prodlike-e2e-cell-up
require_target cell-doctor
require_target tier2-e2e-hooks-check
require_target tier2-e2e
require_target tier2-e2e-up
require_target evidence-up
require_target tier3-evidence
require_target tier3-evidence-up

target_must_not_depend_on e2e e2e-setup
target_must_not_depend_on e2e-scrub e2e-setup
target_must_not_depend_on tier2-e2e prodlike-up
target_must_not_depend_on tier2-e2e-up prodlike-up
target_must_depend_on e2e-up e2e-setup
target_must_depend_on e2e-up e2e
target_must_depend_on e2e-scrub-up e2e-setup
target_must_depend_on e2e-scrub-up e2e-scrub
target_must_depend_on tier1-check check
target_must_depend_on tier1-check vuln
target_must_depend_on tier2-e2e-up prodlike-e2e-cell-up
target_must_depend_on tier2-e2e-up tier2-e2e
target_must_depend_on prodlike-e2e-cell-up prodlike-kind-deploy-e2e
target_must_depend_on prodlike-kind-deploy-e2e prodlike-test-security-assets
target_must_depend_on tier2-e2e prodlike-doctor
target_must_depend_on tier2-e2e tier2-e2e-hooks-check
target_must_depend_on evidence-up stress-setup
target_must_depend_on tier3-evidence-up evidence-up
target_must_depend_on tier3-evidence-up tier3-evidence

reject_pattern 'SCRAP_E2E_CLEANUP' "$MAKEFILE" "cluster cleanup during E2E execution"
reject_pattern 'SCRAP_E2E_CLEANUP' "$SCRUB_E2E" "cluster cleanup during E2E execution"
reject_pattern 'SCRAP_TEST_HOOKS' "$PRODLIKE_OVERLAY/statefulset-prodlike-patch.yaml" "test hooks in prod-like overlay"

require_pattern '^PRODLIKE_E2E_OVERLAY[[:space:]]*\?=' "$MAKEFILE" "prod-like E2E overlay variable"
require_pattern '^PRODLIKE_KUBE_CONTEXT[[:space:]]*\?=' "$MAKEFILE" "prod-like kube context variable"
require_pattern '^ifndef PRODLIKE_E2E_KUBE_CONTEXT' "$MAKEFILE" "Tier 2 kube context conditional default"
require_pattern '^PRODLIKE_E2E_KUBE_CONTEXT[[:space:]]*:=.*PRODLIKE_KUBE_CONTEXT' "$MAKEFILE" "Tier 2 kube context default"
require_pattern '^PRODLIKE_E2E_KUBECTL[[:space:]]*=.*PRODLIKE_E2E_KUBE_CONTEXT' "$MAKEFILE" "Tier 2 kubectl context wrapper"
require_pattern '^TIER2_E2E_TEST_RUN[[:space:]]*\?=.*WriteReadHead' "$MAKEFILE" "Tier 2 write/read/head coverage"
require_pattern '^TIER2_E2E_TEST_RUN[[:space:]]*\?=.*LeaderFailover' "$MAKEFILE" "Tier 2 leader failover coverage"
require_pattern '^TIER2_E2E_TEST_RUN[[:space:]]*\?=.*BackendUploadHappyPath' "$MAKEFILE" "Tier 2 Backend happy-path coverage"
require_pattern '^TIER2_E2E_TEST_RUN[[:space:]]*\?=.*BackendUploadLeaderChange' "$MAKEFILE" "Tier 2 Backend outage/recovery coverage"
require_pattern '^TIER2_E2E_TEST_RUN[[:space:]]*\?=.*BackendUploadAdmissionPressure' "$MAKEFILE" "Tier 2 upload pressure coverage"
require_pattern '^TIER2_E2E_TEST_RUN[[:space:]]*\?=.*LightScrub' "$MAKEFILE" "Tier 2 fast scrub coverage"
require_pattern '^TIER2_E2E_TEST_RUN[[:space:]]*\?=.*ProdlikeSecurityEncryptionEvidence' "$MAKEFILE" "Tier 2 prod-like security evidence coverage"
require_pattern '^SECURITY_EVIDENCE_REPORT[[:space:]]*\?=.*security-evidence\.json' "$MAKEFILE" "security evidence report default"
require_pattern 'SCRAP_E2E_CELL_ID="kind-prodlike"' "$MAKEFILE" "prod-like Cell ID in Tier 2"
require_pattern 'SCRAP_E2E_SECURITY_REPORT=.*SECURITY_EVIDENCE_REPORT|SCRAP_E2E_SECURITY_REPORT=.*PRODLIKE_SECURITY_ASSET_DIR' "$MAKEFILE" "Tier 2 security evidence report path"
require_pattern 'SECURITY_EVIDENCE_REPORT=.*SECURITY_EVIDENCE_REPORT' "$MAKEFILE" "Tier 3 security report export"
require_pattern '^prodlike-e2e-cell-up: PRODLIKE_KUBE_CONTEXT=\$\(PRODLIKE_E2E_KUBE_CONTEXT\)' "$MAKEFILE" "prod-like E2E cell-up kube context"
require_pattern '^tier2-e2e: PRODLIKE_KUBE_CONTEXT=\$\(PRODLIKE_E2E_KUBE_CONTEXT\)' "$MAKEFILE" "Tier 2 prerequisite kube context"
require_pattern 'test ./test/e2e/.*-count=1' "$MAKEFILE" "uncached E2E execution"
require_pattern 'TIER2_E2E_STATUS=passed' "$MAKEFILE" "explicit Tier 2 pass output"
require_pattern 'SCRAP_TEST_HOOKS' "$PRODLIKE_E2E_OVERLAY/statefulset-test-hooks-patch.yaml" "test hooks in prod-like E2E overlay"
require_pattern 'SCRAP_SECURITY_MODE' "$PRODLIKE_E2E_OVERLAY/statefulset-test-hooks-patch.yaml" "security mode in prod-like E2E overlay"
require_pattern 'SCRAP_TLS_PUBLIC_CERT' "$PRODLIKE_E2E_OVERLAY/statefulset-test-hooks-patch.yaml" "public TLS in prod-like E2E overlay"
require_pattern 'SCRAP_TLS_PEER_CERT' "$PRODLIKE_E2E_OVERLAY/statefulset-test-hooks-patch.yaml" "peer TLS in prod-like E2E overlay"
require_pattern 'SCRAP_TLS_ADMIN_CERT' "$PRODLIKE_E2E_OVERLAY/statefulset-test-hooks-patch.yaml" "admin TLS in prod-like E2E overlay"
require_pattern 'SCRAP_TRANSIT_FAKE' "$PRODLIKE_E2E_OVERLAY/statefulset-test-hooks-patch.yaml" "explicit test Transit in prod-like E2E overlay"
require_pattern 'SCRAP_TEST_HOOKS' "$MAKEFILE" "Tier 2 E2E hook overlay check"
require_pattern '^[[:space:]]+types:' "$WORKFLOW" "explicit pull_request activity types"
require_pattern '^[[:space:]]+- labeled[[:space:]]*$' "$WORKFLOW" "E2E label CI trigger"
require_pattern '^[[:space:]]+- unlabeled[[:space:]]*$' "$WORKFLOW" "E2E label removal CI trigger"
require_pattern 'make gates-check' "$WORKFLOW" "CI gate wiring check"
require_pattern 'make integration' "$WORKFLOW" "CI Tier 1 integration tests"
require_pattern 'make vuln' "$WORKFLOW" "CI Tier 1 vulnerability scan"
require_pattern 'Tier 1 commit gate passed' "$WORKFLOW" "explicit Tier 1 aggregate output"
require_pattern 'Tier 2 E2E skipped' "$WORKFLOW" "explicit skipped E2E CI output"
require_pattern 'Tier 2 E2E requested' "$WORKFLOW" "explicit requested E2E CI output"
require_pattern '[[:space:]]+- e2e[[:space:]]*$' "$WORKFLOW" "E2E result dependency in aggregate check"
require_pattern '[[:space:]]+- integration[[:space:]]*$' "$WORKFLOW" "integration result dependency in aggregate check"
require_pattern 'E2E_RESULT:.*needs\.e2e\.result' "$WORKFLOW" "E2E result environment in aggregate check"
require_pattern 'INTEGRATION_RESULT:.*needs\.integration\.result' "$WORKFLOW" "integration result environment in aggregate check"
require_pattern 'test "\$INTEGRATION_RESULT" = success' "$WORKFLOW" "integration success assertion"
require_pattern 'test "\$E2E_RESULT" = success' "$WORKFLOW" "requested E2E success assertion"
require_pattern 'test "\$E2E_RESULT" = skipped' "$WORKFLOW" "skipped E2E assertion"
require_pattern 'ci-tier2-e2e' "$WORKFLOW" "CI Tier 2 artifact upload"
require_pattern 'collect-kind-artifacts' "$WORKFLOW" "CI Tier 2 failure artifact collection"

require_pattern 'workflow_dispatch:' "$PRODLIKE_WORKFLOW" "manual Tier 2 workflow dispatch"
require_pattern 'schedule:' "$PRODLIKE_WORKFLOW" "scheduled Tier 2 workflow"
require_pattern 'make tier2-e2e-up' "$PRODLIKE_WORKFLOW" "Tier 2 prod-like E2E command"
require_pattern 'upload-artifact' "$PRODLIKE_WORKFLOW" "Tier 2 artifact upload"
require_pattern 'collect-kind-artifacts' "$PRODLIKE_WORKFLOW" "Tier 2 failure artifact collection"
require_pattern 'kind-scrap-prodlike' "$PRODLIKE_WORKFLOW" "Tier 2 prod-like Kind context"

require_pattern 'workflow_dispatch:' "$EVIDENCE_WORKFLOW" "manual Tier 3 workflow dispatch"
require_pattern 'make tier3-evidence-up' "$EVIDENCE_WORKFLOW" "Tier 3 evidence command"
require_pattern 'upload-artifact' "$EVIDENCE_WORKFLOW" "Tier 3 bundle artifact upload"
require_pattern 'tier3-bundle-path\.txt' "$EVIDENCE_WORKFLOW" "Tier 3 bundle path artifact"
require_pattern 'collect-kind-artifacts' "$EVIDENCE_WORKFLOW" "Tier 3 failure artifact collection"

require_pattern '#312' "$PRD_CLOSURE_POLICY" "PRD #312 closure guard"
require_pattern '#337' "$PRD_CLOSURE_POLICY" "PRD #337 closure guard"
require_pattern 'green Tier 2' "$PRD_CLOSURE_POLICY" "green Tier 2 closure requirement"
require_pattern 'GitHub Actions run link' "$PRD_CLOSURE_POLICY" "Tier 2 run link requirement"
