#!/usr/bin/env bash
set -euo pipefail

MAKEFILE=${MAKEFILE:-Makefile}
WORKFLOW=${WORKFLOW:-.github/workflows/ci.yml}
SCRUB_E2E=${SCRUB_E2E:-test/e2e/scrub_e2e_test.go}
PRODLIKE_OVERLAY=${PRODLIKE_OVERLAY:-deploy/kustomize/environments/prodlike}
PRODLIKE_E2E_OVERLAY=${PRODLIKE_E2E_OVERLAY:-deploy/kustomize/environments/prodlike-e2e}

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
require_target prodlike-up
require_target prodlike-kind-deploy-e2e
require_target prodlike-e2e-cell-up
require_target cell-doctor
require_target tier2-e2e-hooks-check
require_target tier2-e2e
require_target tier2-e2e-up

target_must_not_depend_on e2e e2e-setup
target_must_not_depend_on e2e-scrub e2e-setup
target_must_not_depend_on tier2-e2e prodlike-up
target_must_not_depend_on tier2-e2e-up prodlike-up
target_must_depend_on e2e-up e2e-setup
target_must_depend_on e2e-up e2e
target_must_depend_on e2e-scrub-up e2e-setup
target_must_depend_on e2e-scrub-up e2e-scrub
target_must_depend_on tier2-e2e-up prodlike-e2e-cell-up
target_must_depend_on tier2-e2e-up tier2-e2e
target_must_depend_on prodlike-e2e-cell-up prodlike-kind-deploy-e2e
target_must_depend_on tier2-e2e cell-doctor
target_must_depend_on tier2-e2e tier2-e2e-hooks-check

reject_pattern 'SCRAP_E2E_CLEANUP' "$MAKEFILE" "cluster cleanup during E2E execution"
reject_pattern 'SCRAP_E2E_CLEANUP' "$SCRUB_E2E" "cluster cleanup during E2E execution"
reject_pattern 'SCRAP_TEST_HOOKS' "$PRODLIKE_OVERLAY/statefulset-prodlike-patch.yaml" "test hooks in prod-like overlay"

require_pattern '^PRODLIKE_E2E_OVERLAY[[:space:]]*\?=' "$MAKEFILE" "prod-like E2E overlay variable"
require_pattern '^TIER2_E2E_TEST_RUN[[:space:]]*\?=.*WriteReadHead' "$MAKEFILE" "Tier 2 write/read/head coverage"
require_pattern '^TIER2_E2E_TEST_RUN[[:space:]]*\?=.*LeaderFailover' "$MAKEFILE" "Tier 2 leader failover coverage"
require_pattern '^TIER2_E2E_TEST_RUN[[:space:]]*\?=.*BackendUploadHappyPath' "$MAKEFILE" "Tier 2 Backend happy-path coverage"
require_pattern '^TIER2_E2E_TEST_RUN[[:space:]]*\?=.*BackendUploadLeaderChange' "$MAKEFILE" "Tier 2 Backend outage/recovery coverage"
require_pattern '^TIER2_E2E_TEST_RUN[[:space:]]*\?=.*BackendUploadAdmissionPressure' "$MAKEFILE" "Tier 2 upload pressure coverage"
require_pattern '^TIER2_E2E_TEST_RUN[[:space:]]*\?=.*LightScrub' "$MAKEFILE" "Tier 2 fast scrub coverage"
require_pattern 'SCRAP_E2E_CELL_ID="kind-prodlike"' "$MAKEFILE" "prod-like Cell ID in Tier 2"
require_pattern 'test ./test/e2e/.*-count=1' "$MAKEFILE" "uncached E2E execution"
require_pattern 'TIER2_E2E_STATUS=passed' "$MAKEFILE" "explicit Tier 2 pass output"
require_pattern 'SCRAP_TEST_HOOKS' "$PRODLIKE_E2E_OVERLAY/statefulset-test-hooks-patch.yaml" "test hooks in prod-like E2E overlay"
require_pattern 'SCRAP_TEST_HOOKS' "$MAKEFILE" "Tier 2 E2E hook overlay check"
require_pattern '^[[:space:]]+types:' "$WORKFLOW" "explicit pull_request activity types"
require_pattern '^[[:space:]]+- labeled[[:space:]]*$' "$WORKFLOW" "E2E label CI trigger"
require_pattern '^[[:space:]]+- unlabeled[[:space:]]*$' "$WORKFLOW" "E2E label removal CI trigger"
require_pattern 'Tier 2 E2E skipped' "$WORKFLOW" "explicit skipped E2E CI output"
require_pattern 'Tier 2 E2E requested' "$WORKFLOW" "explicit requested E2E CI output"
require_pattern '[[:space:]]+- e2e[[:space:]]*$' "$WORKFLOW" "E2E result dependency in aggregate check"
require_pattern 'E2E_RESULT:.*needs\.e2e\.result' "$WORKFLOW" "E2E result environment in aggregate check"
require_pattern 'test "\$E2E_RESULT" = success' "$WORKFLOW" "requested E2E success assertion"
require_pattern 'test "\$E2E_RESULT" = skipped' "$WORKFLOW" "skipped E2E assertion"
