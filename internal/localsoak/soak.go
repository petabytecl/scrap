package localsoak

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/capacitysample"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	scrapv1 "github.com/petabytecl/scrap/internal/gen/scrap/v1"
	"github.com/petabytecl/scrap/internal/safeconv"
)

const (
	ReportKind = "local-release-soak-evidence"

	StatusPassed = "passed"
	StatusFailed = "failed"

	DefaultProfileID                       = "scrap-prod-v1"
	DefaultEnvironmentID                   = "local-kind"
	DefaultRunnerID                        = "local-kind"
	DefaultImageIdentity                   = "localhost/scrapd:local"
	DefaultPublicAddress                   = "127.0.0.1:18080"
	DefaultAdminAddress                    = "127.0.0.1:18081"
	DefaultPublicWorkloadIdentity          = "local-public-client"
	DefaultAdminWorkloadIdentity           = "local-operator"
	DefaultCapacitySampleReportPath        = "capacity-sample-advisory.json"
	DefaultReportPath                      = "local-soak-evidence.json"
	DefaultDuration                        = 30 * time.Second
	DefaultSampleCount                     = 3
	DefaultDocumentSizeBytes        uint64 = 4 * 1024
	MaxSampleCount                         = 64
	MaxDocumentSizeBytes            uint64 = 8 * 1024 * 1024
	MaxDuration                            = 10 * time.Minute
)

type DocumentClient interface {
	WriteDocument(context.Context, ...grpc.CallOption) (grpc.ClientStreamingClient[scrapv1.WriteDocumentRequest, scrapv1.WriteDocumentResponse], error)
	HeadDocument(context.Context, *scrapv1.HeadDocumentRequest, ...grpc.CallOption) (*scrapv1.HeadDocumentResponse, error)
	ReadDocument(context.Context, *scrapv1.ReadDocumentRequest, ...grpc.CallOption) (grpc.ServerStreamingClient[scrapv1.ReadDocumentResponse], error)
}

type InspectClient interface {
	GetCapacityRunway(context.Context, *adminv1.GetCapacityRunwayRequest, ...grpc.CallOption) (*adminv1.GetCapacityRunwayResponse, error)
}

type OperationClient interface {
	ListOperations(context.Context, *adminv1.ListOperationsRequest, ...grpc.CallOption) (*adminv1.ListOperationsResponse, error)
}

type RepairClient interface {
	GetRepairQueue(context.Context, *adminv1.GetRepairQueueRequest, ...grpc.CallOption) (*adminv1.GetRepairQueueResponse, error)
}

type Clients struct {
	Documents  DocumentClient
	Inspect    InspectClient
	Operations OperationClient
	Repair     RepairClient
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
	SampleCount              int
	DocumentSizesBytes       []uint64
	Duration                 time.Duration
	Now                      func() time.Time
}

type Report struct {
	ReportKind                string                `json:"report_kind"`
	GeneratedAt               string                `json:"generated_at"`
	Status                    string                `json:"status"`
	Release                   ReleaseIdentity       `json:"release"`
	CampaignConfig            CampaignConfig        `json:"campaign_config"`
	Workload                  WorkloadShape         `json:"workload"`
	DurationMillis            int64                 `json:"duration_millis"`
	AdvisoryCapacitySample    CapacitySampleSummary `json:"advisory_capacity_sample"`
	Results                   Results               `json:"results"`
	ThresholdViolations       []ThresholdViolation  `json:"threshold_violations"`
	BlockingIssues            []string              `json:"blocking_issues"`
	ReleaseExceptions         []ReleaseException    `json:"release_exceptions"`
	RequiredFailureResolution string                `json:"required_failure_resolution"`
	EvidenceLimits            []string              `json:"evidence_limits"`
}

type ReleaseIdentity struct {
	ReleaseSHA    string `json:"release_sha"`
	ImageIdentity string `json:"image_identity"`
	DirtyTree     string `json:"dirty_tree"`
}

type CampaignConfig struct {
	ProfileID                       string `json:"profile_id"`
	EnvironmentID                   string `json:"environment_id"`
	RunnerID                        string `json:"runner_id"`
	PublicAddress                   string `json:"public_addr"`
	AdminAddress                    string `json:"admin_addr"`
	PublicWorkloadIdentity          string `json:"public_workload_identity"`
	AdminWorkloadIdentity           string `json:"admin_workload_identity"`
	RepoOwnedReleaseBoundariesIssue string `json:"repo_owned_release_boundaries_issue"`
	LocalEnvironmentIssue           string `json:"local_environment_issue"`
	AdvisoryThresholdIssue          string `json:"advisory_threshold_issue"`
	Issue                           string `json:"issue"`
}

type WorkloadShape struct {
	SampleCount              int                      `json:"sample_count"`
	DurationMillis           int64                    `json:"duration_millis"`
	DocumentSizeDistribution DocumentSizeDistribution `json:"document_size_distribution"`
	DocumentClass            string                   `json:"document_class"`
	PriorityClass            string                   `json:"priority_class"`
	Bounded                  bool                     `json:"bounded"`
	NonProductionOnly        bool                     `json:"non_production_only"`
	ActivePublicWrites       bool                     `json:"active_public_writes"`
	ActiveAdminReads         bool                     `json:"active_admin_reads"`
	ActiveAdminObservations  bool                     `json:"active_admin_observations"`
}

type DocumentSizeDistribution struct {
	ConfiguredSizesBytes []uint64 `json:"configured_sizes_bytes"`
	MinBytes             uint64   `json:"min_bytes"`
	MaxBytes             uint64   `json:"max_bytes"`
	TotalBytes           uint64   `json:"total_bytes"`
}

