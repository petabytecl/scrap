package scrapctl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/petabytecl/scrap/internal/security"
)

const (
	minKernelMajor = 5
	minKernelMinor = 14
)

func runDoctor(args []string, stdout io.Writer, deps Deps) error {
	opts, err := parseCommon("doctor", args, withRolloutTimeoutFlag)
	if err != nil {
		return err
	}
	var checks []Check
	checks = append(checks, hostChecks(context.Background(), opts, deps)...)
	checks = append(checks, kubernetesChecks(context.Background(), opts, deps)...)
	checks = append(checks, nodePortChecks(context.Background(), opts, deps)...)

	health, healthCheck := fetchHealthCheck(context.Background(), opts, deps)
	checks = append(checks, healthCheck)
	if health != nil {
		checks = append(checks, productionReadinessCheck(health))
	}

	status := "ok"
	if reportFailed(checks) {
		status = "fail"
	}
	report := DoctorReport{
		Status: status,
		Checks: checks,
		Health: health,
	}
	if opts.output == "json" {
		err = writeJSON(stdout, report)
	} else {
		err = writeDoctorText(stdout, report)
	}
	if err != nil {
		return err
	}
	if status != "ok" {
		return errors.New("doctor checks failed")
	}
	return nil
}

func productionReadinessCheck(health *Health) Check {
	switch health.ProductionReadyStatus {
	case string(security.ReadinessStatusReady):
		return Check{Name: "production.readiness", Status: "ok"}
	case "":
		return Check{Name: "production.readiness", Status: "fail", Reason: "missing_production_readiness_status"}
	default:
		reason := health.ProductionReadyReason
		if reason == "" {
			reason = "status=" + health.ProductionReadyStatus
		}
		return Check{Name: "production.readiness", Status: "fail", Reason: reason}
	}
}

func hostChecks(ctx context.Context, opts commonOptions, deps Deps) []Check {
	checks := []Check{
		expectOutput(ctx, deps.Runner, opts.timeout, "host.cgroup_v2", "cgroup2fs", "stat", "-fc", "%T", "/sys/fs/cgroup"),
		expectOutput(ctx, deps.Runner, opts.timeout, "docker.cgroup_v2", "2", opts.docker, "info", "--format", "{{.CgroupVersion}}"),
		containsOutput(ctx, deps.Runner, opts.timeout, "docker.cgroup_namespace", "name=cgroupns", opts.docker, "info", "--format", "{{range .SecurityOptions}}{{println .}}{{end}}"),
		kernelCheck(ctx, opts, deps),
	}
	checks = append(checks, kindNodeCgroupChecks(ctx, opts, deps)...)
	return checks
}

func kubernetesChecks(ctx context.Context, opts commonOptions, deps Deps) []Check {
	rolloutTimeout := rolloutCommandTimeout(opts)
	rolloutArg := rolloutTimeoutArg(opts)
	checks := []Check{
		commandOK(ctx, deps.Runner, rolloutTimeout, "cilium.daemonset_ready", opts.kubectl, kubectlArgs(opts, "-n", "kube-system", "rollout", "status", "daemonset/cilium", rolloutArg)...),
		commandOK(ctx, deps.Runner, rolloutTimeout, "cilium.operator_ready", opts.kubectl, kubectlArgs(opts, "-n", "kube-system", "rollout", "status", "deployment/cilium-operator", rolloutArg)...),
		commandOK(ctx, deps.Runner, rolloutTimeout, "coredns.ready", opts.kubectl, kubectlArgs(opts, "-n", "kube-system", "rollout", "status", "deployment/coredns", rolloutArg)...),
		ciliumStatusCheck(ctx, opts, deps, "cilium.kube_proxy_replacement", "KubeProxyReplacement:", "True"),
		ciliumStatusCheck(ctx, opts, deps, "cilium.nodeport", "NodePort:", "Enabled"),
		commandAbsent(ctx, deps.Runner, opts.timeout, "kubernetes.no_kube_proxy", opts.kubectl, kubectlArgs(opts, "-n", "kube-system", "get", "daemonset", "kube-proxy")...),
		commandAbsent(ctx, deps.Runner, opts.timeout, "kubernetes.no_kindnet", opts.kubectl, kubectlArgs(opts, "-n", "kube-system", "get", "daemonset", "kindnet")...),
		commandOK(ctx, deps.Runner, opts.timeout, "cell.service", opts.kubectl, kubectlArgs(opts, "-n", opts.namespace, "get", "service", "scrap")...),
		expectOutput(ctx, deps.Runner, opts.timeout, "cell.service_nodeport", "NodePort", opts.kubectl, kubectlArgs(opts, "-n", opts.namespace, "get", "service", "scrap", "-o", "jsonpath={.spec.type}")...),
		commandOK(ctx, deps.Runner, opts.timeout, "cell.headless_service", opts.kubectl, kubectlArgs(opts, "-n", opts.namespace, "get", "service", "scrap-headless")...),
		expectOutput(ctx, deps.Runner, opts.timeout, "cell.headless_cluster_ip", "None", opts.kubectl, kubectlArgs(opts, "-n", opts.namespace, "get", "service", "scrap-headless", "-o", "jsonpath={.spec.clusterIP}")...),
		nonEmptyOutput(ctx, deps.Runner, opts.timeout, "cell.headless_endpoints", opts.kubectl, kubectlArgs(opts, "-n", opts.namespace, "get", "endpointslice", "-l", "kubernetes.io/service-name=scrap-headless", "-o", "name")...),
		commandOK(ctx, deps.Runner, opts.timeout, "networkpolicy.admin_restrict", opts.kubectl, kubectlArgs(opts, "-n", opts.namespace, "get", "networkpolicy", "scrapd-admin-restrict")...),
		expectOutput(ctx, deps.Runner, opts.timeout, "networkpolicy.admin_restrict_selector", "scrap", opts.kubectl, kubectlArgs(opts, "-n", opts.namespace, "get", "networkpolicy", "scrapd-admin-restrict", "-o", "jsonpath={.spec.podSelector.matchLabels.app}")...),
		commandOK(ctx, deps.Runner, opts.timeout, "networkpolicy.host_admin_allow", opts.kubectl, kubectlArgs(opts, "-n", opts.namespace, "get", "ciliumnetworkpolicy", "scrapd-admin-host-allow")...),
		expectOutput(ctx, deps.Runner, opts.timeout, "networkpolicy.host_admin_allow_selector", "scrap", opts.kubectl, kubectlArgs(opts, "-n", opts.namespace, "get", "ciliumnetworkpolicy", "scrapd-admin-host-allow", "-o", "jsonpath={.spec.endpointSelector.matchLabels.app}")...),
	}
	return checks
}

