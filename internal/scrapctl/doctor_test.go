package scrapctl

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	commands []string
	run      func(name string, args ...string) (string, error)
}

func (r *fakeRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	r.commands = append(r.commands, strings.Join(append([]string{name}, args...), " "))
	if r.run == nil {
		return "", nil
	}
	return r.run(name, args...)
}

type deadlineRunner struct {
	commands         []string
	rolloutDeadlines []time.Duration
}

func (r *deadlineRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	joined := strings.Join(append([]string{name}, args...), " ")
	r.commands = append(r.commands, joined)
	if name == "kubectl" && strings.Contains(strings.Join(args, " "), "rollout status") {
		deadline, ok := ctx.Deadline()
		if !ok {
			r.rolloutDeadlines = append(r.rolloutDeadlines, 0)
		} else {
			r.rolloutDeadlines = append(r.rolloutDeadlines, time.Until(deadline))
		}
	}
	return successfulDoctorCommand(name, args...)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestDoctorReportsCiliumKubeProxyReplacementFailure(t *testing.T) {
	runner := successfulDoctorRunner()
	runner.run = func(name string, args ...string) (string, error) {
		if name == "kubectl" && strings.Join(args, " ") == "-n kube-system exec ds/cilium -- cilium-dbg status --verbose" {
			return "KubeProxyReplacement: False\nNodePort: Enabled\n", nil
		}
		return successfulDoctorCommand(name, args...)
	}

	var out bytes.Buffer
	err := Run([]string{"doctor", "--output=json"}, &out, io.Discard, Deps{
		Runner:     runner,
		HTTPClient: healthClient(`{"status":"ok","upload_pressure":"ok","upload_pressure_level":0,"upload_pending_bytes":0,"upload_pending_blocks":0}`),
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			return &fakeConn{}, nil
		},
	})
	if err == nil {
		t.Fatal("doctor should fail when Cilium does not report kube-proxy replacement")
	}
	got := out.String()
	if !strings.Contains(got, `"name":"cilium.kube_proxy_replacement"`) ||
		!strings.Contains(got, `"status":"fail"`) {
		t.Fatalf("missing failed Cilium check in output:\n%s", got)
	}
}

func TestDoctorUsesReadOnlyKubectlOperations(t *testing.T) {
	runner := successfulDoctorRunner()

	var out bytes.Buffer
	err := Run([]string{"doctor", "--output=json"}, &out, io.Discard, Deps{
		Runner:     runner,
		HTTPClient: healthClient(`{"status":"ok","upload_pressure":"ok","upload_pressure_level":0,"upload_pending_bytes":0,"upload_pending_blocks":0}`),
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			return &fakeConn{}, nil
		},
	})
	if err != nil {
		t.Fatalf("doctor: %v\n%s", err, out.String())
	}

	for _, cmd := range runner.commands {
		for _, forbidden := range []string{" run ", " delete ", " apply ", " patch ", " scale "} {
			if strings.Contains(" "+cmd+" ", forbidden) {
				t.Fatalf("doctor used mutating kubectl operation %q in %q", strings.TrimSpace(forbidden), cmd)
			}
		}
	}
}

func TestDoctorFailsWhenAbsenceCheckCannotReachAPIServer(t *testing.T) {
	runner := successfulDoctorRunner()
	runner.run = func(name string, args ...string) (string, error) {
		if name == "kubectl" && strings.Contains(strings.Join(args, " "), "daemonset kube-proxy") {
			return "Unable to connect to the server\n", errCommandFailed
		}
		return successfulDoctorCommand(name, args...)
	}

	var out bytes.Buffer
	err := Run([]string{"doctor", "--output=json"}, &out, io.Discard, Deps{
		Runner:     runner,
		HTTPClient: healthClient(`{"status":"ok","upload_pressure":"ok","upload_pressure_level":0,"upload_pending_bytes":0,"upload_pending_blocks":0}`),
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			return &fakeConn{}, nil
		},
	})
	if err == nil {
		t.Fatal("doctor should fail when kube-proxy absence cannot be verified")
	}
	if !strings.Contains(out.String(), `"name":"kubernetes.no_kube_proxy"`) ||
		!strings.Contains(out.String(), `"status":"fail"`) {
		t.Fatalf("missing failed absence check in output:\n%s", out.String())
	}
}