type CapacitySampleSummary struct {
	SourcePath                             string                                 `json:"source_path"`
	ReportKind                             string                                 `json:"report_kind"`
	ReleaseSHA                             string                                 `json:"release_sha"`
	DirtyTree                              string                                 `json:"dirty_tree"`
	ProfileID                              string                                 `json:"profile_id"`
	EnvironmentID                          string                                 `json:"environment_id"`
	AdvisoryOnly                           bool                                   `json:"advisory_only"`
	DoesNotSatisfyProductionReadinessGates bool                                   `json:"does_not_satisfy_production_readiness_gates"`
	ProposedCapacityProfile                capacitysample.ProposedCapacityProfile `json:"proposed_capacity_profile"`
	EvidenceLimits                         []string                               `json:"evidence_limits"`
}

type Results struct {
	LocalWriteAdmission WriteAdmissionResult        `json:"local_write_admission"`
	LocalReadBack       ReadBackResult              `json:"local_read_back"`
	DiskRunway          DiskRunwayObservation       `json:"disk_runway"`
	BackendUploadLag    LatencySummary              `json:"backend_upload_lag"`
	RepairLag           QueueLagObservation         `json:"repair_lag"`
	RestoreLag          OperationLagObservation     `json:"restore_lag"`
	OperationBacklog    OperationBacklogObservation `json:"operation_backlog"`
	OpenBao             OpenBaoObservation          `json:"openbao"`
}

type WriteAdmissionResult struct {
	Samples             []WriteSample  `json:"samples"`
	Summary             LatencySummary `json:"summary"`
	TotalBytes          uint64         `json:"total_bytes"`
	AchievedReplicaMin  uint32         `json:"achieved_replica_min"`
	DesiredReplicaMax   uint32         `json:"desired_replica_max"`
	RepairRequiredCount int            `json:"repair_required_count"`
}

type ReadBackResult struct {
	Samples    []ReadSample   `json:"samples"`
	Summary    LatencySummary `json:"summary"`
	TotalBytes uint64         `json:"total_bytes"`
	Sources    []string       `json:"sources"`
}

type DocumentIdentity struct {
	TenantID      string `json:"tenant_id"`
	TransactionID string `json:"transaction_id"`
	DocumentName  string `json:"document_name"`
}

type WriteSample struct {
	Document            DocumentIdentity `json:"document"`
	Bytes               uint64           `json:"bytes"`
	LatencyMillis       int64            `json:"latency_millis"`
	DesiredReplicas     uint32           `json:"desired_replicas"`
	AchievedReplicas    uint32           `json:"achieved_replicas"`
	RepairRequired      bool             `json:"repair_required"`
	Availability        string           `json:"availability,omitempty"`
	LifecycleState      string           `json:"lifecycle_state,omitempty"`
	ErrorClass          string           `json:"error_class,omitempty"`
	Error               string           `json:"error,omitempty"`
	LogicalSHA256Digest string           `json:"logical_sha256_digest,omitempty"`
}

type ReadSample struct {
	Document            DocumentIdentity `json:"document"`
	Bytes               uint64           `json:"bytes"`
	LatencyMillis       int64            `json:"latency_millis"`
	Source              string           `json:"source,omitempty"`
	LogicalSHA256Digest string           `json:"logical_sha256_digest,omitempty"`
	ErrorClass          string           `json:"error_class,omitempty"`
	Error               string           `json:"error,omitempty"`
}

type DiskRunwayObservation struct {
	RequestedCapacityProfileID string             `json:"requested_capacity_profile_id,omitempty"`
	CapacityProfileID          string             `json:"capacity_profile_id,omitempty"`
	UsableBytesRemaining       uint64             `json:"usable_bytes_remaining,omitempty"`
	EstimatedBytesPerDay       uint64             `json:"estimated_bytes_per_day,omitempty"`
	RunwayDays                 uint32             `json:"runway_days,omitempty"`
	Warnings                   []OperationWarning `json:"warnings,omitempty"`
	LatencyMillis              int64              `json:"latency_millis"`
	UsedLocalFallback          bool               `json:"used_local_fallback,omitempty"`
	FallbackReason             string             `json:"fallback_reason,omitempty"`
	ErrorClass                 string             `json:"error_class,omitempty"`
	Error                      string             `json:"error,omitempty"`
}

type QueueLagObservation struct {
	ItemCount       int    `json:"item_count"`
	OldestLagMillis int64  `json:"oldest_lag_millis"`
	ErrorClass      string `json:"error_class,omitempty"`
	Error           string `json:"error,omitempty"`
}

type OperationLagObservation struct {
	OperationTypes  []string `json:"operation_types"`
	OperationCount  int      `json:"operation_count"`
	OldestLagMillis int64    `json:"oldest_lag_millis"`
}

type OperationBacklogObservation struct {
	OperationCount         int            `json:"operation_count"`
	ByState                map[string]int `json:"by_state"`
	ByType                 map[string]int `json:"by_type"`
	OldestLagMillis        int64          `json:"oldest_lag_millis"`
	restoreOldestLagMillis int64
	ErrorClass             string `json:"error_class,omitempty"`
	Error                  string `json:"error,omitempty"`
}

type OpenBaoObservation struct {
	LatencySamples     []LatencySummary                      `json:"latency_samples"`
	ErrorClasses       []string                              `json:"error_classes"`
	CircuitBreakers    capacitysample.ProposedCircuitBreaker `json:"circuit_breakers"`
	SaturationBehavior SaturationBehavior                    `json:"saturation_behavior"`
}

