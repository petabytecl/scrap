package localdrill

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/petabytecl/scrap/internal/capacitysample"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	scrapv1 "github.com/petabytecl/scrap/internal/gen/scrap/v1"
	"github.com/petabytecl/scrap/internal/openbaosmoke"
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestRunRecordsRunbookCommandsAndEvidence(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	capacityPath := writeCapacityReport(t, dir, true)
	openbaoPath := writeOpenBaoReport(t, dir, true)
	ops := &fakeOperationClient{operations: map[string]*adminv1.Operation{}}
	dr := &fakeDRClient{
		ops: ops,
		readiness: &adminv1.RecoveryReadiness{
			Ready:                        true,
			LatestRestorableCheckpointAt: timestamppb.New(time.Now().Add(time.Hour).UTC()),
			Warnings: []*adminv1.OperationWarning{
				{Code: "SCRAP_DR_MEASURED_EVIDENCE_ONLY", Message: "local evidence only"},
			},
		},
	}

	report, err := Run(ctx, Options{
		ReleaseSHA:               "release-sha",
		DirtyTree:                "clean",
		CapacitySampleReportPath: capacityPath,
		OpenBaoSmokeReportPath:   openbaoPath,
		DrillID:                  "drill-1",
		Duration:                 5 * time.Second,
		PollInterval:             time.Millisecond,
		Now:                      func() time.Time { return time.Unix(2_000, 0).UTC() },
	}, Clients{
		FixtureWriter: fakeFixtureWriter{},
		DR:            dr,
		Operations:    ops,
	})
	testutil.RequireNoErrorf(t, err, "run drill")
	testutil.RequireEqualf(t, report.Status, StatusPassed, "report status")
	testutil.RequireEqualf(t, report.ReportKind, ReportKind, "report kind")
	gotSteps := make([]string, 0, len(report.Commands))
	for _, command := range report.Commands {
		gotSteps = append(gotSteps, command.Step)
	}
	wantSteps := []string{
		"fixture-write",
		"inspect recovery-readiness",
		"plan recovery dry-run",
		"start metadata-restore",
		"watch metadata-restore",
		"plan recovery",
		"start copy-verify",
		"watch copy-verify",
		"start dr-drill",
		"watch dr-drill",
	}
	testutil.RequireDeepEqualf(t, gotSteps, wantSteps, "command steps")
	requireLocalDrillEvidence(t, report)
}

func requireLocalDrillEvidence(t *testing.T, report Report) {
	t.Helper()
	testutil.RequireTruef(t, report.Recovery.LatestRestorableCheckpointAt != "", "recovery checkpoint evidence is empty")
	testutil.RequireTruef(t, report.Recovery.OperationIDs["metadata_restore"] != "", "metadata restore operation id is empty")
	testutil.RequireTruef(t, report.Recovery.OperationIDs["copy_verify"] != "", "copy verify operation id is empty")
	testutil.RequireTruef(t, report.Recovery.OperationIDs["dr_drill"] != "", "dr drill operation id is empty")
	testutil.RequireTruef(t, report.BackendArtifacts.LocalStackProbe.Passed, "local stack probe did not pass")
	testutil.RequireEqualf(t, report.BackendArtifacts.LocalStackProbe.OperationCounts["PUT"], 1, "local stack PUT count")
	testutil.RequireEqualf(t, report.BackendArtifacts.PublishedCheckpoint.VerifiedEnvelopeObjects, "1", "verified envelope object count")
	testutil.RequireEqualf(t, report.BackendArtifacts.PublishedCheckpoint.BlocksRestored, "1", "blocks restored count")
	testutil.RequireTruef(t, report.OpenBao.Passed, "openbao evidence did not pass")
	testutil.RequireEqualf(t, len(report.OpenBao.CryptoUnavailableOutcomes), 2, "openbao crypto unavailable outcome count")
	testutil.RequireTruef(t, report.Recovery.NoFormalRTOPromise, "RTO disclaimer missing")
	testutil.RequireTruef(t, report.Recovery.NoFormalRPOPromise, "RPO disclaimer missing")
	testutil.RequireFalsef(t, report.Recovery.DownstreamDeploymentApproval, "downstream deployment approval = true")
}