func TestDoctorPassesKubeconfigAndContextToKubectl(t *testing.T) {
	runner := successfulDoctorRunner()

	var out bytes.Buffer
	err := Run([]string{"doctor", "--output=json", "--context=kind-scrap-prodlike", "--kubeconfig=/tmp/kubeconfig"}, &out, io.Discard, Deps{
		Runner:     runner,
		HTTPClient: healthClient(`{"status":"ok","upload_pressure":"ok","upload_pressure_level":0,"upload_pending_bytes":0,"upload_pending_blocks":0}`),
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			return &fakeConn{}, nil
		},
	})
	if err != nil {
		t.Fatalf("doctor: %v\n%s", err, out.String())
	}

	var sawKubectl bool
	for _, cmd := range runner.commands {
		if !strings.HasPrefix(cmd, "kubectl ") {
			continue
		}
		sawKubectl = true
		if !strings.Contains(cmd, "--context kind-scrap-prodlike") ||
			!strings.Contains(cmd, "--kubeconfig /tmp/kubeconfig") {
			t.Fatalf("kubectl command did not include kubeconfig flags: %q", cmd)
		}
	}
	if !sawKubectl {
		t.Fatal("doctor did not run kubectl")
	}
}

func TestDoctorRolloutChecksUseRolloutTimeout(t *testing.T) {
	runner := &deadlineRunner{}

	var out bytes.Buffer
	err := Run([]string{"doctor", "--output=json", "--rollout-timeout=3m", "--timeout=10s"}, &out, io.Discard, Deps{
		Runner:     runner,
		HTTPClient: healthClient(`{"status":"ok","upload_pressure":"ok","upload_pressure_level":0,"upload_pending_bytes":0,"upload_pending_blocks":0}`),
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			return &fakeConn{}, nil
		},
	})
	if err != nil {
		t.Fatalf("doctor: %v\n%s", err, out.String())
	}

	var rolloutCommands int
	for _, cmd := range runner.commands {
		if !strings.Contains(cmd, "rollout status") {
			continue
		}
		rolloutCommands++
		if !strings.Contains(cmd, "--timeout=3m0s") {
			t.Fatalf("rollout command did not include rollout timeout: %q", cmd)
		}
	}
	if rolloutCommands != 3 {
		t.Fatalf("rollout commands = %d, want 3", rolloutCommands)
	}
	if len(runner.rolloutDeadlines) != 3 {
		t.Fatalf("rollout deadlines = %d, want 3", len(runner.rolloutDeadlines))
	}
	for _, remaining := range runner.rolloutDeadlines {
		if remaining <= 3*time.Minute || remaining > 4*time.Minute {
			t.Fatalf("rollout context deadline = %s, want just above 3m", remaining)
		}
	}
}

func TestDoctorRejectsUnsupportedOutput(t *testing.T) {
	var out bytes.Buffer
	err := Run([]string{"doctor", "--output=xml"}, &out, io.Discard, Deps{})
	if err == nil {
		t.Fatal("doctor should reject unsupported output")
	}
}

func TestDoctorTextOutput(t *testing.T) {
	var out bytes.Buffer
	err := Run([]string{"doctor"}, &out, io.Discard, Deps{
		Runner:     successfulDoctorRunner(),
		HTTPClient: healthClient(`{"status":"ok","upload_pressure":"ok","upload_pressure_level":0,"upload_pending_bytes":0,"upload_pending_blocks":0}`),
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			return &fakeConn{}, nil
		},
	})
	if err != nil {
		t.Fatalf("doctor: %v\n%s", err, out.String())
	}
	got := out.String()
	for _, want := range []string{"status: ok", "host.cgroup_v2: ok", "kind.node_cgroup_namespace: ok"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in output:\n%s", want, got)
		}
	}
}