type SaturationBehavior struct {
	ObservedSamples       int    `json:"observed_samples"`
	ObservedFailures      int    `json:"observed_failures"`
	ErrorRatePermille     uint32 `json:"error_rate_permille"`
	ConsecutiveFailures   uint32 `json:"consecutive_failures"`
	Classification        string `json:"classification"`
	AdvisoryThresholdOnly bool   `json:"advisory_threshold_only"`
}

type LatencySummary struct {
	Source        string   `json:"source"`
	Operation     string   `json:"operation"`
	SampleCount   int      `json:"sample_count"`
	SuccessCount  int      `json:"success_count"`
	ErrorCount    int      `json:"error_count"`
	Bytes         uint64   `json:"bytes,omitempty"`
	MinMillis     int64    `json:"min_millis,omitempty"`
	AverageMillis int64    `json:"average_millis,omitempty"`
	P95Millis     int64    `json:"p95_millis,omitempty"`
	MaxMillis     int64    `json:"max_millis,omitempty"`
	ErrorClasses  []string `json:"error_classes,omitempty"`
}

type OperationWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ThresholdViolation struct {
	Scope                 string `json:"scope"`
	Code                  string `json:"code"`
	Message               string `json:"message"`
	BlockingIssueRequired bool   `json:"blocking_issue_required"`
}

type ReleaseException struct {
	Owner     string `json:"owner"`
	ExpiresAt string `json:"expires_at"`
	Reason    string `json:"reason"`
}

func Run(ctx context.Context, opts Options, clients Clients) (Report, error) {
	opts, err := ValidateOptions(opts)
	if err != nil {
		return Report{}, err
	}
	if clients.Documents == nil {
		return Report{}, errors.New("local soak requires document client")
	}
	if clients.Inspect == nil {
		return Report{}, errors.New("local soak requires inspect client")
	}
	if clients.Operations == nil {
		return Report{}, errors.New("local soak requires operation client")
	}
	if clients.Repair == nil {
		return Report{}, errors.New("local soak requires repair client")
	}

	capacityReport, err := loadCapacitySampleReport(opts.CapacitySampleReportPath)
	if err != nil {
		return Report{}, err
	}

	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	started := time.Now()
	generatedAt := now().UTC()
	ctx, cancel := context.WithTimeout(ctx, opts.Duration)
	defer cancel()

	report := Report{
		ReportKind:  ReportKind,
		GeneratedAt: generatedAt.Format(time.RFC3339),
		Status:      StatusPassed,
		Release: ReleaseIdentity{
			ReleaseSHA:    opts.ReleaseSHA,
			ImageIdentity: opts.ImageIdentity,
			DirtyTree:     opts.DirtyTree,
		},
		CampaignConfig: CampaignConfig{
			ProfileID:                       opts.ProfileID,
			EnvironmentID:                   opts.EnvironmentID,
			RunnerID:                        opts.RunnerID,
			PublicAddress:                   opts.PublicAddress,
			AdminAddress:                    opts.AdminAddress,
			PublicWorkloadIdentity:          opts.PublicWorkloadIdentity,
			AdminWorkloadIdentity:           opts.AdminWorkloadIdentity,
			RepoOwnedReleaseBoundariesIssue: "#48",
			LocalEnvironmentIssue:           "#99",
			AdvisoryThresholdIssue:          "#100",
			Issue:                           "#87",
		},
		Workload:                  capacityWorkload(opts),
		AdvisoryCapacitySample:    summarizeCapacityReport(opts.CapacitySampleReportPath, capacityReport),
		BlockingIssues:            []string{},
		ReleaseExceptions:         []ReleaseException{},
		RequiredFailureResolution: "Any local rehearsal failure or exceeded advisory threshold must be linked to a blocking issue or an approved release exception with owner and expiry before release signoff.",
		EvidenceLimits: []string{
			"local soak evidence is release rehearsal evidence only",
			"local soak evidence is not live production capacity approval",
			"local soak evidence is not provider quota approval",
			"local soak evidence is not HA OpenBao approval",
			"advisory capacity thresholds from #100 are consumed but not automatically approved as production settings",
		},
	}

	writeResult := runWriteAdmission(ctx, opts, clients.Documents, generatedAt)
	readResult := runReadBack(ctx, clients.Documents, writeResult.Samples)
	runway := observeDiskRunway(ctx, clients.Inspect, opts.ProfileID)
	repairLag := observeRepairLag(ctx, clients.Repair, now())
	backlog := observeOperationBacklog(ctx, clients.Operations, now())

	report.Results = Results{
		LocalWriteAdmission: writeResult,
		LocalReadBack:       readResult,
		DiskRunway:          runway,
		BackendUploadLag:    summarizeCapacityLatency("capacity-sample-advisory", "backend-put-upload-lag", capacityReport.Backend, "put"),
		RepairLag:           repairLag,
		RestoreLag:          restoreLag(backlog),
		OperationBacklog:    backlog,
		OpenBao:             summarizeOpenBao(capacityReport),
	}
	report.DurationMillis = durationMillis(time.Since(started))
	report.ThresholdViolations = collectThresholdViolations(report)
	if len(report.ThresholdViolations) > 0 {
		report.Status = StatusFailed
	}
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
	opts.CapacitySampleReportPath = defaultText(opts.CapacitySampleReportPath, DefaultCapacitySampleReportPath)
	if opts.SampleCount == 0 {
		opts.SampleCount = DefaultSampleCount
	}
	if len(opts.DocumentSizesBytes) == 0 {
		opts.DocumentSizesBytes = []uint64{DefaultDocumentSizeBytes}
	}
	if opts.Duration == 0 {
		opts.Duration = DefaultDuration
	}
	if opts.SampleCount < 1 || opts.SampleCount > MaxSampleCount {
		return Options{}, fmt.Errorf("local soak --samples must be 1..%d", MaxSampleCount)
	}
	if opts.Duration <= 0 || opts.Duration > MaxDuration {
		return Options{}, fmt.Errorf("local soak --duration must be positive and no more than %s", MaxDuration)
	}
	for _, size := range opts.DocumentSizesBytes {
		if size == 0 || size > MaxDocumentSizeBytes {
			return Options{}, fmt.Errorf("local soak --document-size must be 1..%d bytes", MaxDocumentSizeBytes)
		}
	}
	if strings.TrimSpace(opts.PublicWorkloadIdentity) == "" {
		return Options{}, errors.New("local soak requires public workload identity")
	}
	if strings.TrimSpace(opts.AdminWorkloadIdentity) == "" {
		return Options{}, errors.New("local soak requires admin workload identity")
	}
	return opts, nil
}

