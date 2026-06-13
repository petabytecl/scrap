package scrapctl

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFaultBackendRequiresSafetyGate(t *testing.T) {
	runner := &fakeRunner{}

	err := Run([]string{"fault", "backend", "break", "--cell-id=prod", "--environment=production", "--confirm=prod"}, io.Discard, io.Discard, Deps{
		Runner: runner,
	})
	if err == nil {
		t.Fatal("fault command should reject production environment")
	}
	if len(runner.commands) != 0 {
		t.Fatalf("unsafe fault ran commands: %v", runner.commands)
	}
}

func TestFaultBackendRequiresExplicitKubernetesTarget(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "namespace",
			args: []string{
				"fault", "backend", "break",
				"--context=kind-scrap",
				"--cell-id=kind-dev",
				"--environment=dev",
				"--confirm=kind-dev",
			},
			want: "namespace",
		},
		{
			name: "context",
			args: []string{
				"fault", "backend", "break",
				"--namespace=scrap",
				"--cell-id=kind-dev",
				"--environment=dev",
				"--confirm=kind-dev",
			},
			want: "context or kubeconfig",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeRunner{}

			err := Run(tc.args, io.Discard, io.Discard, Deps{Runner: runner})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			if len(runner.commands) != 0 {
				t.Fatalf("unsafe fault ran commands: %v", runner.commands)
			}
		})
	}
}

func TestFaultRejectsUnsupportedCommand(t *testing.T) {
	err := Run([]string{"fault", "backend", "explode"}, io.Discard, io.Discard, Deps{})
	if err == nil || !strings.Contains(err.Error(), `unsupported fault command "backend" "explode"`) {
		t.Fatalf("error = %v, want unsupported fault command", err)
	}
}

func TestFaultBackendBreakAndRestoreUseSafetyGate(t *testing.T) {
	runner := &fakeRunner{run: func(name string, args ...string) (string, error) {
		if name != "kubectl" {
			return "", nil
		}
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "jsonpath={.status.readyReplicas}") {
			return "0\n", nil
		}
		return "ok\n", nil
	}}

	var out bytes.Buffer
	err := Run([]string{
		"fault", "backend", "break",
		"--namespace=scrap",
		"--context=kind-scrap-prodlike",
		"--cell-id=kind-prodlike",
		"--environment=prodlike",
		"--confirm=kind-prodlike",
		"--run-id=e2e-123",
		"--output=json",
	}, &out, io.Discard, Deps{Runner: runner})
	if err != nil {
		t.Fatalf("backend break: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), `"action":"fault.backend.break"`) ||
		!strings.Contains(out.String(), `"run_id":"e2e-123"`) {
		t.Fatalf("missing auditable report fields:\n%s", out.String())
	}
	assertCommandContains(t, runner.commands, "kubectl --context kind-scrap-prodlike -n scrap scale deployment/localstack --replicas=0")

	runner.commands = nil
	runner.run = func(name string, args ...string) (string, error) {
		if name != "kubectl" {
			return "", nil
		}
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "jsonpath={.status.readyReplicas}") {
			return "1\n", nil
		}
		return "ok\n", nil
	}
	out.Reset()
	err = Run([]string{
		"fault", "backend", "restore",
		"--namespace=scrap",
		"--context=kind-scrap-prodlike",
		"--cell-id=kind-prodlike",
		"--environment=prodlike",
		"--confirm=kind-prodlike",
		"--run-id=e2e-124",
		"--output=json",
	}, &out, io.Discard, Deps{Runner: runner})
	if err != nil {
		t.Fatalf("backend restore: %v\n%s", err, out.String())
	}
	assertCommandContains(t, runner.commands, "kubectl --context kind-scrap-prodlike -n scrap scale deployment/localstack --replicas=1")
	assertCommandContains(t, runner.commands, "kubectl --context kind-scrap-prodlike -n scrap rollout status deployment/localstack")
}

