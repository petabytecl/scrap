package scripts_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestEvidenceBundleScriptDelegatesToScrapctl(t *testing.T) {
	repoRoot := repoRoot(t)
	tempDir := t.TempDir()
	fakeScrapctl := filepath.Join(tempDir, "scrapctl")
	argsFile := filepath.Join(tempDir, "args.txt")
	envFile := filepath.Join(tempDir, "env.txt")
	writeExecutable(t, fakeScrapctl, `#!/usr/bin/env bash
printf '%s\n' "$*" > "$ARGS_FILE"
printf 'SCRAP_REPO_ROOT=%s\nBUNDLE_DIR=%s\n' "$SCRAP_REPO_ROOT" "$BUNDLE_DIR" > "$ENV_FILE"
printf '%s/evidence/throughput-test\n' "$SCRAP_REPO_ROOT"
`)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	//nolint:gosec // The test executes this repo's script with a fake scrapctl under t.TempDir().
	cmd := exec.CommandContext(ctx, "bash", filepath.Join(repoRoot, "scripts/evidence-bundle.sh"), "throughput")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"SCRAPCTL="+fakeScrapctl,
		"ARGS_FILE="+argsFile,
		"ENV_FILE="+envFile,
		"BUNDLE_DIR="+filepath.Join(tempDir, "evidence"),
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("evidence-bundle wrapper failed: %v\n%s", err, output)
	}
	argsData, err := os.ReadFile(argsFile) //nolint:gosec // argsFile is created under the test-owned temp directory.
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	if got := strings.TrimSpace(string(argsData)); got != "evidence bundle throughput" {
		t.Fatalf("args = %q, want evidence bundle throughput", got)
	}
	envData, err := os.ReadFile(envFile) //nolint:gosec // envFile is created under the test-owned temp directory.
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if !strings.Contains(string(envData), "SCRAP_REPO_ROOT="+repoRoot) {
		t.Fatalf("missing repo root env:\n%s", envData)
	}
	if !strings.Contains(string(envData), "BUNDLE_DIR="+filepath.Join(tempDir, "evidence")) {
		t.Fatalf("missing bundle dir env:\n%s", envData)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), ".."))
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	//nolint:gosec // Fake commands in the test-owned temp PATH must be executable.
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
}
