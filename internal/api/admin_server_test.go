package api

import (
	"context"
	"net"
	"testing"

	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestAdminServerPlanRestoreRejectsInvalidTargetOverGRPC(t *testing.T) {
	restoreClient, _, cleanup := newAdminTestClients(t)
	defer cleanup()

	_, err := restoreClient.PlanRestore(context.Background(), &adminv1.PlanRestoreRequest{
		Targets: []*adminv1.Target{
			{
				Target: &adminv1.Target_Shard{
					Shard: &adminv1.ShardTarget{ShardId: "shard-a"},
				},
			},
		},
	})
	violations := requireBadRequest(t, err)
	requireViolation(t, violations, "targets[0]", reasonInvalidTargetKind)
}

func TestAdminServerStartRestoreRejectsMissingOperationIDOverGRPC(t *testing.T) {
	restoreClient, _, cleanup := newAdminTestClients(t)
	defer cleanup()

	_, err := restoreClient.StartRestore(context.Background(), &adminv1.StartRestoreRequest{
		OperationPlanId: "plan-1",
		PlanHash:        "hash-1",
	})
	violations := requireBadRequest(t, err)
	requireViolation(t, violations, "operation_id", identity.ReasonRequired)
}

func TestAdminServerStartRestoreReturnsUnimplementedAfterValidation(t *testing.T) {
	restoreClient, _, cleanup := newAdminTestClients(t)
	defer cleanup()

	_, err := restoreClient.StartRestore(context.Background(), &adminv1.StartRestoreRequest{
		OperationId:     validUUIDv7(),
		OperationPlanId: "plan-1",
		PlanHash:        "hash-1",
	})
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("error is not a status error: %v", err)
	}
	if st.Code() != codes.Unimplemented {
		t.Fatalf("code = %s, want %s", st.Code(), codes.Unimplemented)
	}
}

func TestAdminServerCordonMemberValidatesOverGRPC(t *testing.T) {
	_, memberClient, cleanup := newAdminTestClients(t)
	defer cleanup()

	_, err := memberClient.CordonMember(context.Background(), &adminv1.CordonMemberRequest{
		OperationId:   validUUIDv7(),
		StorageMember: &adminv1.StorageMemberTarget{StorageMemberId: "member-a"},
	})
	violations := requireBadRequest(t, err)
	requireViolation(t, violations, "reason", identity.ReasonRequired)
}

func newAdminTestClients(
	t *testing.T,
) (adminv1.RestoreServiceClient, adminv1.MemberServiceClient, func()) {
	t.Helper()

	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	RegisterAdminServer(server, NewAdminServer())
	go func() {
		_ = server.Serve(listener)
	}()

	dialer := func(ctx context.Context, _ string) (net.Conn, error) {
		return listener.DialContext(ctx)
	}
	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	cleanup := func() {
		_ = conn.Close()
		server.Stop()
		_ = listener.Close()
	}
	return adminv1.NewRestoreServiceClient(conn), adminv1.NewMemberServiceClient(conn), cleanup
}
