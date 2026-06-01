package evidencebundle

import (
	"context"
	"net/http"
	"time"
)

const (
	defaultBundleDirName = "evidence"
	defaultGrafanaURL    = "http://127.0.0.1:13000"
	defaultAdminURL      = "http://127.0.0.1:18100"
	defaultKubectl       = "kubectl"
	defaultNamespace     = "scrap"
	defaultStressAddr    = "127.0.0.1:18090"
	defaultWorkers       = 8
	defaultDuration      = "60s"
	defaultDocSizeBytes  = 16384
	defaultProbeTimeout  = 5 * time.Second
	defaultGoCommand     = "go"
	defaultSettle        = 75 * time.Second
)

// Config describes one Tier 3 evidence bundle run.
type Config struct {
	RepoRoot       string
	BundleDir      string
	GrafanaURL     string
	AdminURL       string
	Kubectl        string
	Namespace      string
	KubeContext    string
	Kubeconfig     string
	MimirProxy     string
	TempoProxy     string
	LokiProxy      string
	PyroscopeURL   string
	EvictionPlanID string
	Scenario       string
	StressAddr     string
	Workers        int
	Duration       string
	DocSizeBytes   int
	Settle         time.Duration
	ProbeTimeout   time.Duration
	GoCommand      string
}

// Options wires Config to replaceable adapters.
type Options struct {
	Config       Config
	Command      CommandRunner
	HTTPClient   *http.Client
	StressRunner StressRunner
	AdminProbe   AdminProber
	Clock        Clock
	Sleeper      Sleeper
	Logf         func(format string, args ...any)
}

// Result is the generated bundle location and evaluated gate.
type Result struct {
	BundlePath string
	Gate       Gate
}

// CommandRunner runs metadata commands used by the bundle.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

// Clock provides deterministic time in tests.
type Clock interface {
	Now() time.Time
}

// Sleeper waits between stress completion and telemetry queries.
type Sleeper func(context.Context, time.Duration) error

// StressRequest describes the stress command to execute.
type StressRequest struct {
	RepoRoot     string
	GoCommand    string
	Addr         string
	Scenario     string
	Workers      int
	Duration     string
	DocSizeBytes int
}

// StressResult contains stress stdout and stderr.
type StressResult struct {
	Stdout string
	Stderr string
}

// StressRunner runs the scenario workload.
type StressRunner interface {
	RunStress(context.Context, StressRequest) (StressResult, error)
}

// AdminProbeRequest describes the admin evidence-marker probe.
type AdminProbeRequest struct {
	AdminURL     string
	Marker       string
	LogDir       string
	ProbeTimeout time.Duration
	Kubectl      string
	Namespace    string
	KubeContext  string
	Kubeconfig   string
}

// AdminProbeResult contains the admin probe response body.
type AdminProbeResult struct {
	Body []byte
	URL  string
}

// AdminProber emits the evidence marker through the admin path.
type AdminProber interface {
	Emit(context.Context, AdminProbeRequest) (AdminProbeResult, error)
}

// Gate is the persisted gates.json schema.
type Gate struct {
	Pass     bool        `json:"pass"`
	Scenario string      `json:"scenario"`
	Checks   []GateCheck `json:"checks"`
}

// GateCheck is one pass/fail evidence check.
type GateCheck struct {
	Name   string `json:"name"`
	Pass   bool   `json:"pass"`
	Reason string `json:"reason"`
}

// GateInput is the signal summary used to evaluate gates.json.
type GateInput struct {
	StressResults       string
	MetricFailures      int
	MetricEmptyRequired int
	TraceOK             bool
	TraceReason         string
	LogOK               bool
	LogReason           string
	CPUProfileOK        bool
	CPUReason           string
	HeapProfileOK       bool
	HeapReason          string
}
