package scrapctl

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/petabytecl/scrap/internal/authz"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestRunRejectsMissingPlanTargetsBeforeDial(t *testing.T) {
	calledDial := false
	err := Run(context.Background(), Config{
		Dial: func(context.Context, string, ...grpc.DialOption) (*grpc.ClientConn, error) {
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
		Dial: func(context.Context, string, ...grpc.DialOption) (*grpc.ClientConn, error) {
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
	testutil.RequireNoErrorf(t, err, "run plan restore")
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
	testutil.RequireNoErrorf(t, err, "run status")
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
	testutil.RequireNoErrorf(t, err, "run watch")
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
	testutil.RequireNoErrorf(t, err, "build status")
	payload := failureFromError(st.Err())
	if payload.Code != "InvalidArgument" || len(payload.Violations) != 1 {
		t.Fatalf("payload = %#v, want invalid argument with one violation", payload)
	}
	if payload.Violations[0].Field != "operation_id" || payload.Violations[0].Reason != "SCRAP_INVALID_UUIDV7" {
		t.Fatalf("violation = %#v, want operation_id UUIDv7 violation", payload.Violations[0])
	}
}

func TestRunAttachesWorkloadIdentityMetadata(t *testing.T) {
	inspect := &fakeInspectServer{}
	dial := newBufconnDialer(t, func(server *grpc.Server) {
		adminv1.RegisterInspectServiceServer(server, inspect)
	})

	err := Run(context.Background(), Config{Dial: dial}, []string{
		"--admin-addr", "bufnet",
		"--workload-identity", "local-operator",
		"inspect", "summary",
	}, bytes.NewBuffer(nil))
	testutil.RequireNoErrorf(t, err, "run inspect summary")
	if inspect.workloadIdentity != "local-operator" {
		t.Fatalf("workload identity = %q, want local-operator", inspect.workloadIdentity)
	}
}

func TestRunComposesWorkloadIdentityWithExistingInterceptors(t *testing.T) {
	inspect := &fakeInspectServer{}
	baseDial := newBufconnDialer(t, func(server *grpc.Server) {
		adminv1.RegisterInspectServiceServer(server, inspect)
	})
	dial := func(ctx context.Context, address string, options ...grpc.DialOption) (*grpc.ClientConn, error) {
		extra := grpc.WithChainUnaryInterceptor(func(
			ctx context.Context,
			method string,
			req any,
			reply any,
			cc *grpc.ClientConn,
			invoker grpc.UnaryInvoker,
			opts ...grpc.CallOption,
		) error {
			return invoker(metadata.AppendToOutgoingContext(ctx, "x-scrap-extra-test", "seen"), method, req, reply, cc, opts...)
		})
		return baseDial(ctx, address, append([]grpc.DialOption{extra}, options...)...)
	}

	err := Run(context.Background(), Config{Dial: dial}, []string{
		"--admin-addr", "bufnet",
		"--workload-identity", "local-operator",
		"inspect", "summary",
	}, bytes.NewBuffer(nil))
	testutil.RequireNoErrorf(t, err, "run inspect summary")
	if inspect.workloadIdentity != "local-operator" {
		t.Fatalf("workload identity = %q, want local-operator", inspect.workloadIdentity)
	}
	if inspect.extraMetadata != "seen" {
		t.Fatalf("extra metadata = %q, want seen", inspect.extraMetadata)
	}
}

func TestCapacitySampleRejectsUnboundedWorkloadBeforeDial(t *testing.T) {
	calledDial := false
	err := Run(context.Background(), Config{
		Dial: func(context.Context, string, ...grpc.DialOption) (*grpc.ClientConn, error) {
			calledDial = true
			return nil, errors.New("dial should not happen")
		},
	}, []string{
		"capacity", "sample",
		"--profile-id", "scrap-prod-v1",
		"--samples", "33",
	}, bytes.NewBuffer(nil))
	var usage usageError
	if !errors.As(err, &usage) {
		t.Fatalf("error = %v, want usageError", err)
	}
	if !strings.Contains(err.Error(), "--samples") {
		t.Fatalf("error = %v, want samples usage error", err)
	}
	if calledDial {
		t.Fatal("dialer was called for invalid capacity sample")
	}
}

func TestCapacitySampleWritesAdvisoryReport(t *testing.T) {
	backendServer := newCapacitySampleBackendServer(t)
	t.Cleanup(backendServer.Close)
	openbaoServer := newCapacitySampleOpenBaoServer()
	t.Cleanup(openbaoServer.Close)

	inspect := &fakeInspectServer{}
	dial := newBufconnDialer(t, func(server *grpc.Server) {
		adminv1.RegisterInspectServiceServer(server, inspect)
	})

	var out bytes.Buffer
	err := Run(context.Background(), Config{Dial: dial}, []string{
		"--admin-addr", "bufnet",
		"capacity", "sample",
		"--profile-id", "scrap-prod-v1",
		"--backend-url", backendServer.URL + "/scrap-local",
		"--openbao-addr", openbaoServer.URL,
		"--openbao-token", "local-root",
		"--release-sha", "abc123",
		"--dirty-tree", "clean",
		"--local-disk-path", t.TempDir(),
		"--samples", "1",
		"--document-size", "64",
		"--duration", "1s",
	}, &out)
	testutil.RequireNoErrorf(t, err, "run capacity sample")
	testutil.RequireEqualf(t, "scrap-prod-v1", inspect.capacityProfileID, "capacity profile id")
	requireCapacitySampleAdvisoryOutput(t, out.String())
}

func newCapacitySampleBackendServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handleCapacitySampleBackend(t, w, r)
	}))
}

