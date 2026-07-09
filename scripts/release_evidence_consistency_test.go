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

func TestReleaseEvidenceConsistencyRejectsContradiction(t *testing.T) {
	dir := t.TempDir()
	closure := filepath.Join(dir, "closure.md")
	matrix := filepath.Join(dir, "matrix.md")
	tier := filepath.Join(dir, "tier.md")
	writeFile(t, closure, "Final gate status: PASS\n")
	writeFile(t, matrix, "| Final SCRAP release gate | FAIL | reason |\n")
	writeFile(t, tier, "Release gate status: FAIL\n")

	output, err := runReleaseEvidenceConsistency(t, closure, matrix, tier)
	if err == nil {
		t.Fatalf("expected contradiction failure, got success:\n%s", output)
	}
	if !strings.Contains(output, "closure PASS contradicts matrix/tier FAIL") {
		t.Fatalf("output = %q, want contradiction message", output)
	}
}

func TestReleaseEvidenceConsistencyAcceptsAlignedFail(t *testing.T) {
	dir := t.TempDir()
	closure := filepath.Join(dir, "closure.md")
	matrix := filepath.Join(dir, "matrix.md")
	tier := filepath.Join(dir, "tier.md")
	writeFile(t, closure, "Final gate status: FAIL\nThermo-nuclear findings H-01 open.\n")
	writeFile(t, matrix, "| Final SCRAP release gate | FAIL | reason |\n")
	writeFile(t, tier, "Release gate status: FAIL\n")

	output, err := runReleaseEvidenceConsistency(t, closure, matrix, tier)
	if err != nil {
		t.Fatalf("aligned FAIL should pass: %v\n%s", err, output)
	}
}

func runReleaseEvidenceConsistency(t *testing.T, closure, matrix, tier string) (string, error) {
	t.Helper()
	repoRoot := repoRoot(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	//nolint:gosec // The test executes this repo's validation script against test-owned files.
	cmd := exec.CommandContext(ctx, "bash", filepath.Join(repoRoot, "scripts/check-release-evidence-consistency.sh"))
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"CLOSURE_EVIDENCE="+closure,
		"RELEASE_EVIDENCE_MATRIX="+matrix,
		"TIER_GATES_EVIDENCE="+tier,
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}
