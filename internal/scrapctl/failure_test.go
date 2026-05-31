package scrapctl

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
)

func TestCheckHelpersReportFailureBranches(t *testing.T) {
	ctx := context.Background()
	errRunner := &fakeRunner{run: func(string, ...string) (string, error) {
		return "", errCommandFailed
	}}
	if got := commandOK(ctx, errRunner, 0, "cmd", "bad"); got.Status != "fail" {
		t.Fatalf("commandOK status = %s, want fail", got.Status)
	}
	if got := expectOutput(ctx, errRunner, 0, "expect", "want", "bad"); got.Status != "fail" {
		t.Fatalf("expectOutput command error status = %s, want fail", got.Status)
	}
	if got := containsOutput(ctx, errRunner, 0, "contains", "want", "bad"); got.Status != "fail" {
		t.Fatalf("containsOutput command error status = %s, want fail", got.Status)
	}
	if got := nonEmptyOutput(ctx, errRunner, 0, "nonempty", "bad"); got.Status != "fail" {
		t.Fatalf("nonEmptyOutput command error status = %s, want fail", got.Status)
	}
	if got := outOrErr("", errors.New("boom")); got != "boom" {
		t.Fatalf("outOrErr = %q, want boom", got)
	}

	okRunner := &fakeRunner{run: func(string, ...string) (string, error) {
		return "wrong\n", nil
	}}
	for name, check := range map[string]Check{
		"absent":   commandAbsent(ctx, okRunner, 0, "absent", "kubectl"),
		"expect":   expectOutput(ctx, okRunner, 0, "expect", "want", "kubectl"),
		"contains": containsOutput(ctx, okRunner, 0, "contains", "want", "kubectl"),
	} {
		if check.Status != "fail" {
			t.Fatalf("%s status = %s, want fail", name, check.Status)
		}
	}

	emptyRunner := &fakeRunner{run: func(string, ...string) (string, error) {
		return " \n", nil
	}}
	if got := nonEmptyOutput(ctx, emptyRunner, 0, "nonempty", "kubectl"); got.Status != "fail" {
		t.Fatalf("nonEmptyOutput empty status = %s, want fail", got.Status)
	}
}

func TestDoctorRuntimeFailureBranches(t *testing.T) {
	ctx := context.Background()
	oldKernelRunner := &fakeRunner{run: func(name string, _ ...string) (string, error) {
		if name == "uname" {
			return "5.10.0\n", nil
		}
		return "", nil
	}}
	if got := kernelCheck(ctx, commonOptions{}, Deps{Runner: oldKernelRunner}); got.Status != "fail" {
		t.Fatalf("kernelCheck status = %s, want fail", got.Status)
	}

	failingDial := func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("refused")
	}
	if got := dialCheck(ctx, commonOptions{}, Deps{DialContext: failingDial}, "nodeport", "127.0.0.1:1"); got.Status != "fail" {
		t.Fatalf("dialCheck status = %s, want fail", got.Status)
	}
}

func TestKindNodeCgroupFailureBranches(t *testing.T) {
	ctx := context.Background()
	for name, runner := range map[string]*fakeRunner{
		"docker_ps_error": {run: func(string, ...string) (string, error) {
			return "", errCommandFailed
		}},
		"no_nodes": {run: func(string, ...string) (string, error) {
			return "", nil
		}},
		"host_namespace_error": {run: func(name string, _ ...string) (string, error) {
			if name == "docker" {
				return "node-0\n", nil
			}
			return "", errCommandFailed
		}},
		"node_exec_error": {run: func(name string, args ...string) (string, error) {
			joined := strings.Join(args, " ")
			if name == "docker" && strings.HasPrefix(joined, "ps ") {
				return "node-0\n", nil
			}
			if name == "ls" {
				return "host\n", nil
			}
			return "", errCommandFailed
		}},
		"shared_namespace": {run: func(name string, args ...string) (string, error) {
			if name == "docker" && strings.HasPrefix(strings.Join(args, " "), "ps ") {
				return "node-0\n", nil
			}
			return "same\n", nil
		}},
	} {
		checks := kindNodeCgroupChecks(ctx, commonOptions{}, Deps{Runner: runner})
		if !reportFailed(checks) {
			t.Fatalf("%s did not report failure: %+v", name, checks)
		}
	}
}

func TestHTTPFailureBranches(t *testing.T) {
	opts := commonOptions{adminURL: defaultAdminURL, metricsURL: defaultMetricsURL}

	warnHealth := healthClient(`{"status":"warn","upload_pressure":"ok","upload_pressure_level":0,"upload_pending_bytes":0,"upload_pending_blocks":0}`)
	if _, got := fetchHealthCheck(context.Background(), opts, Deps{HTTPClient: warnHealth}); got.Status != "fail" {
		t.Fatalf("fetchHealthCheck status = %s, want fail", got.Status)
	}

	for name, client := range map[string]*http.Client{
		"transport": {Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("offline")
		})},
		"status": responseClient(http.StatusInternalServerError, "boom"),
		"decode": responseClient(http.StatusOK, "{"),
	} {
		if _, err := fetchHealth(context.Background(), opts, Deps{HTTPClient: client}); err == nil {
			t.Fatalf("fetchHealth %s should fail", name)
		}
	}

	for name, client := range map[string]*http.Client{
		"transport": {Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("offline")
		})},
		"status": responseClient(http.StatusInternalServerError, "boom"),
		"read": {Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       errBody{},
				Header:     make(http.Header),
				Request:    req,
			}, nil
		})},
	} {
		if err := Run([]string{"leader", "--output=json"}, io.Discard, io.Discard, Deps{HTTPClient: client}); err == nil {
			t.Fatalf("leader %s should fail", name)
		}
	}
}

func TestOutputWriterFailures(t *testing.T) {
	if err := writeDoctorText(errWriter{}, DoctorReport{Status: "ok"}); err == nil {
		t.Fatal("writeDoctorText status write should fail")
	}
	if err := writeDoctorText(&failAfterWriter{}, DoctorReport{
		Status: "ok",
		Checks: []Check{{Name: "a", Status: "ok"}},
	}); err == nil {
		t.Fatal("writeDoctorText check write should fail")
	}
	if err := writeByFormat(errWriter{}, "text", Health{}); err == nil {
		t.Fatal("writeByFormat text write should fail")
	}
	if err := writeJSON(errWriter{}, Health{}); err == nil {
		t.Fatal("writeJSON should fail")
	}
}

func responseClient(status int, body string) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}
}

type errWriter struct{}

func (errWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

type failAfterWriter struct {
	writes int
}

func (w *failAfterWriter) Write(p []byte) (int, error) {
	if w.writes == 0 {
		w.writes++
		return len(p), nil
	}
	return 0, errors.New("write failed")
}

type errBody struct{}

func (errBody) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}

func (errBody) Close() error {
	return nil
}
