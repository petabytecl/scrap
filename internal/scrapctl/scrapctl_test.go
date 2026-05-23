package scrapctl

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"

	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
)

func TestRunRejectsMissingPlanTargetsBeforeDial(t *testing.T) {
	calledDial := false
	err := Run(context.Background(), Config{
		Dial: func(context.Context, string) (*grpc.ClientConn, error) {
			calledDial = true
			return nil, errors.New("dial should not happen")
		},
	}, []string{"plan", "restore"}, bytes.NewBuffer(nil))
	var usage usageError
	if !errors.As(err, &usage) {
		t.Fatalf("error = %v, want usageError", err)
	}
	if calledDial {
		t.Fatal("dialer was called for invalid command")
	}
}

func TestRunRejectsOversizedPageSizeBeforeDial(t *testing.T) {
	calledDial := false
	err := Run(context.Background(), Config{
		Dial: func(context.Context, string) (*grpc.ClientConn, error) {
			calledDial = true
			return nil, errors.New("dial should not happen")
		},
	}, []string{"operations", "list", "--page-size", "4294967296"}, bytes.NewBuffer(nil))
	var usage usageError
	if !errors.As(err, &usage) {
		t.Fatalf("error = %v, want usageError", err)
	}
	if !strings.Contains(err.Error(), "--page-size") {
		t.Fatalf("error = %v, want page-size usage error", err)
	}
	if calledDial {
		t.Fatal("dialer was called for invalid page size")
	}
}

func TestPlanRestoreCallsGeneratedAdminClient(t *testing.T) {
	restore := &fakeRestoreServer{}
	dial := newBufconnDialer(t, func(server *grpc.Server) {
		adminv1.RegisterRestoreServiceServer(server, restore)
	})

	var out bytes.Buffer
	err := Run(context.Background(), Config{Dial: dial}, []string{
		"--admin-addr", "bufnet",
		"plan", "restore",
		"--target", "document:tenant-a/txn-a/invoice.xml",
		"--dry-run",
		"--metadata", "ticket=INC-1",
	}, &out)
	if err != nil {
		t.Fatalf("run plan restore: %v", err)
	}
	if restore.planRestoreReq == nil {
		t.Fatal("PlanRestore was not called")
	}
	if !restore.planRestoreReq.GetDryRun() || restore.planRestoreReq.GetMetadata()["ticket"] != "INC-1" {
		t.Fatalf("request = %#v, want dry-run metadata", restore.planRestoreReq)
	}
	doc := restore.planRestoreReq.GetTargets()[0].GetDocument()
	if doc.GetTenantId() != "tenant-a" || doc.GetTransactionId() != "txn-a" || doc.GetDocumentName() != "invoice.xml" {
		t.Fatalf("target = %#v, want requested document target", doc)
	}
	output := out.String()
	for _, want := range []string{"\"operation_plan_id\":", "\"plan-1\"", "\"plan_hash\":", "\"hash-1\"", "SCRAP_RESTORE_COLD_BYTES"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output = %s, missing %s", output, want)
		}
	}
}

func TestStatusPrintsOperationWarningsAndTypedFailure(t *testing.T) {
	operations := &fakeOperationServer{}
	dial := newBufconnDialer(t, func(server *grpc.Server) {
		adminv1.RegisterOperationServiceServer(server, operations)
	})

	var out bytes.Buffer
	err := Run(context.Background(), Config{Dial: dial}, []string{
		"--admin-addr", "bufnet",
		"status",
		"--operation-id", "op-1",
	}, &out)
	if err != nil {
		t.Fatalf("run status: %v", err)
	}
	if operations.gotOperationID != "op-1" {
		t.Fatalf("operation id = %q, want op-1", operations.gotOperationID)
	}
	output := out.String()
	for _, want := range []string{"OPERATION_STATE_FAILED", "SCRAP_REPAIR_SLOW", "SCRAP_REPAIR_FAILED", "last_error"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output = %s, missing %s", output, want)
		}
	}
}

func TestStartRestoreReturnsRPCErrorWithoutNilResponsePanic(t *testing.T) {
	restore := &fakeRestoreServer{startErr: status.Error(codes.InvalidArgument, "invalid start")}
	dial := newBufconnDialer(t, func(server *grpc.Server) {
		adminv1.RegisterRestoreServiceServer(server, restore)
	})

	err := Run(context.Background(), Config{Dial: dial}, []string{
		"--admin-addr", "bufnet",
		"start", "restore",
		"--plan-id", "plan-1",
		"--plan-hash", "hash-1",
		"--operation-id", "op-1",
	}, bytes.NewBuffer(nil))
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Fatalf("code = %s, want %s; err = %v", code, codes.InvalidArgument, err)
	}
}

