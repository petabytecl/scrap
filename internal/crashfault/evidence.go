package crashfault

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/petabytecl/scrap/internal/config"
	"github.com/petabytecl/scrap/internal/releasegate"
)

const (
	ReportSchemaVersion = "scrap.crashfault.v1"
	ReportKind          = "crash-fault-evidence-report"
	defaultOutputLimit  = 64 * 1024
)

type Options struct {
	WorkDir           string
	GoCommand         string
	Count             int
	Race              bool
	RunnerProfile     string
	FilesystemProfile string
	Seed              string
	ArtifactURI       string
	GeneratedAt       time.Time
	CommandLine       []string
	CommitSHA         string
	DirtyTree         bool
	OutputLimitBytes  int
	Runner            CommandRunner
}

type Scenario struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	ReadinessGates  []string        `json:"readiness_gates"`
	ReleaseGateIDs  []string        `json:"release_gate_ids"`
	Invariants      []string        `json:"invariants"`
	FailureSchedule string          `json:"failure_schedule"`
	Commands        []GoTestCommand `json:"commands"`
}

type GoTestCommand struct {
	Package string `json:"package"`
	Pattern string `json:"pattern"`
}

type CommandInvocation struct {
	Name    string
	Args    []string
	WorkDir string
}

type CommandOutput struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Error    string
}

type CommandRunner interface {
	Run(context.Context, CommandInvocation) CommandOutput
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, invocation CommandInvocation) CommandOutput {
	// #nosec G204 -- invocation comes from the repo-owned crash/fault scenario catalog, not remote input.
	cmd := exec.CommandContext(ctx, invocation.Name, invocation.Args...)
	cmd.Dir = invocation.WorkDir
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	output := CommandOutput{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}
	if err == nil {
		return output
	}
	output.ExitCode = 1
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		output.ExitCode = exitErr.ExitCode()
	} else {
		output.ExitCode = -1
	}
	output.Error = err.Error()
	return output
}

type Report struct {
	SchemaVersion       string                 `json:"schema_version"`
	ReportKind          string                 `json:"report_kind"`
	GeneratedAt         string                 `json:"generated_at"`
	Ready               bool                   `json:"ready"`
	CommitSHA           string                 `json:"commit_sha"`
	DirtyTree           bool                   `json:"dirty_tree"`
	Command             []string               `json:"command"`
	Runner              RunnerInfo             `json:"runner"`
	Config              RunConfig              `json:"config"`
	DurationMillis      int64                  `json:"duration_millis"`
	Summary             Summary                `json:"summary"`
	Scenarios           []ScenarioResult       `json:"scenarios"`
	ReleaseGateEvidence []releasegate.Evidence `json:"release_gate_evidence,omitempty"`
}

type RunnerInfo struct {
	Profile           string `json:"profile"`
	OS                string `json:"os"`
	Arch              string `json:"arch"`
	GoVersion         string `json:"go_version"`
	FilesystemProfile string `json:"filesystem_profile"`
}

type RunConfig struct {
	Seed             string `json:"seed"`
	GoTestCount      int    `json:"go_test_count"`
	Race             bool   `json:"race"`
	ScenarioCount    int    `json:"scenario_count"`
	OutputLimitBytes int    `json:"output_limit_bytes"`
}

type Summary struct {
	Passed int `json:"passed"`
	Failed int `json:"failed"`
}

type ScenarioResult struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	Status          string          `json:"status"`
	DurationMillis  int64           `json:"duration_millis"`
	ReadinessGates  []string        `json:"readiness_gates"`
	ReleaseGateIDs  []string        `json:"release_gate_ids"`
	Invariants      []string        `json:"invariants"`
	FailureSchedule string          `json:"failure_schedule"`
	Commands        []CommandResult `json:"commands"`
}

type CommandResult struct {
	Package        string   `json:"package"`
	Pattern        string   `json:"pattern"`
	Command        []string `json:"command"`
	Status         string   `json:"status"`
	ExitCode       int      `json:"exit_code"`
	DurationMillis int64    `json:"duration_millis"`
	Output         string   `json:"output,omitempty"`
	Error          string   `json:"error,omitempty"`
}

