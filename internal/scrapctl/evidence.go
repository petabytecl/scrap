package scrapctl

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/petabytecl/scrap/internal/scrapctl/evidencebundle"
)

const (
	evidenceMarkerHeader = "X-Scrap-Evidence-Marker"
	defaultProfile       = "heap"
	defaultProfileSecs   = 30
	defaultBundleWorkers = 8
	defaultBundleDocSize = 16384
	defaultSettleSeconds = 75
	defaultProbeSeconds  = 5
)

type evidenceOptions struct {
	common  commonOptions
	marker  string
	profile string
	outPath string
	seconds int
}

func runEvidence(args []string, stdout, stderr io.Writer, deps Deps) error {
	if len(args) == 0 {
		return errors.New("usage: scrapctl evidence <bundle|log-probe|pprof>")
	}
	switch args[0] {
	case "bundle":
		return runEvidenceBundle(args[1:], stdout, stderr, deps)
	case "log-probe":
		return runEvidenceLogProbe(args[1:], stdout, deps)
	case "pprof":
		return runEvidencePprof(args[1:], stdout, deps)
	default:
		return fmt.Errorf("unsupported evidence command %q", args[0])
	}
}

func runEvidenceBundle(args []string, stdout, stderr io.Writer, deps Deps) error {
	opts, err := parseEvidenceBundleOptions(args)
	if err != nil {
		return err
	}
	result, err := evidencebundle.Generate(context.Background(), evidencebundle.Options{
		Config:     opts,
		Command:    deps.Runner,
		HTTPClient: deps.HTTPClient,
		Logf: func(format string, args ...any) {
			_, _ = fmt.Fprintf(stderr, "[evidence] "+format+"\n", args...)
		},
	})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, result.BundlePath)
	if err != nil {
		return fmt.Errorf("write evidence bundle path: %w", err)
	}
	return nil
}

func parseEvidenceBundleOptions(args []string) (evidencebundle.Config, error) {
	common := commonOptions{}
	fs := newFlagSet("evidence bundle", &common)

	defaults, err := bundleDefaultsFromEnv()
	if err != nil {
		return evidencebundle.Config{}, err
	}

	registerEvidenceBundleFlags(fs, &defaults)
	if err := fs.Parse(args); err != nil {
		return evidencebundle.Config{}, fmt.Errorf("parse flags: %w", err)
	}
	applyEvidenceEnvDefaults(fs, &common)
	if err := validateCommon(common); err != nil {
		return evidencebundle.Config{}, err
	}
	remaining := fs.Args()
	scenario, err := bundleScenario(remaining)
	if err != nil {
		return evidencebundle.Config{}, err
	}
	if defaults.settleSeconds < 0 {
		return evidencebundle.Config{}, errors.New("EVIDENCE_SETTLE_SECONDS must be a non-negative integer")
	}
	if defaults.probeSeconds <= 0 {
		return evidencebundle.Config{}, errors.New("EVIDENCE_PROBE_TIMEOUT_SECONDS must be a positive integer")
	}
	adminURL := common.adminURL
	if !flagWasSet(fs, "admin-url") {
		adminURL = envString("SCRAP_ADMIN_URL", adminURL)
	}
	if defaults.stressAddr == "" {
		defaults.stressAddr = common.clientAddr
	}
	return evidencebundle.Config{
		RepoRoot:     defaults.repoRoot,
		BundleDir:    defaults.bundleDir,
		GrafanaURL:   defaults.grafanaURL,
		AdminURL:     adminURL,
		Kubectl:      common.kubectl,
		Namespace:    common.namespace,
		KubeContext:  common.kubeContext,
		Kubeconfig:   common.kubeconfig,
		MimirProxy:   defaults.mimirProxy,
		TempoProxy:   defaults.tempoProxy,
		LokiProxy:    defaults.lokiProxy,
		PyroscopeURL: defaults.pyroscopeProxy,
		Scenario:     scenario,
		StressAddr:   defaults.stressAddr,
		Workers:      defaults.workers,
		Duration:     defaults.duration,
		DocSizeBytes: defaults.docSize,
		Settle:       time.Duration(defaults.settleSeconds) * time.Second,
		ProbeTimeout: time.Duration(defaults.probeSeconds) * time.Second,
		GoCommand:    defaults.goCommand,
	}, nil
}