func handleCapacitySampleBackend(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	w.Header().Set("X-Amz-Request-Id", "backend-request")
	if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
		t.Errorf("missing SigV4 authorization header")
	}
	switch r.Method {
	case http.MethodPut:
		_, _ = io.Copy(io.Discard, r.Body)
	case http.MethodGet:
		_, _ = w.Write([]byte("sample"))
	case http.MethodHead:
	default:
		t.Errorf("unexpected backend method %s", r.Method)
	}
}

func newCapacitySampleOpenBaoServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(handleCapacitySampleOpenBao))
}

func handleCapacitySampleOpenBao(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Vault-Token") != "local-root" {
		http.Error(w, "denied", http.StatusForbidden)
		return
	}
	w.Header().Set("X-Vault-Request", "openbao-request")
	w.Header().Set("Content-Type", "application/json")
	if strings.Contains(r.URL.Path, "/datakey/plaintext/") {
		_, _ = w.Write([]byte(`{"data":{"ciphertext":"vault:v1:wrapped"}}`))
		return
	}
	_, _ = w.Write([]byte(`{"data":{}}`))
}

func requireCapacitySampleAdvisoryOutput(t *testing.T, output string) {
	t.Helper()
	for _, want := range []string{"capacity-sample-advisory", `"advisory_only": true`, `"proposed_capacity_profile"`} {
		testutil.RequireTruef(t, strings.Contains(output, want), "output = %s, missing %s", output, want)
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

type fakeInspectServer struct {
	adminv1.UnimplementedInspectServiceServer
	workloadIdentity  string
	extraMetadata     string
	capacityProfileID string
}

func (s *fakeInspectServer) GetClusterSummary(ctx context.Context, _ *adminv1.GetClusterSummaryRequest) (*adminv1.GetClusterSummaryResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	values := md.Get(authz.WorkloadIdentityMetadataKey)
	if len(values) == 1 {
		s.workloadIdentity = values[0]
	}
	extraValues := md.Get("x-scrap-extra-test")
	if len(extraValues) == 1 {
		s.extraMetadata = extraValues[0]
	}
	return &adminv1.GetClusterSummaryResponse{Summary: &adminv1.ClusterSummary{}}, nil
}

func (s *fakeInspectServer) GetCapacityRunway(_ context.Context, req *adminv1.GetCapacityRunwayRequest) (*adminv1.GetCapacityRunwayResponse, error) {
	s.capacityProfileID = req.GetCapacityProfileId()
	return &adminv1.GetCapacityRunwayResponse{Runway: &adminv1.CapacityRunway{
		CapacityProfileId:    req.GetCapacityProfileId(),
		UsableBytesRemaining: 8 * 1024 * 1024 * 1024,
		EstimatedBytesPerDay: 128 * 1024 * 1024,
		RunwayDays:           30,
	}}, nil
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
	return func(_ context.Context, _ string, options ...grpc.DialOption) (*grpc.ClientConn, error) {
		options = append([]grpc.DialOption{
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return listener.DialContext(ctx)
			}),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		}, options...)
		return grpc.NewClient("passthrough:///bufnet",
			options...,
		)
	}
}