func TestRunFailsWhenEvidenceInputsDoNotProveRequiredBackends(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	capacityPath := writeCapacityReport(t, dir, false)
	openbaoPath := writeOpenBaoReport(t, dir, false)

	report, err := Run(ctx, Options{
		CapacitySampleReportPath: capacityPath,
		OpenBaoSmokeReportPath:   openbaoPath,
		DrillID:                  "drill-1",
		Now:                      func() time.Time { return time.Unix(2_000, 0).UTC() },
	}, Clients{
		FixtureWriter: fakeFixtureWriter{},
		DR:            &fakeDRClient{},
		Operations:    &fakeOperationClient{operations: map[string]*adminv1.Operation{}},
	})
	if !errors.Is(err, ErrDrillFailed) {
		t.Fatalf("run error = %v, want %v", err, ErrDrillFailed)
	}
	if report.Status != StatusFailed || len(report.FailedOrSkippedSteps) != 2 {
		t.Fatalf("failure report = %#v", report)
	}
}

func TestRunAbortsOnFixtureConnectivityFailure(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	capacityPath := writeCapacityReport(t, dir, true)
	openbaoPath := writeOpenBaoReport(t, dir, true)
	ops := &fakeOperationClient{operations: map[string]*adminv1.Operation{}}

	report, err := Run(ctx, Options{
		CapacitySampleReportPath: capacityPath,
		OpenBaoSmokeReportPath:   openbaoPath,
		DrillID:                  "drill-1",
		Duration:                 time.Second,
		PollInterval:             time.Millisecond,
		Now:                      func() time.Time { return time.Unix(2_000, 0).UTC() },
	}, Clients{
		FixtureWriter: failingFixtureWriter{err: status.Error(codes.Unavailable, "public API unavailable")},
		DR: &fakeDRClient{
			ops: ops,
			readiness: &adminv1.RecoveryReadiness{
				Ready:                        true,
				LatestRestorableCheckpointAt: timestamppb.Now(),
			},
		},
		Operations: ops,
	})
	if !errors.Is(err, ErrDrillFailed) {
		t.Fatalf("run error = %v, want %v", err, ErrDrillFailed)
	}
	testutil.RequireEqualf(t, report.Status, StatusFailed, "report status")
	testutil.RequireEqualf(t, report.Fixture.ErrorClass, "transient", "fixture error class")
	testutil.RequireEqualf(t, report.Commands[0].Step, "fixture-write", "failed command step")
	testutil.RequireEqualf(t, report.Commands[0].ErrorClass, "transient", "command error class")
	testutil.RequireEqualf(t, report.FailedOrSkippedSteps[0].Step, "fixture-write", "failure step")
}

func TestRunRecordsQuorumLossScenario(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	capacityPath := writeCapacityReport(t, dir, true)
	openbaoPath := writeOpenBaoReport(t, dir, true)
	ops := &fakeOperationClient{operations: map[string]*adminv1.Operation{}}
	dr := &quorumLossDRClient{fakeDRClient: fakeDRClient{
		ops: ops,
		readiness: &adminv1.RecoveryReadiness{
			Ready:                        true,
			LatestRestorableCheckpointAt: timestamppb.New(time.Now().Add(time.Hour).UTC()),
		},
	}}

	report, err := Run(ctx, Options{
		CapacitySampleReportPath: capacityPath,
		OpenBaoSmokeReportPath:   openbaoPath,
		DrillID:                  "drill-1",
		Duration:                 5 * time.Second,
		PollInterval:             time.Millisecond,
		Now:                      func() time.Time { return time.Unix(2_000, 0).UTC() },
	}, Clients{
		FixtureWriter: fakeFixtureWriter{},
		DR:            dr,
		Operations:    ops,
	})
	if !errors.Is(err, ErrDrillFailed) {
		t.Fatalf("run error = %v, want %v", err, ErrDrillFailed)
	}
	testutil.RequireEqualf(t, report.Status, StatusFailed, "report status")
	last := report.Commands[len(report.Commands)-1]
	testutil.RequireEqualf(t, last.Step, "watch copy-verify", "failed command step")
	testutil.RequireEqualf(t, last.OperationState, adminv1.OperationState_OPERATION_STATE_FAILED.String(), "failed operation state")
	testutil.RequireEqualf(t, last.ErrorClass, "SCRAP_RAFT_QUORUM_LOSS", "failed operation error class")
	testutil.RequireEqualf(t, report.FailedOrSkippedSteps[0].Step, "watch copy-verify", "failure step")
}

