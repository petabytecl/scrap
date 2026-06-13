#!/usr/bin/env bash
set -euo pipefail

MAKEFILE=${MAKEFILE:-Makefile}
WORKFLOW=${WORKFLOW:-.github/workflows/ci.yml}
PRODLIKE_WORKFLOW=${PRODLIKE_WORKFLOW:-.github/workflows/prodlike-e2e.yml}
EVIDENCE_WORKFLOW=${EVIDENCE_WORKFLOW:-.github/workflows/evidence-gate.yml}
SCRUB_E2E=${SCRUB_E2E:-test/e2e/scrub_e2e_test.go}
PRODLIKE_OVERLAY=${PRODLIKE_OVERLAY:-deploy/kustomize/environments/prodlike}
PRODLIKE_E2E_OVERLAY=${PRODLIKE_E2E_OVERLAY:-deploy/kustomize/environments/prodlike-e2e}
PRODUCTION_REHEARSAL_SCRIPT=${PRODUCTION_REHEARSAL_SCRIPT:-scripts/production-rehearsal.sh}
PRD_CLOSURE_POLICY=${PRD_CLOSURE_POLICY:-docs/prd-closure-policy.md}
TIER_GATES_CHECK=${TIER_GATES_CHECK:-scripts/check-release-tier-gates.sh}
TIER_GATES_EVIDENCE=${TIER_GATES_EVIDENCE:-_bmad-output/implementation-artifacts/v2-release-tier-gates-evidence.md}
REAL_S3_IAM_GATE_CHECK=${REAL_S3_IAM_GATE_CHECK:-scripts/check-real-s3-iam-gate.sh}
REAL_S3_IAM_EVIDENCE=${REAL_S3_IAM_EVIDENCE:-_bmad-output/implementation-artifacts/v2-real-s3-iam-production-rehearsal-evidence.md}
V2_CLOSURE_GATE_CHECK=${V2_CLOSURE_GATE_CHECK:-scripts/check-v2-closure-gate.sh}
V2_CLOSURE_EVIDENCE=${V2_CLOSURE_EVIDENCE:-_bmad-output/implementation-artifacts/v2-closure-policy-final-gate-decision.md}

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
require_target prodlike-test-security-rollout
require_target prodlike-e2e-cell-up
require_target cell-doctor
require_target tier2-e2e-hooks-check
require_target tier2-e2e
require_target tier2-e2e-up
require_target evidence-up
require_target tier3-evidence
require_target tier3-evidence-up
require_target production-rehearsal-security
require_target production-rehearsal
require_target production-rehearsal-down

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
target_must_depend_on tier2-e2e prodlike-test-security-assets
target_must_depend_on tier2-e2e prodlike-test-security-rollout
target_must_depend_on tier2-e2e tier2-e2e-hooks-check
target_must_depend_on evidence-up stress-setup
target_must_depend_on tier3-evidence-up evidence-up
target_must_depend_on tier3-evidence-up tier3-evidence

reject_pattern 'SCRAP_E2E_CLEANUP' "$MAKEFILE" "cluster cleanup during E2E execution"
reject_pattern 'SCRAP_E2E_CLEANUP' "$SCRUB_E2E" "cluster cleanup during E2E execution"
reject_pattern 'SCRAP_TEST_HOOKS' "$PRODLIKE_OVERLAY/statefulset-prodlike-patch.yaml" "test hooks in prod-like overlay"

