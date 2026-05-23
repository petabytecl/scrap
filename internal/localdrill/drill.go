package localdrill

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/petabytecl/scrap/internal/capacitysample"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	scrapv1 "github.com/petabytecl/scrap/internal/gen/scrap/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/openbaosmoke"
)

const (
	ReportKind = "local-release-dr-drill-evidence"

	StatusPassed = "passed"
	StatusFailed = "failed"

	DefaultProfileID              = "scrap-prod-v1"
	DefaultEnvironmentID          = "local-kind"
	DefaultRunnerID               = "local-kind"
	DefaultImageIdentity          = "localhost/scrapd:local"
	DefaultPublicAddress          = "127.0.0.1:18080"
	DefaultAdminAddress           = "127.0.0.1:18081"
	DefaultPublicWorkloadIdentity = "local-public-client"
	DefaultAdminWorkloadIdentity  = "local-operator"
	DefaultCapacityReportPath     = "capacity-sample-advisory.json"
	DefaultOpenBaoReportPath      = "openbao-transit-smoke-evidence.json"
	DefaultReportPath             = "local-dr-drill-evidence.json"
	DefaultOperatorOwner          = "@cotocisternas"
	DefaultDuration               = 2 * time.Minute
	DefaultPollInterval           = time.Second
	DefaultFixtureSizeBytes       = 4 * 1024
	MaxFixtureSizeBytes           = 8 * 1024 * 1024
)

var ErrDrillFailed = errors.New("local DR drill evidence failed")

type FixtureWriter interface {
	WriteFixtureDocument(context.Context, FixtureDocument, []byte, Options) (FixtureWriteEvidence, error)
}

type DisasterRecoveryClient interface {
	GetRecoveryReadiness(context.Context, *adminv1.GetRecoveryReadinessRequest, ...grpc.CallOption) (*adminv1.GetRecoveryReadinessResponse, error)
	PlanRecovery(context.Context, *adminv1.PlanRecoveryRequest, ...grpc.CallOption) (*adminv1.PlanRecoveryResponse, error)
	StartMetadataRestore(context.Context, *adminv1.StartMetadataRestoreRequest, ...grpc.CallOption) (*adminv1.StartMetadataRestoreResponse, error)
	StartCopyVerify(context.Context, *adminv1.StartCopyVerifyRequest, ...grpc.CallOption) (*adminv1.StartCopyVerifyResponse, error)
	StartDRDrill(context.Context, *adminv1.StartDRDrillRequest, ...grpc.CallOption) (*adminv1.StartDRDrillResponse, error)
}

type OperationClient interface {
	GetOperation(context.Context, *adminv1.GetOperationRequest, ...grpc.CallOption) (*adminv1.GetOperationResponse, error)
}

type Clients struct {
	FixtureWriter FixtureWriter
	DR            DisasterRecoveryClient
	Operations    OperationClient
}

type Options struct {
	ReleaseSHA               string
	DirtyTree                string
	ProfileID                string
	EnvironmentID            string
	RunnerID                 string
	ImageIdentity            string
	PublicAddress            string
	AdminAddress             string
	PublicWorkloadIdentity   string
	AdminWorkloadIdentity    string
	CapacitySampleReportPath string
	OpenBaoSmokeReportPath   string
	OperatorOwner            string
	ApprovalState            string
	DrillID                  string
	SnapshotTargetID         string
	Duration                 time.Duration
	PollInterval             time.Duration
	FixtureSizeBytes         uint64
	Now                      func() time.Time
}

type Report struct {
	ReportKind                string                   `json:"report_kind"`
	GeneratedAt               string                   `json:"generated_at"`
	Status                    string                   `json:"status"`
	Release                   ReleaseIdentity          `json:"release"`
	DrillConfig               DrillConfig              `json:"drill_config"`
	OperatorApproval          OperatorApproval         `json:"operator_approval"`
	Runbook                   RunbookEvidence          `json:"runbook"`
	Fixture                   FixtureWriteEvidence     `json:"fixture"`
	Commands                  []CommandEvidence        `json:"commands"`
	Recovery                  RecoveryEvidence         `json:"recovery"`
	BackendArtifacts          BackendArtifactEvidence  `json:"backend_artifacts"`
	OpenBao                   OpenBaoEvidence          `json:"openbao"`
	FailedOrSkippedSteps      []StepResolutionRequired `json:"failed_or_skipped_steps"`
	BlockingIssues            []string                 `json:"blocking_issues"`
	ReleaseExceptions         []ReleaseException       `json:"release_exceptions"`
	RequiredFailureResolution string                   `json:"required_failure_resolution"`
	EvidenceLimits            []string                 `json:"evidence_limits"`
}

type ReleaseIdentity struct {
	ReleaseSHA    string `json:"release_sha"`
	ImageIdentity string `json:"image_identity"`
	DirtyTree     string `json:"dirty_tree"`
}

type DrillConfig struct {
	ProfileID              string `json:"profile_id"`
	EnvironmentID          string `json:"environment_id"`
	RunnerID               string `json:"runner_id"`
	PublicAddress          string `json:"public_addr"`
	AdminAddress           string `json:"admin_addr"`
	PublicWorkloadIdentity string `json:"public_workload_identity"`
	AdminWorkloadIdentity  string `json:"admin_workload_identity"`
	Issue                  string `json:"issue"`
	LocalEnvironmentIssue  string `json:"local_environment_issue"`
	OpenBaoIssue           string `json:"openbao_issue"`
	StopCondition          string `json:"stop_condition"`
}

type OperatorApproval struct {
	Owner      string `json:"owner"`
	State      string `json:"state"`
	ApprovedAt string `json:"approved_at"`
	Scope      string `json:"scope"`
}

type RunbookEvidence struct {
	DocumentPath       string   `json:"document_path"`
	Section            string   `json:"section"`
	CommandsRecorded   bool     `json:"commands_recorded"`
	LocalAdaptations   []string `json:"local_adaptations"`
	NoFormalRTOOrRPO   bool     `json:"no_formal_rto_or_rpo"`
	DownstreamApproval bool     `json:"downstream_approval"`
}

