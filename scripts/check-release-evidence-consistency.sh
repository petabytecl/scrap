#!/usr/bin/env bash
# Story 6.8 / H-19: fail closed when release artifacts contradict each other.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
CLOSURE=${CLOSURE_EVIDENCE:-$ROOT/_bmad-output/implementation-artifacts/closure-policy-final-gate-decision.md}
MATRIX=${RELEASE_EVIDENCE_MATRIX:-$ROOT/_bmad-output/implementation-artifacts/release-evidence-matrix.md}
TIER=${TIER_GATES_EVIDENCE:-$ROOT/_bmad-output/implementation-artifacts/release-tier-gates-evidence.md}

fail() {
	echo "release evidence consistency check failed: $*" >&2
	exit 1
}

status_of() {
	local file=$1
	local pattern=$2
	sed -nE "s/^${pattern}: (PASS|CONCERNS|FAIL)$/\\1/p" "$file" | head -n 1
}

[ -s "$CLOSURE" ] || fail "missing $CLOSURE"
[ -s "$MATRIX" ] || fail "missing $MATRIX"
[ -s "$TIER" ] || fail "missing $TIER"

closure_status=$(status_of "$CLOSURE" "Final gate status")
matrix_status=$(grep -E '^\| Final SCRAP release gate \|' "$MATRIX" | head -n 1 | awk -F'|' '{print $3}' | tr -d ' ')
if [ -z "$matrix_status" ]; then
	matrix_status=$(grep -E 'Final SCRAP release gate' "$MATRIX" | head -n 1 | grep -oE '(PASS|CONCERNS|FAIL)' | head -n 1 || true)
fi
tier_status=$(status_of "$TIER" "Release gate status")

[ -n "$closure_status" ] || fail "missing Final gate status in $CLOSURE"
[ -n "$tier_status" ] || fail "missing Release gate status in $TIER"

# AC-6.8.1: closure PASS while matrix/tier FAIL is a contradiction.
if [ "$closure_status" = "PASS" ]; then
	if [ "$tier_status" = "FAIL" ] || [ "$matrix_status" = "FAIL" ]; then
		fail "closure PASS contradicts matrix/tier FAIL (Story 6.8)"
	fi
fi

# Thermo-nuclear baseline: unresolved High/Medium findings keep release at FAIL.
if grep -Eq 'H-0[0-9]|H-1[0-9]|M-0[0-9]|M-1[0-2]' "$CLOSURE" && [ "$closure_status" = "PASS" ]; then
	if grep -Eq 'unresolved|open findings|31 thermo-nuclear|FAIL baseline' "$CLOSURE"; then
		fail "closure PASS with unresolved thermo-nuclear findings"
	fi
fi

echo "release evidence consistency: closure=$closure_status matrix=${matrix_status:-unknown} tier=$tier_status"
