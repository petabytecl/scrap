#!/usr/bin/env bash
set -euo pipefail

TIER_GATES_EVIDENCE=${TIER_GATES_EVIDENCE:-_bmad-output/implementation-artifacts/release-tier-gates-evidence.md}

fail() {
	echo "release tier gate check failed: $*" >&2
	exit 1
}

require_pattern() {
	local pattern=$1
	local description=$2

	grep -Eiq "$pattern" "$TIER_GATES_EVIDENCE" || fail "missing ${description} in ${TIER_GATES_EVIDENCE}"
}

table_row() {
	local pattern=$1
	local description=$2
	local row

	row=$(grep -Ei "^\|[[:space:]]*${pattern}[[:space:]]*\|" "$TIER_GATES_EVIDENCE" | head -n 1 || true)
	[ -n "$row" ] || fail "missing ${description} in ${TIER_GATES_EVIDENCE}"
	printf '%s\n' "$row"
}

cell_value() {
	local row=$1
	local cell=$2

	awk -F '|' -v cell="$cell" '{
		value = $(cell + 1)
		gsub(/^[[:space:]]+|[[:space:]]+$/, "", value)
		gsub(/`/, "", value)
		print value
	}' <<<"$row"
}

require_cell_count() {
	local row=$1
	local expected=$2
	local description=$3

	local count
	count=$(awk -F '|' '{print NF - 2}' <<<"$row")
	[ "$count" -eq "$expected" ] || fail "${description} has ${count} cells, want ${expected}"

	awk -F '|' '{
		for (i = 2; i < NF; i++) {
			value = $i
			gsub(/^[[:space:]]+|[[:space:]]+$/, "", value)
			if (value == "" || value == "N/A") {
				exit 1
			}
		}
	}' <<<"$row" || fail "${description} has an empty required cell"
}

require_row_pattern() {
	local row=$1
	local pattern=$2
	local description=$3

	grep -Eiq "$pattern" <<<"$row" || fail "missing ${description}"
}

require_status() {
	local status=$1
	local description=$2

	case "$status" in
	PASS | CONCERNS | FAIL) ;;
	*) fail "${description} has invalid status ${status}" ;;
	esac
}

reject_weak_pass() {
	local row=$1
	local status=$2
	local description=$3

	if [ "$status" = "PASS" ] && grep -Eiq '(local-only|screenshot|unlinked|stale)' <<<"$row"; then
		fail "found ${description} from weak evidence"
	fi
}

[ -s "$TIER_GATES_EVIDENCE" ] || fail "missing non-empty evidence artifact ${TIER_GATES_EVIDENCE}"

require_pattern '^# SCRAP Release Tier Gates Evidence$' "title"
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

tier2_summary=$(table_row 'Tier 2 prod-like Cilium' "Tier 2 summary row")
tier3_summary=$(table_row 'Tier 3 evidence bundle' "Tier 3 summary row")
tier2_full=$(table_row 'AC-6\.5\.1 Tier 2 prod-like Cilium' "Tier 2 full evidence row")
tier3_full=$(table_row 'AC-6\.5\.2 Tier 3 evidence bundle' "Tier 3 full evidence row")

require_cell_count "$tier2_summary" 4 "Tier 2 summary row"
require_cell_count "$tier3_summary" 4 "Tier 3 summary row"
require_cell_count "$tier2_full" 14 "Tier 2 full evidence row"
require_cell_count "$tier3_full" 14 "Tier 3 full evidence row"

release_status=$(sed -nE 's/^Release gate status: (PASS|CONCERNS|FAIL)$/\1/p' "$TIER_GATES_EVIDENCE" | head -n 1)
tier2_summary_status=$(cell_value "$tier2_summary" 2)
tier3_summary_status=$(cell_value "$tier3_summary" 2)
tier2_full_status=$(cell_value "$tier2_full" 11)
tier3_full_status=$(cell_value "$tier3_full" 11)

require_status "$release_status" "Release gate"
require_status "$tier2_summary_status" "Tier 2 summary row"
require_status "$tier3_summary_status" "Tier 3 summary row"
require_status "$tier2_full_status" "Tier 2 full evidence row"
require_status "$tier3_full_status" "Tier 3 full evidence row"

require_row_pattern "$tier2_full" 'make tier2-e2e-up' "Tier 2 command in full evidence row"
require_row_pattern "$tier2_full" 'artifacts/tier2-e2e\.log' "Tier 2 log artifact in full evidence row"
require_row_pattern "$tier2_full" 'prodlike-security/security-evidence\.json' "Tier 2 security evidence report in full evidence row"
require_row_pattern "$tier2_full" 'Kind.*Cilium|Cilium.*Kind' "Tier 2 prod-like Kind/Cilium environment in full evidence row"
require_row_pattern "$tier3_full" 'make tier3-evidence-up' "Tier 3 command in full evidence row"
require_row_pattern "$tier3_full" 'artifacts/tier3-bundle-path\.txt' "Tier 3 bundle path artifact in full evidence row"
require_row_pattern "$tier3_full" 'manifest\.json' "Tier 3 manifest in full evidence row"
require_row_pattern "$tier3_full" 'gates\.json' "Tier 3 gates in full evidence row"
require_row_pattern "$tier3_full" 'privacy-scan\.json' "Tier 3 privacy scan in full evidence row"

reject_weak_pass "$tier2_summary" "$tier2_summary_status" "Tier 2 PASS"
reject_weak_pass "$tier3_summary" "$tier3_summary_status" "Tier 3 PASS"
reject_weak_pass "$tier2_full" "$tier2_full_status" "Tier 2 PASS"
reject_weak_pass "$tier3_full" "$tier3_full_status" "Tier 3 PASS"

if [ "$release_status" = "PASS" ]; then
	[ "$tier2_summary_status" = "PASS" ] || fail "Release PASS requires Tier 2 summary status PASS"
	[ "$tier3_summary_status" = "PASS" ] || fail "Release PASS requires Tier 3 summary status PASS"
	[ "$tier2_full_status" = "PASS" ] || fail "Release PASS requires Tier 2 full evidence status PASS"
	[ "$tier3_full_status" = "PASS" ] || fail "Release PASS requires Tier 3 full evidence status PASS"
fi