type FixtureDocument struct {
	TenantID      string `json:"tenant_id"`
	TransactionID string `json:"transaction_id"`
	DocumentName  string `json:"document_name"`
}

type FixtureWriteEvidence struct {
	Document            FixtureDocument `json:"document"`
	Bytes               uint64          `json:"bytes"`
	LatencyMillis       int64           `json:"latency_millis"`
	LogicalSHA256Digest string          `json:"logical_sha256_digest"`
	DesiredReplicas     uint32          `json:"desired_replicas,omitempty"`
	AchievedReplicas    uint32          `json:"achieved_replicas,omitempty"`
	Availability        string          `json:"availability,omitempty"`
	LifecycleState      string          `json:"lifecycle_state,omitempty"`
	ErrorClass          string          `json:"error_class,omitempty"`
	Error               string          `json:"error,omitempty"`
}

type CommandEvidence struct {
	Step           string             `json:"step"`
	Command        string             `json:"command"`
	StartedAt      string             `json:"started_at"`
	FinishedAt     string             `json:"finished_at"`
	DurationMillis int64              `json:"duration_millis"`
	Status         string             `json:"status"`
	PlanID         string             `json:"plan_id,omitempty"`
	PlanHash       string             `json:"plan_hash,omitempty"`
	OperationID    string             `json:"operation_id,omitempty"`
	OperationType  string             `json:"operation_type,omitempty"`
	OperationState string             `json:"operation_state,omitempty"`
	DryRun         bool               `json:"dry_run,omitempty"`
	Counters       map[string]string  `json:"counters,omitempty"`
	Warnings       []OperationWarning `json:"warnings,omitempty"`
	ErrorClass     string             `json:"error_class,omitempty"`
	Error          string             `json:"error,omitempty"`
}

type OperationWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type RecoveryEvidence struct {
	DrillID                      string            `json:"drill_id"`
	SnapshotTargetID             string            `json:"snapshot_target_id"`
	LatestRestorableCheckpointAt string            `json:"latest_restorable_checkpoint_at"`
	DryRunPlanID                 string            `json:"dry_run_plan_id"`
	DryRunPlanHash               string            `json:"dry_run_plan_hash"`
	PlanID                       string            `json:"plan_id"`
	PlanHash                     string            `json:"plan_hash"`
	OperationIDs                 map[string]string `json:"operation_ids"`
	MeasuredLocalRecoveryMillis  int64             `json:"measured_local_recovery_millis"`
	RecoveryPointEvidence        map[string]string `json:"recovery_point_evidence"`
	MetadataImportProvedBy       string            `json:"metadata_import_proved_by"`
	NoFormalRTOPromise           bool              `json:"no_formal_rto_promise"`
	NoFormalRPOPromise           bool              `json:"no_formal_rpo_promise"`
	ReleaseArtifactRehearsalOnly bool              `json:"release_artifact_rehearsal_only"`
	DownstreamDeploymentApproval bool              `json:"downstream_deployment_approval"`
}

type BackendArtifactEvidence struct {
	CapacitySampleSourcePath string                      `json:"capacity_sample_source_path"`
	LocalStackProbe          LocalStackProbeEvidence     `json:"localstack_probe"`
	PublishedCheckpoint      PublishedCheckpointEvidence `json:"published_checkpoint"`
}

type LocalStackProbeEvidence struct {
	ReportKind         string         `json:"report_kind"`
	ProfileID          string         `json:"profile_id"`
	EnvironmentID      string         `json:"environment_id"`
	Provider           string         `json:"provider"`
	AdvisoryOnly       bool           `json:"advisory_only"`
	OperationCounts    map[string]int `json:"operation_counts"`
	SuccessCount       int            `json:"success_count"`
	ErrorCount         int            `json:"error_count"`
	RedactedRequestIDs []string       `json:"redacted_request_ids"`
	Passed             bool           `json:"passed"`
}

type PublishedCheckpointEvidence struct {
	ManifestID              string `json:"manifest_id,omitempty"`
	Generation              string `json:"generation,omitempty"`
	VerifiedArtifacts       string `json:"verified_artifacts,omitempty"`
	VerifiedRequiredObjects string `json:"verified_required_objects,omitempty"`
	VerifiedBlockObjects    string `json:"verified_block_objects,omitempty"`
	VerifiedIndexObjects    string `json:"verified_index_objects,omitempty"`
	VerifiedEnvelopeObjects string `json:"verified_envelope_objects,omitempty"`
	BlocksRestored          string `json:"blocks_restored,omitempty"`
	Documents               string `json:"documents,omitempty"`
	UploadIntents           string `json:"upload_intents,omitempty"`
}

type OpenBaoEvidence struct {
	SourcePath                string                     `json:"source_path"`
	ReportKind                string                     `json:"report_kind"`
	Status                    string                     `json:"status"`
	Namespace                 string                     `json:"namespace"`
	Deployment                string                     `json:"deployment"`
	KubernetesClient          string                     `json:"kubernetes_client"`
	TransitKeyPath            string                     `json:"transit_key_path"`
	AuditDeviceStatus         string                     `json:"audit_device_status"`
	DataKeyVersion            uint32                     `json:"data_key_version"`
	RewrapVersion             uint32                     `json:"rewrap_version"`
	KeyVersionAfter           uint32                     `json:"key_version_after"`
	PlaintextDEKRedacted      bool                       `json:"plaintext_dek_redacted"`
	WrappedDEKRedacted        bool                       `json:"wrapped_dek_redacted"`
	CryptoUnavailableOutcomes []CryptoUnavailableOutcome `json:"crypto_unavailable_outcomes"`
	Passed                    bool                       `json:"passed"`
}

type CryptoUnavailableOutcome struct {
	Name        string `json:"name"`
	Status      string `json:"status"`
	ErrorClass  string `json:"error_class"`
	Description string `json:"description"`
}

type StepResolutionRequired struct {
	Step                    string `json:"step"`
	Reason                  string `json:"reason"`
	BlockingIssueRequired   bool   `json:"blocking_issue_required"`
	ReleaseExceptionAllowed bool   `json:"release_exception_allowed"`
}