func loadCapacitySampleReport(path string) (capacitysample.Report, error) {
	// #nosec G304 -- the capacity sample path is an operator-provided local evidence input path.
	file, err := os.Open(path)
	if err != nil {
		return capacitysample.Report{}, fmt.Errorf("open capacity sample report: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()
	var report capacitysample.Report
	if err := json.NewDecoder(file).Decode(&report); err != nil {
		return capacitysample.Report{}, fmt.Errorf("decode capacity sample report: %w", err)
	}
	if report.ReportKind != capacitysample.ReportKind {
		return capacitysample.Report{}, fmt.Errorf("capacity sample report kind = %q, want %q", report.ReportKind, capacitysample.ReportKind)
	}
	return report, nil
}

func summarizeCapacityReport(path string, report capacitysample.Report) CapacitySampleSummary {
	return CapacitySampleSummary{
		SourcePath:                             path,
		ReportKind:                             report.ReportKind,
		ReleaseSHA:                             report.ReleaseSHA,
		DirtyTree:                              report.DirtyTree,
		ProfileID:                              report.ProfileID,
		EnvironmentID:                          report.EnvironmentID,
		AdvisoryOnly:                           report.AdvisoryOnly,
		DoesNotSatisfyProductionReadinessGates: report.DoesNotSatisfyProductionReadinessGates,
		ProposedCapacityProfile:                report.ProposedProfile,
		EvidenceLimits:                         append([]string(nil), report.EvidenceLimits...),
	}
}

func capacityWorkload(opts Options) WorkloadShape {
	return WorkloadShape{
		SampleCount:              opts.SampleCount,
		DurationMillis:           durationMillis(opts.Duration),
		DocumentSizeDistribution: documentSizeDistribution(opts),
		DocumentClass:            scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT.String(),
		PriorityClass:            scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL.String(),
		Bounded:                  true,
		NonProductionOnly:        true,
		ActivePublicWrites:       true,
		ActiveAdminReads:         false,
		ActiveAdminObservations:  true,
	}
}

func documentSizeDistribution(opts Options) DocumentSizeDistribution {
	dist := DocumentSizeDistribution{
		ConfiguredSizesBytes: append([]uint64(nil), opts.DocumentSizesBytes...),
	}
	for i := range opts.SampleCount {
		size := opts.DocumentSizesBytes[i%len(opts.DocumentSizesBytes)]
		if dist.MinBytes == 0 || size < dist.MinBytes {
			dist.MinBytes = size
		}
		if size > dist.MaxBytes {
			dist.MaxBytes = size
		}
		dist.TotalBytes += size
	}
	return dist
}

func runWriteAdmission(ctx context.Context, opts Options, client DocumentClient, generatedAt time.Time) WriteAdmissionResult {
	result := WriteAdmissionResult{
		Samples:            make([]WriteSample, 0, opts.SampleCount),
		AchievedReplicaMin: ^uint32(0),
	}
	runID := "local-soak-" + generatedAt.Format("20060102T150405Z")
	for i := range opts.SampleCount {
		size := opts.DocumentSizesBytes[i%len(opts.DocumentSizesBytes)]
		data := sampleBytes(size, byte(i+1))
		doc := DocumentIdentity{
			TenantID:      "local-soak",
			TransactionID: runID,
			DocumentName:  fmt.Sprintf("issue-87/document-%06d.bin", i+1),
		}
		sample := writeOneDocument(ctx, client, opts.ProfileID, doc, data, i+1)
		result.Samples = append(result.Samples, sample)
		result.TotalBytes += sample.Bytes
		if sample.ErrorClass != "" {
			continue
		}
		if sample.AchievedReplicas < result.AchievedReplicaMin {
			result.AchievedReplicaMin = sample.AchievedReplicas
		}
		if sample.DesiredReplicas > result.DesiredReplicaMax {
			result.DesiredReplicaMax = sample.DesiredReplicas
		}
		if sample.RepairRequired {
			result.RepairRequiredCount++
		}
	}
	if result.AchievedReplicaMin == ^uint32(0) {
		result.AchievedReplicaMin = 0
	}
	result.Summary = summarizeWriteSamples(result.Samples)
	return result
}

func writeOneDocument(ctx context.Context, client DocumentClient, profileID string, doc DocumentIdentity, data []byte, sequence int) WriteSample {
	started := time.Now()
	size := uint64(len(data))
	sum := sha256.Sum256(data)
	stream, err := client.WriteDocument(ctx)
	if err != nil {
		return failedWriteSample(doc, size, started, err)
	}
	contentType := "application/octet-stream"
	idempotencyKey := fmt.Sprintf("local-soak-%s-%s-%d", doc.TenantID, doc.TransactionID, sequence)
	workflowStage := "local-soak"
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
			CreatedByService:     "scrap-local-soak-evidence",
			WorkflowStage:        &workflowStage,
			Tags: map[string]string{
				"issue":    "87",
				"profile":  profileID,
				"evidence": ReportKind,
			},
		}},
	}); err != nil {
		return failedWriteSample(doc, size, started, err)
	}
	if err := stream.Send(&scrapv1.WriteDocumentRequest{
		Message: &scrapv1.WriteDocumentRequest_Chunk{Chunk: &scrapv1.WriteDocumentChunk{Data: data}},
	}); err != nil {
		return failedWriteSample(doc, size, started, err)
	}
	resp, err := stream.CloseAndRecv()
	if err != nil {
		return failedWriteSample(doc, size, started, err)
	}
	metadata := resp.GetMetadata()
	return WriteSample{
		Document:            doc,
		Bytes:               size,
		LatencyMillis:       durationMillis(time.Since(started)),
		DesiredReplicas:     resp.GetDesiredReplicaCount(),
		AchievedReplicas:    resp.GetAchievedReplicaCount(),
		RepairRequired:      resp.GetRepairRequired(),
		Availability:        metadata.GetAvailability().String(),
		LifecycleState:      metadata.GetLifecycleState().String(),
		LogicalSHA256Digest: hexDigest(metadata.GetLogicalSha256()),
	}
}

