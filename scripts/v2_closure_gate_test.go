package scripts_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestV2ClosureGateScriptRejectsMissingEvidence(t *testing.T) {
	output, err := runV2ClosureGateCheck(t, filepath.Join(t.TempDir(), "missing.md"))
	if err == nil {
		t.Fatalf("check unexpectedly passed:\n%s", output)
	}
	if !strings.Contains(output, "missing non-empty closure evidence artifact") {
		t.Fatalf("output = %q, want missing evidence failure", output)
	}
}

func TestV2ClosureGateScriptAcceptsHonestFailEvidence(t *testing.T) {
	evidence := filepath.Join(t.TempDir(), "closure.md")
	writeEvidence(t, evidence, validV2ClosureFailEvidence())

	output, err := runV2ClosureGateCheck(t, evidence)
	if err != nil {
		t.Fatalf("check failed: %v\n%s", err, output)
	}
}

func TestV2ClosureGateScriptAcceptsCompletePassEvidence(t *testing.T) {
	evidence := filepath.Join(t.TempDir(), "closure.md")
	writeEvidence(t, evidence, validV2ClosurePassEvidence())

	output, err := runV2ClosureGateCheck(t, evidence)
	if err != nil {
		t.Fatalf("check failed: %v\n%s", err, output)
	}
}

func TestV2ClosureGateScriptRejectsPassWithOpenIssue429(t *testing.T) {
	evidence := filepath.Join(t.TempDir(), "closure.md")
	content := strings.Replace(validV2ClosurePassEvidence(), "issue `#429` closed", "issue `#429` open", 1)
	writeEvidence(t, evidence, content)

	output, err := runV2ClosureGateCheck(t, evidence)
	if err == nil {
		t.Fatalf("check unexpectedly passed:\n%s", output)
	}
	if !strings.Contains(output, "Final PASS cannot cite issue #429 as open") {
		t.Fatalf("output = %q, want open issue failure", output)
	}
}

func TestV2ClosureGateScriptRejectsPassWithMissingTierRuntimeEvidence(t *testing.T) {
	evidence := filepath.Join(t.TempDir(), "closure.md")
	content := strings.Replace(validV2ClosurePassEvidence(), "current linked Tier 2 and Tier 3 runtime artifacts", "missing Tier 2 and Tier 3 runtime artifacts", 1)
	writeEvidence(t, evidence, content)

	output, err := runV2ClosureGateCheck(t, evidence)
	if err == nil {
		t.Fatalf("check unexpectedly passed:\n%s", output)
	}
	if !strings.Contains(output, "Final PASS from weak or missing evidence") {
		t.Fatalf("output = %q, want missing Tier runtime failure", output)
	}
}

func TestV2ClosureGateScriptRejectsPassWithMissingRedactionProof(t *testing.T) {
	evidence := filepath.Join(t.TempDir(), "closure.md")
	content := strings.Replace(validV2ClosurePassEvidence(), "Redaction proof PASS", "Redaction proof missing", 1)
	writeEvidence(t, evidence, content)

	output, err := runV2ClosureGateCheck(t, evidence)
	if err == nil {
		t.Fatalf("check unexpectedly passed:\n%s", output)
	}
	if !strings.Contains(output, "Final PASS from weak or missing evidence") {
		t.Fatalf("output = %q, want missing redaction failure", output)
	}
}

func TestV2ClosureGateScriptRejectsFieldsOnlyInProse(t *testing.T) {
	evidence := filepath.Join(t.TempDir(), "closure.md")
	writeEvidence(t, evidence, proseOnlyV2ClosureEvidence())

	output, err := runV2ClosureGateCheck(t, evidence)
	if err == nil {
		t.Fatalf("check unexpectedly passed:\n%s", output)
	}
	if !strings.Contains(output, "missing Final V2 release gate full row") {
		t.Fatalf("output = %q, want missing full row failure", output)
	}
}

func runV2ClosureGateCheck(t *testing.T, evidencePath string) (string, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	repoRoot := repoRoot(t)
	//nolint:gosec // The test executes this repo's validation script against test-owned files.
	cmd := exec.CommandContext(ctx, "bash", filepath.Join(repoRoot, "scripts/check-v2-closure-gate.sh"))
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "V2_CLOSURE_EVIDENCE="+evidencePath)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func validV2ClosureFailEvidence() string {
	return `# V2 Closure Policy Final Gate Decision

Artifact status: complete for Story 6.7 validation
Final gate status: FAIL

Story: 6.7 - V2 Closure Policy and Final Gate Decision

## Policy Review

V2 has no intermediate releases. Closed issues, merged PRs, and closed phase
milestones are progress evidence, not release PASS proof without current linked
evidence. Non-waivable blockers include required P0 feature evidence, production
security evidence, Tier 2/Tier 3 evidence, real S3/IAM evidence, and redaction
proof.

## Gate Summary

| Gate | Status | Evidence | Owner / next action |
| --- | --- | --- | --- |
| Final V2 release gate | FAIL | issue ` + "`#429`" + ` open; missing real S3/IAM proof; Tier 2/Tier 3 runtime artifacts not linked; latest ci run ` + "`27451981266`" + ` and CodeQL run ` + "`27451981267`" + ` green for commit ` + "`9efe29c`" + `. | Release owner: link required runtime evidence and close or explicitly waive blockers before PASS. |

## Full Blocker Rows

| Requirement | Source | Evidence command | Commit/ref | Environment | Evidence artifact | Issue/Run | Expected result | Actual result | Redaction proof | Freshness | Status | Owner | Mitigation | Next action |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| AC-6.7 Final V2 release gate | Story 6.7 / FR-16 / DG-5 | ` + "`scripts/check-v2-closure-gate.sh`" + ` | ` + "`9efe29c`" + ` | Release evidence/docs | ` + "`_bmad-output/implementation-artifacts/v2-closure-policy-final-gate-decision.md`" + ` | issue ` + "`#429`" + ` open; ci ` + "`27451981266`" + ` green; CodeQL ` + "`27451981267`" + ` green | Final V2 release PASS only with current linked evidence for every required gate. | FAIL: real S3/IAM proof and Tier 2/Tier 3 runtime artifacts are missing. | Redaction proof PASS: artifact excludes secrets, raw Backend keys, raw logs, Document payloads, private material, trace IDs, request IDs, and host-absolute paths. | Current live check. | FAIL | Release owner | Run/link Tier 2, Tier 3, and real S3/IAM evidence; close issue ` + "`#429`" + `. | Keep V2 release below PASS. |

## Epic Rollup

| Epic | Status | Artifact | Command/ref | Owner | Release status |
| --- | --- | --- | --- | --- | --- |
| Epic 1 through Epic 6 | CONCERNS | ` + "`_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md`" + ` | current release matrix | Release owner | FAIL |

## Non-Goal Review

| Item | Source | Scope decision | Release impact |
| --- | --- | --- | --- |
| S3-compatible API | ` + "`docs/v2-scope-reconciliation.md`" + ` | Explicit non-goal unless re-chartered. | Out of scope; not a release blocker. |

Hard criteria reject local-only output, screenshots, stale artifacts, unlinked
terminal snippets, ownerless blockers, and non-waivable waiver bypasses.
`
}

