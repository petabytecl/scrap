// Package scrapctl implements the S.C.R.A.P. operator command-line checks.
package scrapctl

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"time"
)

const (
	kubectlGlobalFlagCapacity = 4
	scrapctlUsage             = "scrapctl <doctor|status|upload-pressure|peers|leader|fault|evidence>"

	defaultNamespace      = "scrap"
	defaultCluster        = "scrap-prodlike"
	defaultAdminURL       = "http://127.0.0.1:18100"
	defaultMetricsURL     = "http://127.0.0.1:18100/metrics"
	defaultClientAddr     = "127.0.0.1:18090"
	defaultAdminAddr      = "127.0.0.1:18100"
	defaultTimeout        = 5 * time.Second
	defaultRolloutTimeout = 180 * time.Second
)

var errCommandFailed = errors.New("command failed")

type Runner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

type Deps struct {
	Runner      Runner
	HTTPClient  *http.Client
	DialContext func(ctx context.Context, network, address string) (net.Conn, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	//nolint:gosec // scrapctl intentionally runs operator-selected kubectl, docker, and host diagnostic commands.
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%w: %s %v: %w: %s", errCommandFailed, name, args, err, string(out))
	}
	return string(out), nil
}

func Run(args []string, stdout, stderr io.Writer, deps Deps) error {
	if len(args) == 0 {
		return errors.New("usage: " + scrapctlUsage)
	}
	deps = deps.withDefaults()
	if args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		_, _ = fmt.Fprintln(stdout, "usage: "+scrapctlUsage)
		return nil
	}
	return runCommand(args[0], args[1:], stdout, stderr, deps)
}

func runCommand(name string, args []string, stdout, stderr io.Writer, deps Deps) error {
	switch name {
	case "doctor":
		return runDoctor(args, stdout, deps)
	case "status":
		return runStatus(args, stdout, deps)
	case "upload-pressure":
		return runUploadPressure(args, stdout, deps)
	case "peers":
		return runPeers(args, stdout, deps)
	case "leader":
		return runLeader(args, stdout, deps)
	case "fault":
		return runFault(args, stdout, deps)
	case "evidence":
		return runEvidence(args, stdout, deps)
	default:
		_, _ = fmt.Fprintf(stderr, "unknown command %q\n", name)
		return fmt.Errorf("unknown command %q", name)
	}
}

func (d Deps) withDefaults() Deps {
	if d.Runner == nil {
		d.Runner = execRunner{}
	}
	if d.HTTPClient == nil {
		d.HTTPClient = http.DefaultClient
	}
	if d.DialContext == nil {
		var dialer net.Dialer
		d.DialContext = dialer.DialContext
	}
	return d
}

type commonOptions struct {
	namespace      string
	cluster        string
	kubectl        string
	kubeContext    string
	kubeconfig     string
	docker         string
	adminURL       string
	metricsURL     string
	clientAddr     string
	adminAddr      string
	timeout        time.Duration
	rolloutTimeout time.Duration
	output         string
}

type flagSetOption func(*flag.FlagSet, *commonOptions)

func newFlagSet(name string, opts *commonOptions, configure ...flagSetOption) *flag.FlagSet {
	fs := flag.NewFlagSet("scrapctl "+name, flag.ContinueOnError)
	fs.StringVar(&opts.namespace, "namespace", defaultNamespace, "Kubernetes namespace containing the Cell")
	fs.StringVar(&opts.cluster, "cluster", defaultCluster, "Kind cluster name for host runtime checks")
	fs.StringVar(&opts.kubectl, "kubectl", "kubectl", "kubectl command")
	fs.StringVar(&opts.kubeContext, "context", "", "kubeconfig context for kubectl")
	fs.StringVar(&opts.kubeconfig, "kubeconfig", "", "kubeconfig path for kubectl")
	fs.StringVar(&opts.docker, "docker", "docker", "docker command")
	fs.StringVar(&opts.adminURL, "admin-url", defaultAdminURL, "S.C.R.A.P. admin HTTP base URL")
	fs.StringVar(&opts.metricsURL, "metrics-url", defaultMetricsURL, "S.C.R.A.P. metrics URL")
	fs.StringVar(&opts.clientAddr, "client-addr", defaultClientAddr, "client NodePort host:port")
	fs.StringVar(&opts.adminAddr, "admin-addr", defaultAdminAddr, "admin NodePort host:port")
	fs.DurationVar(&opts.timeout, "timeout", defaultTimeout, "per-check timeout")
	fs.StringVar(&opts.output, "output", "text", "output format: text or json")
	for _, apply := range configure {
		apply(fs, opts)
	}
	return fs
}

func withRolloutTimeoutFlag(fs *flag.FlagSet, opts *commonOptions) {
	fs.DurationVar(&opts.rolloutTimeout, "rollout-timeout", defaultRolloutTimeout, "Kubernetes rollout status timeout")
}

func parseCommon(name string, args []string, configure ...flagSetOption) (commonOptions, error) {
	opts := commonOptions{}
	fs := newFlagSet(name, &opts, configure...)
	if err := fs.Parse(args); err != nil {
		return commonOptions{}, fmt.Errorf("parse flags: %w", err)
	}
	if err := validateCommon(opts); err != nil {
		return commonOptions{}, err
	}
	return opts, nil
}

func validateCommon(opts commonOptions) error {
	if opts.namespace == "" {
		return errors.New("namespace is required")
	}
	if opts.output != "text" && opts.output != "json" {
		return fmt.Errorf("unsupported output %q", opts.output)
	}
	return nil
}

func commandTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return defaultTimeout
	}
	return timeout
}

func commandContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, commandTimeout(timeout))
}

func kubectlArgs(opts commonOptions, args ...string) []string {
	out := make([]string, 0, len(args)+kubectlGlobalFlagCapacity)
	if opts.kubeContext != "" {
		out = append(out, "--context", opts.kubeContext)
	}
	if opts.kubeconfig != "" {
		out = append(out, "--kubeconfig", opts.kubeconfig)
	}
	out = append(out, args...)
	return out
}