func TestFaultBackendReportsKubectlFailure(t *testing.T) {
	runner := &fakeRunner{run: func(_ string, _ ...string) (string, error) {
		return "scale failed", errors.New("failed")
	}}

	err := Run([]string{
		"fault", "backend", "break",
		"--namespace=scrap",
		"--context=kind-scrap-prodlike",
		"--cell-id=kind-prodlike",
		"--environment=prodlike",
		"--confirm=kind-prodlike",
	}, io.Discard, io.Discard, Deps{Runner: runner})
	if err == nil || !strings.Contains(err.Error(), "kubectl scale") {
		t.Fatalf("error = %v, want kubectl scale failure", err)
	}
}

func TestFaultLeaderDeleteUsesSafetyGate(t *testing.T) {
	runner := &fakeRunner{run: func(_ string, args ...string) (string, error) {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "{.metadata.uid} {.status.phase}") {
			return "new-uid Running true", nil
		}
		if strings.Contains(joined, "jsonpath={.metadata.uid}") {
			return "old-uid", nil
		}
		return "ok", nil
	}}
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/metrics" {
			t.Fatalf("path = %s", req.URL.Path)
		}
		body := `scrap_raft_is_leader{service_name="scrapd",instance="scrapd-2"} 1
scrap_raft_leader_id{service_name="scrapd",instance="scrapd-2"} 3
`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}

	var out bytes.Buffer
	err := Run([]string{
		"fault", "leader", "delete",
		"--namespace=scrap",
		"--context=kind-scrap-prodlike",
		"--cell-id=kind-prodlike",
		"--environment=prodlike",
		"--confirm=kind-prodlike",
		"--metrics-url=http://admin.local/metrics",
		"--output=json",
	}, &out, io.Discard, Deps{Runner: runner, HTTPClient: client})
	if err != nil {
		t.Fatalf("leader delete: %v\n%s", err, out.String())
	}
	assertCommandContains(t, runner.commands, "kubectl --context kind-scrap-prodlike -n scrap delete pod scrapd-2")
	assertCommandContains(t, runner.commands, "kubectl --context kind-scrap-prodlike -n scrap get pod scrapd-2 -o jsonpath={.metadata.uid}")
	assertCommandContains(t, runner.commands, "jsonpath={.metadata.uid} {.status.phase} {.status.containerStatuses[0].ready}")
	assertCommandContains(t, runner.commands, "kubectl --context kind-scrap-prodlike -n scrap rollout status statefulset/scrapd")
	if !strings.Contains(out.String(), `"action":"fault.leader.delete"`) {
		t.Fatalf("unexpected output: %s", out.String())
	}
}

func TestFaultLeaderDeleteRejectsInvalidLeaderID(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := `scrap_raft_is_leader{service_name="scrapd",instance="scrapd-0"} 0
scrap_raft_leader_id{service_name="scrapd",instance="scrapd-0"} 0
`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}

	err := Run([]string{
		"fault", "leader", "delete",
		"--namespace=scrap",
		"--context=kind-scrap-prodlike",
		"--cell-id=kind-prodlike",
		"--environment=prodlike",
		"--confirm=kind-prodlike",
		"--metrics-url=http://admin.local/metrics",
	}, io.Discard, io.Discard, Deps{Runner: &fakeRunner{}, HTTPClient: client})
	if err == nil || !strings.Contains(err.Error(), "cannot map") {
		t.Fatalf("error = %v, want invalid leader id", err)
	}
}