type ReleaseException struct {
	Owner     string `json:"owner"`
	ExpiresAt string `json:"expires_at"`
	Reason    string `json:"reason"`
}

type GRPCFixtureWriter struct {
	Client scrapv1.DocumentServiceClient
}

func (w GRPCFixtureWriter) WriteFixtureDocument(ctx context.Context, doc FixtureDocument, data []byte, opts Options) (FixtureWriteEvidence, error) {
	started := time.Now()
	size := uint64(len(data))
	sum := sha256.Sum256(data)
	evidence := FixtureWriteEvidence{
		Document:            doc,
		Bytes:               size,
		LogicalSHA256Digest: hex.EncodeToString(sum[:]),
	}
	if w.Client == nil {
		return failedFixtureWrite(evidence, started, errors.New("local DR drill requires document client"))
	}
	stream, err := w.Client.WriteDocument(ctx)
	if err != nil {
		return failedFixtureWrite(evidence, started, err)
	}
	contentType := "application/octet-stream"
	workflowStage := "local-dr-drill"
	idempotencyKey := "local-dr-drill-" + opts.DrillID
	if err := stream.Send(&scrapv1.WriteDocumentRequest{
		Message: &scrapv1.WriteDocumentRequest_Init{Init: &scrapv1.WriteDocumentInit{
			Identity: &scrapv1.DocumentIdentity{
				TenantId:      doc.TenantID,
				TransactionId: doc.TransactionID,
				DocumentName:  doc.DocumentName,
			},
			DocumentClass:        scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
			PriorityClass:        scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
			ContentType:          &contentType,
			ExpectedLength:       &size,
			ExpectedSha256:       sum[:],
			ClientIdempotencyKey: &idempotencyKey,
			CreatedByService:     "scrap-local-dr-drill-evidence",
			WorkflowStage:        &workflowStage,
			Tags: map[string]string{
				"issue":    "88",
				"profile":  opts.ProfileID,
				"drill_id": opts.DrillID,
				"evidence": ReportKind,
			},
		}},
	}); err != nil {
		return failedFixtureWrite(evidence, started, err)
	}
	if err := stream.Send(&scrapv1.WriteDocumentRequest{
		Message: &scrapv1.WriteDocumentRequest_Chunk{Chunk: &scrapv1.WriteDocumentChunk{Data: data}},
	}); err != nil {
		return failedFixtureWrite(evidence, started, err)
	}
	resp, err := stream.CloseAndRecv()
	if err != nil {
		return failedFixtureWrite(evidence, started, err)
	}
	metadata := resp.GetMetadata()
	evidence.LatencyMillis = durationMillis(time.Since(started))
	evidence.DesiredReplicas = resp.GetDesiredReplicaCount()
	evidence.AchievedReplicas = resp.GetAchievedReplicaCount()
	evidence.Availability = metadata.GetAvailability().String()
	evidence.LifecycleState = metadata.GetLifecycleState().String()
	return evidence, nil
}