func Catalog() []Scenario {
	scenarios := []Scenario{
		{
			ID:   "local-ack-replay",
			Name: "Local write ACK, crash boundary, replay, and visible-read evidence",
			ReadinessGates: []string{
				config.ReadinessGateRaftMetadataDurability,
			},
			ReleaseGateIDs: []string{"crash-fault"},
			Invariants: []string{
				"acknowledged-write",
				"visible-read",
				"partial-write-invisible",
				"prepare-openlog-replay",
				"local-projection-rebuild",
			},
			FailureSchedule: "deterministic local write crash hooks after block sync, prepare sync, metadata apply, and before ACK",
			Commands: []GoTestCommand{
				{
					Package: "./internal/localstorage",
					Pattern: "Test(CommittedWriteSurvivesReopen|CrashAfterBlockSyncLeavesDocumentInvisible|CrashAfterPrepareSyncLeavesDocumentInvisible|CrashAfterMetadataApplyKeepsCommittedDocumentRetryable|CrashBeforeACKKeepsCommittedDocumentRetryable|PrepareLogRecoveryTruncatesCrashCutTail|MetadataProjectionRebuildsFromAuthorityLog|MetadataProjectionRebuildQueuesRepairForMissingLocalRef|MetadataProjectionRebuildQueuesRepairForCorruptLocalRef)$",
				},
			},
		},
		{
			ID:   "raft-metadata-faults",
			Name: "Raft metadata restart, snapshot, stale leader, and ReadIndex fault evidence",
			ReadinessGates: []string{
				config.ReadinessGateRaftMetadataDurability,
			},
			ReleaseGateIDs: []string{"crash-fault"},
			Invariants: []string{
				"authoritative-metadata-replay",
				"snapshot-tail-replay",
				"stale-leader-fail-closed",
				"read-index-fail-closed",
			},
			FailureSchedule: "deterministic Raft transport restart, partition, snapshot install, and stale leader checks",
			Commands: []GoTestCommand{
				{
					Package: "./internal/raftmeta",
					Pattern: "Test(AuthoritySnapshotCompactionReplaysSnapshotAndTail|AuthorityReadIndexFailsClosedDuringPartition|AuthorityFencesStaleLeaderAfterLeaderChange|LogAppendReplayAndContinueAfterReopen|RaftFaultHarnessTransportRestartAndReadIndex|RaftFaultHarnessRechecksByteEligibilityAfterSnapshotInstall)$",
				},
			},
		},
		{
			ID:   "peer-byte-and-repair",
			Name: "Peer byte durability, source quarantine, and repair evidence",
			ReadinessGates: []string{
				config.ReadinessGatePeerByteDurability,
			},
			ReleaseGateIDs: []string{"peer-byte-durability", "crash-fault"},
			Invariants: []string{
				"peer-prepare-before-visibility",
				"corrupt-source-quarantine",
				"repair-from-verified-source",
				"all-sources-corrupt-fail-closed",
			},
			FailureSchedule: "deterministic peer prepare failure, corrupt peer read, restart retry, and verified repair paths",
			Commands: []GoTestCommand{
				{
					Package: "./internal/localstorage",
					Pattern: "Test(WriteDocumentPreparesPeersBeforeMetadataVisibility|WriteDocumentPeerPrepareFailureLeavesDocumentInvisible|RunQueuedOperationsOnceRepairsQuarantinedLocalBlockFromPeer|RunQueuedOperationsOnceQuarantinesCorruptPeerAndFailsWithoutVerifiedSource|RunQueuedOperationsOnceRetriesPeerRepairAfterRestart)$",
				},
				{
					Package: "./internal/replication",
					Pattern: "Test(PrepareNormalWriteSucceedsWithQuorumAndMarksRepairRequired|PrepareCriticalWriteRequiresAllReplicasByDefault|PrepareFailsWhenPeerTargetsCannotReachQuorum|PrepareTransfersRangeAndChecksumEvidence|PrepareCanRetryAfterPeerFailure|PrepareRejectsCorruptTransfer)$",
				},
			},
		},
		{
			ID:   "backend-upload-restore",
			Name: "Backend upload, restore, prewarm, repair, and DR drill evidence",
			ReadinessGates: []string{
				config.ReadinessGateBackendRestoreWorkflow,
			},
			ReleaseGateIDs: []string{"backend-restore", "crash-fault"},
			Invariants: []string{
				"backend-upload-idempotency",
				"restore-pending-visible",
				"verified-backend-restore",
				"repair-from-backend",
				"drill-failure-evidence",
			},
			FailureSchedule: "deterministic backend partial-object retry, archive pending retry, corrupt object, missing artifact, and scratch DR drill checks",
			Commands: []GoTestCommand{
				{
					Package: "./internal/backendupload",
					Pattern: "Test(UploadBlockIsIdempotent|UploadBlockVerifiesCompleteObjectSetBeforeSuccess|ProcessorRetriesPartialObjectSetAndRecordsUploaded|ProcessorRecoversDurableFailedIntentAfterRestart|ProcessorRecordsBackendErrorClass)$",
				},
				{
					Package: "./internal/localstorage",
					Pattern: "Test(RunBackendUploadOnceSealsDueBlockAndUploads|ReadDocumentFallsBackToVerifiedBackendCopy|RunQueuedOperationsOnceRestoresDocumentFromBackend|ReadDocumentQueuesRestoreOnColdReadAndRetriesAfterRestart|RunQueuedOperationsOnceKeepsArchiveRestorePendingAndRetries|RunQueuedOperationsOncePrewarmsDocumentFromBackendAndAudits|RunQueuedOperationsOnceRepairsQuarantinedLocalBlock|RunQueuedOperationsOnceDRDrillRestoresScratchMetadata|RunQueuedOperationsOnceDRDrillFailsWhenRequiredArtifactMissing|RunQueuedOperationsOnceDRDrillFailsWhenRequiredArtifactCorrupt)$",
				},
			},
		},
		{
			ID:   "operations-recovery",
			Name: "Durable operation recovery, idempotency, and audit evidence",
			ReadinessGates: []string{
				config.ReadinessGateBackendRestoreWorkflow,
				config.ReadinessGateOperatorReadiness,
			},
			ReleaseGateIDs: []string{"backend-restore", "crash-fault"},
			Invariants: []string{
				"durable-operation-replay",
				"operation-idempotency",
				"operation-audit-evidence",
			},
			FailureSchedule: "deterministic operation-store reopen, running-operation recovery, and queued retry evidence",
			Commands: []GoTestCommand{
				{
					Package: "./internal/operations",
					Pattern: "Test(StoreRecoverInterruptedKeepsQueuedRetryEvidenceAcrossReopen|StoreRecoverInterruptedRequeuesRunningSupportedOperation|StoreRecoverInterruptedFailsUnsupportedRunningOperation|StoreAppendAuditEventPersistsAndIsIdempotent)$",
				},
				{
					Package: "./internal/localstorage",
					Pattern: "Test(RecoverInterruptedOperationsRequeuesRunningCapacityOverrideAfterRestart|RunQueuedOperationsOnceCopyVerifySucceedsWithPublishedCheckpoint|RunQueuedOperationsOnceMetadataRestoreImportsColdDocuments|RunQueuedOperationsOnceDryRunDROperationReportsReadiness)$",
				},
			},
		},
		{
			ID:   "openbao-fake-outage",
			Name: "Fake OpenBao Transit outage and missing-key failure evidence",
			ReadinessGates: []string{
				config.ReadinessGateOpenBaoEnvelopeWorkflow,
			},
			ReleaseGateIDs: []string{"crash-fault"},
			Invariants: []string{
				"crypto-unavailable-fail-closed",
				"missing-key-material-fail-closed",
				"secret-hygiene",
			},
			FailureSchedule: "deterministic fake Transit unavailable, missing key material, and AAD mismatch checks",
			Commands: []GoTestCommand{
				{
					Package: "./internal/cryptoenv",
					Pattern: "Test(ValidateEnvelopeRecordForRestoreClassifiesUnavailableTransit|FakeTransitRejectsAADOrAlgorithmMismatch|RewrapEnvelopeRecordIsDeterministicAndPreservesAAD)$",
				},
				{
					Package: "./internal/localstorage",
					Pattern: "Test(ReadDocumentWithTransitEnvelopeRequiresKeyMaterial|ReadDocumentReturnsCryptoUnavailableDetail|RunQueuedOperationsOnceDRDrillFailsWhenKeyMaterialMissing)$",
				},
			},
		},
	}
	out := make([]Scenario, len(scenarios))
	copy(out, scenarios)
	return out
}

