#!/usr/bin/env bash
set -euo pipefail

REAL_S3_IAM_EVIDENCE=${REAL_S3_IAM_EVIDENCE:-_bmad-output/implementation-artifacts/v2-real-s3-iam-production-rehearsal-evidence.md}
REAL_S3_IAM_REPORT=${REAL_S3_IAM_REPORT:-artifacts/production-rehearsal/report.json}

fail() {
	echo "real S3/IAM gate check failed: $*" >&2
	exit 1
}

require_pattern() {
	local pattern=$1
	local description=$2

	grep -Eiq "$pattern" "$REAL_S3_IAM_EVIDENCE" || fail "missing ${description} in ${REAL_S3_IAM_EVIDENCE}"
}

table_row() {
	local pattern=$1
	local description=$2
	local row

	row=$(grep -Ei "^\|[[:space:]]*${pattern}[[:space:]]*\|" "$REAL_S3_IAM_EVIDENCE" | head -n 1 || true)
	[ -n "$row" ] || fail "missing ${description} in ${REAL_S3_IAM_EVIDENCE}"
	printf '%s\n' "$row"
}

markdown_row() {
	local mode=$1
	local row=$2
	local arg=${3:-}

	python3 - "$mode" "$row" "$arg" <<'PY'
import sys


def split_markdown_row(row):
    row = row.strip()
    if row.startswith("|"):
        row = row[1:]
    if row.endswith("|"):
        row = row[:-1]

    cells = []
    current = []
    escaped = False
    in_code = False
    for char in row:
        if escaped:
            current.append(char)
            escaped = False
            continue
        if char == "\\":
            current.append(char)
            escaped = True
            continue
        if char == "`":
            in_code = not in_code
            current.append(char)
            continue
        if char == "|" and not in_code:
            cells.append("".join(current).strip())
            current = []
            continue
        current.append(char)
    cells.append("".join(current).strip())
    return cells


mode = sys.argv[1]
cells = split_markdown_row(sys.argv[2])
if mode == "count":
    print(len(cells))
elif mode == "cell":
    cell = int(sys.argv[3]) - 1
    if cell < 0 or cell >= len(cells):
        sys.exit(1)
    print(cells[cell].replace("`", ""))
elif mode == "nonempty":
    for value in cells:
        if value == "" or value == "N/A":
            sys.exit(1)
else:
    sys.exit(1)
PY
}

cell_value() {
	local row=$1
	local cell=$2

	markdown_row cell "$row" "$cell"
}

require_cell_count() {
	local row=$1
	local expected=$2
	local description=$3
	local count

	count=$(markdown_row count "$row")
	[ "$count" -eq "$expected" ] || fail "${description} has ${count} cells, want ${expected}"

	markdown_row nonempty "$row" || fail "${description} has an empty required cell"
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

	if [ "$status" = "PASS" ] && grep -Eiq '(localhost|127\.0\.0\.1|localstack|local-only|screenshot|unlinked|stale|vague|missing IAM|missing real|filesystem Backend|SCRAP_PROD_REHEARSAL_ALLOW_LOCAL_S3=true)' <<<"$row"; then
		fail "found ${description} from weak evidence"
	fi
}