func runReadBack(ctx context.Context, client DocumentClient, writes []WriteSample) ReadBackResult {
	result := ReadBackResult{Samples: make([]ReadSample, 0, len(writes))}
	for _, write := range writes {
		if write.ErrorClass != "" {
			continue
		}
		sample := readOneDocument(ctx, client, write.Document, write.Bytes)
		result.Samples = append(result.Samples, sample)
		result.TotalBytes += sample.Bytes
	}
	result.Summary = summarizeReadSamples(result.Samples)
	result.Sources = collectReadSources(result.Samples)
	return result
}

func readOneDocument(ctx context.Context, client DocumentClient, doc DocumentIdentity, expectedBytes uint64) ReadSample {
	started := time.Now()
	identity := &scrapv1.DocumentIdentity{
		TenantId:      doc.TenantID,
		TransactionId: doc.TransactionID,
		DocumentName:  doc.DocumentName,
	}
	head, err := client.HeadDocument(ctx, &scrapv1.HeadDocumentRequest{Identity: identity})
	if err != nil {
		return failedReadSample(doc, started, err)
	}
	if head.GetMetadata().GetLength() != expectedBytes {
		return failedReadSample(doc, started, fmt.Errorf("head length = %d, want %d", head.GetMetadata().GetLength(), expectedBytes))
	}
	expectedDigest := head.GetMetadata().GetLogicalSha256()
	stream, err := client.ReadDocument(ctx, &scrapv1.ReadDocumentRequest{Identity: identity})
	if err != nil {
		return failedReadSample(doc, started, err)
	}
	var source string
	var bytesRead uint64
	hasher := sha256.New()
	for {
		msg, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return failedReadSample(doc, started, err)
		}
		if metadata := msg.GetMetadata(); metadata != nil {
			source = metadata.GetSource().String()
			continue
		}
		chunk := msg.GetChunk()
		if chunk == nil {
			return failedReadSample(doc, started, errors.New("read stream returned response without metadata or chunk"))
		}
		data := chunk.GetData()
		bytesRead += uint64(len(data))
		_, _ = hasher.Write(data)
	}
	if bytesRead != expectedBytes {
		return failedReadSample(doc, started, fmt.Errorf("read bytes = %d, want %d", bytesRead, expectedBytes))
	}
	actualDigest := hasher.Sum(nil)
	if len(expectedDigest) > 0 && !bytes.Equal(actualDigest, expectedDigest) {
		return failedReadSample(doc, started, errors.New("read content sha256 does not match document metadata"))
	}
	return ReadSample{
		Document:            doc,
		Bytes:               bytesRead,
		LatencyMillis:       durationMillis(time.Since(started)),
		Source:              source,
		LogicalSHA256Digest: hexDigest(actualDigest),
	}
}

func observeDiskRunway(ctx context.Context, client InspectClient, profileID string) DiskRunwayObservation {
	observation := queryDiskRunway(ctx, client, profileID)
	if observation.ErrorClass != string(backend.ErrorClassNotFound) || strings.TrimSpace(profileID) == "" {
		return observation
	}
	fallback := queryDiskRunway(ctx, client, "")
	fallback.RequestedCapacityProfileID = profileID
	fallback.UsedLocalFallback = true
	fallback.FallbackReason = "local kind reports disk runway under its built-in non-production capacity profile; scrap-prod-v1 remains the campaign profile and capacity-sample advisory profile"
	return fallback
}