func Run(ctx context.Context, options Options) (Report, error) {
	options = options.withDefaults()
	runStart := time.Now().UTC()
	commitSHA, dirtyTree := options.CommitSHA, options.DirtyTree
	if commitSHA == "" {
		var err error
		commitSHA, dirtyTree, err = collectGitState(ctx, options.WorkDir)
		if err != nil {
			commitSHA = "unknown"
			dirtyTree = true
		}
	}

	scenarios := Catalog()
	report := Report{
		SchemaVersion: ReportSchemaVersion,
		ReportKind:    ReportKind,
		GeneratedAt:   options.GeneratedAt.Format(time.RFC3339Nano),
		Ready:         true,
		CommitSHA:     commitSHA,
		DirtyTree:     dirtyTree,
		Command:       append([]string(nil), options.CommandLine...),
		Runner: RunnerInfo{
			Profile:           options.RunnerProfile,
			OS:                runtime.GOOS,
			Arch:              runtime.GOARCH,
			GoVersion:         runtime.Version(),
			FilesystemProfile: options.FilesystemProfile,
		},
		Config: RunConfig{
			Seed:             options.Seed,
			GoTestCount:      options.Count,
			Race:             options.Race,
			ScenarioCount:    len(scenarios),
			OutputLimitBytes: options.OutputLimitBytes,
		},
		Scenarios: make([]ScenarioResult, 0, len(scenarios)),
	}

	for _, scenario := range scenarios {
		result := runScenario(ctx, options, scenario)
		report.Scenarios = append(report.Scenarios, result)
		if result.Status == "passed" {
			report.Summary.Passed++
			continue
		}
		report.Summary.Failed++
		report.Ready = false
	}
	report.DurationMillis = millisSince(runStart)
	if report.Ready {
		report.ReleaseGateEvidence = releaseEvidence(options.ArtifactURI)
	}
	return report, nil
}

