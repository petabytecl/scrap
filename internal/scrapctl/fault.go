package scrapctl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBackendDeployment = "localstack"
	defaultRestoreReplicas   = 1
	defaultDebugImage        = "busybox:1.36"
	defaultCorruptOffset     = 72
	defaultCorruptByte       = "Z"
	defaultRolloutArg        = "--timeout=180s"
	faultCommandParts        = 2
	faultPollInterval        = 100 * time.Millisecond
	raftIDOrdinalOffset      = 1
	defaultStatefulSet       = "scrapd"
)

type operationReport struct {
	Status      string `json:"status"`
	Action      string `json:"action"`
	CellID      string `json:"cell_id,omitempty"`
	Environment string `json:"environment,omitempty"`
	RunID       string `json:"run_id,omitempty"`
	Marker      string `json:"marker,omitempty"`
	Profile     string `json:"profile,omitempty"`
	Path        string `json:"path,omitempty"`
}

type safetyOptions struct {
	cellID      string
	environment string
	confirm     string
	runID       string
}

type faultOptions struct {
	common          commonOptions
	safety          safetyOptions
	namespaceSet    bool
	contextSet      bool
	kubeconfigSet   bool
	deployment      string
	restoreReplicas int
	pod             string
	statefulset     string
	transactionID   string
	blockID         uint64
	docCount        uint
	completed       bool
	offset          int
	byteValue       string
	debugImage      string
}

func runFault(args []string, stdout io.Writer, deps Deps) error {
	if len(args) < faultCommandParts {
		return errors.New("usage: scrapctl fault <backend|leader|projection|block> <action>")
	}
	target, action := args[0], args[1]
	rest := args[2:]
	switch target + " " + action {
	case "backend break", "backend restore":
		return runFaultBackend(action, rest, stdout, deps)
	case "leader delete":
		return runFaultLeaderDelete(rest, stdout, deps)
	case "projection inject":
		return runFaultProjectionInject(rest, stdout, deps)
	case "block corrupt":
		return runFaultBlockCorrupt(rest, stdout, deps)
	default:
		return fmt.Errorf("unsupported fault command %q %q", target, action)
	}
}

func runFaultBackend(action string, args []string, stdout io.Writer, deps Deps) error {
	opts, err := parseFaultOptions("fault backend "+action, args, func(fs *flag.FlagSet, opts *faultOptions) {
		fs.StringVar(&opts.deployment, "deployment", defaultBackendDeployment, "Backend deployment name")
		fs.IntVar(&opts.restoreReplicas, "restore-replicas", defaultRestoreReplicas, "Backend replicas for restore")
	})
	if err != nil {
		return err
	}
	if err := validateFaultTarget(opts); err != nil {
		return err
	}
	ctx := context.Background()
	replicas := 0
	if action == "restore" {
		replicas = opts.restoreReplicas
	}
	if err := kubectlRun(ctx, deps.Runner, opts.common, opts.common.timeout, "scale", "deployment/"+opts.deployment, fmt.Sprintf("--replicas=%d", replicas)); err != nil {
		return err
	}
	if action == "restore" {
		if err := kubectlRun(ctx, deps.Runner, opts.common, defaultRolloutTimeout+commandTimeout(opts.common.timeout), "rollout", "status", "deployment/"+opts.deployment, defaultRolloutArg); err != nil {
			return err
		}
		if err := kubectlRun(ctx, deps.Runner, opts.common, 2*time.Minute+commandTimeout(opts.common.timeout), "wait", "--for=condition=Ready", "pod", "-l", "app="+opts.deployment, "--timeout=120s"); err != nil {
			return err
		}
	} else if err := waitDeploymentReadyReplicas(ctx, deps.Runner, opts.common, opts.deployment, 0); err != nil {
		return err
	}
	return writeOperationReport(stdout, opts.common.output, opts.safety, "fault.backend."+action, operationReport{Status: "ok"})
}