type evidenceBundleDefaults struct {
	bundleDir      string
	repoRoot       string
	grafanaURL     string
	mimirProxy     string
	tempoProxy     string
	lokiProxy      string
	pyroscopeProxy string
	stressAddr     string
	duration       string
	goCommand      string
	workers        int
	docSize        int
	settleSeconds  int
	probeSeconds   int
}

func bundleDefaultsFromEnv() (evidenceBundleDefaults, error) {
	workers, err := envInt("STRESS_WORKERS", defaultBundleWorkers)
	if err != nil {
		return evidenceBundleDefaults{}, err
	}
	docSize, err := envInt("STRESS_DOC_SIZE", defaultBundleDocSize)
	if err != nil {
		return evidenceBundleDefaults{}, err
	}
	settleSeconds, err := envInt("EVIDENCE_SETTLE_SECONDS", defaultSettleSeconds)
	if err != nil {
		return evidenceBundleDefaults{}, err
	}
	probeSeconds, err := envInt("EVIDENCE_PROBE_TIMEOUT_SECONDS", defaultProbeSeconds)
	if err != nil {
		return evidenceBundleDefaults{}, err
	}
	return evidenceBundleDefaults{
		bundleDir:      os.Getenv("BUNDLE_DIR"),
		repoRoot:       os.Getenv("SCRAP_REPO_ROOT"),
		grafanaURL:     os.Getenv("GRAFANA_URL"),
		mimirProxy:     os.Getenv("MIMIR_PROXY"),
		tempoProxy:     os.Getenv("TEMPO_PROXY"),
		lokiProxy:      os.Getenv("LOKI_PROXY"),
		pyroscopeProxy: os.Getenv("PYROSCOPE_PROXY"),
		stressAddr:     os.Getenv("STRESS_ADDR"),
		duration:       envString("STRESS_DURATION", "60s"),
		goCommand:      envString("GO", "go"),
		workers:        workers,
		docSize:        docSize,
		settleSeconds:  settleSeconds,
		probeSeconds:   probeSeconds,
	}, nil
}

func registerEvidenceBundleFlags(fs *flag.FlagSet, defaults *evidenceBundleDefaults) {
	fs.StringVar(&defaults.repoRoot, "repo-root", defaults.repoRoot, "Repository root containing test/stress")
	fs.StringVar(&defaults.bundleDir, "bundle-dir", defaults.bundleDir, "Output directory for evidence bundles")
	fs.StringVar(&defaults.grafanaURL, "grafana-url", defaults.grafanaURL, "Grafana base URL")
	fs.StringVar(&defaults.mimirProxy, "mimir-proxy", defaults.mimirProxy, "Mimir query base URL")
	fs.StringVar(&defaults.tempoProxy, "tempo-proxy", defaults.tempoProxy, "Tempo query base URL")
	fs.StringVar(&defaults.lokiProxy, "loki-proxy", defaults.lokiProxy, "Loki query base URL")
	fs.StringVar(&defaults.pyroscopeProxy, "pyroscope-proxy", defaults.pyroscopeProxy, "Pyroscope query base URL")
	fs.StringVar(&defaults.stressAddr, "stress-addr", defaults.stressAddr, "Stress target address")
	fs.IntVar(&defaults.workers, "stress-workers", defaults.workers, "Stress worker count")
	fs.StringVar(&defaults.duration, "stress-duration", defaults.duration, "Stress run duration")
	fs.IntVar(&defaults.docSize, "stress-doc-size", defaults.docSize, "Stress Document size in bytes")
	fs.IntVar(&defaults.settleSeconds, "settle-seconds", defaults.settleSeconds, "Seconds to wait after stress before querying telemetry")
	fs.IntVar(&defaults.probeSeconds, "probe-timeout-seconds", defaults.probeSeconds, "Per-attempt timeout for the admin log probe")
	fs.StringVar(&defaults.goCommand, "go-command", defaults.goCommand, "Go command used to run the stress workload")
}

func bundleScenario(args []string) (string, error) {
	switch len(args) {
	case 0:
		return envString("STRESS_SCENARIO", "throughput"), nil
	case 1:
		return args[0], nil
	default:
		return "", errors.New("usage: scrapctl evidence bundle [scenario]")
	}
}

