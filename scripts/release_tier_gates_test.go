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

func TestReleaseTierGatesScriptRejectsMissingEvidence(t *testing.T) {
	output, err := runReleaseTierGateCheck(t, filepath.Join(t.TempDir(), "missing.md"))
	if err == nil {
		t.Fatalf("check unexpectedly passed:\n%s", output)
	}
	if !strings.Contains(output, "missing non-empty evidence artifact") {
		t.Fatalf("output = %q, want missing evidence failure", output)
	}
}

func TestReleaseTierGatesScriptAcceptsCompleteEvidence(t *testing.T) {
	evidence := filepath.Join(t.TempDir(), "evidence.md")
	writeEvidence(t, evidence, validTierGateEvidence())

	output, err := runReleaseTierGateCheck(t, evidence)
	if err != nil {
		t.Fatalf("check failed: %v\n%s", err, output)
	}
}

func TestReleaseTierGatesScriptRejectsWeakPassEvidence(t *testing.T) {
	evidence := filepath.Join(t.TempDir(), "evidence.md")
	content := strings.Replace(
		validTierGateEvidence(),
		"| Tier 2 prod-like Cilium | CONCERNS | current but dedicated workflow unavailable |",
		"| Tier 2 prod-like Cilium | PASS | current durable run |",
		1,
	)
	content = strings.Replace(content, "durable artifact pending", "local-only output copied from a terminal", 1)
	writeEvidence(t, evidence, content)

	output, err := runReleaseTierGateCheck(t, evidence)
	if err == nil {
		t.Fatalf("check unexpectedly passed:\n%s", output)
	}
	if !strings.Contains(output, "Tier 2 PASS from weak evidence") {
		t.Fatalf("output = %q, want weak PASS failure", output)
	}
}

func TestReleaseTierGatesScriptRejectsTier3WeakPassEvidence(t *testing.T) {
	evidence := filepath.Join(t.TempDir(), "evidence.md")
	content := strings.Replace(
		validTierGateEvidence(),
		"| Tier 3 evidence bundle | FAIL | current target and workflow wiring; no current bundle linked |",
		"| Tier 3 evidence bundle | PASS | current durable bundle |",
		1,
	)
	content = strings.Replace(content, "durable bundle pending", "screenshot-only output", 1)
	writeEvidence(t, evidence, content)

	output, err := runReleaseTierGateCheck(t, evidence)
	if err == nil {
		t.Fatalf("check unexpectedly passed:\n%s", output)
	}
	if !strings.Contains(output, "Tier 3 PASS from weak evidence") {
		t.Fatalf("output = %q, want Tier 3 weak PASS failure", output)
	}
}

func TestReleaseTierGatesScriptRejectsReleasePassWithFailingTiers(t *testing.T) {
	evidence := filepath.Join(t.TempDir(), "evidence.md")
	content := strings.Replace(validTierGateEvidence(), "Release gate status: CONCERNS", "Release gate status: PASS", 1)
	writeEvidence(t, evidence, content)

	output, err := runReleaseTierGateCheck(t, evidence)
	if err == nil {
		t.Fatalf("check unexpectedly passed:\n%s", output)
	}
	if !strings.Contains(output, "Release PASS requires Tier 2 summary status PASS") {
		t.Fatalf("output = %q, want release PASS consistency failure", output)
	}
}

func TestReleaseTierGatesScriptRejectsFieldsOnlyInProse(t *testing.T) {
	evidence := filepath.Join(t.TempDir(), "evidence.md")
	writeEvidence(t, evidence, proseOnlyTierGateEvidence())

	output, err := runReleaseTierGateCheck(t, evidence)
	if err == nil {
		t.Fatalf("check unexpectedly passed:\n%s", output)
	}
	if !strings.Contains(output, "missing Tier 2 full evidence row") {
		t.Fatalf("output = %q, want missing full row failure", output)
	}
}

func runReleaseTierGateCheck(t *testing.T, evidencePath string) (string, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	repoRoot := repoRoot(t)
	//nolint:gosec // The test executes this repo's validation script against test-owned files.
	cmd := exec.CommandContext(ctx, "bash", filepath.Join(repoRoot, "scripts/check-release-tier-gates.sh"))
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "TIER_GATES_EVIDENCE="+evidencePath)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func writeEvidence(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write evidence: %v", err)
	}
}