func (o Options) withDefaults() Options {
	if o.WorkDir == "" {
		o.WorkDir = "."
	}
	if o.GoCommand == "" {
		o.GoCommand = "go"
	}
	if o.Count <= 0 {
		o.Count = 1
	}
	if o.RunnerProfile == "" {
		o.RunnerProfile = firstNonEmpty(os.Getenv("SCRAP_EVIDENCE_RUNNER_PROFILE"), "local-dedicated")
	}
	if o.FilesystemProfile == "" {
		o.FilesystemProfile = firstNonEmpty(os.Getenv("SCRAP_EVIDENCE_FILESYSTEM_PROFILE"), runtime.GOOS+"-local-filesystem-unverified")
	}
	if o.Seed == "" {
		o.Seed = firstNonEmpty(os.Getenv("SCRAP_EVIDENCE_SEED"), "deterministic")
	}
	if o.ArtifactURI == "" {
		o.ArtifactURI = "crash-fault-evidence-report"
	}
	if o.GeneratedAt.IsZero() {
		o.GeneratedAt = time.Now().UTC()
	}
	if o.OutputLimitBytes <= 0 {
		o.OutputLimitBytes = defaultOutputLimit
	}
	if o.Runner == nil {
		o.Runner = ExecRunner{}
	}
	return o
}