func Run(ctx context.Context, opts Options, clients Clients) (Report, error) {
	opts, err := ValidateOptions(opts)
	if err != nil {
		return Report{}, err
	}
	if clients.FixtureWriter == nil {
		return Report{}, errors.New("local DR drill requires fixture writer")
	}
	if clients.DR == nil {
		return Report{}, errors.New("local DR drill requires disaster recovery client")
	}
	if clients.Operations == nil {
		return Report{}, errors.New("local DR drill requires operation client")
	}
	capacityReport, err := loadCapacityReport(opts.CapacitySampleReportPath)
	if err != nil {
		return Report{}, err
	}
	openbaoReport, err := loadOpenBaoReport(opts.OpenBaoSmokeReportPath)
	if err != nil {
		return Report{}, err
	}

	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	generatedAt := now().UTC()
	report := newReport(opts, generatedAt, summarizeCapacity(opts.CapacitySampleReportPath, capacityReport), summarizeOpenBao(opts.OpenBaoSmokeReportPath, openbaoReport))
	if !report.BackendArtifacts.LocalStackProbe.Passed {
		report.addFailure("localstack-backend-artifacts", "capacity sample does not contain successful LocalStack PUT/HEAD/GET backend probes")
	}
	if !report.OpenBao.Passed {
		report.addFailure("openbao-key-material", "OpenBao smoke report does not prove key material and crypto-unavailable outcomes")
	}
	if report.Status == StatusFailed {
		return report, ErrDrillFailed
	}

	runCtx, cancel := context.WithTimeout(ctx, opts.Duration)
	defer cancel()
	runStarted := time.Now()
	fixture := FixtureDocument{
		TenantID:      "local-dr-drill",
		TransactionID: opts.DrillID,
		DocumentName:  "issue-88/fixture.bin",
	}
	fixtureData := sampleBytes(opts.FixtureSizeBytes)
	fixtureStarted := time.Now()
	report.Fixture, err = clients.FixtureWriter.WriteFixtureDocument(runCtx, fixture, fixtureData, opts)
	fixtureResult := commandResult{Status: StatusPassed}
	if err != nil {
		fixtureResult = commandError(err)
	}
	report.Commands = append(report.Commands, commandEvidence(fixtureStarted, "fixture-write", fixtureCommand(opts, fixture), fixtureResult))
	if err != nil {
		report.addFailure("fixture-write", err.Error())
		return report, ErrDrillFailed
	}

	readiness, command := waitForReadinessCommand(runCtx, opts, clients.DR, fixtureStarted)
	report.Commands = append(report.Commands, command)
	if command.Status != StatusPassed {
		report.addFailure("inspect recovery-readiness", command.Error)
		return report, ErrDrillFailed
	}
	checkpointAt := readiness.GetLatestRestorableCheckpointAt()
	if checkpointAt == nil {
		report.addFailure("inspect recovery-readiness", "recovery readiness did not include latest restorable checkpoint timestamp")
		return report, ErrDrillFailed
	}
	report.Recovery.LatestRestorableCheckpointAt = checkpointAt.AsTime().UTC().Format(time.RFC3339)

	dryPlan, command := planRecoveryCommand(runCtx, opts, clients.DR, true)
	report.Commands = append(report.Commands, command)
	if command.Status != StatusPassed {
		report.addFailure("plan recovery dry-run", command.Error)
		return report, ErrDrillFailed
	}
	report.Recovery.DryRunPlanID = dryPlan.GetOperationPlanId()
	report.Recovery.DryRunPlanHash = dryPlan.GetPlanHash()

	metadataRestore, command := startOperationCommand(runCtx, opts, clients.DR, dryPlan, "metadata-restore")
	report.Commands = append(report.Commands, command)
	if command.Status != StatusPassed {
		report.addFailure("start metadata-restore", command.Error)
		return report, ErrDrillFailed
	}
	report.Recovery.OperationIDs["metadata_restore"] = metadataRestore.GetOperationId()
	_, command = waitOperationCommand(runCtx, opts, clients.Operations, metadataRestore.GetOperationId(), "metadata-restore")
	report.Commands = append(report.Commands, command)
	if command.Status != StatusPassed {
		report.addFailure("watch metadata-restore", command.Error)
		return report, ErrDrillFailed
	}

	plan, command := planRecoveryCommand(runCtx, opts, clients.DR, false)
	report.Commands = append(report.Commands, command)
	if command.Status != StatusPassed {
		report.addFailure("plan recovery", command.Error)
		return report, ErrDrillFailed
	}
	report.Recovery.PlanID = plan.GetOperationPlanId()
	report.Recovery.PlanHash = plan.GetPlanHash()

	copyVerify, command := startOperationCommand(runCtx, opts, clients.DR, plan, "copy-verify")
	report.Commands = append(report.Commands, command)
	if command.Status != StatusPassed {
		report.addFailure("start copy-verify", command.Error)
		return report, ErrDrillFailed
	}
	report.Recovery.OperationIDs["copy_verify"] = copyVerify.GetOperationId()
	copyVerify, command = waitOperationCommand(runCtx, opts, clients.Operations, copyVerify.GetOperationId(), "copy-verify")
	report.Commands = append(report.Commands, command)
	if command.Status != StatusPassed {
		report.addFailure("watch copy-verify", command.Error)
		return report, ErrDrillFailed
	}
	report.BackendArtifacts.PublishedCheckpoint = checkpointEvidence(copyVerify.GetProgress().GetCounters())

	drill, command := startOperationCommand(runCtx, opts, clients.DR, plan, "dr-drill")
	report.Commands = append(report.Commands, command)
	if command.Status != StatusPassed {
		report.addFailure("start dr-drill", command.Error)
		return report, ErrDrillFailed
	}
	report.Recovery.OperationIDs["dr_drill"] = drill.GetOperationId()
	drill, command = waitOperationCommand(runCtx, opts, clients.Operations, drill.GetOperationId(), "dr-drill")
	report.Commands = append(report.Commands, command)
	if command.Status != StatusPassed {
		report.addFailure("watch dr-drill", command.Error)
		return report, ErrDrillFailed
	}
	report.Recovery.MeasuredLocalRecoveryMillis = durationMillis(time.Since(runStarted))
	report.Recovery.RecoveryPointEvidence = cloneCounters(drill.GetProgress().GetCounters())
	report.Recovery.MetadataImportProvedBy = "metadata-restore dry-run verifies the published checkpoint and dr-drill imports it into a fresh scratch metadata store before restoring backend bytes"
	report.BackendArtifacts.PublishedCheckpoint = mergeCheckpointEvidence(report.BackendArtifacts.PublishedCheckpoint, checkpointEvidence(drill.GetProgress().GetCounters()))
	return report, nil
}

func ValidateOptions(opts Options) (Options, error) {
	opts.ReleaseSHA = defaultText(opts.ReleaseSHA, "unknown")
	opts.DirtyTree = defaultText(opts.DirtyTree, "unknown")
	opts.ProfileID = defaultText(opts.ProfileID, DefaultProfileID)
	opts.EnvironmentID = defaultText(opts.EnvironmentID, DefaultEnvironmentID)
	opts.RunnerID = defaultText(opts.RunnerID, DefaultRunnerID)
	opts.ImageIdentity = defaultText(opts.ImageIdentity, DefaultImageIdentity)
	opts.PublicAddress = defaultText(opts.PublicAddress, DefaultPublicAddress)
	opts.AdminAddress = defaultText(opts.AdminAddress, DefaultAdminAddress)
	opts.PublicWorkloadIdentity = defaultText(opts.PublicWorkloadIdentity, DefaultPublicWorkloadIdentity)
	opts.AdminWorkloadIdentity = defaultText(opts.AdminWorkloadIdentity, DefaultAdminWorkloadIdentity)
	opts.CapacitySampleReportPath = defaultText(opts.CapacitySampleReportPath, DefaultCapacityReportPath)
	opts.OpenBaoSmokeReportPath = defaultText(opts.OpenBaoSmokeReportPath, DefaultOpenBaoReportPath)
	opts.OperatorOwner = defaultText(opts.OperatorOwner, DefaultOperatorOwner)
	opts.ApprovalState = defaultText(opts.ApprovalState, "approved-local-release-artifact-rehearsal")
	if opts.DrillID == "" {
		now := time.Now().UTC()
		if opts.Now != nil {
			now = opts.Now().UTC()
		}
		opts.DrillID = "local-dr-" + now.Format("20060102T150405Z")
	}
	opts.SnapshotTargetID = defaultText(opts.SnapshotTargetID, "latest-restorable-checkpoint")
	if opts.Duration == 0 {
		opts.Duration = DefaultDuration
	}
	if opts.PollInterval == 0 {
		opts.PollInterval = DefaultPollInterval
	}
	if opts.FixtureSizeBytes == 0 {
		opts.FixtureSizeBytes = DefaultFixtureSizeBytes
	}
	if strings.TrimSpace(opts.PublicWorkloadIdentity) == "" {
		return Options{}, errors.New("local DR drill requires public workload identity")
	}
	if strings.TrimSpace(opts.AdminWorkloadIdentity) == "" {
		return Options{}, errors.New("local DR drill requires admin workload identity")
	}
	if opts.Duration <= 0 {
		return Options{}, errors.New("local DR drill duration must be positive")
	}
	if opts.PollInterval <= 0 {
		return Options{}, errors.New("local DR drill poll interval must be positive")
	}
	if opts.FixtureSizeBytes > MaxFixtureSizeBytes {
		return Options{}, fmt.Errorf("local DR drill fixture size must be no more than %d bytes", MaxFixtureSizeBytes)
	}
	return opts, nil
}