func rolloutCommandTimeout(opts commonOptions) time.Duration {
	return doctorRolloutTimeout(opts) + commandTimeout(opts.timeout)
}

func rolloutTimeoutArg(opts commonOptions) string {
	return "--timeout=" + doctorRolloutTimeout(opts).String()
}

func doctorRolloutTimeout(opts commonOptions) time.Duration {
	if opts.rolloutTimeout <= 0 {
		return defaultRolloutTimeout
	}
	return opts.rolloutTimeout
}

func nodePortChecks(ctx context.Context, opts commonOptions, deps Deps) []Check {
	return []Check{
		dialCheck(ctx, opts, deps, "nodeport.client", opts.clientAddr),
		dialCheck(ctx, opts, deps, "nodeport.admin", opts.adminAddr),
	}
}

func commandOK(ctx context.Context, runner Runner, timeout time.Duration, checkName, name string, args ...string) Check {
	cctx, cancel := commandContext(ctx, timeout)
	defer cancel()
	out, err := runner.Run(cctx, name, args...)
	if err != nil {
		return Check{Name: checkName, Status: "fail", Reason: strings.TrimSpace(outOrErr(out, err))}
	}
	return Check{Name: checkName, Status: "ok"}
}

func commandAbsent(ctx context.Context, runner Runner, timeout time.Duration, checkName, name string, args ...string) Check {
	cctx, cancel := commandContext(ctx, timeout)
	defer cancel()
	out, err := runner.Run(cctx, name, args...)
	if err == nil {
		reason := strings.TrimSpace(out)
		if reason == "" {
			reason = "resource exists"
		}
		return Check{Name: checkName, Status: "fail", Reason: reason}
	}
	reason := strings.TrimSpace(outOrErr(out, err))
	lowerReason := strings.ToLower(reason)
	if errors.Is(err, errCommandFailed) &&
		(strings.Contains(lowerReason, "notfound") || strings.Contains(lowerReason, "not found")) {
		return Check{Name: checkName, Status: "ok"}
	}
	return Check{Name: checkName, Status: "fail", Reason: reason}
}

func expectOutput(ctx context.Context, runner Runner, timeout time.Duration, checkName, want, name string, args ...string) Check {
	cctx, cancel := commandContext(ctx, timeout)
	defer cancel()
	out, err := runner.Run(cctx, name, args...)
	if err != nil {
		return Check{Name: checkName, Status: "fail", Reason: strings.TrimSpace(outOrErr(out, err))}
	}
	got := strings.TrimSpace(out)
	if got != want {
		return Check{Name: checkName, Status: "fail", Reason: fmt.Sprintf("got %q, want %q", got, want)}
	}
	return Check{Name: checkName, Status: "ok"}
}

func containsOutput(ctx context.Context, runner Runner, timeout time.Duration, checkName, want, name string, args ...string) Check {
	cctx, cancel := commandContext(ctx, timeout)
	defer cancel()
	out, err := runner.Run(cctx, name, args...)
	if err != nil {
		return Check{Name: checkName, Status: "fail", Reason: strings.TrimSpace(outOrErr(out, err))}
	}
	if !strings.Contains(out, want) {
		return Check{Name: checkName, Status: "fail", Reason: fmt.Sprintf("missing %q", want)}
	}
	return Check{Name: checkName, Status: "ok"}
}

