#!/usr/bin/env bash
set -euo pipefail

V2_CLOSURE_EVIDENCE=${V2_CLOSURE_EVIDENCE:-_bmad-output/implementation-artifacts/v2-closure-policy-final-gate-decision.md}

fail() {
	echo "v2 closure gate check failed: $*" >&2
	exit 1
}

[ -s "$V2_CLOSURE_EVIDENCE" ] || fail "missing non-empty closure evidence artifact ${V2_CLOSURE_EVIDENCE}"

python3 - "$V2_CLOSURE_EVIDENCE" <<'PY'
import re
import sys


path = sys.argv[1]
def fail(message):
    print(f"v2 closure gate check failed: {message}", file=sys.stderr)
    sys.exit(1)


try:
    with open(path, "r", encoding="utf-8") as fh:
        text = fh.read()
except (OSError, UnicodeDecodeError) as exc:
    fail(f"unreadable closure evidence artifact {path}: {exc}")


def require(pattern, description):
    if not re.search(pattern, text, re.IGNORECASE | re.MULTILINE):
        fail(f"missing {description} in {path}")


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


def is_separator(cells):
    return all(re.fullmatch(r":?-{3,}:?", cell.strip()) for cell in cells)


def table_rows():
    for line in text.splitlines():
        if not line.lstrip().startswith("|"):
            continue
        cells = split_markdown_row(line)
        if len(cells) < 2 or is_separator(cells):
            continue
        yield line, cells


def find_row(pattern, description):
    matcher = re.compile(pattern, re.IGNORECASE)
    for line, cells in table_rows():
        if matcher.search(cells[0].replace("`", "")):
            return line, [cell.replace("`", "").strip() for cell in cells]
    fail(f"missing {description} in {path}")


def section_rows(heading):
    rows = []
    in_section = False
    heading_re = re.compile(rf"^##\s+{re.escape(heading)}\s*$", re.IGNORECASE)
    for line in text.splitlines():
        if line.startswith("## "):
            in_section = bool(heading_re.match(line))
            continue
        if not in_section or not line.lstrip().startswith("|"):
            continue
        cells = split_markdown_row(line)
        if len(cells) < 2 or is_separator(cells):
            continue
        rows.append((line, [cell.replace("`", "").strip() for cell in cells]))
    return rows


def table_body(heading, expected_columns):
    rows = section_rows(heading)
    if len(rows) < 2:
        fail(f"missing {heading} table rows in {path}")
    _, header = rows[0]
    if len(header) != expected_columns:
        fail(f"{heading} table has {len(header)} columns, want {expected_columns}")
    body = rows[1:]
    for _, cells in body:
        require_cell_count(cells, expected_columns, f"{heading} row")
    return body


def require_cell_count(cells, expected, description):
    if len(cells) != expected:
        fail(f"{description} has {len(cells)} cells, want {expected}")
    for cell in cells:
        if cell in {"", "N/A"}:
            fail(f"{description} has an empty required cell")


def require_status(status, description):
    if status not in {"PASS", "CONCERNS", "FAIL"}:
        fail(f"{description} has invalid status {status}")


def require_meaningful(cell, description):
    if re.fullmatch(r"(?i)(|N/A|none|tbd|todo|unknown|unowned)", cell.strip()):
        fail(f"{description} is not meaningful")


def require_row(pattern, line, description):
    if not re.search(pattern, line, re.IGNORECASE):
        fail(f"missing {description}")


status_match = re.search(r"^Final gate status: (PASS|CONCERNS|FAIL)$", text, re.MULTILINE)
if not status_match:
    fail(f"missing final gate status in {path}")
release_status = status_match.group(1)

require(r"^# V2 Closure Policy Final Gate Decision$", "title")
require(r"^Artifact status:", "artifact status")
require(r"^Story: 6\.7 - V2 Closure Policy and Final Gate Decision$", "story identity")
require(r"^## Source Inputs$", "Source Inputs section")
require(r"\|\s*(Current\s+)?Branch\s*\|[^\n]*`?v2`?[^\n]*\|", "current branch")
require(r"https://github\.com/petabytecl/scrap/actions/runs/[0-9]+", "GitHub Actions run URL")
require(r"\|\s*Latest pushed CI\s*\|[^\n]*https://github\.com/petabytecl/scrap/actions/runs/[0-9]+[^\n]*\|", "CI run URL")
require(r"\|\s*Latest pushed CodeQL\s*\|[^\n]*https://github\.com/petabytecl/scrap/actions/runs/[0-9]+[^\n]*\|", "CodeQL run URL")
require(r"no intermediate releases", "no-intermediate-release policy")
require(r"closed issues", "closed issue progress-evidence warning")
require(r"merged PRs", "merged PR progress-evidence warning")
require(r"closed phase", "closed phase progress-evidence warning")
require(r"current linked evidence", "current linked evidence requirement")
require(r"non-waivable", "non-waivable blocker policy")
require(r"P0", "P0 evidence blocker")
require(r"production\s+security", "production security blocker")
require(r"Tier 2", "Tier 2 blocker")
require(r"Tier 3", "Tier 3 blocker")
require(r"real S3/IAM", "real S3/IAM blocker")
require(r"redaction proof", "redaction proof blocker")
require(r"issue\s+`?#429`?|petabytecl/scrap/issues/429", "issue #429 linkage")
require(r"\bci\b", "CI run linkage")
require(r"CodeQL", "CodeQL run linkage")
require(r"owner", "owner field")
require(r"mitigation", "mitigation field")
require(r"next action", "next action field")
require(r"non-goal", "non-goal review")
require(r"local-only", "local-only rejection")
require(r"screenshot", "screenshot rejection")
require(r"stale", "stale evidence rejection")
require(r"unlinked", "unlinked evidence rejection")
require(r"waiver", "waiver policy")