require_pattern '^PRODLIKE_E2E_OVERLAY[[:space:]]*\?=' "$MAKEFILE" "prod-like E2E overlay variable"
require_pattern '^PRODUCTION_REHEARSAL_SCRIPT[[:space:]]*\?=' "$MAKEFILE" "production rehearsal script variable"
require_pattern '^PRODUCTION_REHEARSAL_OPENBAO_IMAGE[[:space:]]*\?=.*openbao/openbao:2\.5\.4' "$MAKEFILE" "pinned production rehearsal OpenBao image"
require_pattern 'SCRAP_PROD_REHEARSAL_BACKEND=fs' "$MAKEFILE" "security-only production rehearsal backend"
require_pattern 'SCRAP_PROD_REHEARSAL_BACKEND=s3' "$MAKEFILE" "S3 production rehearsal backend"
require_pattern '^PRODLIKE_KUBE_CONTEXT[[:space:]]*\?=' "$MAKEFILE" "prod-like kube context variable"
require_pattern '^ifndef PRODLIKE_E2E_KUBE_CONTEXT' "$MAKEFILE" "Tier 2 kube context conditional default"
require_pattern '^PRODLIKE_E2E_KUBE_CONTEXT[[:space:]]*:=.*PRODLIKE_KUBE_CONTEXT' "$MAKEFILE" "Tier 2 kube context default"
require_pattern '^PRODLIKE_E2E_KUBECTL[[:space:]]*=.*PRODLIKE_E2E_KUBE_CONTEXT' "$MAKEFILE" "Tier 2 kubectl context wrapper"
require_pattern '^TIER2_E2E_TEST_RUN[[:space:]]*\?=.*WriteReadHead' "$MAKEFILE" "Tier 2 write/read/head coverage"
require_pattern '^TIER2_E2E_TEST_RUN[[:space:]]*\?=.*LeaderFailover' "$MAKEFILE" "Tier 2 leader failover coverage"
require_pattern '^TIER2_E2E_TEST_RUN[[:space:]]*\?=.*BackendUploadHappyPath' "$MAKEFILE" "Tier 2 Backend happy-path coverage"
require_pattern '^TIER2_E2E_TEST_RUN[[:space:]]*\?=.*BackendUploadLeaderChange' "$MAKEFILE" "Tier 2 Backend outage/recovery coverage"
require_pattern '^TIER2_E2E_TEST_RUN[[:space:]]*\?=.*BackendUploadAdmissionPressure' "$MAKEFILE" "Tier 2 upload pressure coverage"
require_pattern '^TIER2_E2E_TEST_RUN[[:space:]]*\?=.*MultiShardRestartDeterminism' "$MAKEFILE" "Tier 2 multi-Shard restart determinism coverage"
require_pattern '^TIER2_E2E_TEST_RUN[[:space:]]*\?=.*MultiShardBackendUploadUsesNonZeroShard' "$MAKEFILE" "Tier 2 non-zero Shard Backend upload coverage"
require_pattern '^TIER2_E2E_TEST_RUN[[:space:]]*\?=.*LightScrub' "$MAKEFILE" "Tier 2 fast scrub coverage"
require_pattern '^TIER2_E2E_TEST_RUN[[:space:]]*\?=.*ProdlikeSecurityEncryptionEvidence' "$MAKEFILE" "Tier 2 prod-like security evidence coverage"
require_pattern '^TIER2_SECURITY_EVIDENCE_REPORT[[:space:]]*\?=.*security-evidence\.json' "$MAKEFILE" "Tier 2 security evidence report default"
require_pattern '^SECURITY_EVIDENCE_REPORT[[:space:]]*\?=.*TIER2_SECURITY_EVIDENCE_REPORT' "$MAKEFILE" "Tier 3 security evidence report default"
require_pattern 'SCRAP_E2E_CELL_ID="kind-prodlike"' "$MAKEFILE" "prod-like Cell ID in Tier 2"
require_pattern 'SCRAP_E2E_SECURITY_REPORT=.*TIER2_SECURITY_EVIDENCE_REPORT' "$MAKEFILE" "Tier 2 security evidence report path"
require_pattern 'SECURITY_EVIDENCE_REPORT=.*SECURITY_EVIDENCE_REPORT' "$MAKEFILE" "Tier 3 security report export"
require_pattern 'rollout restart statefulset/scrapd' "$MAKEFILE" "Tier 2 security asset rollout restart"
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
require_pattern 'scrap-shard-placement' "$PRODLIKE_E2E_OVERLAY/kustomization.yaml" "Shard placement ConfigMap in prod-like E2E overlay"
require_pattern 'statefulset-shard-placement-patch\.yaml' "$PRODLIKE_E2E_OVERLAY/kustomization.yaml" "Shard placement StatefulSet patch in prod-like E2E overlay"
require_pattern 'SCRAP_SHARD_PLACEMENT_FILE' "$PRODLIKE_E2E_OVERLAY/statefulset-shard-placement-patch.yaml" "Shard placement env in prod-like E2E overlay"
require_pattern 'scrap-shard-placement' "$PRODLIKE_E2E_OVERLAY/statefulset-shard-placement-patch.yaml" "Shard placement volume in prod-like E2E overlay"
require_pattern 'SCRAP_SECURITY_MODE="production"' "$PRODUCTION_REHEARSAL_SCRIPT" "production mode in production rehearsal"
require_pattern 'openbao_transit:[[:space:]]*"real"' "$PRODUCTION_REHEARSAL_SCRIPT" "real OpenBao report marker in production rehearsal"
require_pattern 'SCRAP_PROD_REHEARSAL_RUN_ID' "$PRODUCTION_REHEARSAL_SCRIPT" "per-run production rehearsal Cell ID"
require_pattern 'write_upload_trigger_document' "$PRODUCTION_REHEARSAL_SCRIPT" "Block seal trigger write in production rehearsal"
require_pattern 'decode_read_payload' "$PRODUCTION_REHEARSAL_SCRIPT" "streaming ReadDocument payload verification in production rehearsal"
require_pattern 'wait_backend_upload_confirmed' "$PRODUCTION_REHEARSAL_SCRIPT" "committed Backend upload confirmation wait in production rehearsal"
require_pattern 'backend_upload_confirmed:[[:space:]]*true' "$PRODUCTION_REHEARSAL_SCRIPT" "Backend upload confirmation report marker in production rehearsal"
require_pattern 'SCRAP_SHARD_PLACEMENT_FILE' "$PRODUCTION_REHEARSAL_SCRIPT" "Shard placement file in production rehearsal"
require_pattern 'write_shard_placement' "$PRODUCTION_REHEARSAL_SCRIPT" "Shard placement writer in production rehearsal"
require_pattern 'scrapctl_bin.*openbao.*bootstrap' "$PRODUCTION_REHEARSAL_SCRIPT" "supported scrapctl OpenBao bootstrap path in production rehearsal"
require_pattern 'create_auth_denied_token' "$PRODUCTION_REHEARSAL_SCRIPT" "bounded OpenBao auth-denied token in production rehearsal"
require_pattern 'assert_expected_drill_error' "$PRODUCTION_REHEARSAL_SCRIPT" "expected fail-closed drill error validation in production rehearsal"
require_pattern 'assert_artifact_redaction' "$PRODUCTION_REHEARSAL_SCRIPT" "artifact redaction scan in production rehearsal"
require_pattern 'assert_report_invariants' "$PRODUCTION_REHEARSAL_SCRIPT" "generated report invariant validation in production rehearsal"
require_pattern 'active_drill_pid' "$PRODUCTION_REHEARSAL_SCRIPT" "drill process cleanup in production rehearsal"
require_pattern 'command:[[:space:]]*\$command' "$PRODUCTION_REHEARSAL_SCRIPT" "command metadata in production rehearsal report"
require_pattern 'commit_ref:[[:space:]]*\$commit_ref' "$PRODUCTION_REHEARSAL_SCRIPT" "commit/ref metadata in production rehearsal report"
require_pattern 'git_worktree_state:[[:space:]]*\$worktree_state' "$PRODUCTION_REHEARSAL_SCRIPT" "worktree state metadata in production rehearsal report"
require_pattern 'git_diff_sha256:[[:space:]]*\$diff_sha' "$PRODUCTION_REHEARSAL_SCRIPT" "dirty diff attribution in production rehearsal report"
require_pattern 'expected_result:[[:space:]]*\$expected' "$PRODUCTION_REHEARSAL_SCRIPT" "expected result metadata in production rehearsal report"
require_pattern 'actual_result:[[:space:]]*\$actual' "$PRODUCTION_REHEARSAL_SCRIPT" "actual result metadata in production rehearsal report"
require_pattern 'artifact_path:[[:space:]]*\$artifact' "$PRODUCTION_REHEARSAL_SCRIPT" "artifact path metadata in production rehearsal report"
require_pattern 'redaction_proof:' "$PRODUCTION_REHEARSAL_SCRIPT" "redaction proof metadata in production rehearsal report"
require_pattern 'local_overrides:' "$PRODUCTION_REHEARSAL_SCRIPT" "local override classification in production rehearsal report"
require_pattern 'fail_closed_drills:[[:space:]]*\$drills' "$PRODUCTION_REHEARSAL_SCRIPT" "fail-closed drill evidence in production rehearsal report"
reject_pattern 'SCRAP_TRANSIT_FAKE' "$PRODUCTION_REHEARSAL_SCRIPT" "fake Transit in production rehearsal"
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
require_pattern 'make tier2-e2e-up' "$EVIDENCE_WORKFLOW" "Tier 3 security evidence pre-run"
require_pattern 'make prodlike-kind-delete' "$EVIDENCE_WORKFLOW" "Tier 3 prod-like cleanup before stress Cell"
require_pattern 'unset PRODLIKE_KUBE_CONTEXT PRODLIKE_E2E_KUBE_CONTEXT SCRAP_E2E_KUBE_CONTEXT' "$EVIDENCE_WORKFLOW" "Tier 3 prod-like context reset before stress Cell"
require_pattern 'make tier3-evidence-up' "$EVIDENCE_WORKFLOW" "Tier 3 evidence command"
require_pattern 'upload-artifact' "$EVIDENCE_WORKFLOW" "Tier 3 bundle artifact upload"
require_pattern 'tier3-bundle-path\.txt' "$EVIDENCE_WORKFLOW" "Tier 3 bundle path artifact"
require_pattern 'collect-kind-artifacts' "$EVIDENCE_WORKFLOW" "Tier 3 failure artifact collection"
require_pattern 'kind-scrap-prodlike' "$EVIDENCE_WORKFLOW" "Tier 3 prod-like failure artifact collection"
require_pattern 'kind-scrap-stress' "$EVIDENCE_WORKFLOW" "Tier 3 stress failure artifact collection"