func runFaultLeaderDelete(args []string, stdout io.Writer, deps Deps) error {
	opts, err := parseFaultOptions("fault leader delete", args, func(fs *flag.FlagSet, opts *faultOptions) {
		fs.StringVar(&opts.pod, "pod", "", "Leader pod name override")
		fs.StringVar(&opts.statefulset, "statefulset", defaultStatefulSet, "StatefulSet name used to derive the leader pod")
	})
	if err != nil {
		return err
	}
	if err := validateFaultTarget(opts); err != nil {
		return err
	}
	if opts.statefulset == "" {
		return errors.New("statefulset is required")
	}
	pod := opts.pod
	if pod == "" {
		pod, err = currentLeaderPod(context.Background(), deps, opts.common, opts.statefulset)
		if err != nil {
			return err
		}
	}
	ctx := context.Background()
	if err := kubectlRun(ctx, deps.Runner, opts.common, opts.common.timeout, "delete", "pod", pod, "--grace-period=0", "--force", "--wait=false"); err != nil {
		return err
	}
	if err := kubectlRun(ctx, deps.Runner, opts.common, defaultRolloutTimeout+commandTimeout(opts.common.timeout), "rollout", "status", "statefulset/"+opts.statefulset, defaultRolloutArg); err != nil {
		return err
	}
	return writeOperationReport(stdout, opts.common.output, opts.safety, "fault.leader.delete", operationReport{Status: "ok"})
}

func currentLeaderPod(ctx context.Context, deps Deps, opts commonOptions, statefulset string) (string, error) {
	leader, err := fetchLeaderStatus(ctx, opts, deps)
	if err != nil {
		return "", fmt.Errorf("resolve leader pod: %w", err)
	}
	if leader.LeaderID < raftIDOrdinalOffset {
		return "", fmt.Errorf("leader_id %d cannot map to StatefulSet pod", leader.LeaderID)
	}
	ordinal := leader.LeaderID - raftIDOrdinalOffset
	return fmt.Sprintf("%s-%d", statefulset, ordinal), nil
}

func runFaultProjectionInject(args []string, stdout io.Writer, deps Deps) error {
	opts, err := parseFaultOptions("fault projection inject", args, func(fs *flag.FlagSet, opts *faultOptions) {
		fs.StringVar(&opts.transactionID, "transaction-id", "", "Transaction ID to inject into the Pebble Projection")
		fs.Uint64Var(&opts.blockID, "block-id", 0, "Block ID to inject")
		fs.UintVar(&opts.docCount, "doc-count", 0, "Document count to inject")
		fs.BoolVar(&opts.completed, "completed", false, "Whether the injected projection entry is completed")
	})
	if err != nil {
		return err
	}
	if err := validateFaultTarget(opts); err != nil {
		return err
	}
	if opts.transactionID == "" || opts.blockID == 0 || opts.docCount == 0 {
		return errors.New("transaction-id, block-id, and doc-count are required")
	}
	body, err := json.Marshal(map[string]any{
		"transaction_id": opts.transactionID,
		"block_id":       opts.blockID,
		"doc_count":      opts.docCount,
		"completed":      opts.completed,
	})
	if err != nil {
		return fmt.Errorf("marshal projection hook request: %w", err)
	}
	if err := postJSON(context.Background(), deps.HTTPClient, opts.common, "/test-hooks/projection-key", body); err != nil {
		return err
	}
	return writeOperationReport(stdout, opts.common.output, opts.safety, "fault.projection.inject", operationReport{Status: "ok"})
}

func runFaultBlockCorrupt(args []string, stdout io.Writer, deps Deps) error {
	opts, err := parseFaultOptions("fault block corrupt", args, func(fs *flag.FlagSet, opts *faultOptions) {
		fs.StringVar(&opts.pod, "pod", "", "Pod containing the Block to corrupt")
		fs.IntVar(&opts.offset, "offset", defaultCorruptOffset, "Byte offset to overwrite in the first Block")
		fs.StringVar(&opts.byteValue, "byte", defaultCorruptByte, "Single byte to write")
		fs.StringVar(&opts.debugImage, "debug-image", defaultDebugImage, "kubectl debug image")
	})
	if err != nil {
		return err
	}
	if err := validateFaultTarget(opts); err != nil {
		return err
	}
	if opts.pod == "" {
		return errors.New("pod is required")
	}
	if len(opts.byteValue) != 1 {
		return errors.New("byte must be a single character")
	}
	script := fmt.Sprintf(`set -eu
blk=$(ls /proc/1/root/data/blocks/*.blk | sort | head -n 1)
printf %q | dd of="$blk" bs=1 seek=%d conv=notrunc
`, opts.byteValue, opts.offset)
	if err := kubectlRun(context.Background(), deps.Runner, opts.common, opts.common.timeout, "debug", "pod/"+opts.pod, "--target=scrapd", "--image="+opts.debugImage, "--share-processes", "--", "sh", "-c", script); err != nil {
		return err
	}
	return writeOperationReport(stdout, opts.common.output, opts.safety, "fault.block.corrupt", operationReport{Status: "ok"})
}