func TestWatchPrintsStreamedOperationEvents(t *testing.T) {
	operations := &fakeOperationServer{}
	dial := newBufconnDialer(t, func(server *grpc.Server) {
		adminv1.RegisterOperationServiceServer(server, operations)
	})

	var out bytes.Buffer
	err := Run(context.Background(), Config{Dial: dial}, []string{
		"--admin-addr", "bufnet",
		"watch",
		"--operation-id", "op-1",
		"--after-sequence", "1",
	}, &out)
	if err != nil {
		t.Fatalf("run watch: %v", err)
	}
	if operations.watchOperationID != "op-1" || operations.afterSequence == nil || *operations.afterSequence != 1 {
		t.Fatalf("watch request id=%q after=%v, want op-1 after 1", operations.watchOperationID, operations.afterSequence)
	}
	output := out.String()
	for _, want := range []string{"\"sequence\":", "\"2\"", "OPERATION_STATE_RUNNING", "OPERATION_STATE_SUCCEEDED"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output = %s, missing %s", output, want)
		}
	}
}

func TestFailureFromGRPCBadRequestExposesViolations(t *testing.T) {
	badRequest := &errdetails.BadRequest{FieldViolations: []*errdetails.BadRequest_FieldViolation{
		{Field: "operation_id", Reason: "SCRAP_INVALID_UUIDV7", Description: "operation_id must be a UUIDv7"},
	}}
	st, err := status.New(codes.InvalidArgument, "invalid request").WithDetails(badRequest)
	if err != nil {
		t.Fatalf("build status: %v", err)
	}
	payload := failureFromError(st.Err())
	if payload.Code != "InvalidArgument" || len(payload.Violations) != 1 {
		t.Fatalf("payload = %#v, want invalid argument with one violation", payload)
	}
	if payload.Violations[0].Field != "operation_id" || payload.Violations[0].Reason != "SCRAP_INVALID_UUIDV7" {
		t.Fatalf("violation = %#v, want operation_id UUIDv7 violation", payload.Violations[0])
	}
}

type fakeRestoreServer struct {
	adminv1.UnimplementedRestoreServiceServer
	planRestoreReq *adminv1.PlanRestoreRequest
	startErr       error
}

func (s *fakeRestoreServer) PlanRestore(_ context.Context, req *adminv1.PlanRestoreRequest) (*adminv1.PlanRestoreResponse, error) {
	s.planRestoreReq = req
	return &adminv1.PlanRestoreResponse{
		Plan: &adminv1.OperationPlan{
			OperationPlanId: "plan-1",
			PlanHash:        "hash-1",
			ExpiresAt:       timestamppb.New(time.Unix(100, 0).UTC()),
			Targets:         req.GetTargets(),
			Warnings: []*adminv1.OperationWarning{
				{Code: "SCRAP_RESTORE_COLD_BYTES", Message: "restore reads cold backend bytes"},
			},
		},
	}, nil
}

func (s *fakeRestoreServer) StartRestore(_ context.Context, _ *adminv1.StartRestoreRequest) (*adminv1.StartRestoreResponse, error) {
	if s.startErr != nil {
		return nil, s.startErr
	}
	return &adminv1.StartRestoreResponse{Operation: &adminv1.Operation{OperationId: "op-1"}}, nil
}

type fakeOperationServer struct {
	adminv1.UnimplementedOperationServiceServer
	gotOperationID   string
	watchOperationID string
	afterSequence    *uint64
}

func (s *fakeOperationServer) GetOperation(_ context.Context, req *adminv1.GetOperationRequest) (*adminv1.GetOperationResponse, error) {
	s.gotOperationID = req.GetOperationId()
	return &adminv1.GetOperationResponse{
		Operation: &adminv1.Operation{
			OperationId:   req.GetOperationId(),
			OperationType: "repair",
			State:         adminv1.OperationState_OPERATION_STATE_FAILED,
			Warnings: []*adminv1.OperationWarning{
				{Code: "SCRAP_REPAIR_SLOW", Message: "repair is slower than expected"},
			},
			LastError: &adminv1.OperationError{
				Code:    "SCRAP_REPAIR_FAILED",
				Message: "repair source was unavailable",
			},
		},
	}, nil
}

func (s *fakeOperationServer) WatchOperation(req *adminv1.WatchOperationRequest, stream grpc.ServerStreamingServer[adminv1.WatchOperationResponse]) error {
	s.watchOperationID = req.GetOperationId()
	s.afterSequence = req.AfterSequence
	for _, event := range []*adminv1.WatchOperationResponse{
		{
			Sequence: 2,
			Operation: &adminv1.Operation{
				OperationId:   req.GetOperationId(),
				OperationType: "repair",
				State:         adminv1.OperationState_OPERATION_STATE_RUNNING,
			},
			Delta: &adminv1.OperationDelta{State: adminv1.OperationState_OPERATION_STATE_RUNNING},
		},
		{
			Sequence: 3,
			Operation: &adminv1.Operation{
				OperationId:   req.GetOperationId(),
				OperationType: "repair",
				State:         adminv1.OperationState_OPERATION_STATE_SUCCEEDED,
			},
			Delta: &adminv1.OperationDelta{State: adminv1.OperationState_OPERATION_STATE_SUCCEEDED},
		},
	} {
		if err := stream.Send(event); err != nil {
			return err
		}
	}
	return nil
}

func newBufconnDialer(t *testing.T, register func(*grpc.Server)) Dialer {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	register(server)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	return func(context.Context, string) (*grpc.ClientConn, error) {
		return grpc.NewClient("passthrough:///bufnet",
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return listener.DialContext(ctx)
			}),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
	}
}