report_proves_real_s3_iam() {
	[ -s "$REAL_S3_IAM_REPORT" ] || fail "release PASS requires non-empty report ${REAL_S3_IAM_REPORT}"

	python3 - "$REAL_S3_IAM_REPORT" "$REAL_S3_IAM_REPORT" <<'PY' || fail "release PASS report does not prove real S3/IAM"
import json
import re
import sys

with open(sys.argv[1], "r", encoding="utf-8") as fh:
    report = json.load(fh)

expected_report_path = sys.argv[2]
upload_count = report.get("confirmed_upload_count")
worktree_state = report.get("git_worktree_state")
forbidden_key_parts = (
    "aws_secret_access_key",
    "secret_access_key",
    "aws_access_key_id",
    "aws_session_token",
    "raw_backend_object_key",
    "backend_object_key",
    "validation_token",
    "root_token",
    "client_token",
    "unseal_keys",
    "keys_base64",
    "x-vault-token",
    "authorization",
)
forbidden_value = re.compile(
    r"AKIA[0-9A-Z]{16}|ASIA[0-9A-Z]{16}|aws_secret_access_key|"
    r"AWS_SECRET_ACCESS_KEY=|xox[baprs]-|ghp_[A-Za-z0-9_]{36,}|"
    r"-----BEGIN (RSA |EC |OPENSSH |DSA |)?PRIVATE KEY-----|"
    r"X-Vault-Token|Authorization:\s*Bearer|validation_token|"
    r"raw_backend_object_key",
    re.IGNORECASE,
)


def report_excludes_forbidden_shapes(value):
    if isinstance(value, dict):
        for key, child in value.items():
            normalized = key.lower().replace("-", "_")
            if any(part.replace("-", "_") in normalized for part in forbidden_key_parts):
                return False
            if not report_excludes_forbidden_shapes(child):
                return False
    elif isinstance(value, list):
        return all(report_excludes_forbidden_shapes(child) for child in value)
    elif isinstance(value, str):
        return forbidden_value.search(value) is None
    return True


checks = [
    report.get("status") == "passed",
    report.get("command") == "make production-rehearsal",
    report.get("commit_ref", "") != "",
    worktree_state in {"clean", "dirty", "unknown"},
    worktree_state != "dirty" or report.get("git_diff_sha256", "") != "",
    report.get("timestamp", "") != "",
    report.get("environment") == "production-rehearsal",
    report.get("evidence_tier") == "real-s3-iam",
    report.get("expected_result", "") != "",
    report.get("actual_result", "") != "",
    report.get("artifact_path") == expected_report_path,
    report.get("report_path") == expected_report_path,
    report.get("security_mode") == "production",
    report.get("production_readiness_status") == "ready",
    report.get("backend") == "s3",
    report.get("local_overrides", {}).get("filesystem_backend") is False,
    report.get("local_overrides", {}).get("local_s3_endpoint_allowed") is False,
    report.get("local_overrides", {}).get("real_s3_iam") is True,
    report.get("openbao_transit") == "real",
    report.get("test_hooks_enabled") is False,
    report.get("pprof_enabled") is False,
    report.get("encrypted_write_read_ok") is True,
    report.get("plaintext_leak_scan_ok") is True,
    report.get("backend_upload_confirmed") is True,
    type(upload_count) is int and upload_count >= 1,
    report.get("redaction_proof", {}).get("status") == "passed",
    report.get("redaction_proof", {}).get("plaintext_leak_scan_ok") is True,
    report.get("redaction_proof", {}).get("report_excludes_secret_material") is True,
    report.get("redaction_proof", {}).get("tracker_ready_evidence_excludes_raw_logs") is True,
    report.get("redaction_proof", {}).get("scan_artifact_path", "") != "",
    report_excludes_forbidden_shapes(report),
]

if not all(checks):
    sys.exit(1)
PY
}

[ -s "$REAL_S3_IAM_EVIDENCE" ] || fail "missing non-empty evidence artifact ${REAL_S3_IAM_EVIDENCE}"

require_pattern '^# V2 Real S3/IAM Production Rehearsal Evidence$' "title"
require_pattern '^Artifact status:' "artifact status"
require_pattern '^Release gate status: (PASS|CONCERNS|FAIL)$' "release gate status"
require_pattern '^Story: 6\.6 - Real S3/IAM Production Rehearsal Closure$' "story identity"
require_pattern 'issue[[:space:]]*`?#429`?|petabytecl/scrap/issues/429' "issue #429 linkage"
require_pattern 'env GOFLAGS=-buildvcs=false make production-rehearsal|make production-rehearsal' "production rehearsal command"
require_pattern 'SCRAP_S3_BUCKET' "S3 bucket environment requirement"
require_pattern 'SCRAP_S3_REGION' "S3 region environment requirement"
require_pattern 'default provider chain|configured profile|workload identity' "IAM credential provenance"
require_pattern 'SCRAP_S3_ENDPOINT' "S3 endpoint classification"
require_pattern 'SCRAP_PROD_REHEARSAL_ALLOW_LOCAL_S3' "local S3 override classification"
require_pattern 'artifacts/production-rehearsal/report\.json|report\.json' "production rehearsal report path"
require_pattern 'status=passed' "report status criterion"
require_pattern 'command=make production-rehearsal' "report command criterion"
require_pattern 'evidence_tier=real-s3-iam' "real S3/IAM report tier criterion"
require_pattern 'backend=s3' "S3 backend report criterion"
require_pattern 'local_overrides\.real_s3_iam=true' "real S3/IAM local override marker"
require_pattern 'local_overrides\.local_s3_endpoint_allowed=false' "no local S3 override marker"
require_pattern 'confirmed_upload_count[[:space:]]*>=[[:space:]]*1|confirmed_upload_count >= 1' "confirmed upload count criterion"
require_pattern 'redaction proof|redaction_proof' "redaction proof field"
require_pattern 'secret|token|raw Backend|Document payload|private material|raw logs' "redaction-sensitive output exclusions"
require_pattern 'screenshot-only|localhost-only|LocalStack-only|local-only|stale|unlinked|missing IAM|vague' "hard weak-proof rejection criteria"