func parseFaultOptions(name string, args []string, configure func(*flag.FlagSet, *faultOptions)) (faultOptions, error) {
	opts := faultOptions{}
	fs := newFlagSet(name, &opts.common)
	addSafetyFlags(fs, &opts.safety)
	if configure != nil {
		configure(fs, &opts)
	}
	if err := fs.Parse(args); err != nil {
		return faultOptions{}, fmt.Errorf("parse flags: %w", err)
	}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "namespace":
			opts.namespaceSet = true
		case "context":
			opts.contextSet = true
		case "kubeconfig":
			opts.kubeconfigSet = true
		}
	})
	if err := validateCommon(opts.common); err != nil {
		return faultOptions{}, err
	}
	return opts, nil
}

func addSafetyFlags(fs *flag.FlagSet, opts *safetyOptions) {
	fs.StringVar(&opts.cellID, "cell-id", "", "Target Cell ID")
	fs.StringVar(&opts.environment, "environment", "", "Target Cell environment: local, dev, test, prodlike, or evidence")
	fs.StringVar(&opts.confirm, "confirm", "", "Required confirmation; must match --cell-id")
	fs.StringVar(&opts.runID, "run-id", "", "Evidence run ID for audit output")
}

func validateSafety(opts safetyOptions) error {
	if opts.cellID == "" {
		return errors.New("cell-id is required")
	}
	if opts.confirm != opts.cellID {
		return errors.New("confirm must match cell-id")
	}
	switch strings.ToLower(opts.environment) {
	case "local", "dev", "test", "prodlike", "e2e", "evidence":
		return nil
	case "":
		return errors.New("environment is required")
	default:
		return fmt.Errorf("environment %q is not allowed for fault commands", opts.environment)
	}
}

func validateFaultTarget(opts faultOptions) error {
	if err := validateSafety(opts.safety); err != nil {
		return err
	}
	if !opts.namespaceSet {
		return errors.New("namespace must be set explicitly for fault commands")
	}
	if opts.common.kubeContext == "" && opts.common.kubeconfig == "" {
		return errors.New("context or kubeconfig must be set explicitly for fault commands")
	}
	if opts.common.kubeContext != "" && !opts.contextSet {
		return errors.New("context must be set explicitly for fault commands")
	}
	if opts.common.kubeconfig != "" && !opts.kubeconfigSet {
		return errors.New("kubeconfig must be set explicitly for fault commands")
	}
	return nil
}

func writeOperationReport(stdout io.Writer, output string, safety safetyOptions, action string, report operationReport) error {
	report.Action = action
	report.CellID = safety.cellID
	report.Environment = safety.environment
	report.RunID = safety.runID
	if report.RunID == "" {
		report.RunID = "scrapctl-" + time.Now().UTC().Format("20060102T150405Z")
	}
	return writeByFormat(stdout, output, report)
}

func kubectlRun(ctx context.Context, runner Runner, opts commonOptions, timeout time.Duration, args ...string) error {
	cctx, cancel := commandContext(ctx, timeout)
	defer cancel()
	out, err := runner.Run(cctx, opts.kubectl, kubectlArgs(opts, append([]string{"-n", opts.namespace}, args...)...)...)
	if err != nil {
		return fmt.Errorf("kubectl %s: %s", strings.Join(args, " "), strings.TrimSpace(outOrErr(out, err)))
	}
	return nil
}

func waitDeploymentReadyReplicas(ctx context.Context, runner Runner, opts commonOptions, deployment string, want int) error {
	deadline := time.Now().Add(commandTimeout(opts.timeout))
	for {
		cctx, cancel := commandContext(ctx, opts.timeout)
		out, err := runner.Run(cctx, opts.kubectl, kubectlArgs(opts, "-n", opts.namespace, "get", "deployment", deployment, "-o", "jsonpath={.status.readyReplicas}")...)
		cancel()
		if err != nil {
			return fmt.Errorf("get deployment ready replicas: %s", strings.TrimSpace(outOrErr(out, err)))
		}
		ready := 0
		if trimmed := strings.TrimSpace(out); trimmed != "" {
			parsed, err := strconv.Atoi(trimmed)
			if err != nil {
				return fmt.Errorf("parse ready replicas %q: %w", trimmed, err)
			}
			ready = parsed
		}
		if ready == want {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("deployment %s ready replicas = %d, want %d", deployment, ready, want)
		}
		time.Sleep(faultPollInterval)
	}
}

func postJSON(ctx context.Context, client *http.Client, opts commonOptions, path string, body []byte) error {
	cctx, cancel := commandContext(ctx, opts.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, strings.TrimRight(opts.adminURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("POST %s status: %d: %s", path, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}