func newReport(opts Options, generatedAt time.Time, backend BackendArtifactEvidence, openbao OpenBaoEvidence) Report {
	return Report{
		ReportKind:  ReportKind,
		GeneratedAt: generatedAt.Format(time.RFC3339),
		Status:      StatusPassed,
		Release: ReleaseIdentity{
			ReleaseSHA:    opts.ReleaseSHA,
			ImageIdentity: opts.ImageIdentity,
			DirtyTree:     opts.DirtyTree,
		},
		DrillConfig: DrillConfig{
			ProfileID:              opts.ProfileID,
			EnvironmentID:          opts.EnvironmentID,
			RunnerID:               opts.RunnerID,
			PublicAddress:          opts.PublicAddress,
			AdminAddress:           opts.AdminAddress,
			PublicWorkloadIdentity: opts.PublicWorkloadIdentity,
			AdminWorkloadIdentity:  opts.AdminWorkloadIdentity,
			Issue:                  "#88",
			LocalEnvironmentIssue:  "#99",
			OpenBaoIssue:           "#86",
			StopCondition:          "metadata-restore, copy-verify, and dr-drill operation evidence reaches terminal succeeded state",
		},
		OperatorApproval: OperatorApproval{
			Owner:      opts.OperatorOwner,
			State:      opts.ApprovalState,
			ApprovedAt: generatedAt.Format(time.RFC3339),
			Scope:      "local release-artifact DR rehearsal for #88; not production RTO/RPO or downstream deployment approval",
		},
		Runbook: RunbookEvidence{
			DocumentPath:     "docs/storage-gateway-operator-runbooks.md",
			Section:          "DR Rebuild Drill",
			CommandsRecorded: true,
			LocalAdaptations: []string{
				"the local-kind target is represented by the dr-drill operation's fresh scratch metadata store",
				"metadata-restore is run from the dry-run recovery plan to verify published checkpoint import without mutating the source cell",
				"the watch command is recorded as durable operation status polling because the current local watch RPC emits snapshots",
			},
			NoFormalRTOOrRPO:   true,
			DownstreamApproval: false,
		},
		Recovery: RecoveryEvidence{
			DrillID:                      opts.DrillID,
			SnapshotTargetID:             opts.SnapshotTargetID,
			OperationIDs:                 map[string]string{},
			RecoveryPointEvidence:        map[string]string{},
			NoFormalRTOPromise:           true,
			NoFormalRPOPromise:           true,
			ReleaseArtifactRehearsalOnly: true,
			DownstreamDeploymentApproval: false,
		},
		BackendArtifacts:          backend,
		OpenBao:                   openbao,
		FailedOrSkippedSteps:      []StepResolutionRequired{},
		BlockingIssues:            []string{},
		ReleaseExceptions:         []ReleaseException{},
		RequiredFailureResolution: "Any failed or skipped drill step must be linked to a blocking issue or approved release exception with owner and expiry before release signoff.",
		EvidenceLimits: []string{
			"local DR drill evidence is release-artifact rehearsal evidence only",
			"local DR drill evidence does not approve live production RTO or RPO",
			"local DR drill evidence does not approve downstream deployment, provider account, or OpenBao HA operations",
			"evidence redacts document bytes, OpenBao tokens, backend credentials, plaintext DEKs, wrapped DEKs, and customer payloads",
		},
	}
}

func (r *Report) addFailure(step, reason string) {
	r.Status = StatusFailed
	r.FailedOrSkippedSteps = append(r.FailedOrSkippedSteps, StepResolutionRequired{
		Step:                    step,
		Reason:                  reason,
		BlockingIssueRequired:   true,
		ReleaseExceptionAllowed: true,
	})
}

type commandResult struct {
	Status         string
	PlanID         string
	PlanHash       string
	OperationID    string
	OperationType  string
	OperationState string
	DryRun         bool
	Counters       map[string]string
	Warnings       []OperationWarning
	ErrorClass     string
	Error          string
}

func waitForReadinessCommand(ctx context.Context, opts Options, client DisasterRecoveryClient, notBefore time.Time) (*adminv1.RecoveryReadiness, CommandEvidence) {
	started := time.Now()
	deadline := started.Add(opts.Duration)
	var last *adminv1.RecoveryReadiness
	var lastErr error
	for {
		resp, err := client.GetRecoveryReadiness(ctx, &adminv1.GetRecoveryReadinessRequest{})
		if err != nil {
			lastErr = err
		} else {
			last = resp.GetReadiness()
			if last.GetReady() && checkpointFreshEnough(last, notBefore) {
				return last, commandEvidence(started, "inspect recovery-readiness", inspectReadinessCommand(opts), commandResult{
					Status:   StatusPassed,
					Warnings: warningsFromProto(last.GetWarnings()),
				})
			}
		}
		if time.Now().After(deadline) {
			err := lastErr
			if err == nil {
				err = errors.New("recovery readiness did not publish a checkpoint after the fixture write before timeout")
			}
			return last, commandEvidence(started, "inspect recovery-readiness", inspectReadinessCommand(opts), commandError(err))
		}
		select {
		case <-ctx.Done():
			return last, commandEvidence(started, "inspect recovery-readiness", inspectReadinessCommand(opts), commandError(ctx.Err()))
		case <-time.After(opts.PollInterval):
		}
	}
}

func checkpointFreshEnough(readiness *adminv1.RecoveryReadiness, notBefore time.Time) bool {
	checkpointAt := readiness.GetLatestRestorableCheckpointAt()
	if checkpointAt == nil {
		return false
	}
	if notBefore.IsZero() {
		return true
	}
	return !checkpointAt.AsTime().Before(notBefore.UTC())
}

