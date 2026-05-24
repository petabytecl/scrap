package localdrill

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/petabytecl/scrap/internal/capacitysample"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
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
