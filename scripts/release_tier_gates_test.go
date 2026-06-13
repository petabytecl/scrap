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
	writeEvidence(t, evidence, validTierGateEvidence("CONCERNS", "current but dedicated workflow unavailable"))

	output, err := runReleaseTierGateCheck(t, evidence)
	if err != nil {
		t.Fatalf("check failed: %v\n%s", err, output)
	}
}

func TestReleaseTierGatesScriptRejectsWeakPassEvidence(t *testing.T) {
	evidence := filepath.Join(t.TempDir(), "evidence.md")
	writeEvidence(t, evidence, validTierGateEvidence("PASS", "local-only output"))

	output, err := runReleaseTierGateCheck(t, evidence)
	if err == nil {
		t.Fatalf("check unexpectedly passed:\n%s", output)
	}
	if !strings.Contains(output, "Tier 2 PASS from weak evidence") {
		t.Fatalf("output = %q, want weak PASS failure", output)
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

func validTierGateEvidence(tierStatus, tierReason string) string {
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
| Tier 2 prod-like Cilium | ` + tierStatus + ` | ` + tierReason + ` | command ` + "`make tier2-e2e-up`" + `, GitHub Actions workflow ` + "`prodlike-e2e.yml`" + `, artifact ` + "`artifacts/tier2-e2e.log`" + `, screenshot and unlinked output rejected, stale local-only evidence rejected. |
| Tier 3 evidence bundle | CONCERNS | current but dedicated workflow unavailable | command ` + "`make tier3-evidence-up STRESS_SCENARIO=throughput`" + `, GitHub Actions workflow ` + "`evidence-gate.yml`" + `, artifact ` + "`artifacts/tier3-bundle-path.txt`" + `, bundle ` + "`manifest.json`" + `, ` + "`gates.json`" + `, ` + "`privacy-scan.json`" + `. |

## Retention

GitHub Actions artifact retention must be tracked. Local-only artifacts are not
final release proof unless copied to durable reviewable storage.
`
}
