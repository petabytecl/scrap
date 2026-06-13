package evidencebundle

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	portForwardTimeout      = 5 * time.Second
	portForwardPollInterval = 100 * time.Millisecond
	portForwardMatchGroups  = 2
	kubectlArgCapacity      = 6
)

type systemClock struct{}

func (systemClock) Now() time.Time {
	return time.Now()
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type execCommandRunner struct{}

func (execCommandRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	//nolint:gosec // Operator-selected local commands are required for evidence metadata.
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %v: %w: %s", name, args, err, string(out))
	}
	return string(out), nil
}

type execStressRunner struct{}

func (execStressRunner) RunStress(ctx context.Context, req StressRequest) (StressResult, error) {
	args := []string{
		"run",
		filepath.Join(req.RepoRoot, "test/stress"),
		"-addr=" + req.Addr,
		"-scenario=" + req.Scenario,
		"-workers=" + strconv.Itoa(req.Workers),
		"-duration=" + req.Duration,
		"-doc-size=" + strconv.Itoa(req.DocSizeBytes),
	}
	//nolint:gosec // The evidence runner intentionally executes this repo's stress command.
	cmd := exec.CommandContext(ctx, req.GoCommand, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return StressResult{Stdout: stdout.String(), Stderr: stderr.String()}, err
}

type httpAdminProbe struct {
	client *http.Client
}

func (p httpAdminProbe) Emit(ctx context.Context, req AdminProbeRequest) (AdminProbeResult, error) {
	result, err := p.curlLogProbe(ctx, req.AdminURL, req)
	if err == nil {
		return result, nil
	}
	result, fallbackErr := p.emitViaPortForward(ctx, req)
	if fallbackErr == nil {
		return result, nil
	}
	return AdminProbeResult{Body: []byte(`{"error":"admin evidence log probe failed"}`)}, fmt.Errorf("direct probe: %w; port-forward probe: %w", err, fallbackErr)
}

func (p httpAdminProbe) curlLogProbe(ctx context.Context, baseURL string, req AdminProbeRequest) (AdminProbeResult, error) {
	probeCtx, cancel := context.WithTimeout(ctx, req.ProbeTimeout)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(probeCtx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/healthz", nil)
	if err != nil {
		return AdminProbeResult{}, fmt.Errorf("build health request: %w", err)
	}
	httpReq.Header.Set("X-Scrap-Evidence-Marker", req.Marker)
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return AdminProbeResult{}, fmt.Errorf("GET healthz: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return AdminProbeResult{}, fmt.Errorf("read healthz: %w", readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return AdminProbeResult{Body: body, URL: baseURL}, httpError(resp.StatusCode, string(body))
	}
	return AdminProbeResult{Body: body, URL: baseURL}, nil
}

func (p httpAdminProbe) emitViaPortForward(ctx context.Context, req AdminProbeRequest) (AdminProbeResult, error) {
	logPath := filepath.Join(req.LogDir, "admin-port-forward.log")
	logFile, err := os.Create(logPath) //nolint:gosec // Evidence log file path is generated under the bundle directory.
	if err != nil {
		return AdminProbeResult{}, fmt.Errorf("create port-forward log: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	pfCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	args := kubectlArgs(req, "port-forward", "svc/scrap", ":9100")
	//nolint:gosec // Operator-selected kubectl command is required for local evidence port-forward fallback.
	cmd := exec.CommandContext(pfCtx, req.Kubectl, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		return AdminProbeResult{}, fmt.Errorf("start admin port-forward: %w", err)
	}
	defer func() {
		cancel()
		_ = cmd.Wait()
	}()

	port, err := waitForPortForwardPort(ctx, logPath)
	if err != nil {
		return AdminProbeResult{}, err
	}
	return p.curlLogProbe(ctx, "http://127.0.0.1:"+port, req)
}

func waitForPortForwardPort(ctx context.Context, logPath string) (string, error) {
	deadline := time.NewTimer(portForwardTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(portForwardPollInterval)
	defer ticker.Stop()
	portRE := regexp.MustCompile(`127\.0\.0\.1:([0-9]+)`)
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline.C:
			return "", errors.New("admin port-forward did not report a local port")
		case <-ticker.C:
			data, err := os.ReadFile(logPath) //nolint:gosec // logPath is generated under the evidence bundle directory.
			if err != nil {
				continue
			}
			match := portRE.FindSubmatch(data)
			if len(match) == portForwardMatchGroups {
				return string(match[1]), nil
			}
		}
	}
}

func kubectlArgs(req AdminProbeRequest, args ...string) []string {
	out := make([]string, 0, safeStringSliceCapacity(len(args), kubectlArgCapacity))
	if req.KubeContext != "" {
		out = append(out, "--context", req.KubeContext)
	}
	if req.Kubeconfig != "" {
		out = append(out, "--kubeconfig", req.Kubeconfig)
	}
	out = append(out, "-n", req.Namespace)
	out = append(out, args...)
	return out
}
