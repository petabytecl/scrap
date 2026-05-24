package localsoak

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/petabytecl/scrap/internal/capacitysample"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	scrapv1 "github.com/petabytecl/scrap/internal/gen/scrap/v1"
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestRunEmitsLocalRehearsalEvidence(t *testing.T) {
	capacityPath := writeCapacityReport(t, sampleCapacityReport())
	docs := newFakeDocuments()
	now := time.Unix(1700000000, 0).UTC()

	report, err := Run(context.Background(), Options{
		ReleaseSHA:               "abc123",
		DirtyTree:                "clean",
		ProfileID:                "scrap-prod-v1",
		EnvironmentID:            "local-kind",
		RunnerID:                 "kind-local",
		ImageIdentity:            "localhost/scrapd:local",
		CapacitySampleReportPath: capacityPath,
		SampleCount:              2,
		DocumentSizesBytes:       []uint64{128, 256},
		Duration:                 time.Second,
		Now: func() time.Time {
			return now
		},
	}, Clients{
		Documents:  docs,
		Inspect:    fakeInspect{runway: sampleRunway()},
		Operations: fakeOperations{},
		Repair:     fakeRepair{},
	})
	testutil.RequireNoErrorf(t, err, "Run")

	requireLocalSoakReport(t, report)
	for _, limit := range report.EvidenceLimits {
		if strings.Contains(limit, "not live production capacity approval") {
			return
		}
	}
	t.Fatalf("evidence limits = %#v, want production boundary", report.EvidenceLimits)
}

func requireLocalSoakReport(t *testing.T, report Report) {
	t.Helper()
	testutil.RequireEqualf(t, report.Status, StatusPassed, "status")
	testutil.RequireEqualf(t, report.CampaignConfig.RepoOwnedReleaseBoundariesIssue, "#48", "repo-owned release boundaries issue")
	testutil.RequireEqualf(t, report.CampaignConfig.LocalEnvironmentIssue, "#99", "local environment issue")
	testutil.RequireEqualf(t, report.CampaignConfig.AdvisoryThresholdIssue, "#100", "advisory threshold issue")
	testutil.RequireEqualf(t, report.Workload.DocumentSizeDistribution.TotalBytes, uint64(384), "document distribution total bytes")
	testutil.RequireFalsef(t, report.Workload.ActiveAdminReads, "active admin reads = true")
	testutil.RequireTruef(t, report.Workload.ActiveAdminObservations, "active admin observations = false")
	testutil.RequireEqualf(t, len(report.Results.LocalWriteAdmission.Samples), 2, "write sample count")
	testutil.RequireEqualf(t, report.Results.LocalReadBack.Summary.SuccessCount, 2, "read-back success count")
	testutil.RequireTruef(t, contains(report.Results.LocalReadBack.Sources, scrapv1.StorageSource_STORAGE_SOURCE_LOCAL.String()), "read-back sources = %#v", report.Results.LocalReadBack.Sources)
	testutil.RequireEqualf(t, report.Results.BackendUploadLag.SuccessCount, 2, "backend upload success count")
	testutil.RequireEqualf(t, report.Results.OpenBao.SaturationBehavior.Classification, "within-advisory-threshold", "openbao saturation classification")
	testutil.RequireEqualf(t, report.AdvisoryCapacitySample.ProposedCapacityProfile.ProfileID, "scrap-prod-v1-advisory", "advisory capacity profile")
}