summary_line, summary = find_row(r"^Final V2 release gate$", "Final V2 release gate summary row")
full_line, full = find_row(r"^AC-6\.7 Final V2 release gate$", "Final V2 release gate full row")
epic_line, epic = find_row(r"^Epic 1 through Epic 6$", "Epic rollup row")
non_goal_line, non_goal = find_row(r"^S3-compatible API$", "non-goal review row")

require_cell_count(summary, 4, "Final V2 release gate summary row")
require_cell_count(full, 15, "Final V2 release gate full row")
require_cell_count(epic, 6, "Epic rollup row")
require_cell_count(non_goal, 4, "Non-goal review row")

summary_status = summary[1]
full_status = full[11]
epic_status = epic[1]

require_status(release_status, "Final gate")
require_status(summary_status, "Final V2 release gate summary row")
require_status(full_status, "Final V2 release gate full row")
require_status(epic_status, "Epic rollup row")

require_row(r"scripts/check-v2-closure-gate\.sh", full_line, "validator command in full evidence row")
require_row(r"#429|petabytecl/scrap/issues/429", full_line, "issue #429 in full evidence row")
require_row(r"\bci\b", full_line, "CI run in full evidence row")
require_row(r"CodeQL", full_line, "CodeQL run in full evidence row")
require_row(r"redaction proof", full_line, "redaction proof in full evidence row")
require_row(r"owner|Release owner", full_line, "owner in full evidence row")
require_row(r"Release evidence/docs", full_line, "release evidence environment in full evidence row")
require_row(r"v2-closure-policy-final-gate-decision\.md", full_line, "closure artifact path in full evidence row")
require_row(r"out of scope|not a release blocker", non_goal_line, "non-goal release impact")

if release_status != summary_status or release_status != full_status:
    fail("inconsistent final statuses")

gap_rows = table_body("Gap Table", 7)
required_gap_patterns = (
    r"Tier 2",
    r"Tier 3",
    r"real S3/IAM",
)
for pattern in required_gap_patterns:
    if not any(re.search(pattern, cells[0], re.IGNORECASE) for _, cells in gap_rows):
        fail(f"missing {pattern} gap row")
for _, cells in gap_rows:
    require_status(cells[1], "Gap row")
    require_status(cells[6], "Gap row release status")
    require_meaningful(cells[2], "Gap row owner")
    require_meaningful(cells[3], "Gap row mitigation")
    require_meaningful(cells[4], "Gap row next action")
    require_meaningful(cells[5], "Gap row freshness")

any_pass = release_status == "PASS" or summary_status == "PASS" or full_status == "PASS"
if any_pass:
    if release_status != "PASS":
        fail("Final gate PASS required when any final closure row is PASS")
    if summary_status != "PASS":
        fail("Final PASS requires summary status PASS")
    if full_status != "PASS":
        fail("Final PASS requires full evidence status PASS")
    if epic_status != "PASS":
        fail("Final PASS requires Epic rollup status PASS")
    if re.search(r"#429[^|.\n]*(open)|issue\s+`?#429`?[^|.\n]*(open)", text, re.IGNORECASE):
        fail("Final PASS cannot cite issue #429 as open")
    if any(cells[1] != "PASS" or cells[6] != "PASS" for _, cells in gap_rows):
        fail("Final PASS with unresolved blockers")

    pass_text = "\n".join([summary_line, full_line, epic_line])
    weak_patterns = (
        r"missing Tier",
        r"Tier 2/Tier 3 runtime artifacts (are )?missing",
        r"missing real S3/IAM",
        r"real S3/IAM proof .*missing",
        r"missing redaction",
        r"Redaction proof missing",
        r"local-only",
        r"screenshot",
        r"stale",
        r"unlinked terminal",
        r"closed issues? (only|alone)",
        r"merged PRs? (only|alone)",
        r"closed phase",
        r"waiver bypass",
        r"waiver",
    )
    if any(re.search(pattern, pass_text, re.IGNORECASE) for pattern in weak_patterns):
        fail("Final PASS from weak or missing evidence")

    required_pass_patterns = (
        r"issue\s+`?#429`?\s+closed|#429[^|.\n]*closed",
        r"Tier 2",
        r"Tier 3",
        r"production security",
        r"real S3/IAM",
        r"Redaction proof PASS",
        r"ci[^|.\n]*green|green[^|.\n]*ci",
        r"CodeQL[^|.\n]*green|green[^|.\n]*CodeQL",
        r"artifacts/tier2-e2e\.log",
        r"artifacts/tier3-bundle-path\.txt",
        r"artifacts/production-rehearsal/report\.json",
        r"epic-4-production-security-rehearsal-closure-evidence\.md",
    )
    for pattern in required_pass_patterns:
        if not re.search(pattern, pass_text, re.IGNORECASE):
            fail("Final PASS from weak or missing evidence")
elif release_status in {"FAIL", "CONCERNS"}:
    require_meaningful(full[12], "Final non-PASS owner")
    require_meaningful(full[13], "Final non-PASS mitigation")
    require_meaningful(full[14], "Final non-PASS next action")
PY