require_pattern '#312' "$PRD_CLOSURE_POLICY" "PRD #312 closure guard"
require_pattern '#337' "$PRD_CLOSURE_POLICY" "PRD #337 closure guard"
require_pattern 'green Tier 2' "$PRD_CLOSURE_POLICY" "green Tier 2 closure requirement"
require_pattern 'GitHub Actions run link' "$PRD_CLOSURE_POLICY" "Tier 2 run link requirement"

[ -x "$TIER_GATES_CHECK" ] || fail "missing executable Tier 2/Tier 3 evidence validator ${TIER_GATES_CHECK}"
TIER_GATES_EVIDENCE="$TIER_GATES_EVIDENCE" "$TIER_GATES_CHECK"

[ -x "$REAL_S3_IAM_GATE_CHECK" ] || fail "missing executable real S3/IAM evidence validator ${REAL_S3_IAM_GATE_CHECK}"
REAL_S3_IAM_EVIDENCE="$REAL_S3_IAM_EVIDENCE" "$REAL_S3_IAM_GATE_CHECK"

[ -x "$V2_CLOSURE_GATE_CHECK" ] || fail "missing executable final V2 closure evidence validator ${V2_CLOSURE_GATE_CHECK}"
V2_CLOSURE_EVIDENCE="$V2_CLOSURE_EVIDENCE" "$V2_CLOSURE_GATE_CHECK"