summary_row=$(table_row 'Real S3/IAM production rehearsal' "real S3/IAM summary row")
full_row=$(table_row 'AC-6\.6 Real S3/IAM production rehearsal' "real S3/IAM full evidence row")

require_cell_count "$summary_row" 4 "Real S3/IAM summary row"
require_cell_count "$full_row" 14 "Real S3/IAM full evidence row"

release_status=$(sed -nE 's/^Release gate status: (PASS|CONCERNS|FAIL)$/\1/p' "$REAL_S3_IAM_EVIDENCE" | head -n 1)
summary_status=$(cell_value "$summary_row" 2)
full_status=$(cell_value "$full_row" 12)

require_status "$release_status" "Release gate"
require_status "$summary_status" "Real S3/IAM summary row"
require_status "$full_status" "Real S3/IAM full evidence row"

require_row_pattern "$full_row" 'env GOFLAGS=-buildvcs=false make production-rehearsal|make production-rehearsal' "real S3/IAM command in full evidence row"
require_row_pattern "$full_row" 'SCRAP_S3_BUCKET' "S3 bucket requirement in full evidence row"
require_row_pattern "$full_row" 'SCRAP_S3_REGION' "S3 region requirement in full evidence row"
require_row_pattern "$full_row" 'default provider chain|configured profile|workload identity' "IAM provenance in full evidence row"
require_row_pattern "$full_row" 'SCRAP_S3_ENDPOINT' "S3 endpoint classification in full evidence row"
require_row_pattern "$full_row" 'SCRAP_PROD_REHEARSAL_ALLOW_LOCAL_S3' "local S3 override classification in full evidence row"
require_row_pattern "$full_row" '#429|petabytecl/scrap/issues/429' "issue #429 in full evidence row"
require_row_pattern "$full_row" 'report\.json' "report path in full evidence row"
require_row_pattern "$full_row" 'redaction' "redaction proof in full evidence row"
require_row_pattern "$full_row" 'confirmed_upload_count[[:space:]]*>=[[:space:]]*1|confirmed_upload_count >= 1' "confirmed upload count in full evidence row"

reject_weak_pass "$summary_row" "$summary_status" "Real S3/IAM PASS"
reject_weak_pass "$full_row" "$full_status" "Real S3/IAM PASS"

if [ "$release_status" = "PASS" ] || [ "$summary_status" = "PASS" ] || [ "$full_status" = "PASS" ]; then
	[ "$release_status" = "PASS" ] || fail "Release gate PASS required when any Real S3/IAM row is PASS"
	[ "$summary_status" = "PASS" ] || fail "Release PASS requires Real S3/IAM summary status PASS"
	[ "$full_status" = "PASS" ] || fail "Release PASS requires Real S3/IAM full evidence status PASS"
	if grep -Eiq '#429[^|[:cntrl:]]*open|issue[[:space:]]*`?#429`?[^|[:cntrl:]]*open' "$REAL_S3_IAM_EVIDENCE"; then
		fail "Release PASS cannot cite issue #429 as open"
	fi
	grep -Eiq '#429[^|[:cntrl:]]*(closed|waived)|issue[[:space:]]*`?#429`?[^|[:cntrl:]]*(closed|waived)' "$REAL_S3_IAM_EVIDENCE" || fail "Release PASS requires issue #429 closed or explicitly waived"
	grep -Fq "$REAL_S3_IAM_REPORT" "$REAL_S3_IAM_EVIDENCE" || fail "Release PASS evidence must reference ${REAL_S3_IAM_REPORT}"
	report_proves_real_s3_iam
fi
