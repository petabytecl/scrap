package node

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/petabytecl/scrap/internal/api"
	"github.com/petabytecl/scrap/internal/authz"
	"github.com/petabytecl/scrap/internal/config"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	scrapv1 "github.com/petabytecl/scrap/internal/gen/scrap/v1"
	"github.com/petabytecl/scrap/internal/operations"
	"google.golang.org/grpc"
)

type Applications struct {
	Documents    api.DocumentApplication
	Transactions api.TransactionApplication
	Inspect      api.InspectApplication
	Repair       api.RepairApplication
	Member       api.MemberApplication
	DR           api.DisasterRecoveryApplication
	Operations   *operations.Store
}

type Server struct {
	publicListener net.Listener
	adminListener  net.Listener
	publicGRPC     *grpc.Server
	adminGRPC      *grpc.Server
	authorization  *authz.Manager
	policyPath     string
}

func Listen(cfg config.Config, apps Applications) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	authorization, err := authz.LoadManagerFromFile(cfg.AuthorizationPolicyPath, authorizationCapabilities())
	if err != nil {
		return nil, fmt.Errorf("load authorization policy: %w", err)
	}

	listenConfig := net.ListenConfig{}
	publicListener, err := listenConfig.Listen(context.Background(), "tcp", cfg.PublicListenAddress)
	if err != nil {
		return nil, fmt.Errorf("listen public grpc: %w", err)
	}
	adminListener, err := listenConfig.Listen(context.Background(), "tcp", cfg.AdminListenAddress)
	if err != nil {
		_ = publicListener.Close()
		return nil, fmt.Errorf("listen admin grpc: %w", err)
	}

	return newServer(publicListener, adminListener, apps, authorization, cfg.AuthorizationPolicyPath), nil
}

func newServer(publicListener net.Listener, adminListener net.Listener, apps Applications, authorization *authz.Manager, policyPath string) *Server {
	publicGRPC := grpc.NewServer(
		grpc.UnaryInterceptor(authz.UnaryServerInterceptor(authorization, publicMethodCapabilities())),
		grpc.StreamInterceptor(authz.StreamServerInterceptor(authorization, publicMethodCapabilities())),
	)
	api.RegisterPublicServer(publicGRPC, api.NewPublicServer(apps.Documents, apps.Transactions))
	adminGRPC := grpc.NewServer(
		grpc.UnaryInterceptor(authz.UnaryServerInterceptor(authorization, adminMethodCapabilities())),
		grpc.StreamInterceptor(authz.StreamServerInterceptor(authorization, adminMethodCapabilities())),
	)
	api.RegisterAdminServer(adminGRPC, api.NewAdminServer(
		api.WithInspectApplication(apps.Inspect),
		api.WithRepairApplication(apps.Repair),
		api.WithMemberApplication(apps.Member),
		api.WithDisasterRecoveryApplication(apps.DR),
		api.WithOperationStore(apps.Operations),
	))
	return &Server{
		publicListener: publicListener,
		adminListener:  adminListener,
		publicGRPC:     publicGRPC,
		adminGRPC:      adminGRPC,
		authorization:  authorization,
		policyPath:     policyPath,
	}
}

func (s *Server) PublicAddress() string {
	return s.publicListener.Addr().String()
}

func (s *Server) AdminAddress() string {
	return s.adminListener.Addr().String()
}