func TestValidateOptionsAndErrorClassificationBranches(t *testing.T) {
	fixedNow := func() time.Time { return time.Unix(2_000, 0).UTC() }
	opts, err := ValidateOptions(Options{Now: fixedNow})
	testutil.RequireNoErrorf(t, err, "validate defaults")
	testutil.RequireEqualf(t, opts.DrillID, "local-dr-19700101T003320Z", "default drill id")
	testutil.RequireEqualf(t, opts.FixtureSizeBytes, uint64(DefaultFixtureSizeBytes), "default fixture size")

	for _, tc := range []struct {
		name string
		opts Options
		want string
	}{
		{name: "negative duration", opts: Options{Duration: -time.Second}, want: "duration must be positive"},
		{name: "negative poll interval", opts: Options{PollInterval: -time.Second}, want: "poll interval must be positive"},
		{name: "oversized fixture", opts: Options{FixtureSizeBytes: MaxFixtureSizeBytes + 1}, want: "fixture size"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateOptions(tc.opts)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want containing %q", err, tc.want)
			}
		})
	}

	testutil.RequireEqualf(t, classifyError(nil), "", "nil error class")
	testutil.RequireEqualf(t, classifyError(context.Canceled), "transient", "canceled error class")
	testutil.RequireEqualf(t, classifyError(status.Error(codes.NotFound, "missing")), "not_found", "not found error class")
	testutil.RequireEqualf(t, classifyError(status.Error(codes.PermissionDenied, "denied")), "auth", "auth error class")
	testutil.RequireEqualf(t, classifyError(status.Error(codes.FailedPrecondition, "conflict")), "conflict", "conflict error class")
	testutil.RequireEqualf(t, classifyError(status.Error(codes.InvalidArgument, "invalid")), "permanent", "permanent error class")
	testutil.RequireEqualf(t, classifyError(status.Error(codes.Unknown, "unknown")), "unknown", "unknown status class")
	testutil.RequireEqualf(t, classifyError(errors.New("plain")), "permanent", "plain error class")
}