func nonEmptyOutput(ctx context.Context, runner Runner, timeout time.Duration, checkName, name string, args ...string) Check {
	cctx, cancel := commandContext(ctx, timeout)
	defer cancel()
	out, err := runner.Run(cctx, name, args...)
	if err != nil {
		return Check{Name: checkName, Status: "fail", Reason: strings.TrimSpace(outOrErr(out, err))}
	}
	if strings.TrimSpace(out) == "" {
		return Check{Name: checkName, Status: "fail", Reason: "empty output"}
	}
	return Check{Name: checkName, Status: "ok"}
}

func ciliumStatusCheck(ctx context.Context, opts commonOptions, deps Deps, checkName, key, want string) Check {
	cctx, cancel := commandContext(ctx, opts.timeout)
	defer cancel()
	out, err := deps.Runner.Run(cctx, opts.kubectl, kubectlArgs(opts, "-n", "kube-system", "exec", "ds/cilium", "--", "cilium-dbg", "status", "--verbose")...)
	if err != nil {
		return Check{Name: checkName, Status: "fail", Reason: strings.TrimSpace(outOrErr(out, err))}
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, key) && strings.Contains(line, want) {
			return Check{Name: checkName, Status: "ok"}
		}
	}
	return Check{Name: checkName, Status: "fail", Reason: fmt.Sprintf("%s did not contain %s", key, want)}
}

func kernelCheck(ctx context.Context, opts commonOptions, deps Deps) Check {
	cctx, cancel := commandContext(ctx, opts.timeout)
	defer cancel()
	out, err := deps.Runner.Run(cctx, "uname", "-r")
	if err != nil {
		return Check{Name: "host.kernel", Status: "fail", Reason: strings.TrimSpace(outOrErr(out, err))}
	}
	if !kernelAtLeast(strings.TrimSpace(out), minKernelMajor, minKernelMinor) {
		return Check{Name: "host.kernel", Status: "fail", Reason: fmt.Sprintf("got %s, want >= 5.14", strings.TrimSpace(out))}
	}
	return Check{Name: "host.kernel", Status: "ok"}
}

func kindNodeCgroupChecks(ctx context.Context, opts commonOptions, deps Deps) []Check {
	cctx, cancel := commandContext(ctx, opts.timeout)
	defer cancel()
	out, err := deps.Runner.Run(cctx, opts.docker, "ps", "--filter", "label=io.x-k8s.kind.cluster="+opts.cluster, "--format", "{{.Names}}")
	if err != nil {
		return []Check{{Name: "kind.nodes", Status: "fail", Reason: strings.TrimSpace(outOrErr(out, err))}}
	}
	nodes := strings.Fields(out)
	if len(nodes) == 0 {
		return []Check{{Name: "kind.nodes", Status: "fail", Reason: "no Kind nodes found"}}
	}
	checks := []Check{{Name: "kind.nodes", Status: "ok"}}
	hostCtx, hostCancel := commandContext(ctx, opts.timeout)
	hostNS, hostErr := deps.Runner.Run(hostCtx, "ls", "-al", "/proc/self/ns/cgroup")
	hostCancel()
	if hostErr != nil {
		checks = append(checks, Check{Name: "host.cgroup_namespace", Status: "fail", Reason: strings.TrimSpace(outOrErr(hostNS, hostErr))})
		return checks
	}
	for _, node := range nodes {
		cctx, cancel := commandContext(ctx, opts.timeout)
		nodeOut, err := deps.Runner.Run(cctx, opts.docker, "exec", node, "ls", "-al", "/proc/self/ns/cgroup")
		cancel()
		if err != nil {
			checks = append(checks, Check{Name: "kind.node_cgroup_namespace", Status: "fail", Reason: strings.TrimSpace(outOrErr(nodeOut, err))})
			continue
		}
		if strings.TrimSpace(nodeOut) == strings.TrimSpace(hostNS) {
			checks = append(checks, Check{Name: "kind.node_cgroup_namespace", Status: "fail", Reason: node + " shares host cgroup namespace"})
			continue
		}
		checks = append(checks, Check{Name: "kind.node_cgroup_namespace", Status: "ok", Reason: node})
	}
	return checks
}

func dialCheck(ctx context.Context, opts commonOptions, deps Deps, checkName, address string) Check {
	cctx, cancel := commandContext(ctx, opts.timeout)
	defer cancel()
	conn, err := deps.DialContext(cctx, "tcp", address)
	if err != nil {
		return Check{Name: checkName, Status: "fail", Reason: err.Error()}
	}
	_ = conn.Close()
	return Check{Name: checkName, Status: "ok"}
}

func outOrErr(out string, err error) string {
	if strings.TrimSpace(out) != "" {
		return out
	}
	return err.Error()
}

func kernelAtLeast(version string, major, minor int) bool {
	var gotMajor, gotMinor int
	_, _ = fmt.Sscanf(version, "%d.%d", &gotMajor, &gotMinor)
	return gotMajor > major || gotMajor == major && gotMinor >= minor
}