func runScenario(ctx context.Context, options Options, scenario Scenario) ScenarioResult {
	start := time.Now().UTC()
	result := ScenarioResult{
		ID:              scenario.ID,
		Name:            scenario.Name,
		Status:          "passed",
		ReadinessGates:  append([]string(nil), scenario.ReadinessGates...),
		ReleaseGateIDs:  append([]string(nil), scenario.ReleaseGateIDs...),
		Invariants:      append([]string(nil), scenario.Invariants...),
		FailureSchedule: scenario.FailureSchedule,
		Commands:        make([]CommandResult, 0, len(scenario.Commands)),
	}
	for _, testCommand := range scenario.Commands {
		commandStart := time.Now().UTC()
		args := goTestArgs(options, testCommand)
		output := options.Runner.Run(ctx, CommandInvocation{
			Name:    options.GoCommand,
			Args:    args,
			WorkDir: options.WorkDir,
		})
		status := "passed"
		if output.ExitCode != 0 {
			status = "failed"
			result.Status = "failed"
		}
		result.Commands = append(result.Commands, CommandResult{
			Package:        testCommand.Package,
			Pattern:        testCommand.Pattern,
			Command:        append([]string{options.GoCommand}, args...),
			Status:         status,
			ExitCode:       output.ExitCode,
			DurationMillis: millisSince(commandStart),
			Output:         sanitizeOutput(output.Stdout+"\n"+output.Stderr, options.OutputLimitBytes),
			Error:          output.Error,
		})
	}
	result.DurationMillis = millisSince(start)
	return result
}

func goTestArgs(options Options, command GoTestCommand) []string {
	args := []string{"test", "-count=" + strconv.Itoa(options.Count)}
	if options.Race {
		args = append(args, "-race")
	}
	args = append(args, "-run", command.Pattern, command.Package)
	return args
}

func releaseEvidence(artifactURI string) []releasegate.Evidence {
	gates := []string{"crash-fault", "peer-byte-durability", "backend-restore"}
	evidence := make([]releasegate.Evidence, 0, len(gates))
	for _, gate := range gates {
		evidence = append(evidence, releasegate.Evidence{
			GateID:   gate,
			Status:   releasegate.EvidencePassed,
			Artifact: artifactURI,
		})
	}
	return evidence
}

func collectGitState(ctx context.Context, workDir string) (string, bool, error) {
	sha, err := runGit(ctx, workDir, "rev-parse", "HEAD")
	if err != nil {
		return "", true, err
	}
	status, err := runGit(ctx, workDir, "status", "--porcelain")
	if err != nil {
		return "", true, err
	}
	return strings.TrimSpace(sha), strings.TrimSpace(status) != "", nil
}

func runGit(ctx context.Context, workDir string, args ...string) (string, error) {
	// #nosec G204 -- git probes are limited to fixed metadata commands assembled by this package.
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func sanitizeOutput(output string, limit int) string {
	replacements := []string{
		"PlaintextDEK", "[redacted-field]",
		"plaintext_dek", "[redacted-field]",
		"WrappedDEK", "[redacted-field]",
		"wrapped_dek", "[redacted-field]",
		"SCRAP_OPENBAO_TOKEN", "[redacted-env]",
	}
	sanitized := strings.NewReplacer(replacements...).Replace(strings.TrimSpace(output))
	if len(sanitized) <= limit {
		return sanitized
	}
	return sanitized[:limit] + "\n[truncated]"
}

func millisSince(start time.Time) int64 {
	return time.Since(start).Milliseconds()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func ValidateCatalog(scenarios []Scenario) error {
	seen := make(map[string]bool, len(scenarios))
	for _, scenario := range scenarios {
		if scenario.ID == "" || scenario.Name == "" || len(scenario.Commands) == 0 {
			return fmt.Errorf("scenario %#v must have id, name, and commands", scenario)
		}
		if seen[scenario.ID] {
			return fmt.Errorf("duplicate scenario id %q", scenario.ID)
		}
		seen[scenario.ID] = true
		if len(scenario.Invariants) == 0 || len(scenario.ReadinessGates) == 0 || len(scenario.ReleaseGateIDs) == 0 {
			return fmt.Errorf("scenario %s must declare invariants and gates", scenario.ID)
		}
		for _, command := range scenario.Commands {
			if command.Package == "" || command.Pattern == "" {
				return fmt.Errorf("scenario %s has incomplete command %#v", scenario.ID, command)
			}
		}
	}
	return nil
}

func ReleaseGateIDs(scenarios []Scenario) []string {
	seen := make(map[string]bool)
	for _, scenario := range scenarios {
		for _, gate := range scenario.ReleaseGateIDs {
			seen[gate] = true
		}
	}
	out := make([]string, 0, len(seen))
	for gate := range seen {
		out = append(out, gate)
	}
	slices.Sort(out)
	return out
}

func MarshalReport(report Report) ([]byte, error) {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