func TestRunHelpAndUnknownCommand(t *testing.T) {
	var help bytes.Buffer
	if err := Run([]string{"help"}, &help, io.Discard, Deps{}); err != nil {
		t.Fatalf("help: %v", err)
	}
	if !strings.Contains(help.String(), "scrapctl <doctor|status|upload-pressure|peers|leader>") {
		t.Fatalf("unexpected help output: %s", help.String())
	}

	var stderr bytes.Buffer
	err := Run([]string{"unknown"}, io.Discard, &stderr, Deps{})
	if err == nil {
		t.Fatal("unknown command should fail")
	}
	if !strings.Contains(stderr.String(), `unknown command "unknown"`) {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
}

func TestUploadPressurePrintsAdminHealthFields(t *testing.T) {
	var out bytes.Buffer
	err := Run([]string{"upload-pressure", "--output=json"}, &out, io.Discard, Deps{
		HTTPClient: healthClient(`{"status":"ok","upload_pressure":"warn","upload_pressure_level":1,"upload_pending_bytes":2048,"upload_pending_blocks":4}`),
	})
	if err != nil {
		t.Fatalf("upload-pressure: %v", err)
	}
	got := out.String()
	for _, want := range []string{`"upload_pressure":"warn"`, `"upload_pressure_level":1`, `"upload_pending_bytes":2048`, `"upload_pending_blocks":4`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in output:\n%s", want, got)
		}
	}
}

func TestStatusTextOutput(t *testing.T) {
	var out bytes.Buffer
	err := Run([]string{"status"}, &out, io.Discard, Deps{
		HTTPClient: healthClient(`{"status":"ok","upload_pressure":"ok","upload_pressure_level":0,"upload_pending_bytes":0,"upload_pending_blocks":0}`),
	})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out.String(), "UploadPressure:ok") {
		t.Fatalf("unexpected text output:\n%s", out.String())
	}
}

func TestStatusUsesTimeoutForAdminHealth(t *testing.T) {
	var sawDeadline bool
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		_, sawDeadline = req.Context().Deadline()
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"status":"ok","upload_pressure":"ok","upload_pressure_level":0,"upload_pending_bytes":0,"upload_pending_blocks":0}`)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}

	var out bytes.Buffer
	err := Run([]string{"status", "--output=json", "--timeout=250ms"}, &out, io.Discard, Deps{HTTPClient: client})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !sawDeadline {
		t.Fatal("status request did not carry a deadline")
	}
}

func TestLeaderReportsMetrics(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`scrap_raft_is_leader{service_name="scrapd",instance="scrapd-0"} 1
scrap_raft_leader_id{service_name="scrapd",instance="scrapd-0"} 3
`)),
			Header:  make(http.Header),
			Request: req,
		}, nil
	})}

	var out bytes.Buffer
	err := Run([]string{"leader", "--output=json"}, &out, io.Discard, Deps{HTTPClient: client})
	if err != nil {
		t.Fatalf("leader: %v", err)
	}
	got := out.String()
	for _, want := range []string{`"leader_id":3`, `"is_leader":true`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in output:\n%s", want, got)
		}
	}
}

func TestLeaderFailsWhenMetricsMissing(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("scrap_other_metric 1\n")),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}

	err := Run([]string{"leader", "--output=json"}, io.Discard, io.Discard, Deps{HTTPClient: client})
	if err == nil {
		t.Fatal("leader should fail when required metrics are missing")
	}
	if !strings.Contains(err.Error(), "leader metrics missing") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPeersReportsReadyPods(t *testing.T) {
	runner := &fakeRunner{run: func(name string, args ...string) (string, error) {
		if name != "kubectl" {
			return "", errors.New("unexpected command")
		}
		if !strings.Contains(strings.Join(args, " "), "-o json") {
			return "", errors.New("peers should request pod JSON")
		}
		return `{
			"items": [
				{"metadata": {"name": "scrapd-0"}, "status": {"conditions": [{"type": "Ready", "status": "True"}]}},
				{"metadata": {"name": "scrapd-1"}, "status": {}},
				{"metadata": {"name": "scrapd-2"}, "status": {"conditions": [{"type": "Ready", "status": "False"}]}}
			]
		}`, nil
	}}

	var out bytes.Buffer
	err := Run([]string{"peers", "--output=json"}, &out, io.Discard, Deps{Runner: runner})
	if err != nil {
		t.Fatalf("peers: %v", err)
	}
	got := out.String()
	for _, want := range []string{`"name":"scrapd-0"`, `"ready":true`, `"name":"scrapd-2"`, `"ready":false`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in output:\n%s", want, got)
		}
	}
}

func TestParseLeaderMetricsSupportsPrometheusLabels(t *testing.T) {
	status, err := parseLeaderMetrics(`# HELP scrap_raft_is_leader Whether this member is the Raft leader.