func validTierGateEvidence() string {
	return `# V2 Release Tier Gates Evidence

Artifact status: complete for Story 6.5 validation
Release gate status: CONCERNS

Story: 6.5 - Tier 2 and Tier 3 Release Evidence Gates

## Evidence Schema

Rows record command, commit/ref, environment, expected result, actual result,
artifact path, timestamp, redaction proof, freshness, owner, mitigation, and
artifact retention.

## Gate Summary

| Gate | Status | Freshness | Evidence |
| --- | --- | --- | --- |
| Tier 2 prod-like Cilium | CONCERNS | current but dedicated workflow unavailable | command ` + "`make tier2-e2e-up`" + `, GitHub Actions workflow ` + "`prodlike-e2e.yml`" + `, artifact ` + "`artifacts/tier2-e2e.log`" + `, security report ` + "`artifacts/prodlike-security/security-evidence.json`" + `, durable artifact pending. |
| Tier 3 evidence bundle | FAIL | current target and workflow wiring; no current bundle linked | command ` + "`make tier3-evidence-up STRESS_SCENARIO=throughput`" + `, GitHub Actions workflow ` + "`evidence-gate.yml`" + `, artifact ` + "`artifacts/tier3-bundle-path.txt`" + `, bundle ` + "`manifest.json`" + `, ` + "`gates.json`" + `, ` + "`privacy-scan.json`" + `, durable bundle pending. |

## Full Evidence Rows

| Requirement | Command | Commit/ref | Environment | Expected result | Actual result | Artifact path | Timestamp | Redaction proof | Freshness | Status | Owner | Mitigation | Retention |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| AC-6.5.1 Tier 2 prod-like Cilium | ` + "`make tier2-e2e-up`" + ` through ` + "`prodlike-e2e.yml`" + ` | current implementation commit ` + "`abc1234`" + ` | Prod-like Kind Cell with Cilium | Deployed gateway behavior and security posture pass. | CONCERNS: current workflow artifact is not linked. | Expected ` + "`artifacts/tier2-e2e.log`" + ` and ` + "`artifacts/prodlike-security/security-evidence.json`" + `. | 2026-06-12T20:18:53-04:00 | Redaction proof recorded without raw logs or secrets. | Missing current runtime run. | CONCERNS | Release owner | Link a green current run. | GitHub Actions artifact retention must be tracked. |
| AC-6.5.2 Tier 3 evidence bundle | ` + "`make tier3-evidence-up STRESS_SCENARIO=throughput`" + ` through ` + "`evidence-gate.yml`" + ` | current implementation commit ` + "`abc1234`" + ` | Tier 3 evidence Cell with telemetry bundle | Logs metrics traces profiles manifest gates and privacy scan pass. | FAIL: current bundle is not linked. | Expected ` + "`artifacts/tier3-bundle-path.txt`" + ` plus ` + "`manifest.json`" + `, ` + "`gates.json`" + `, and ` + "`privacy-scan.json`" + `. | 2026-06-12T20:18:53-04:00 | Redaction proof recorded without raw logs or secrets. | Missing current runtime bundle. | FAIL | Release owner | Link a green current bundle. | GitHub Actions artifact retention must be tracked. |

Weak proof such as screenshot-only, unlinked, stale, or local-only output is rejected.

## Retention

GitHub Actions artifact retention must be tracked. Local-only artifacts are not
final release proof unless copied to durable reviewable storage.
`
}

func proseOnlyTierGateEvidence() string {
	return `# V2 Release Tier Gates Evidence

Artifact status: incomplete validation fixture
Release gate status: CONCERNS

Story: 6.5 - Tier 2 and Tier 3 Release Evidence Gates

Rows record command, commit/ref, environment, expected result, actual result,
artifact path, timestamp, redaction proof, freshness, owner, mitigation, and
artifact retention. Screenshots, local-only, unlinked, and stale output are rejected.

| Gate | Status | Freshness | Evidence |
| --- | --- | --- | --- |
| Tier 2 prod-like Cilium | CONCERNS | current but dedicated workflow unavailable | command ` + "`make tier2-e2e-up`" + `, GitHub Actions workflow ` + "`prodlike-e2e.yml`" + `, artifact ` + "`artifacts/tier2-e2e.log`" + `. |
| Tier 3 evidence bundle | FAIL | current target and workflow wiring; no current bundle linked | command ` + "`make tier3-evidence-up STRESS_SCENARIO=throughput`" + `, GitHub Actions workflow ` + "`evidence-gate.yml`" + `, artifact ` + "`artifacts/tier3-bundle-path.txt`" + `, bundle ` + "`manifest.json`" + `, ` + "`gates.json`" + `, ` + "`privacy-scan.json`" + `. |

## Retention

GitHub Actions artifact retention must be tracked.
`
}