func applyEvidenceEnvDefaults(fs *flag.FlagSet, opts *commonOptions) {
	if !flagWasSet(fs, "kubectl") {
		opts.kubectl = envString("KUBECTL", opts.kubectl)
	}
	if !flagWasSet(fs, "namespace") {
		opts.namespace = envString("SCRAP_NAMESPACE", opts.namespace)
	}
	if !flagWasSet(fs, "context") {
		opts.kubeContext = envString("KUBE_CONTEXT", opts.kubeContext)
	}
	if !flagWasSet(fs, "kubeconfig") {
		opts.kubeconfig = envString("KUBECONFIG", opts.kubeconfig)
	}
}

func flagWasSet(fs *flag.FlagSet, name string) bool {
	wasSet := false
	fs.Visit(func(flag *flag.Flag) {
		if flag.Name == name {
			wasSet = true
		}
	})
	return wasSet
}

func envString(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	return parsed, nil
}

func runEvidenceLogProbe(args []string, stdout io.Writer, deps Deps) error {
	opts, err := parseEvidenceOptions("evidence log-probe", args, func(fs *flag.FlagSet, opts *evidenceOptions) {
		fs.StringVar(&opts.marker, "marker", "", "Evidence marker to emit through the admin health endpoint")
	})
	if err != nil {
		return err
	}
	if opts.marker == "" {
		opts.marker = "scrapctl-evidence-" + time.Now().UTC().Format("20060102T150405Z")
	}
	cctx, cancel := commandContext(context.Background(), opts.common.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, strings.TrimRight(opts.common.adminURL, "/")+"/healthz", nil)
	if err != nil {
		return fmt.Errorf("build health request: %w", err)
	}
	req.Header.Set(evidenceMarkerHeader, opts.marker)
	resp, err := deps.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET healthz: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GET healthz status: %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return writeByFormat(stdout, opts.common.output, operationReport{
		Status: "ok",
		Action: "evidence.log-probe",
		Marker: opts.marker,
	})
}

func runEvidencePprof(args []string, stdout io.Writer, deps Deps) error {
	opts, err := parseEvidenceOptions("evidence pprof", args, func(fs *flag.FlagSet, opts *evidenceOptions) {
		fs.StringVar(&opts.profile, "profile", defaultProfile, "pprof profile: heap, cpu, goroutine, trace, allocs, block, mutex")
		fs.StringVar(&opts.outPath, "out", "", "Output profile path")
		fs.IntVar(&opts.seconds, "seconds", defaultProfileSecs, "CPU or trace profile duration in seconds")
	})
	if err != nil {
		return err
	}
	if opts.outPath == "" {
		return errors.New("out is required")
	}
	path, err := pprofPath(opts.profile, opts.seconds)
	if err != nil {
		return err
	}
	cctx, cancel := commandContext(context.Background(), opts.common.timeout+time.Duration(opts.seconds)*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, strings.TrimRight(opts.common.adminURL, "/")+path, nil)
	if err != nil {
		return fmt.Errorf("build pprof request: %w", err)
	}
	resp, err := deps.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET pprof: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GET pprof status: %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read pprof: %w", err)
	}
	if err := os.WriteFile(opts.outPath, body, 0o600); err != nil {
		return fmt.Errorf("write pprof %s: %w", opts.outPath, err)
	}
	return writeByFormat(stdout, opts.common.output, operationReport{
		Status:  "ok",
		Action:  "evidence.pprof",
		Profile: opts.profile,
		Path:    opts.outPath,
	})
}

func parseEvidenceOptions(name string, args []string, configure func(*flag.FlagSet, *evidenceOptions)) (evidenceOptions, error) {
	opts := evidenceOptions{}
	fs := newFlagSet(name, &opts.common)
	if configure != nil {
		configure(fs, &opts)
	}
	if err := fs.Parse(args); err != nil {
		return evidenceOptions{}, fmt.Errorf("parse flags: %w", err)
	}
	if err := validateCommon(opts.common); err != nil {
		return evidenceOptions{}, err
	}
	return opts, nil
}

func pprofPath(profile string, seconds int) (string, error) {
	switch profile {
	case "heap", "goroutine", "allocs", "block", "mutex":
		return "/debug/pprof/" + profile, nil
	case "cpu":
		if seconds <= 0 {
			return "", errors.New("seconds must be positive")
		}
		return fmt.Sprintf("/debug/pprof/profile?seconds=%d", seconds), nil
	case "trace":
		if seconds <= 0 {
			return "", errors.New("seconds must be positive")
		}
		return fmt.Sprintf("/debug/pprof/trace?seconds=%d", seconds), nil
	default:
		return "", fmt.Errorf("unsupported profile %q", profile)
	}
}