func (s *Server) Serve(ctx context.Context) error {
	errCh := make(chan error, 2)
	var once sync.Once
	stop := func() {
		s.publicGRPC.GracefulStop()
		s.adminGRPC.GracefulStop()
	}

	go func() {
		if err := s.publicGRPC.Serve(s.publicListener); err != nil {
			errCh <- fmt.Errorf("serve public grpc: %w", err)
		}
	}()
	go func() {
		if err := s.adminGRPC.Serve(s.adminListener); err != nil {
			errCh <- fmt.Errorf("serve admin grpc: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
		once.Do(stop)
		return nil
	case err := <-errCh:
		once.Do(stop)
		return err
	}
}

func (s *Server) Stop() {
	s.publicGRPC.Stop()
	s.adminGRPC.Stop()
}

func (s *Server) ReloadAuthorizationPolicy() error {
	if s.authorization == nil {
		return fmt.Errorf("authorization policy is not configured")
	}
	return s.authorization.ReloadFile(s.policyPath)
}

func (s *Server) Close() error {
	s.Stop()
	return errors.Join(s.publicListener.Close(), s.adminListener.Close())
}

func publicMethodCapabilities() map[string]authz.Capability {
	return map[string]authz.Capability{
		scrapv1.DocumentService_WriteDocument_FullMethodName:          "public.document.write",
		scrapv1.DocumentService_HeadDocument_FullMethodName:           "public.document.head",
		scrapv1.DocumentService_ReadDocument_FullMethodName:           "public.document.read",
		scrapv1.DocumentService_FindDocuments_FullMethodName:          "public.document.find",
		scrapv1.TransactionService_CompleteTransaction_FullMethodName: "public.transaction.complete",
		scrapv1.TransactionService_GetTransaction_FullMethodName:      "public.transaction.get",
	}
}

func adminMethodCapabilities() map[string]authz.Capability {
	return map[string]authz.Capability{
		adminv1.InspectService_GetClusterSummary_FullMethodName:             "admin.inspect.cluster_summary",
		adminv1.InspectService_GetShard_FullMethodName:                      "admin.inspect.shard",
		adminv1.InspectService_GetDocument_FullMethodName:                   "admin.inspect.document",
		adminv1.InspectService_GetBlock_FullMethodName:                      "admin.inspect.block",
		adminv1.InspectService_GetMember_FullMethodName:                     "admin.inspect.member",
		adminv1.InspectService_GetCapacityRunway_FullMethodName:             "admin.inspect.capacity_runway",
		adminv1.OperationService_GetOperation_FullMethodName:                "admin.operation.get",
		adminv1.OperationService_WatchOperation_FullMethodName:              "admin.operation.watch",
		adminv1.OperationService_ListOperations_FullMethodName:              "admin.operation.list",
		adminv1.OperationService_CancelOperation_FullMethodName:             "admin.operation.cancel",
		adminv1.RestoreService_PlanRestore_FullMethodName:                   "admin.restore.plan",
		adminv1.RestoreService_StartRestore_FullMethodName:                  "admin.restore.start",
		adminv1.RestoreService_PlanPrewarm_FullMethodName:                   "admin.prewarm.plan",
		adminv1.RestoreService_StartPrewarm_FullMethodName:                  "admin.prewarm.start",
		adminv1.RepairService_GetRepairQueue_FullMethodName:                 "admin.repair.queue",
		adminv1.RepairService_PlanRepair_FullMethodName:                     "admin.repair.plan",
		adminv1.RepairService_StartRepair_FullMethodName:                    "admin.repair.start",
		adminv1.RepairService_PlanScrub_FullMethodName:                      "admin.scrub.plan",
		adminv1.RepairService_StartScrub_FullMethodName:                     "admin.scrub.start",
		adminv1.MemberService_CordonMember_FullMethodName:                   "admin.member.cordon",
		adminv1.MemberService_UncordonMember_FullMethodName:                 "admin.member.uncordon",
		adminv1.MemberService_GetEvictionSafety_FullMethodName:              "admin.member.eviction_safety",
		adminv1.MemberService_PlanDrain_FullMethodName:                      "admin.member.drain.plan",
		adminv1.MemberService_StartDrain_FullMethodName:                     "admin.member.drain.start",
		adminv1.LifecycleService_PlanTombstone_FullMethodName:               "admin.lifecycle.tombstone.plan",
		adminv1.LifecycleService_StartTombstone_FullMethodName:              "admin.lifecycle.tombstone.start",
		adminv1.KeyService_PlanKeyRotation_FullMethodName:                   "admin.key_rotation.plan",
		adminv1.KeyService_StartKeyRotation_FullMethodName:                  "admin.key_rotation.start",
		adminv1.CapacityService_PlanCapacityOverride_FullMethodName:         "admin.capacity_override.plan",
		adminv1.CapacityService_StartCapacityOverride_FullMethodName:        "admin.capacity_override.start",
		adminv1.DisasterRecoveryService_GetRecoveryReadiness_FullMethodName: "admin.dr.readiness",
		adminv1.DisasterRecoveryService_PlanRecovery_FullMethodName:         "admin.dr.recovery.plan",
		adminv1.DisasterRecoveryService_StartMetadataRestore_FullMethodName: "admin.dr.metadata_restore.start",
		adminv1.DisasterRecoveryService_StartCopyVerify_FullMethodName:      "admin.dr.copy_verify.start",
		adminv1.DisasterRecoveryService_StartDRDrill_FullMethodName:         "admin.dr.drill.start",
	}
}

func authorizationCapabilities() []authz.Capability {
	capabilities := make([]authz.Capability, 0, len(publicMethodCapabilities())+len(adminMethodCapabilities()))
	for _, capability := range publicMethodCapabilities() {
		capabilities = append(capabilities, capability)
	}
	for _, capability := range adminMethodCapabilities() {
		capabilities = append(capabilities, capability)
	}
	return authz.SortedCapabilities(capabilities)
}