scrap_raft_is_leader{service_name="scrapd",instance="scrapd-0"} 1
scrap_raft_leader_id{service_name="scrapd",instance="scrapd-0"} 3
`)
	if err != nil {
		t.Fatalf("parseLeaderMetrics: %v", err)
	}
	if !status.IsLeader || status.LeaderID != 3 {
		t.Fatalf("unexpected leader status: %+v", status)
	}
}

func successfulDoctorRunner() *fakeRunner {
	return &fakeRunner{run: successfulDoctorCommand}
}

func successfulDoctorCommand(name string, args ...string) (string, error) {
	joined := strings.Join(args, " ")
	switch name {
	case "stat":
		return "cgroup2fs\n", nil
	case "uname":
		return "6.14.0\n", nil
	case "ls":
		return "cgroup:[4026531840]\n", nil
	case "docker":
		return successfulDockerCommand(joined)
	case "kubectl":
		return successfulKubectlCommand(joined)
	default:
		return "", nil
	}
}

func successfulDockerCommand(joined string) (string, error) {
	if joined == "info --format {{.CgroupVersion}}" {
		return "2\n", nil
	}
	for _, match := range []struct {
		prefix string
		out    string
	}{
		{prefix: "info --format", out: "name=cgroupns\n"},
		{prefix: "ps ", out: "scrap-prodlike-control-plane\nscrap-prodlike-worker\n"},
		{prefix: "exec ", out: "cgroup:[4026531835]\n"},
	} {
		if strings.HasPrefix(joined, match.prefix) {
			return match.out, nil
		}
	}
	return "", nil
}

func successfulKubectlCommand(joined string) (string, error) {
	for _, match := range kubectlFailureMatches() {
		if strings.Contains(joined, match) {
			return "Error from server (NotFound): daemonsets.apps not found\n", errCommandFailed
		}
	}
	for _, match := range kubectlSuccessMatches() {
		if strings.Contains(joined, match.contains) {
			return match.out, nil
		}
	}
	return "ok\n", nil
}

func kubectlFailureMatches() []string {
	return []string{"daemonset kube-proxy", "daemonset kindnet"}
}

func kubectlSuccessMatches() []struct {
	contains string
	out      string
} {
	return []struct {
		contains string
		out      string
	}{
		{contains: "exec ds/cilium", out: "KubeProxyReplacement: True\nKubeProxyReplacement Details:\n  - NodePort: Enabled (Range: 30000-32767)\n"},
		{contains: "service scrap -o jsonpath={.spec.type}", out: "NodePort\n"},
		{contains: "service scrap-headless -o jsonpath={.spec.clusterIP}", out: "None\n"},
		{contains: "networkpolicy scrapd-admin-restrict -o jsonpath={.spec.podSelector.matchLabels.app}", out: "scrap\n"},
		{contains: "ciliumnetworkpolicy scrapd-admin-host-allow -o jsonpath={.spec.endpointSelector.matchLabels.app}", out: "scrap\n"},
		{contains: "endpointslice", out: "endpointslice.discovery.k8s.io/scrap-headless-abc\n"},
	}
}

func healthClient(body string) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}
}

type fakeConn struct{}

func (*fakeConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (*fakeConn) Write(p []byte) (int, error)      { return len(p), nil }
func (*fakeConn) Close() error                     { return nil }
func (*fakeConn) LocalAddr() net.Addr              { return fakeAddr("local") }
func (*fakeConn) RemoteAddr() net.Addr             { return fakeAddr("remote") }
func (*fakeConn) SetDeadline(time.Time) error      { return nil }
func (*fakeConn) SetReadDeadline(time.Time) error  { return nil }
func (*fakeConn) SetWriteDeadline(time.Time) error { return nil }

type fakeAddr string

func (a fakeAddr) Network() string { return string(a) }
func (a fakeAddr) String() string  { return string(a) }