func TestGRPCFixtureWriterWritesInitChunkAndEvidence(t *testing.T) {
	stream := &fakeWriteDocumentStream{
		response: &scrapv1.WriteDocumentResponse{
			Metadata: &scrapv1.DocumentMetadata{
				Availability:   scrapv1.DocumentAvailability_DOCUMENT_AVAILABILITY_HOT,
				LifecycleState: scrapv1.DocumentLifecycleState_DOCUMENT_LIFECYCLE_STATE_ACTIVE,
			},
			DesiredReplicaCount:  3,
			AchievedReplicaCount: 2,
		},
	}
	writer := GRPCFixtureWriter{Client: fakeDocumentServiceClient{stream: stream}}
	doc := FixtureDocument{TenantID: "tenant", TransactionID: "txn", DocumentName: "fixture.bin"}
	data := []byte("fixture")

	evidence, err := writer.WriteFixtureDocument(context.Background(), doc, data, Options{
		DrillID:   "drill-1",
		ProfileID: "profile-a",
	})
	testutil.RequireNoErrorf(t, err, "write fixture document")
	testutil.RequireEqualf(t, len(stream.sent), 2, "sent message count")
	init := stream.sent[0].GetInit()
	testutil.RequireEqualf(t, init.GetIdentity().GetTenantId(), "tenant", "tenant")
	testutil.RequireEqualf(t, init.GetTags()["profile"], "profile-a", "profile tag")
	testutil.RequireEqualf(t, init.GetClientIdempotencyKey(), "local-dr-drill-drill-1", "idempotency key")
	testutil.RequireEqualf(t, string(stream.sent[1].GetChunk().GetData()), "fixture", "chunk data")
	testutil.RequireEqualf(t, evidence.Bytes, uint64(len(data)), "evidence bytes")
	testutil.RequireEqualf(t, evidence.DesiredReplicas, uint32(3), "desired replicas")
	testutil.RequireEqualf(t, evidence.AchievedReplicas, uint32(2), "achieved replicas")
	testutil.RequireEqualf(t, evidence.Availability, scrapv1.DocumentAvailability_DOCUMENT_AVAILABILITY_HOT.String(), "availability")

	failed, err := (GRPCFixtureWriter{}).WriteFixtureDocument(context.Background(), doc, data, Options{DrillID: "drill-1"})
	if err == nil {
		t.Fatal("missing client was accepted")
	}
	testutil.RequireEqualf(t, failed.ErrorClass, "permanent", "missing client error class")
}

type fakeFixtureWriter struct{}

func (fakeFixtureWriter) WriteFixtureDocument(_ context.Context, doc FixtureDocument, data []byte, _ Options) (FixtureWriteEvidence, error) {
	sum := sha256.Sum256(data)
	return FixtureWriteEvidence{
		Document:            doc,
		Bytes:               uint64(len(data)),
		LogicalSHA256Digest: hex.EncodeToString(sum[:]),
		DesiredReplicas:     1,
		AchievedReplicas:    1,
		Availability:        "AVAILABILITY_HOT",
		LifecycleState:      "LIFECYCLE_STATE_ACTIVE",
	}, nil
}

type failingFixtureWriter struct {
	err error
}

func (w failingFixtureWriter) WriteFixtureDocument(_ context.Context, doc FixtureDocument, data []byte, _ Options) (FixtureWriteEvidence, error) {
	sum := sha256.Sum256(data)
	evidence := FixtureWriteEvidence{
		Document:            doc,
		Bytes:               uint64(len(data)),
		LogicalSHA256Digest: hex.EncodeToString(sum[:]),
	}
	return failedFixtureWrite(evidence, time.Now(), w.err)
}

type fakeDocumentServiceClient struct {
	scrapv1.DocumentServiceClient
	stream *fakeWriteDocumentStream
	err    error
}

func (c fakeDocumentServiceClient) WriteDocument(context.Context, ...grpc.CallOption) (grpc.ClientStreamingClient[scrapv1.WriteDocumentRequest, scrapv1.WriteDocumentResponse], error) {
	if c.err != nil {
		return nil, c.err
	}
	return c.stream, nil
}

type fakeWriteDocumentStream struct {
	grpc.ClientStream
	sent     []*scrapv1.WriteDocumentRequest
	response *scrapv1.WriteDocumentResponse
	err      error
}

func (s *fakeWriteDocumentStream) Send(req *scrapv1.WriteDocumentRequest) error {
	if s.err != nil {
		return s.err
	}
	s.sent = append(s.sent, req)
	return nil
}

func (s *fakeWriteDocumentStream) CloseAndRecv() (*scrapv1.WriteDocumentResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.response, nil
}

type fakeDRClient struct {
	ops       *fakeOperationClient
	readiness *adminv1.RecoveryReadiness
	plans     int
}