func queryDiskRunway(ctx context.Context, client InspectClient, profileID string) DiskRunwayObservation {
	started := time.Now()
	resp, err := client.GetCapacityRunway(ctx, &adminv1.GetCapacityRunwayRequest{CapacityProfileId: optionalString(profileID)})
	observation := DiskRunwayObservation{
		RequestedCapacityProfileID: profileID,
		LatencyMillis:              durationMillis(time.Since(started)),
	}
	if err != nil {
		observation.ErrorClass = string(classifyGRPCError(err))
		observation.Error = err.Error()
		return observation
	}
	runway := resp.GetRunway()
	observation.CapacityProfileID = runway.GetCapacityProfileId()
	observation.UsableBytesRemaining = runway.GetUsableBytesRemaining()
	observation.EstimatedBytesPerDay = runway.GetEstimatedBytesPerDay()
	observation.RunwayDays = runway.GetRunwayDays()
	for _, warning := range runway.GetWarnings() {
		observation.Warnings = append(observation.Warnings, OperationWarning{
			Code:    warning.GetCode(),
			Message: warning.GetMessage(),
		})
	}
	return observation
}

func observeRepairLag(ctx context.Context, client RepairClient, now time.Time) QueueLagObservation {
	resp, err := client.GetRepairQueue(ctx, &adminv1.GetRepairQueueRequest{PageSize: optionalUint32(100)})
	if err != nil {
		return QueueLagObservation{
			ErrorClass: string(classifyGRPCError(err)),
			Error:      err.Error(),
		}
	}
	observation := QueueLagObservation{ItemCount: len(resp.GetItems())}
	for _, item := range resp.GetItems() {
		lag := lagMillis(now, item.GetDetectedAt().AsTime())
		if lag > observation.OldestLagMillis {
			observation.OldestLagMillis = lag
		}
	}
	return observation
}

func observeOperationBacklog(ctx context.Context, client OperationClient, now time.Time) OperationBacklogObservation {
	resp, err := client.ListOperations(ctx, &adminv1.ListOperationsRequest{
		States: []adminv1.OperationState{
			adminv1.OperationState_OPERATION_STATE_PLANNED,
			adminv1.OperationState_OPERATION_STATE_QUEUED,
			adminv1.OperationState_OPERATION_STATE_RUNNING,
		},
		PageSize: optionalUint32(100),
	})
	observation := OperationBacklogObservation{
		ByState: map[string]int{},
		ByType:  map[string]int{},
	}
	if err != nil {
		observation.ErrorClass = string(classifyGRPCError(err))
		observation.Error = err.Error()
		return observation
	}
	for _, operation := range resp.GetOperations() {
		observation.OperationCount++
		state := operation.GetState().String()
		operationType := operation.GetOperationType()
		observation.ByState[state]++
		observation.ByType[operationType]++
		lag := lagMillis(now, operation.GetRequestedAt().AsTime())
		if lag > observation.OldestLagMillis {
			observation.OldestLagMillis = lag
		}
		if isRestoreOperation(operationType) && lag > observation.restoreOldestLagMillis {
			observation.restoreOldestLagMillis = lag
		}
	}
	return observation
}

func restoreLag(backlog OperationBacklogObservation) OperationLagObservation {
	observation := OperationLagObservation{OperationTypes: []string{}}
	types := make([]string, 0, len(backlog.ByType))
	for operationType := range backlog.ByType {
		if isRestoreOperation(operationType) {
			types = append(types, operationType)
		}
	}
	sort.Strings(types)
	for _, operationType := range types {
		observation.OperationTypes = append(observation.OperationTypes, operationType)
		observation.OperationCount += backlog.ByType[operationType]
	}
	if observation.OperationCount > 0 {
		observation.OldestLagMillis = backlog.restoreOldestLagMillis
	}
	return observation
}

func isRestoreOperation(operationType string) bool {
	lower := strings.ToLower(operationType)
	return strings.Contains(lower, "restore") ||
		strings.Contains(lower, "prewarm") ||
		strings.Contains(lower, "dr")
}

func summarizeOpenBao(report capacitysample.Report) OpenBaoObservation {
	operations := uniqueSampleOperations(report.OpenBao)
	summaries := make([]LatencySummary, 0, len(operations))
	for _, operation := range operations {
		summaries = append(summaries, summarizeCapacityLatency("capacity-sample-advisory", "openbao-"+operation, report.OpenBao, operation))
	}
	return OpenBaoObservation{
		LatencySamples:     summaries,
		ErrorClasses:       collectRequestSampleErrorClasses(report.OpenBao),
		CircuitBreakers:    report.ProposedProfile.CircuitBreakers,
		SaturationBehavior: summarizeSaturation(report.OpenBao, report.ProposedProfile.CircuitBreakers),
	}
}

func summarizeSaturation(samples []capacitysample.RequestSample, breaker capacitysample.ProposedCircuitBreaker) SaturationBehavior {
	failures := 0
	consecutive := uint32(0)
	maxConsecutive := uint32(0)
	for _, sample := range samples {
		if sample.ErrorClass == "" {
			consecutive = 0
			continue
		}
		failures++
		consecutive++
		if consecutive > maxConsecutive {
			maxConsecutive = consecutive
		}
	}
	rate := uint32(0)
	if len(samples) > 0 {
		converted, err := safeconv.IntToUint32("OpenBao error rate permille", failures*1000/len(samples))
		if err == nil {
			rate = converted
		}
	}
	classification := "within-advisory-threshold"
	if breaker.MinimumSamples > 0 &&
		len(samples) >= int(breaker.MinimumSamples) &&
		((breaker.ErrorRatePermille > 0 && rate >= breaker.ErrorRatePermille) ||
			(breaker.ConsecutiveFailures > 0 && maxConsecutive >= breaker.ConsecutiveFailures)) {
		classification = "exceeds-advisory-threshold"
	}
	return SaturationBehavior{
		ObservedSamples:       len(samples),
		ObservedFailures:      failures,
		ErrorRatePermille:     rate,
		ConsecutiveFailures:   maxConsecutive,
		Classification:        classification,
		AdvisoryThresholdOnly: true,
	}
}

