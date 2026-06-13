#!/usr/bin/env bash
set -euo pipefail

TIER_GATES_EVIDENCE=${TIER_GATES_EVIDENCE:-_bmad-output/implementation-artifacts/v2-release-tier-gates-evidence.md}

fail() {
	echo "release tier gate check failed: $*" >&2
	exit 1
}

require_pattern() {
	local pattern=$1
	local description=$2

	grep -Eiq "$pattern" "$TIER_GATES_EVIDENCE" || fail "missing ${description} in ${TIER_GATES_EVIDENCE}"
}

reject_pattern() {
	local pattern=$1
	local description=$2

	if grep -Eiq "$pattern" "$TIER_GATES_EVIDENCE"; then
		fail "found ${description} in ${TIER_GATES_EVIDENCE}"
	fi
}

[ -s "$TIER_GATES_EVIDENCE" ] || fail "missing non-empty evidence artifact ${TIER_GATES_EVIDENCE}"

require_pattern '^# V2 Release Tier Gates Evidence$' "title"
require_pattern '^Artifact status:' "artifact status"
require_pattern '^Release gate status: (PASS|CONCERNS|FAIL)$' "release gate status"
require_pattern '^Story: 6\.5 - Tier 2 and Tier 3 Release Evidence Gates$' "story identity"
require_pattern 'Tier 2 prod-like Cilium' "Tier 2 row"
require_pattern 'Tier 3 evidence bundle' "Tier 3 row"
require_pattern 'make tier2-e2e-up' "Tier 2 command"
require_pattern 'make tier3-evidence-up' "Tier 3 command"
require_pattern 'prodlike-e2e\.yml' "Tier 2 dedicated workflow reference"
require_pattern 'evidence-gate\.yml' "Tier 3 dedicated workflow reference"
require_pattern 'artifacts/tier2-e2e\.log' "Tier 2 log artifact"
require_pattern 'artifacts/tier3-bundle-path\.txt' "Tier 3 bundle path artifact"
require_pattern 'manifest\.json' "Tier 3 manifest artifact"
require_pattern 'gates\.json' "Tier 3 gates artifact"
require_pattern 'privacy-scan\.json' "Tier 3 privacy scan artifact"
require_pattern 'GitHub Actions' "GitHub Actions evidence source"
require_pattern 'artifact retention|retention' "artifact retention decision"
require_pattern 'screenshot' "screenshot rejection criteria"
require_pattern 'local-only' "local-only evidence classification"
require_pattern 'unlinked' "unlinked evidence classification"
require_pattern 'stale' "stale evidence classification"
require_pattern 'command' "command field"
require_pattern 'commit/ref' "commit/ref field"
require_pattern 'environment' "environment field"
require_pattern 'expected result' "expected result field"
require_pattern 'actual result' "actual result field"
require_pattern 'artifact path' "artifact path field"
require_pattern 'timestamp' "timestamp field"
require_pattern 'redaction proof' "redaction proof field"
require_pattern 'freshness' "freshness field"
require_pattern 'owner' "owner field"
require_pattern 'mitigation' "mitigation field"

reject_pattern 'Tier 2[^|]*\|[^|]*PASS[^|]*\|[^|]*(local-only|screenshot|unlinked|stale)' "Tier 2 PASS from weak evidence"
reject_pattern 'Tier 3[^|]*\|[^|]*PASS[^|]*\|[^|]*(local-only|screenshot|unlinked|stale)' "Tier 3 PASS from weak evidence"