func validV2ClosurePassEvidence() string {
	return `# V2 Closure Policy Final Gate Decision

Artifact status: complete for Story 6.7 validation
Final gate status: PASS

Story: 6.7 - V2 Closure Policy and Final Gate Decision

## Policy Review

V2 has no intermediate releases. Closed issues, merged PRs, and closed phase
milestones are progress evidence, not release PASS proof without current linked
evidence. Non-waivable blockers include required P0 feature evidence, production
security evidence, Tier 2/Tier 3 evidence, real S3/IAM evidence, and redaction
proof.

## Gate Summary

| Gate | Status | Evidence | Owner / next action |
| --- | --- | --- | --- |
| Final V2 release gate | PASS | issue ` + "`#429`" + ` closed; current linked Tier 2 and Tier 3 runtime artifacts; production security rehearsal linked; real S3/IAM report linked; Redaction proof PASS; ci run ` + "`27451981266`" + ` and CodeQL run ` + "`27451981267`" + ` green for commit ` + "`9efe29c`" + `. | Release owner: release can proceed. |

## Full Blocker Rows

| Requirement | Source | Evidence command | Commit/ref | Environment | Evidence artifact | Issue/Run | Expected result | Actual result | Redaction proof | Freshness | Status | Owner | Mitigation | Next action |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| AC-6.7 Final V2 release gate | Story 6.7 / FR-16 / DG-5 | ` + "`scripts/check-v2-closure-gate.sh`" + ` | ` + "`9efe29c`" + ` | Release evidence/docs | ` + "`_bmad-output/implementation-artifacts/v2-closure-policy-final-gate-decision.md`" + `; ` + "`artifacts/tier2-e2e.log`" + `; ` + "`artifacts/tier3-bundle-path.txt`" + `; ` + "`artifacts/production-rehearsal/report.json`" + ` | issue ` + "`#429`" + ` closed; ci ` + "`27451981266`" + ` green; CodeQL ` + "`27451981267`" + ` green | Final V2 release PASS only with current linked evidence for every required gate. | PASS: all required release evidence is current and linked. | Redaction proof PASS: artifact excludes secrets, raw Backend keys, raw logs, Document payloads, private material, trace IDs, request IDs, and host-absolute paths. | Current linked Tier 2/Tier 3 and real S3/IAM evidence. | PASS | Release owner | None. | Release can proceed. |

## Epic Rollup

| Epic | Status | Artifact | Command/ref | Owner | Release status |
| --- | --- | --- | --- | --- | --- |
| Epic 1 through Epic 6 | PASS | ` + "`_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md`" + ` | current release matrix | Release owner | PASS |

## Non-Goal Review

| Item | Source | Scope decision | Release impact |
| --- | --- | --- | --- |
| S3-compatible API | ` + "`docs/v2-scope-reconciliation.md`" + ` | Explicit non-goal unless re-chartered. | Out of scope; not a release blocker. |

Hard criteria reject local-only output, screenshots, stale artifacts, unlinked
terminal snippets, ownerless blockers, and non-waivable waiver bypasses.
`
}

func proseOnlyV2ClosureEvidence() string {
	return `# V2 Closure Policy Final Gate Decision

Artifact status: incomplete validation fixture
Final gate status: FAIL

Story: 6.7 - V2 Closure Policy and Final Gate Decision

This prose mentions V2 has no intermediate releases, closed issues, merged PRs,
closed phase milestones, current linked evidence, non-waivable blockers, P0
feature evidence, production security evidence, Tier 2, Tier 3, real S3/IAM,
redaction proof, issue #429, ci, CodeQL, owner, mitigation, next action,
non-goals, local-only, screenshots, stale, unlinked, and waiver bypasses.

| Gate | Status | Evidence | Owner / next action |
| --- | --- | --- | --- |
| Final V2 release gate | FAIL | issue ` + "`#429`" + ` open. | Release owner. |
`
}