func (c *fakeDRClient) GetRecoveryReadiness(context.Context, *adminv1.GetRecoveryReadinessRequest, ...grpc.CallOption) (*adminv1.GetRecoveryReadinessResponse, error) {
	return &adminv1.GetRecoveryReadinessResponse{Readiness: c.readiness}, nil
}

func (c *fakeDRClient) PlanRecovery(_ context.Context, req *adminv1.PlanRecoveryRequest, _ ...grpc.CallOption) (*adminv1.PlanRecoveryResponse, error) {
	c.plans++
	id := "recovery-plan"
	hash := "recovery-hash"
	if req.GetDryRun() {
		id = "dry-run-plan"
		hash = "dry-run-hash"
	}
	return &adminv1.PlanRecoveryResponse{Plan: &adminv1.OperationPlan{
		OperationPlanId: id,
		PlanHash:        hash,
		Targets:         req.GetTargets(),
		Metadata:        req.GetMetadata(),
	}}, nil
}

func (c *fakeDRClient) StartMetadataRestore(_ context.Context, req *adminv1.StartMetadataRestoreRequest, _ ...grpc.CallOption) (*adminv1.StartMetadataRestoreResponse, error) {
	operation := c.succeededOperation(req.GetOperationId(), "metadata-restore", true, map[string]string{
		"manifest_id":          "manifest-1",
		"documents":            "1",
		"upload_intents":       "1",
		"recovery_report_kind": "measured_evidence",
	})
	return &adminv1.StartMetadataRestoreResponse{Operation: operation}, nil
}

func (c *fakeDRClient) StartCopyVerify(_ context.Context, req *adminv1.StartCopyVerifyRequest, _ ...grpc.CallOption) (*adminv1.StartCopyVerifyResponse, error) {
	operation := c.succeededOperation(req.GetOperationId(), "copy-verify", false, map[string]string{
		"manifest_id":               "manifest-1",
		"generation":                "10",
		"verified_artifacts":        "4",
		"verified_required_objects": "3",
		"verified_block_objects":    "1",
		"verified_index_objects":    "1",
		"verified_envelope_objects": "1",
	})
	return &adminv1.StartCopyVerifyResponse{Operation: operation}, nil
}

func (c *fakeDRClient) StartDRDrill(_ context.Context, req *adminv1.StartDRDrillRequest, _ ...grpc.CallOption) (*adminv1.StartDRDrillResponse, error) {
	operation := c.succeededOperation(req.GetOperationId(), "dr-drill", false, map[string]string{
		"manifest_id":               "manifest-1",
		"generation":                "10",
		"documents":                 "1",
		"upload_intents":            "1",
		"verified_artifacts":        "4",
		"verified_required_objects": "3",
		"verified_block_objects":    "1",
		"verified_index_objects":    "1",
		"verified_envelope_objects": "1",
		"blocks_restored":           "1",
		"rto_promise":               "none",
		"rpo_promise":               "none",
	})
	return &adminv1.StartDRDrillResponse{Operation: operation}, nil
}

func (c *fakeDRClient) succeededOperation(id, operationType string, dryRun bool, counters map[string]string) *adminv1.Operation {
	operation := &adminv1.Operation{
		OperationId:   id,
		OperationType: operationType,
		State:         adminv1.OperationState_OPERATION_STATE_SUCCEEDED,
		DryRun:        dryRun,
		Progress: &adminv1.OperationProgress{
			WorkUnitsTotal:     1,
			WorkUnitsCompleted: 1,
			Message:            operationType + " succeeded",
			Counters:           counters,
		},
	}
	c.ops.operations[id] = operation
	return operation
}

type quorumLossDRClient struct {
	fakeDRClient
}