func planRecoveryCommand(ctx context.Context, opts Options, client DisasterRecoveryClient, dryRun bool) (*adminv1.OperationPlan, CommandEvidence) {
	started := time.Now()
	command := planRecoveryCLI(opts, dryRun)
	resp, err := client.PlanRecovery(ctx, &adminv1.PlanRecoveryRequest{
		Targets: []*adminv1.Target{snapshotTarget(opts.SnapshotTargetID)},
		DryRun:  dryRun,
		Metadata: map[string]string{
			"drill_id": opts.DrillID,
			"issue":    "88",
			"profile":  opts.ProfileID,
			"scope":    "local-release-artifact-rehearsal",
		},
	})
	if err != nil {
		return nil, commandEvidence(started, stepName("plan recovery", dryRun), command, commandError(err))
	}
	plan := resp.GetPlan()
	return plan, commandEvidence(started, stepName("plan recovery", dryRun), command, commandResult{
		Status:   StatusPassed,
		PlanID:   plan.GetOperationPlanId(),
		PlanHash: plan.GetPlanHash(),
		DryRun:   dryRun,
		Warnings: warningsFromProto(plan.GetWarnings()),
	})
}

func startOperationCommand(ctx context.Context, opts Options, client DisasterRecoveryClient, plan *adminv1.OperationPlan, operationType string) (*adminv1.Operation, CommandEvidence) {
	started := time.Now()
	if plan == nil {
		err := errors.New("operation plan is missing")
		return nil, commandEvidence(started, "start "+operationType, startCommand(opts, operationType, "", "", ""), commandError(err))
	}
	operationID, err := identity.NewUUIDv7()
	if err != nil {
		return nil, commandEvidence(started, "start "+operationType, startCommand(opts, operationType, plan.GetOperationPlanId(), plan.GetPlanHash(), ""), commandError(err))
	}
	metadata := map[string]string{
		"drill_id": opts.DrillID,
		"issue":    "88",
		"profile":  opts.ProfileID,
	}
	var operation *adminv1.Operation
	switch operationType {
	case "metadata-restore":
		metadata["local_rehearsal_target"] = "dry-run-source-safe"
		resp, err := client.StartMetadataRestore(ctx, &adminv1.StartMetadataRestoreRequest{
			OperationPlanId: plan.GetOperationPlanId(),
			PlanHash:        plan.GetPlanHash(),
			OperationId:     operationID,
			Metadata:        metadata,
		})
		if err != nil {
			return nil, commandEvidence(started, "start metadata-restore", startCommand(opts, operationType, plan.GetOperationPlanId(), plan.GetPlanHash(), operationID), commandError(err))
		}
		operation = resp.GetOperation()
	case "copy-verify":
		resp, err := client.StartCopyVerify(ctx, &adminv1.StartCopyVerifyRequest{
			OperationPlanId: plan.GetOperationPlanId(),
			PlanHash:        plan.GetPlanHash(),
			OperationId:     operationID,
			Metadata:        metadata,
		})
		if err != nil {
			return nil, commandEvidence(started, "start copy-verify", startCommand(opts, operationType, plan.GetOperationPlanId(), plan.GetPlanHash(), operationID), commandError(err))
		}
		operation = resp.GetOperation()
	case "dr-drill":
		metadata["local_rehearsal_target"] = "fresh-scratch-target"
		resp, err := client.StartDRDrill(ctx, &adminv1.StartDRDrillRequest{
			OperationPlanId: plan.GetOperationPlanId(),
			PlanHash:        plan.GetPlanHash(),
			OperationId:     operationID,
			Metadata:        metadata,
		})
		if err != nil {
			return nil, commandEvidence(started, "start dr-drill", startCommand(opts, operationType, plan.GetOperationPlanId(), plan.GetPlanHash(), operationID), commandError(err))
		}
		operation = resp.GetOperation()
	default:
		err := fmt.Errorf("unsupported operation type %q", operationType)
		return nil, commandEvidence(started, "start "+operationType, startCommand(opts, operationType, plan.GetOperationPlanId(), plan.GetPlanHash(), operationID), commandError(err))
	}
	return operation, commandEvidence(started, "start "+operationType, startCommand(opts, operationType, plan.GetOperationPlanId(), plan.GetPlanHash(), operationID), operationCommandResult(operation))
}

func waitOperationCommand(ctx context.Context, opts Options, client OperationClient, operationID, operationType string) (*adminv1.Operation, CommandEvidence) {
	started := time.Now()
	command := watchCommand(opts, operationID)
	for {
		resp, err := client.GetOperation(ctx, &adminv1.GetOperationRequest{OperationId: operationID})
		if err != nil {
			return nil, commandEvidence(started, "watch "+operationType, command, commandError(err))
		}
		operation := resp.GetOperation()
		if isTerminal(operation.GetState()) {
			result := operationCommandResult(operation)
			if operation.GetState() != adminv1.OperationState_OPERATION_STATE_SUCCEEDED {
				err := operation.GetLastError()
				result.Status = StatusFailed
				result.ErrorClass = err.GetCode()
				result.Error = err.GetMessage()
			}
			return operation, commandEvidence(started, "watch "+operationType, command, result)
		}
		select {
		case <-ctx.Done():
			return operation, commandEvidence(started, "watch "+operationType, command, commandError(ctx.Err()))
		case <-time.After(opts.PollInterval):
		}
	}
}

func operationCommandResult(operation *adminv1.Operation) commandResult {
	state := operation.GetState()
	result := commandResult{
		Status:         StatusPassed,
		OperationID:    operation.GetOperationId(),
		OperationType:  operation.GetOperationType(),
		OperationState: state.String(),
		DryRun:         operation.GetDryRun(),
		Counters:       cloneCounters(operation.GetProgress().GetCounters()),
		Warnings:       warningsFromProto(operation.GetWarnings()),
	}
	if state == adminv1.OperationState_OPERATION_STATE_FAILED ||
		state == adminv1.OperationState_OPERATION_STATE_CANCELED ||
		state == adminv1.OperationState_OPERATION_STATE_EXPIRED {
		result.Status = StatusFailed
		result.ErrorClass = operation.GetLastError().GetCode()
		result.Error = operation.GetLastError().GetMessage()
	}
	return result
}