func summarizeCapacityLatency(source, operation string, samples []capacitysample.RequestSample, sampleOperation string) LatencySummary {
	var latencies []int64
	summary := LatencySummary{Source: source, Operation: operation}
	errorClasses := map[string]bool{}
	for _, sample := range samples {
		if sample.Operation != sampleOperation {
			continue
		}
		summary.SampleCount++
		summary.Bytes += sample.Bytes
		if sample.ErrorClass != "" {
			summary.ErrorCount++
			errorClasses[sample.ErrorClass] = true
			continue
		}
		summary.SuccessCount++
		latencies = append(latencies, sample.LatencyMillis)
	}
	applyLatencyStats(&summary, latencies)
	summary.ErrorClasses = sortedStringKeys(errorClasses)
	return summary
}

func summarizeWriteSamples(samples []WriteSample) LatencySummary {
	var latencies []int64
	summary := LatencySummary{Source: "public-grpc", Operation: "write-admission"}
	errorClasses := map[string]bool{}
	for _, sample := range samples {
		summary.SampleCount++
		summary.Bytes += sample.Bytes
		if sample.ErrorClass != "" {
			summary.ErrorCount++
			errorClasses[sample.ErrorClass] = true
			continue
		}
		summary.SuccessCount++
		latencies = append(latencies, sample.LatencyMillis)
	}
	applyLatencyStats(&summary, latencies)
	summary.ErrorClasses = sortedStringKeys(errorClasses)
	return summary
}

func summarizeReadSamples(samples []ReadSample) LatencySummary {
	var latencies []int64
	summary := LatencySummary{Source: "public-grpc", Operation: "read-back"}
	errorClasses := map[string]bool{}
	for _, sample := range samples {
		summary.SampleCount++
		summary.Bytes += sample.Bytes
		if sample.ErrorClass != "" {
			summary.ErrorCount++
			errorClasses[sample.ErrorClass] = true
			continue
		}
		summary.SuccessCount++
		latencies = append(latencies, sample.LatencyMillis)
	}
	applyLatencyStats(&summary, latencies)
	summary.ErrorClasses = sortedStringKeys(errorClasses)
	return summary
}

func applyLatencyStats(summary *LatencySummary, latencies []int64) {
	if len(latencies) == 0 {
		return
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	summary.MinMillis = latencies[0]
	summary.MaxMillis = latencies[len(latencies)-1]
	summary.P95Millis = percentileMillis(latencies, 95)
	var total int64
	for _, latency := range latencies {
		total += latency
	}
	summary.AverageMillis = total / int64(len(latencies))
}

func percentileMillis(sorted []int64, percentile int) int64 {
	if len(sorted) == 0 {
		return 0
	}
	index := (len(sorted)*percentile + 99) / 100
	if index < 1 {
		index = 1
	}
	if index > len(sorted) {
		index = len(sorted)
	}
	return sorted[index-1]
}

func collectThresholdViolations(report Report) []ThresholdViolation {
	var violations []ThresholdViolation
	violations = append(violations, capacitySampleViolations(report)...)
	violations = append(violations, writeAdmissionViolations(report.Results.LocalWriteAdmission.Samples)...)
	violations = append(violations, readBackViolations(report.Results.LocalReadBack.Samples)...)
	violations = append(violations, latencySummaryViolations("backend-upload-lag", report.Results.BackendUploadLag)...)
	for _, summary := range report.Results.OpenBao.LatencySamples {
		violations = append(violations, latencySummaryViolations("openbao", summary)...)
	}
	if report.Results.OpenBao.SaturationBehavior.Classification == "exceeds-advisory-threshold" {
		violations = append(violations, thresholdViolation("openbao", "SCRAP_OPENBAO_SATURATION_THRESHOLD_EXCEEDED",
			"OpenBao samples exceed advisory circuit breaker thresholds from #100"))
	}
	if report.Results.DiskRunway.ErrorClass != "" {
		violations = append(violations, thresholdViolation("disk-runway", "SCRAP_DISK_RUNWAY_UNAVAILABLE", report.Results.DiskRunway.Error))
	}
	if report.Results.RepairLag.ErrorClass != "" {
		violations = append(violations, thresholdViolation("repair-lag", "SCRAP_REPAIR_QUEUE_UNAVAILABLE", report.Results.RepairLag.Error))
	}
	if report.Results.RepairLag.ItemCount > 0 {
		violations = append(violations, thresholdViolation("repair-lag", "SCRAP_REPAIR_QUEUE_NOT_EMPTY",
			fmt.Sprintf("repair queue contains %d item(s)", report.Results.RepairLag.ItemCount)))
	}
	if report.Results.OperationBacklog.ErrorClass != "" {
		violations = append(violations, thresholdViolation("operation-backlog", "SCRAP_OPERATION_BACKLOG_UNAVAILABLE", report.Results.OperationBacklog.Error))
	}
	return violations
}

func capacitySampleViolations(report Report) []ThresholdViolation {
	var violations []ThresholdViolation
	if report.AdvisoryCapacitySample.ProfileID != report.CampaignConfig.ProfileID {
		violations = append(violations, thresholdViolation("capacity-sample", "SCRAP_CAPACITY_PROFILE_MISMATCH",
			fmt.Sprintf("capacity sample profile %q does not match campaign profile %q", report.AdvisoryCapacitySample.ProfileID, report.CampaignConfig.ProfileID)))
	}
	if !report.AdvisoryCapacitySample.AdvisoryOnly || !report.AdvisoryCapacitySample.DoesNotSatisfyProductionReadinessGates {
		violations = append(violations, thresholdViolation("capacity-sample", "SCRAP_CAPACITY_SAMPLE_BOUNDARY_MISSING",
			"capacity sample must remain advisory-only and must not satisfy production readiness gates by itself"))
	}
	return violations
}

func writeAdmissionViolations(samples []WriteSample) []ThresholdViolation {
	var violations []ThresholdViolation
	for _, sample := range samples {
		if sample.ErrorClass != "" {
			violations = append(violations, thresholdViolation("write-admission", "SCRAP_LOCAL_WRITE_FAILED", sample.Error))
		}
		if sample.RepairRequired {
			violations = append(violations, thresholdViolation("write-admission", "SCRAP_LOCAL_WRITE_REPAIR_REQUIRED",
				sample.Document.DocumentName+" requires repair after admission"))
		}
	}
	return violations
}

func readBackViolations(samples []ReadSample) []ThresholdViolation {
	var violations []ThresholdViolation
	for _, sample := range samples {
		if sample.ErrorClass != "" {
			violations = append(violations, thresholdViolation("read-back", "SCRAP_LOCAL_READ_BACK_FAILED", sample.Error))
		}
	}
	return violations
}

func latencySummaryViolations(scope string, summary LatencySummary) []ThresholdViolation {
	var violations []ThresholdViolation
	for _, errorClass := range summary.ErrorClasses {
		violations = append(violations, thresholdViolation(scope, "SCRAP_REHEARSAL_SAMPLE_ERROR",
			fmt.Sprintf("%s observed %s errors", summary.Operation, errorClass)))
	}
	return violations
}

func thresholdViolation(scope, code, message string) ThresholdViolation {
	return ThresholdViolation{
		Scope:                 scope,
		Code:                  code,
		Message:               message,
		BlockingIssueRequired: true,
	}
}

func failedWriteSample(doc DocumentIdentity, size uint64, started time.Time, err error) WriteSample {
	return WriteSample{
		Document:      doc,
		Bytes:         size,
		LatencyMillis: durationMillis(time.Since(started)),
		ErrorClass:    string(classifyError(err)),
		Error:         err.Error(),
	}
}

func failedReadSample(doc DocumentIdentity, started time.Time, err error) ReadSample {
	return ReadSample{
		Document:      doc,
		LatencyMillis: durationMillis(time.Since(started)),
		ErrorClass:    string(classifyError(err)),
		Error:         err.Error(),
	}
}

func classifyError(err error) backend.ErrorClass {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return backend.ErrorClassTransient
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return backend.ErrorClassTransient
	}
	if status.Code(err) != codes.Unknown {
		return classifyGRPCError(err)
	}
	return backend.ErrorClassPermanent
}

