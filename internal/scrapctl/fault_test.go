package scrapctl

import (
	"bytes"
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

func TestFaultLeaderDeleteUsesSafetyGate(t *testing.T) {
	runner := &fakeRunner{}
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
	assertCommandContains(t, runner.commands, "kubectl --context kind-scrap-prodlike -n scrap rollout status statefulset/scrapd")
	if !strings.Contains(out.String(), `"action":"fault.leader.delete"`) {
		t.Fatalf("unexpected output: %s", out.String())
	}
}

func TestFaultProjectionInjectPostsAdminHook(t *testing.T) {
	var gotBody string
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", req.Method)
		}
		if req.URL.Path != "/test-hooks/projection-key" {
			t.Fatalf("path = %s", req.URL.Path)
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		gotBody = string(body)
		return &http.Response{
			StatusCode: http.StatusNoContent,
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}

	var out bytes.Buffer
	err := Run([]string{
		"fault", "projection", "inject",
		"--namespace=scrap",
		"--context=kind-scrap-prodlike",
		"--cell-id=kind-prodlike",
		"--environment=prodlike",
		"--confirm=kind-prodlike",
		"--run-id=projection-1",
		"--admin-url=http://admin.local",
		"--transaction-id=tx-divergent",
		"--block-id=999",
		"--doc-count=1",
		"--output=json",
	}, &out, io.Discard, Deps{HTTPClient: client})
	if err != nil {
		t.Fatalf("projection inject: %v", err)
	}
	for _, want := range []string{`"transaction_id":"tx-divergent"`, `"block_id":999`, `"doc_count":1`} {
		if !strings.Contains(gotBody, want) {
			t.Fatalf("request body missing %s: %s", want, gotBody)
		}
	}
	if !strings.Contains(out.String(), `"action":"fault.projection.inject"`) {
		t.Fatalf("unexpected output: %s", out.String())
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
	assertCommandContains(t, runner.commands, "seek=13")
	if !strings.Contains(out.String(), `"action":"fault.block.corrupt"`) {
		t.Fatalf("unexpected output: %s", out.String())
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

func assertCommandContains(t *testing.T, commands []string, want string) {
	t.Helper()
	for _, cmd := range commands {
		if strings.Contains(cmd, want) {
			return
		}
	}
	t.Fatalf("missing command containing %q in %v", want, commands)
}