func isTerminal(state adminv1.OperationState) bool {
	switch state {
	case adminv1.OperationState_OPERATION_STATE_SUCCEEDED,
		adminv1.OperationState_OPERATION_STATE_FAILED,
		adminv1.OperationState_OPERATION_STATE_CANCELED,
		adminv1.OperationState_OPERATION_STATE_EXPIRED:
		return true
	default:
		return false
	}
}

func commandEvidence(started time.Time, step, command string, result commandResult) CommandEvidence {
	finished := time.Now()
	return CommandEvidence{
		Step:           step,
		Command:        command,
		StartedAt:      started.UTC().Format(time.RFC3339),
		FinishedAt:     finished.UTC().Format(time.RFC3339),
		DurationMillis: durationMillis(finished.Sub(started)),
		Status:         result.Status,
		PlanID:         result.PlanID,
		PlanHash:       result.PlanHash,
		OperationID:    result.OperationID,
		OperationType:  result.OperationType,
		OperationState: result.OperationState,
		DryRun:         result.DryRun,
		Counters:       result.Counters,
		Warnings:       result.Warnings,
		ErrorClass:     result.ErrorClass,
		Error:          result.Error,
	}
}

func commandError(err error) commandResult {
	return commandResult{
		Status:     StatusFailed,
		ErrorClass: classifyError(err),
		Error:      err.Error(),
	}
}

func fixtureCommand(opts Options, doc FixtureDocument) string {
	return fmt.Sprintf("scrap-local-dr-drill-evidence fixture-write --public-addr %q --document %q --bytes %d", opts.PublicAddress, doc.TenantID+"/"+doc.TransactionID+"/"+doc.DocumentName, opts.FixtureSizeBytes)
}

func inspectReadinessCommand(opts Options) string {
	return fmt.Sprintf("scrapctl --admin-addr %q inspect recovery-readiness", opts.AdminAddress)
}

func planRecoveryCLI(opts Options, dryRun bool) string {
	parts := []string{
		"scrapctl",
		"--admin-addr", quote(opts.AdminAddress),
		"plan", "recovery",
		"--target", quote("snapshot:" + opts.SnapshotTargetID),
		"--metadata", quote("drill_id=" + opts.DrillID),
	}
	if dryRun {
		parts = append(parts, "--dry-run")
	}
	return strings.Join(parts, " ")
}

func startCommand(opts Options, operationType, planID, planHash, operationID string) string {
	return strings.Join([]string{
		"scrapctl",
		"--admin-addr", quote(opts.AdminAddress),
		"start", operationType,
		"--plan-id", quote(planID),
		"--plan-hash", quote(planHash),
		"--operation-id", quote(operationID),
		"--metadata", quote("drill_id=" + opts.DrillID),
	}, " ")
}

func watchCommand(opts Options, operationID string) string {
	return strings.Join([]string{
		"scrapctl",
		"--admin-addr", quote(opts.AdminAddress),
		"watch",
		"--operation-id", quote(operationID),
	}, " ")
}

func quote(value string) string {
	return strconv.Quote(value)
}

func stepName(base string, dryRun bool) string {
	if dryRun {
		return base + " dry-run"
	}
	return base
}

func snapshotTarget(snapshotID string) *adminv1.Target {
	shardID := "local"
	return &adminv1.Target{Target: &adminv1.Target_Snapshot{Snapshot: &adminv1.SnapshotTarget{
		ShardId:    &shardID,
		SnapshotId: snapshotID,
	}}}
}

func warningsFromProto(warnings []*adminv1.OperationWarning) []OperationWarning {
	out := make([]OperationWarning, 0, len(warnings))
	for _, warning := range warnings {
		out = append(out, OperationWarning{
			Code:    warning.GetCode(),
			Message: warning.GetMessage(),
		})
	}
	return out
}

func loadCapacityReport(path string) (capacitysample.Report, error) {
	file, err := os.Open(path) // #nosec G304 -- operator-provided evidence input path.
	if err != nil {
		return capacitysample.Report{}, fmt.Errorf("open capacity sample report: %w", err)
	}
	defer func() { _ = file.Close() }()
	var report capacitysample.Report
	if err := json.NewDecoder(file).Decode(&report); err != nil {
		return capacitysample.Report{}, fmt.Errorf("decode capacity sample report: %w", err)
	}
	if report.ReportKind != capacitysample.ReportKind {
		return capacitysample.Report{}, fmt.Errorf("capacity sample report kind = %q, want %q", report.ReportKind, capacitysample.ReportKind)
	}
	return report, nil
}

func loadOpenBaoReport(path string) (openbaosmoke.Report, error) {
	file, err := os.Open(path) // #nosec G304 -- operator-provided evidence input path.
	if err != nil {
		return openbaosmoke.Report{}, fmt.Errorf("open OpenBao smoke report: %w", err)
	}
	defer func() { _ = file.Close() }()
	var report openbaosmoke.Report
	if err := json.NewDecoder(file).Decode(&report); err != nil {
		return openbaosmoke.Report{}, fmt.Errorf("decode OpenBao smoke report: %w", err)
	}
	if report.ReportKind != openbaosmoke.ReportKind {
		return openbaosmoke.Report{}, fmt.Errorf("OpenBao smoke report kind = %q, want %q", report.ReportKind, openbaosmoke.ReportKind)
	}
	return report, nil
}