func TestFaultProjectionInjectPostsThroughSelectedPod(t *testing.T) {
	runner := &fakeRunner{}

	var out bytes.Buffer
	err := Run([]string{
		"fault", "projection", "inject",
		"--namespace=scrap",
		"--context=kind-scrap-prodlike",
		"--cell-id=kind-prodlike",
		"--environment=prodlike",
		"--confirm=kind-prodlike",
		"--run-id=projection-1",
		"--pod=scrapd-1",
		"--transaction-id=tx-divergent",
		"--block-id=999",
		"--doc-count=1",
		"--output=json",
	}, &out, io.Discard, Deps{Runner: runner})
	if err != nil {
		t.Fatalf("projection inject: %v", err)
	}
	for _, want := range []string{`"transaction_id":"tx-divergent"`, `"block_id":999`, `"doc_count":1`} {
		assertCommandContains(t, runner.commands, want)
	}
	assertCommandContains(t, runner.commands, "kubectl --context kind-scrap-prodlike -n scrap debug pod/scrapd-1")
	assertCommandContains(t, runner.commands, "--attach=true")
	assertCommandContains(t, runner.commands, "--post-data=")
	assertCommandContains(t, runner.commands, "http://127.0.0.1:9100/test-hooks/projection-key")
	if !strings.Contains(out.String(), `"action":"fault.projection.inject"`) {
		t.Fatalf("unexpected output: %s", out.String())
	}
}