func classifyGRPCError(err error) backend.ErrorClass {
	switch status.Code(err) {
	case codes.OK:
		return ""
	case codes.ResourceExhausted:
		return backend.ErrorClassThrottled
	case codes.Unavailable, codes.DeadlineExceeded, codes.Canceled:
		return backend.ErrorClassTransient
	case codes.Unauthenticated, codes.PermissionDenied:
		return backend.ErrorClassAuth
	case codes.NotFound:
		return backend.ErrorClassNotFound
	case codes.AlreadyExists, codes.Aborted:
		return backend.ErrorClassConflict
	case codes.DataLoss:
		return backend.ErrorClassCorrupt
	default:
		return backend.ErrorClassPermanent
	}
}

func collectReadSources(samples []ReadSample) []string {
	sources := map[string]bool{}
	for _, sample := range samples {
		if sample.Source != "" {
			sources[sample.Source] = true
		}
	}
	return sortedStringKeys(sources)
}

func collectRequestSampleErrorClasses(samples []capacitysample.RequestSample) []string {
	classes := map[string]bool{}
	for _, sample := range samples {
		if sample.ErrorClass != "" {
			classes[sample.ErrorClass] = true
		}
	}
	return sortedStringKeys(classes)
}

func uniqueSampleOperations(samples []capacitysample.RequestSample) []string {
	operations := map[string]bool{}
	for _, sample := range samples {
		if sample.Operation != "" {
			operations[sample.Operation] = true
		}
	}
	return sortedStringKeys(operations)
}

func sortedStringKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func lagMillis(now, observed time.Time) int64 {
	if observed.IsZero() {
		return 0
	}
	if observed.After(now) {
		return 0
	}
	return durationMillis(now.Sub(observed))
}

func sampleBytes(size uint64, seed byte) []byte {
	n, err := safeconv.Uint64ToInt("local soak sample size", size)
	if err != nil {
		return nil
	}
	data := make([]byte, n)
	for i := range data {
		data[i] = seed + byte(i%251)
	}
	return data
}

func hexDigest(value []byte) string {
	if len(value) == 0 {
		return ""
	}
	return "sha256:" + hex.EncodeToString(value)
}

func defaultText(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func optionalString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func optionalUint32(value uint32) *uint32 {
	if value == 0 {
		return nil
	}
	return &value
}

func durationMillis(duration time.Duration) int64 {
	if duration <= 0 {
		return 0
	}
	millis := duration / time.Millisecond
	if millis == 0 {
		return 1
	}
	return int64(millis)
}