func TestRunFailsWhenAdvisoryThresholdsAreExceeded(t *testing.T) {
	capacity := sampleCapacityReport()
	capacity.Backend = append(capacity.Backend, capacitysample.RequestSample{
		Target:        "backend",
		Operation:     "put",
		Method:        "PUT",
		LatencyMillis: 1,
		ErrorClass:    "throttled",
		Error:         "too many requests",
	})
	capacity.OpenBao = []capacitysample.RequestSample{
		{Target: "openbao", Operation: "wrap", Method: "POST", ErrorClass: "transient", Error: "timeout"},
		{Target: "openbao", Operation: "unwrap", Method: "POST", ErrorClass: "transient", Error: "timeout"},
		{Target: "openbao", Operation: "rewrap", Method: "POST", ErrorClass: "transient", Error: "timeout"},
	}

	report, err := Run(context.Background(), Options{
		ReleaseSHA:               "abc123",
		DirtyTree:                "clean",
		ProfileID:                "scrap-prod-v1",
		CapacitySampleReportPath: writeCapacityReport(t, capacity),
		SampleCount:              1,
		DocumentSizesBytes:       []uint64{64},
		Duration:                 time.Second,
		Now: func() time.Time {
			return time.Unix(1700000000, 0).UTC()
		},
	}, Clients{
		Documents:  newFakeDocuments(),
		Inspect:    fakeInspect{runway: sampleRunway()},
		Operations: fakeOperations{},
		Repair:     fakeRepair{},
	})
	testutil.RequireNoErrorf(t, err, "Run")

	if report.Status != StatusFailed {
		t.Fatalf("status = %q, want failed", report.Status)
	}
	if len(report.ThresholdViolations) == 0 {
		t.Fatalf("threshold violations = %#v, want capacity/openbao violations", report.ThresholdViolations)
	}
	if !strings.Contains(report.RequiredFailureResolution, "blocking issue") {
		t.Fatalf("failure resolution = %q, want linked issue or exception policy", report.RequiredFailureResolution)
	}
}

func TestRunFailsOnReadDigestMismatch(t *testing.T) {
	report, err := Run(context.Background(), Options{
		ReleaseSHA:               "abc123",
		DirtyTree:                "clean",
		ProfileID:                "scrap-prod-v1",
		CapacitySampleReportPath: writeCapacityReport(t, sampleCapacityReport()),
		SampleCount:              1,
		DocumentSizesBytes:       []uint64{64},
		Duration:                 time.Second,
		Now: func() time.Time {
			return time.Unix(1700000000, 0).UTC()
		},
	}, Clients{
		Documents:  &fakeDocuments{values: map[string][]byte{}, corruptReads: true},
		Inspect:    fakeInspect{runway: sampleRunway()},
		Operations: fakeOperations{},
		Repair:     fakeRepair{},
	})
	testutil.RequireNoErrorf(t, err, "Run")
	if report.Status != StatusFailed {
		t.Fatalf("status = %q, want failed", report.Status)
	}
	if !hasViolation(report.ThresholdViolations, "SCRAP_LOCAL_READ_BACK_FAILED") {
		t.Fatalf("violations = %#v, want read-back failure", report.ThresholdViolations)
	}
}

func TestRestoreLagUsesRestoreOperationsOnly(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	backlog := observeOperationBacklog(context.Background(), fakeOperations{
		operations: []*adminv1.Operation{
			{
				OperationId:   "older-capacity",
				OperationType: "capacity-override",
				State:         adminv1.OperationState_OPERATION_STATE_RUNNING,
				RequestedAt:   timestamppb.New(now.Add(-10 * time.Hour)),
			},
			{
				OperationId:   "newer-restore",
				OperationType: "restore",
				State:         adminv1.OperationState_OPERATION_STATE_RUNNING,
				RequestedAt:   timestamppb.New(now.Add(-2 * time.Hour)),
			},
		},
	}, now)

	restore := restoreLag(backlog)
	if restore.OldestLagMillis != durationMillis(2*time.Hour) {
		t.Fatalf("restore lag = %d, want only restore lag", restore.OldestLagMillis)
	}
}

func TestClassifyErrorTreatsContextTimeoutsAsTransient(t *testing.T) {
	if got := classifyError(context.DeadlineExceeded); got != "transient" {
		t.Fatalf("classify deadline = %q, want transient", got)
	}
	if got := classifyError(context.Canceled); got != "transient" {
		t.Fatalf("classify canceled = %q, want transient", got)
	}
}