func TestFaultProjectionInjectRequiresIdentifiers(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "missing pod",
			args: []string{
				"fault", "projection", "inject",
				"--namespace=scrap",
				"--context=kind-scrap-prodlike",
				"--cell-id=kind-prodlike",
				"--environment=prodlike",
				"--confirm=kind-prodlike",
				"--transaction-id=tx-divergent",
				"--block-id=999",
				"--doc-count=1",
			},
			want: "pod",
		},
		{
			name: "missing identifiers",
			args: []string{
				"fault", "projection", "inject",
				"--namespace=scrap",
				"--context=kind-scrap-prodlike",
				"--cell-id=kind-prodlike",
				"--environment=prodlike",
				"--confirm=kind-prodlike",
				"--pod=scrapd-1",
			},
			want: "transaction-id",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := Run(tc.args, io.Discard, io.Discard, Deps{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestFaultBlockCorruptUsesSafetyGate(t *testing.T) {
	runner := &fakeRunner{}

	var out bytes.Buffer
	err := Run([]string{
		"fault", "block", "corrupt",
		"--namespace=scrap",
		"--context=kind-scrap-prodlike",
		"--cell-id=kind-prodlike",
		"--environment=prodlike",
		"--confirm=kind-prodlike",
		"--pod=scrapd-1",
		"--offset=13",
		"--byte=X",
		"--output=json",
	}, &out, io.Discard, Deps{Runner: runner})
	if err != nil {
		t.Fatalf("block corrupt: %v\n%s", err, out.String())
	}
	assertCommandContains(t, runner.commands, "kubectl --context kind-scrap-prodlike -n scrap debug pod/scrapd-1")
	assertCommandContains(t, runner.commands, "--attach=true")
	assertCommandContains(t, runner.commands, "seek=13")
	if !strings.Contains(out.String(), `"action":"fault.block.corrupt"`) {
		t.Fatalf("unexpected output: %s", out.String())
	}
}

func TestFaultBlockCorruptReportsDebugFailure(t *testing.T) {
	runner := &fakeRunner{run: func(_ string, _ ...string) (string, error) {
		return "dd failed", errors.New("failed")
	}}

	err := Run([]string{
		"fault", "block", "corrupt",
		"--namespace=scrap",
		"--context=kind-scrap-prodlike",
		"--cell-id=kind-prodlike",
		"--environment=prodlike",
		"--confirm=kind-prodlike",
		"--pod=scrapd-1",
	}, io.Discard, io.Discard, Deps{Runner: runner})
	if err == nil || !strings.Contains(err.Error(), "kubectl debug") || !strings.Contains(err.Error(), "dd failed") {
		t.Fatalf("error = %v, want kubectl debug failure", err)
	}
}

func TestFaultBlockCorruptRejectsInvalidByte(t *testing.T) {
	runner := &fakeRunner{}

	err := Run([]string{
		"fault", "block", "corrupt",
		"--namespace=scrap",
		"--context=kind-scrap-prodlike",
		"--cell-id=kind-prodlike",
		"--environment=prodlike",
		"--confirm=kind-prodlike",
		"--pod=scrapd-1",
		"--byte=too-long",
	}, io.Discard, io.Discard, Deps{Runner: runner})
	if err == nil || !strings.Contains(err.Error(), "single character") {
		t.Fatalf("error = %v, want invalid byte", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("invalid block corrupt ran commands: %v", runner.commands)
	}
}

func TestEvidenceRejectsUnsupportedCommand(t *testing.T) {
	err := Run([]string{"evidence", "unknown"}, io.Discard, io.Discard, Deps{})
	if err == nil || !strings.Contains(err.Error(), `unsupported evidence command "unknown"`) {
		t.Fatalf("error = %v, want unsupported evidence command", err)
	}
}

func TestEvidenceLogProbeEmitsMarker(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.Header.Get("X-Scrap-Evidence-Marker"); got != "evidence-123" {
			t.Fatalf("marker = %q", got)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"status":"ok"}`)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}

	var out bytes.Buffer
	err := Run([]string{"evidence", "log-probe", "--marker=evidence-123", "--admin-url=http://admin.local", "--output=json"}, &out, io.Discard, Deps{HTTPClient: client})
	if err != nil {
		t.Fatalf("log probe: %v", err)
	}
	if !strings.Contains(out.String(), `"marker":"evidence-123"`) {
		t.Fatalf("unexpected output: %s", out.String())
	}
}

func TestEvidenceLogProbeRejectsUnhealthyStatus(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Body:       io.NopCloser(strings.NewReader("not ready")),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}

	err := Run([]string{"evidence", "log-probe", "--admin-url=http://admin.local"}, io.Discard, io.Discard, Deps{HTTPClient: client})
	if err == nil || !strings.Contains(err.Error(), "status: 503") {
		t.Fatalf("error = %v, want unhealthy status", err)
	}
}

func TestEvidencePprofWritesProfile(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/debug/pprof/heap" {
			t.Fatalf("path = %s", req.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("profile-bytes")),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}
	outPath := filepath.Join(t.TempDir(), "heap.pb.gz")

	var out bytes.Buffer
	err := Run([]string{"evidence", "pprof", "--profile=heap", "--out=" + outPath, "--admin-url=http://admin.local", "--output=json"}, &out, io.Discard, Deps{HTTPClient: client})
	if err != nil {
		t.Fatalf("pprof: %v", err)
	}
	got, err := os.ReadFile(outPath) //nolint:gosec // Test reads a profile path under t.TempDir().
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}
	if string(got) != "profile-bytes" {
		t.Fatalf("profile bytes = %q", got)
	}
	if !strings.Contains(out.String(), `"profile":"heap"`) {
		t.Fatalf("unexpected output: %s", out.String())
	}
}

func TestEvidencePprofValidatesOptions(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "missing output",
			args: []string{"evidence", "pprof", "--profile=heap"},
			want: "out is required",
		},
		{
			name: "unsupported profile",
			args: []string{"evidence", "pprof", "--profile=unknown", "--out=/tmp/profile.pb.gz"},
			want: "unsupported profile",
		},
		{
			name: "cpu seconds",
			args: []string{"evidence", "pprof", "--profile=cpu", "--seconds=0", "--out=/tmp/profile.pb.gz"},
			want: "seconds must be positive",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := Run(tc.args, io.Discard, io.Discard, Deps{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestPprofPath(t *testing.T) {
	for _, tc := range []struct {
		name    string
		profile string
		seconds int
		want    string
	}{
		{name: "heap", profile: "heap", want: "/debug/pprof/heap"},
		{name: "cpu", profile: "cpu", seconds: 7, want: "/debug/pprof/profile?seconds=7"},
		{name: "trace", profile: "trace", seconds: 9, want: "/debug/pprof/trace?seconds=9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := pprofPath(tc.profile, tc.seconds)
			if err != nil {
				t.Fatalf("pprofPath: %v", err)
			}
			if got != tc.want {
				t.Fatalf("path = %q, want %q", got, tc.want)
			}
		})
	}
}

func assertCommandContains(t *testing.T, commands []string, want string) {
	t.Helper()
	for _, cmd := range commands {
		if strings.Contains(cmd, want) {
			return
		}
	}
	t.Fatalf("missing command containing %q in %v", want, commands)
}