func summarizeCapacity(path string, report capacitysample.Report) BackendArtifactEvidence {
	operationCounts := make(map[string]int)
	successCount := 0
	errorCount := 0
	for _, sample := range report.Backend {
		operation := strings.ToUpper(sample.Operation)
		operationCounts[operation]++
		if sample.ErrorClass == "" && sample.StatusCode >= 200 && sample.StatusCode < 300 {
			successCount++
			continue
		}
		errorCount++
	}
	return BackendArtifactEvidence{
		CapacitySampleSourcePath: path,
		LocalStackProbe: LocalStackProbeEvidence{
			ReportKind:         report.ReportKind,
			ProfileID:          report.ProfileID,
			EnvironmentID:      report.EnvironmentID,
			Provider:           report.ProposedProfile.Provider,
			AdvisoryOnly:       report.AdvisoryOnly,
			OperationCounts:    operationCounts,
			SuccessCount:       successCount,
			ErrorCount:         errorCount,
			RedactedRequestIDs: sortedStrings(report.RedactedRequestIDs),
			Passed:             errorCount == 0 && operationCounts["PUT"] > 0 && operationCounts["HEAD"] > 0 && operationCounts["GET"] > 0,
		},
	}
}

func summarizeOpenBao(path string, report openbaosmoke.Report) OpenBaoEvidence {
	outcomes := make([]CryptoUnavailableOutcome, 0, len(report.CryptoUnavailableOutcomes))
	passedOutcomes := len(report.CryptoUnavailableOutcomes) > 0
	for _, outcome := range report.CryptoUnavailableOutcomes {
		outcomes = append(outcomes, CryptoUnavailableOutcome{
			Name:        outcome.Name,
			Status:      outcome.Status,
			ErrorClass:  outcome.ErrorClass,
			Description: outcome.Description,
		})
		if outcome.Status != "crypto-unavailable" || strings.TrimSpace(outcome.ErrorClass) == "" {
			passedOutcomes = false
		}
	}
	return OpenBaoEvidence{
		SourcePath:                path,
		ReportKind:                report.ReportKind,
		Status:                    report.Status,
		Namespace:                 report.Namespace,
		Deployment:                report.Deployment,
		KubernetesClient:          report.KubernetesClient,
		TransitKeyPath:            report.TransitKeyPath,
		AuditDeviceStatus:         report.AuditDeviceStatus,
		DataKeyVersion:            report.Transit.DataKeyVersion,
		RewrapVersion:             report.Transit.RewrapVersion,
		KeyVersionAfter:           report.Transit.KeyVersionAfter,
		PlaintextDEKRedacted:      report.Transit.PlaintextDEKRedacted,
		WrappedDEKRedacted:        report.Transit.WrappedDEKRedacted,
		CryptoUnavailableOutcomes: outcomes,
		Passed: report.Status == StatusPassed &&
			strings.HasPrefix(report.AuditDeviceStatus, "enabled:") &&
			report.Transit.DataKeyVersion > 0 &&
			report.Transit.UnwrapAADMatched &&
			report.Transit.RewrapAADMatched &&
			report.Transit.PlaintextDEKRedacted &&
			report.Transit.WrappedDEKRedacted &&
			passedOutcomes,
	}
}

func checkpointEvidence(counters map[string]string) PublishedCheckpointEvidence {
	return PublishedCheckpointEvidence{
		ManifestID:              counters["manifest_id"],
		Generation:              counters["generation"],
		VerifiedArtifacts:       counters["verified_artifacts"],
		VerifiedRequiredObjects: counters["verified_required_objects"],
		VerifiedBlockObjects:    counters["verified_block_objects"],
		VerifiedIndexObjects:    counters["verified_index_objects"],
		VerifiedEnvelopeObjects: counters["verified_envelope_objects"],
		BlocksRestored:          counters["blocks_restored"],
		Documents:               counters["documents"],
		UploadIntents:           counters["upload_intents"],
	}
}

func mergeCheckpointEvidence(first, second PublishedCheckpointEvidence) PublishedCheckpointEvidence {
	if second.ManifestID != "" {
		first.ManifestID = second.ManifestID
	}
	if second.Generation != "" {
		first.Generation = second.Generation
	}
	if second.VerifiedArtifacts != "" {
		first.VerifiedArtifacts = second.VerifiedArtifacts
	}
	if second.VerifiedRequiredObjects != "" {
		first.VerifiedRequiredObjects = second.VerifiedRequiredObjects
	}
	if second.VerifiedBlockObjects != "" {
		first.VerifiedBlockObjects = second.VerifiedBlockObjects
	}
	if second.VerifiedIndexObjects != "" {
		first.VerifiedIndexObjects = second.VerifiedIndexObjects
	}
	if second.VerifiedEnvelopeObjects != "" {
		first.VerifiedEnvelopeObjects = second.VerifiedEnvelopeObjects
	}
	if second.BlocksRestored != "" {
		first.BlocksRestored = second.BlocksRestored
	}
	if second.Documents != "" {
		first.Documents = second.Documents
	}
	if second.UploadIntents != "" {
		first.UploadIntents = second.UploadIntents
	}
	return first
}

func failedFixtureWrite(evidence FixtureWriteEvidence, started time.Time, err error) (FixtureWriteEvidence, error) {
	evidence.LatencyMillis = durationMillis(time.Since(started))
	evidence.ErrorClass = classifyError(err)
	evidence.Error = err.Error()
	return evidence, err
}

func classifyError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "transient"
	}
	if st, ok := status.FromError(err); ok {
		switch st.Code() {
		case codes.Unavailable, codes.DeadlineExceeded, codes.Aborted, codes.ResourceExhausted:
			return "transient"
		case codes.NotFound:
			return "not_found"
		case codes.Unauthenticated, codes.PermissionDenied:
			return "auth"
		case codes.AlreadyExists, codes.FailedPrecondition:
			return "conflict"
		case codes.InvalidArgument, codes.OutOfRange:
			return "permanent"
		default:
			return strings.ToLower(st.Code().String())
		}
	}
	return "permanent"
}

func sampleBytes(size uint64) []byte {
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(65 + (i % 26))
	}
	return data
}

func cloneCounters(counters map[string]string) map[string]string {
	if len(counters) == 0 {
		return nil
	}
	out := make(map[string]string, len(counters))
	for key, value := range counters {
		out[key] = value
	}
	return out
}

func sortedStrings(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

func durationMillis(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	return d.Milliseconds()
}

func defaultText(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