func TestRunFallsBackToLocalRunwayProfile(t *testing.T) {
	report, err := Run(context.Background(), Options{
		ReleaseSHA:               "abc123",
		DirtyTree:                "clean",
		ProfileID:                "scrap-prod-v1",
		CapacitySampleReportPath: writeCapacityReport(t, sampleCapacityReport()),
		SampleCount:              1,
		DocumentSizesBytes:       []uint64{64},
		Duration:                 time.Second,
		Now: func() time.Time {
			return time.Unix(1700000000, 0).UTC()
		},
	}, Clients{
		Documents:  newFakeDocuments(),
		Inspect:    fallbackInspect{runway: sampleLocalRunway()},
		Operations: fakeOperations{},
		Repair:     fakeRepair{},
	})
	testutil.RequireNoErrorf(t, err, "Run")
	if report.Status != StatusPassed {
		t.Fatalf("status = %q, want passed; violations=%#v", report.Status, report.ThresholdViolations)
	}
	runway := report.Results.DiskRunway
	if !runway.UsedLocalFallback ||
		runway.RequestedCapacityProfileID != "scrap-prod-v1" ||
		runway.CapacityProfileID != "local-non-production" {
		t.Fatalf("runway fallback = %#v, want local profile fallback", runway)
	}
}

func TestValidateOptionsRejectsUnboundedCampaigns(t *testing.T) {
	tests := map[string]struct {
		mutate func(*Options)
		want   string
	}{
		"too many samples": {
			mutate: func(opts *Options) { opts.SampleCount = MaxSampleCount + 1 },
			want:   "--samples",
		},
		"oversized document": {
			mutate: func(opts *Options) { opts.DocumentSizesBytes = []uint64{MaxDocumentSizeBytes + 1} },
			want:   "--document-size",
		},
		"too long": {
			mutate: func(opts *Options) { opts.Duration = MaxDuration + time.Second },
			want:   "--duration",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			opts := Options{ProfileID: "scrap-prod-v1"}
			test.mutate(&opts)
			if _, err := ValidateOptions(opts); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateOptions error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func sampleCapacityReport() capacitysample.Report {
	return capacitysample.Report{
		ReportKind:                             capacitysample.ReportKind,
		ReleaseSHA:                             "abc123",
		DirtyTree:                              "clean",
		ProfileID:                              "scrap-prod-v1",
		EnvironmentID:                          "local-kind",
		AdvisoryOnly:                           true,
		DoesNotSatisfyProductionReadinessGates: true,
		Backend: []capacitysample.RequestSample{
			{Target: "backend", Operation: "put", Method: "PUT", StatusCode: 200, Bytes: 128, LatencyMillis: 10},
			{Target: "backend", Operation: "put", Method: "PUT", StatusCode: 200, Bytes: 256, LatencyMillis: 20},
			{Target: "backend", Operation: "head", Method: "HEAD", StatusCode: 200, LatencyMillis: 5},
		},
		OpenBao: []capacitysample.RequestSample{
			{Target: "openbao", Operation: "wrap", Method: "POST", StatusCode: 200, LatencyMillis: 5},
			{Target: "openbao", Operation: "unwrap", Method: "POST", StatusCode: 200, LatencyMillis: 6},
			{Target: "openbao", Operation: "rewrap", Method: "POST", StatusCode: 200, LatencyMillis: 7},
		},
		ProposedProfile: capacitysample.ProposedCapacityProfile{
			ProfileID: "scrap-prod-v1-advisory",
			CircuitBreakers: capacitysample.ProposedCircuitBreaker{
				ConsecutiveFailures: 3,
				ErrorRatePermille:   100,
				MinimumSamples:      3,
				OpenIntervalMillis:  int64((30 * time.Second) / time.Millisecond),
				HalfOpenRequests:    2,
			},
		},
		EvidenceLimits: []string{"capacity sample evidence is advisory non-production evidence only"},
	}
}

func writeCapacityReport(t *testing.T, report capacitysample.Report) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "capacity-sample-advisory.json")
	// #nosec G304 -- the path is built under the test-owned temporary directory.
	file, err := os.Create(path)
	testutil.RequireNoErrorf(t, err, "create capacity report")
	testutil.RequireNoErrorf(t, json.NewEncoder(file).Encode(report), "encode capacity report")
	testutil.RequireNoErrorf(t, file.Close(), "close capacity report")
	return path
}

func sampleRunway() *adminv1.CapacityRunway {
	return &adminv1.CapacityRunway{
		CapacityProfileId:    "scrap-prod-v1",
		UsableBytesRemaining: 1024 * 1024 * 1024,
		EstimatedBytesPerDay: 64 * 1024 * 1024,
		RunwayDays:           16,
	}
}

func sampleLocalRunway() *adminv1.CapacityRunway {
	runway := sampleRunway()
	runway.CapacityProfileId = "local-non-production"
	return runway
}

type fakeInspect struct {
	runway *adminv1.CapacityRunway
}

func (f fakeInspect) GetCapacityRunway(context.Context, *adminv1.GetCapacityRunwayRequest, ...grpc.CallOption) (*adminv1.GetCapacityRunwayResponse, error) {
	return &adminv1.GetCapacityRunwayResponse{Runway: f.runway}, nil
}

type fallbackInspect struct {
	runway *adminv1.CapacityRunway
}

func (f fallbackInspect) GetCapacityRunway(_ context.Context, req *adminv1.GetCapacityRunwayRequest, _ ...grpc.CallOption) (*adminv1.GetCapacityRunwayResponse, error) {
	if req.GetCapacityProfileId() != "" {
		return nil, status.Error(codes.NotFound, "capacity profile not found")
	}
	return &adminv1.GetCapacityRunwayResponse{Runway: f.runway}, nil
}

type fakeRepair struct {
	items []*adminv1.RepairQueueItem
}

func (f fakeRepair) GetRepairQueue(context.Context, *adminv1.GetRepairQueueRequest, ...grpc.CallOption) (*adminv1.GetRepairQueueResponse, error) {
	return &adminv1.GetRepairQueueResponse{Items: f.items}, nil
}

type fakeOperations struct {
	operations []*adminv1.Operation
}

func (f fakeOperations) ListOperations(context.Context, *adminv1.ListOperationsRequest, ...grpc.CallOption) (*adminv1.ListOperationsResponse, error) {
	return &adminv1.ListOperationsResponse{Operations: f.operations}, nil
}

type fakeDocuments struct {
	values       map[string][]byte
	corruptReads bool
}

func newFakeDocuments() *fakeDocuments {
	return &fakeDocuments{values: map[string][]byte{}}
}

func (f *fakeDocuments) WriteDocument(ctx context.Context, _ ...grpc.CallOption) (grpc.ClientStreamingClient[scrapv1.WriteDocumentRequest, scrapv1.WriteDocumentResponse], error) {
	return &fakeWriteStream{ctx: ctx, documents: f}, nil
}

func (f *fakeDocuments) HeadDocument(_ context.Context, req *scrapv1.HeadDocumentRequest, _ ...grpc.CallOption) (*scrapv1.HeadDocumentResponse, error) {
	key := documentKey(req.GetIdentity())
	body, ok := f.values[key]
	if !ok {
		return nil, status.Error(codes.NotFound, "document not found")
	}
	return &scrapv1.HeadDocumentResponse{Metadata: documentMetadata(req.GetIdentity(), body)}, nil
}

func (f *fakeDocuments) ReadDocument(ctx context.Context, req *scrapv1.ReadDocumentRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[scrapv1.ReadDocumentResponse], error) {
	key := documentKey(req.GetIdentity())
	body, ok := f.values[key]
	if !ok {
		return nil, status.Error(codes.NotFound, "document not found")
	}
	readBody := append([]byte(nil), body...)
	if f.corruptReads && len(readBody) > 0 {
		readBody[0] ^= 0xff
	}
	return &fakeReadStream{
		ctx: ctx,
		responses: []*scrapv1.ReadDocumentResponse{
			{
				Message: &scrapv1.ReadDocumentResponse_Metadata{Metadata: &scrapv1.ReadDocumentMetadata{
					Metadata: documentMetadata(req.GetIdentity(), body),
					Source:   scrapv1.StorageSource_STORAGE_SOURCE_LOCAL,
				}},
			},
			{
				Message: &scrapv1.ReadDocumentResponse_Chunk{Chunk: &scrapv1.ReadDocumentChunk{Data: readBody}},
			},
		},
	}, nil
}

type fakeWriteStream struct {
	grpc.ClientStream
	ctx       context.Context
	documents *fakeDocuments
	init      *scrapv1.WriteDocumentInit
	body      []byte
}

func (s *fakeWriteStream) Send(req *scrapv1.WriteDocumentRequest) error {
	if init := req.GetInit(); init != nil {
		s.init = init
		return nil
	}
	if chunk := req.GetChunk(); chunk != nil {
		s.body = append(s.body, chunk.GetData()...)
		return nil
	}
	return status.Error(codes.InvalidArgument, "missing write message")
}

func (s *fakeWriteStream) CloseAndRecv() (*scrapv1.WriteDocumentResponse, error) {
	if s.init == nil {
		return nil, status.Error(codes.InvalidArgument, "missing init")
	}
	key := documentKey(s.init.GetIdentity())
	s.documents.values[key] = append([]byte(nil), s.body...)
	return &scrapv1.WriteDocumentResponse{
		Metadata:             documentMetadata(s.init.GetIdentity(), s.body),
		DesiredReplicaCount:  1,
		AchievedReplicaCount: 1,
	}, nil
}

func (s *fakeWriteStream) Header() (metadata.MD, error) { return metadata.MD{}, nil }
func (s *fakeWriteStream) Trailer() metadata.MD         { return nil }
func (s *fakeWriteStream) CloseSend() error             { return nil }
func (s *fakeWriteStream) Context() context.Context     { return s.ctx }
func (s *fakeWriteStream) SendMsg(msg any) error {
	req, ok := msg.(*scrapv1.WriteDocumentRequest)
	if !ok {
		return errors.New("unexpected write message")
	}
	return s.Send(req)
}
func (s *fakeWriteStream) RecvMsg(any) error { return nil }

type fakeReadStream struct {
	grpc.ClientStream
	ctx       context.Context
	responses []*scrapv1.ReadDocumentResponse
	index     int
}

func (s *fakeReadStream) Recv() (*scrapv1.ReadDocumentResponse, error) {
	if s.index >= len(s.responses) {
		return nil, io.EOF
	}
	resp := s.responses[s.index]
	s.index++
	return resp, nil
}

func (s *fakeReadStream) Header() (metadata.MD, error) { return metadata.MD{}, nil }
func (s *fakeReadStream) Trailer() metadata.MD         { return nil }
func (s *fakeReadStream) CloseSend() error             { return nil }
func (s *fakeReadStream) Context() context.Context     { return s.ctx }
func (s *fakeReadStream) SendMsg(any) error            { return nil }
func (s *fakeReadStream) RecvMsg(msg any) error {
	resp, err := s.Recv()
	if err != nil {
		return err
	}
	target, ok := msg.(*scrapv1.ReadDocumentResponse)
	if !ok {
		return errors.New("unexpected read message")
	}
	proto.Merge(target, resp)
	return nil
}

func documentMetadata(identity *scrapv1.DocumentIdentity, body []byte) *scrapv1.DocumentMetadata {
	sum := sha256.Sum256(body)
	now := timestamppb.New(time.Unix(1700000000, 0).UTC())
	return &scrapv1.DocumentMetadata{
		Identity:       identity,
		DocumentClass:  scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
		PriorityClass:  scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
		Length:         uint64(len(body)),
		LogicalSha256:  sum[:],
		CreatedAt:      now,
		FinalizedAt:    now,
		Availability:   scrapv1.DocumentAvailability_DOCUMENT_AVAILABILITY_HOT,
		LifecycleState: scrapv1.DocumentLifecycleState_DOCUMENT_LIFECYCLE_STATE_ACTIVE,
	}
}

func documentKey(identity *scrapv1.DocumentIdentity) string {
	return strings.Join([]string{identity.GetTenantId(), identity.GetTransactionId(), identity.GetDocumentName()}, "/")
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func hasViolation(violations []ThresholdViolation, code string) bool {
	for _, violation := range violations {
		if violation.Code == code {
			return true
		}
	}
	return false
}