func (c *quorumLossDRClient) StartCopyVerify(_ context.Context, req *adminv1.StartCopyVerifyRequest, _ ...grpc.CallOption) (*adminv1.StartCopyVerifyResponse, error) {
	started := &adminv1.Operation{
		OperationId:   req.GetOperationId(),
		OperationType: "copy-verify",
		State:         adminv1.OperationState_OPERATION_STATE_RUNNING,
	}
	failed := &adminv1.Operation{
		OperationId:   req.GetOperationId(),
		OperationType: "copy-verify",
		State:         adminv1.OperationState_OPERATION_STATE_FAILED,
		LastError: &adminv1.OperationError{
			Code:    "SCRAP_RAFT_QUORUM_LOSS",
			Message: "copy verification lost quorum",
		},
	}
	c.ops.operations[req.GetOperationId()] = failed
	return &adminv1.StartCopyVerifyResponse{Operation: started}, nil
}

type fakeOperationClient struct {
	operations map[string]*adminv1.Operation
}

func (c *fakeOperationClient) GetOperation(_ context.Context, req *adminv1.GetOperationRequest, _ ...grpc.CallOption) (*adminv1.GetOperationResponse, error) {
	return &adminv1.GetOperationResponse{Operation: c.operations[req.GetOperationId()]}, nil
}

func writeCapacityReport(t *testing.T, dir string, passing bool) string {
	t.Helper()
	report := capacitysample.Report{
		ReportKind:    capacitysample.ReportKind,
		ProfileID:     DefaultProfileID,
		EnvironmentID: DefaultEnvironmentID,
		AdvisoryOnly:  true,
		Backend: []capacitysample.RequestSample{
			{Target: "backend", Operation: "put", Method: "PUT", StatusCode: 200, RedactedRequestID: "request-put"},
			{Target: "backend", Operation: "head", Method: "HEAD", StatusCode: 200, RedactedRequestID: "request-head"},
			{Target: "backend", Operation: "get", Method: "GET", StatusCode: 200, RedactedRequestID: "request-get"},
		},
		RedactedRequestIDs: []string{"request-put", "request-head", "request-get"},
		ProposedProfile: capacitysample.ProposedCapacityProfile{
			Provider: "s3_like",
		},
	}
	if !passing {
		report.Backend[2].ErrorClass = "transient"
		report.Backend[2].StatusCode = 503
	}
	return writeJSON(t, dir, "capacity.json", report)
}

func writeOpenBaoReport(t *testing.T, dir string, passing bool) string {
	t.Helper()
	report := openbaosmoke.Report{
		ReportKind:        openbaosmoke.ReportKind,
		Status:            StatusPassed,
		Namespace:         "scrap-local",
		Deployment:        "openbao",
		KubernetesClient:  "openbao-transit-smoke",
		TransitKeyPath:    "transit/keys/scrap-backend",
		AuditDeviceStatus: "enabled:file:/tmp/openbao-audit.log",
		Transit: openbaosmoke.TransitEvidence{
			DataKeyVersion:       1,
			RewrapVersion:        2,
			KeyVersionAfter:      2,
			UnwrapAADMatched:     true,
			RewrapAADMatched:     true,
			PlaintextDEKRedacted: true,
			WrappedDEKRedacted:   true,
		},
		CryptoUnavailableOutcomes: []openbaosmoke.CryptoUnavailableCase{
			{Name: "missing-key-version", Status: "crypto-unavailable", ErrorClass: "key-material-unavailable"},
			{Name: "transit-outage", Status: "crypto-unavailable", ErrorClass: "transit-unavailable"},
		},
	}
	if !passing {
		report.Status = StatusFailed
		report.CryptoUnavailableOutcomes[0].Status = "unexpected-success"
	}
	return writeJSON(t, dir, "openbao.json", report)
}

func writeJSON(t *testing.T, dir, name string, value any) string {
	t.Helper()
	path := dir + string(os.PathSeparator) + name
	// #nosec G304 -- the path is built under the test-owned temporary directory.
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Fatalf("close %s: %v", name, err)
		}
	}()
	if err := json.NewEncoder(file).Encode(value); err != nil {
		t.Fatalf("encode %s: %v", name, err)
	}
	return path
}
