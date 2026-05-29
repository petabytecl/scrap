package scripts_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type evidenceGate struct {
	Pass   bool `json:"pass"`
	Checks []struct {
		Name   string `json:"name"`
		Pass   bool   `json:"pass"`
		Reason string `json:"reason"`
	} `json:"checks"`
}

func TestEvidenceBundleRequiresCurrentRunTelemetry(t *testing.T) {
	gate := runEvidenceBundle(t, "")

	if !gate.Pass {
		t.Fatalf("expected gate to pass with all telemetry present: %+v", gate.Checks)
	}
	assertCheck(t, gate, "metrics_captured", true)
	assertCheck(t, gate, "traces_captured", true)
	assertCheck(t, gate, "logs_captured", true)
	assertCheck(t, gate, "cpu_profile_captured", true)
	assertCheck(t, gate, "heap_profile_captured", true)
}

func TestEvidenceBundleFailsWhenTraceEvidenceIsMissing(t *testing.T) {
	gate := runEvidenceBundle(t, "trace")

	if gate.Pass {
		t.Fatalf("expected gate to fail when trace evidence is missing")
	}
	assertCheck(t, gate, "traces_captured", false)
}

func TestEvidenceBundleFailsWhenLogEvidenceIsMissing(t *testing.T) {
	gate := runEvidenceBundle(t, "log")

	if gate.Pass {
		t.Fatalf("expected gate to fail when log evidence is missing")
	}
	assertCheck(t, gate, "logs_captured", false)
}

func TestEvidenceBundleFailsWhenProfileEvidenceIsMissing(t *testing.T) {
	gate := runEvidenceBundle(t, "profile")

	if gate.Pass {
		t.Fatalf("expected gate to fail when profile evidence is missing")
	}
	assertCheck(t, gate, "cpu_profile_captured", false)
	assertCheck(t, gate, "heap_profile_captured", false)
}

func runEvidenceBundle(t *testing.T, missing string) evidenceGate {
	t.Helper()

	repoRoot := repoRoot(t)
	bundleDir := t.TempDir()
	fakeBin := t.TempDir()
	writeFakeCommands(t, fakeBin)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	//nolint:gosec // The test executes this repo's script with a fake PATH in a temp workspace.
	cmd := exec.CommandContext(ctx, "bash", filepath.Join(repoRoot, "scripts/evidence-bundle.sh"), "throughput")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"BUNDLE_DIR="+bundleDir,
		"EVIDENCE_SETTLE_SECONDS=0",
		"EVIDENCE_FAKE_MISSING="+missing,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("evidence-bundle failed: %v\n%s", err, output)
	}

	bundlePath := lastNonEmptyLine(string(output))
	if !strings.HasPrefix(bundlePath, bundleDir+string(os.PathSeparator)) {
		t.Fatalf("bundle path %q escaped test bundle dir %q", bundlePath, bundleDir)
	}
	//nolint:gosec // bundlePath is validated to live under the test-owned temp directory.
	data, err := os.ReadFile(filepath.Join(bundlePath, "gates.json"))
	if err != nil {
		t.Fatalf("read gates.json: %v\noutput:\n%s", err, output)
	}

	var gate evidenceGate
	if err := json.Unmarshal(data, &gate); err != nil {
		t.Fatalf("parse gates.json: %v\n%s", err, data)
	}
	return gate
}

func repoRoot(t *testing.T) string {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), ".."))
}

func writeFakeCommands(t *testing.T, dir string) {
	t.Helper()

	writeExecutable(t, filepath.Join(dir, "date"), `#!/usr/bin/env bash
if [[ "$*" == "-u +%Y%m%dT%H%M%SZ" ]]; then
  echo 20260529T120000Z
  exit 0
fi
if [[ "$*" == "-u +%s" ]]; then
  echo 1780056000
  exit 0
fi
exec /bin/date "$@"
`)

	writeExecutable(t, filepath.Join(dir, "git"), `#!/usr/bin/env bash
if [[ "$*" == *"rev-parse --short HEAD"* ]]; then
  echo abc1234
  exit 0
fi
if [[ "$*" == *"rev-parse HEAD"* ]]; then
  echo abc1234567890
  exit 0
fi
if [[ "$*" == *"diff --quiet"* ]]; then
  exit 0
fi
echo "unsupported git invocation: $*" >&2
exit 1
`)

	writeExecutable(t, filepath.Join(dir, "kubectl"), `#!/usr/bin/env bash
case "$*" in
  *"current-context"*) echo test-context ;;
  *"jsonpath={.spec.replicas}"*) echo 3 ;;
  *"jsonpath={.spec.template.spec.containers[0].image}"*) echo localhost/scrapd:test ;;
  *) echo "" ;;
esac
`)

	writeExecutable(t, filepath.Join(dir, "go"), `#!/usr/bin/env bash
if [[ "$1" == "run" ]]; then
  cat <<'JSON'
{"scenario":"throughput","total_ops":20,"failed_ops":0}
JSON
  exit 0
fi
echo "unsupported go invocation: $*" >&2
exit 1
`)

	writeExecutable(t, filepath.Join(dir, "curl"), `#!/usr/bin/env bash
args="$*"
missing="${EVIDENCE_FAKE_MISSING:-}"

if [[ "$args" == *"/api/search"* ]]; then
  if [[ "$missing" == "trace" ]]; then
    echo '{"traces":[]}'
  else
    echo '{"traces":[{"traceID":"abc"}]}'
  fi
  exit 0
fi

if [[ "$args" == *"/loki/api/v1/query_range"* ]]; then
  if [[ "$missing" == "log" ]]; then
    echo '{"status":"success","data":{"resultType":"streams","result":[]}}'
  else
    echo '{"status":"success","data":{"resultType":"streams","result":[{"stream":{"service_name":"scrapd"},"values":[["1780056000000000000","scrapd starting"]]}]}}'
  fi
  exit 0
fi

if [[ "$args" == *"/api/v1/query"* ]]; then
  if [[ "$args" == *"scrap_rpc_server_requests_total"* && "$args" != *"increase(scrap_rpc_server_requests_total"* ]]; then
    echo "rpc metric did not use increase()" >&2
    exit 22
  fi
  if [[ "$args" == *"scrap_rpc_server_requests_total"* && "$args" == *"[60s]"* ]]; then
    echo "rpc metric window was expanded before the run start" >&2
    exit 22
  fi
  cat <<'JSON'
{"status":"success","data":{"result":[{"metric":{"rpc_method":"WriteDocument","rpc_grpc_status_code":"0"},"value":[1780056000,"20"]}]}}
JSON
  exit 0
fi

if [[ "$args" == *"/pyroscope/render"* ]]; then
  if [[ "$missing" == "profile" ]]; then
    echo '{"timeline":{"samples":[]}}'
  else
    echo '{"timeline":{"samples":[1]}}'
  fi
  exit 0
fi

echo "unsupported curl invocation: $args" >&2
exit 22
`)
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

func assertCheck(t *testing.T, gate evidenceGate, name string, want bool) {
	t.Helper()

	for _, check := range gate.Checks {
		if check.Name == name {
			if check.Pass != want {
				t.Fatalf("check %s pass=%v, want %v; reason=%s", name, check.Pass, want, check.Reason)
			}
			return
		}
	}
	t.Fatalf("check %s not found in %+v", name, gate.Checks)
}

func lastNonEmptyLine(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 {
		return ""
	}
	return strings.TrimSpace(lines[len(lines)-1])
}
