package node

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/petabytecl/scrap/internal/api"
	"github.com/petabytecl/scrap/internal/authz"
	"github.com/petabytecl/scrap/internal/config"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	scrapv1 "github.com/petabytecl/scrap/internal/gen/scrap/v1"
	"github.com/petabytecl/scrap/internal/operations"
	"github.com/petabytecl/scrap/internal/safeconv"
)

type Applications struct {
	Documents    api.DocumentApplication
	Transactions api.TransactionApplication
	Inspect      api.InspectApplication
	Repair       api.RepairApplication
	Member       api.MemberApplication
	DR           api.DisasterRecoveryApplication
	Operations   *operations.Store
	Health       HealthApplication
}

type Server struct {
	publicListener net.Listener
	adminListener  net.Listener
	publicGRPC     *grpc.Server
	adminGRPC      *grpc.Server
	authorization  *authz.Manager
	policyPath     string
	auditEvents    auditEventAppender
}

type auditEventAppender interface {
	AppendAuditEvent(event *adminv1.AuditEvent) error
}

func Listen(cfg config.Config, apps Applications) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := requireGRPCMTLSConfig(cfg); err != nil {
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

	server, err := newServerWithConfig(cfg, publicListener, adminListener, apps, authorization, cfg.AuthorizationPolicyPath)
	if err != nil {
		_ = adminListener.Close()
		_ = publicListener.Close()
		return nil, err
	}
	return server, nil
}

func newServer(publicListener, adminListener net.Listener, apps Applications, authorization *authz.Manager, policyPath string) *Server {
	server, err := newServerWithConfig(config.Default(), publicListener, adminListener, apps, authorization, policyPath)
	if err != nil {
		panic(err)
	}
	return server
}

func newServerWithConfig(
	cfg config.Config,
	publicListener,
	adminListener net.Listener,
	apps Applications,
	authorization *authz.Manager,
	policyPath string,
) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	auditSink := authorizationAuditSink{store: apps.Operations, now: func() time.Time { return time.Now().UTC() }}
	publicAuthorizationOptions := authz.InterceptorOptions{
		DeniedAuditSink:            auditSink,
		RequireCertificateIdentity: cfg.RequireCertificateIdentity,
		TenantExtractors:           publicTenantExtractors(),
	}
	adminAuthorizationOptions := authz.InterceptorOptions{
		DeniedAuditSink:            auditSink,
		RequireCertificateIdentity: cfg.RequireCertificateIdentity,
	}
	baseOptions, err := grpcServerBaseOptions(cfg)
	if err != nil {
		return nil, err
	}
	publicOptions := combineServerOptions(baseOptions,
		grpc.UnaryInterceptor(authz.UnaryServerInterceptorWithOptions(authorization, publicMethodCapabilities(), publicAuthorizationOptions)),
		grpc.StreamInterceptor(authz.StreamServerInterceptorWithOptions(authorization, publicMethodCapabilities(), publicAuthorizationOptions)),
	)
	publicGRPC := grpc.NewServer(publicOptions...)
	api.RegisterPublicServer(publicGRPC, api.NewPublicServer(apps.Documents, apps.Transactions, api.WithPublicAuditStore(apps.Operations)))
	adminOptions := combineServerOptions(baseOptions,
		grpc.UnaryInterceptor(bypassHealthUnaryInterceptor(authz.UnaryServerInterceptorWithOptions(authorization, adminMethodCapabilities(), adminAuthorizationOptions))),
		grpc.StreamInterceptor(bypassHealthStreamInterceptor(authz.StreamServerInterceptorWithOptions(authorization, adminMethodCapabilities(), adminAuthorizationOptions))),
	)
	adminGRPC := grpc.NewServer(adminOptions...)
	api.RegisterAdminServer(adminGRPC, api.NewAdminServer(
		api.WithInspectApplication(apps.Inspect),
		api.WithRepairApplication(apps.Repair),
		api.WithMemberApplication(apps.Member),
		api.WithDisasterRecoveryApplication(apps.DR),
		api.WithOperationStore(apps.Operations),
	))
	registerHealthServer(adminGRPC, apps.Health)
	return &Server{
		publicListener: publicListener,
		adminListener:  adminListener,
		publicGRPC:     publicGRPC,
		adminGRPC:      adminGRPC,
		authorization:  authorization,
		policyPath:     policyPath,
		auditEvents:    auditEventAppenderFromStore(apps.Operations),
	}, nil
}

func combineServerOptions(base []grpc.ServerOption, options ...grpc.ServerOption) []grpc.ServerOption {
	combined := make([]grpc.ServerOption, 0, len(base)+len(options))
	combined = append(combined, base...)
	combined = append(combined, options...)
	return combined
}

func requireGRPCMTLSConfig(cfg config.Config) error {
	if cfg.TLSEnabled || cfg.EnableLocalNonProductionStorage {
		return nil
	}
	return errors.New("grpc TLS must be enabled unless local non-production storage is enabled")
}

func grpcServerBaseOptions(cfg config.Config) ([]grpc.ServerOption, error) {
	limitOptions, err := grpcServerLimitOptions(cfg)
	if err != nil {
		return nil, err
	}
	tlsOptions, err := grpcServerTLSOptions(cfg)
	if err != nil {
		return nil, err
	}
	options := make([]grpc.ServerOption, 0, len(limitOptions)+len(tlsOptions))
	options = append(options, limitOptions...)
	options = append(options, tlsOptions...)
	return options, nil
}

func grpcServerLimitOptions(cfg config.Config) ([]grpc.ServerOption, error) {
	limits := cfg.GRPCServerLimits
	maxConcurrentStreams, err := safeconv.Uint64ToUint32("grpc_max_concurrent_streams", limits.MaxConcurrentStreams)
	if err != nil {
		return nil, err
	}
	return []grpc.ServerOption{
		grpc.MaxConcurrentStreams(maxConcurrentStreams),
		grpc.MaxRecvMsgSize(limits.MaxRecvMsgSizeBytes),
		grpc.MaxSendMsgSize(limits.MaxSendMsgSizeBytes),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             limits.KeepaliveMinTime,
			PermitWithoutStream: limits.KeepalivePermitWithoutStream,
		}),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle:     limits.KeepaliveMaxConnectionIdle,
			MaxConnectionAge:      limits.KeepaliveMaxConnectionAge,
			MaxConnectionAgeGrace: limits.KeepaliveMaxConnectionAgeGrace,
			Time:                  limits.KeepaliveTime,
			Timeout:               limits.KeepaliveTimeout,
		}),
	}, nil
}

func grpcServerTLSOptions(cfg config.Config) ([]grpc.ServerOption, error) {
	if !cfg.TLSEnabled {
		return nil, nil
	}
	cert, err := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load grpc TLS certificate pair: %w", err)
	}
	caPEM, err := os.ReadFile(cfg.TLSCACertFile)
	if err != nil {
		return nil, fmt.Errorf("read grpc TLS client CA certificate: %w", err)
	}
	clientCAs := x509.NewCertPool()
	if !clientCAs.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("grpc TLS client CA certificate file contains no PEM certificates")
	}
	tlsConfig := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"h2"},
		Certificates: []tls.Certificate{cert},
		ClientCAs:    clientCAs,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}
	return []grpc.ServerOption{grpc.Creds(credentials.NewTLS(tlsConfig))}, nil
}

func auditEventAppenderFromStore(store *operations.Store) auditEventAppender {
	if store == nil {
		return nil
	}
	return store
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
		return errors.New("authorization policy is not configured")
	}
	err := s.authorization.ReloadFile(s.policyPath)
	auditErr := s.auditAuthorizationPolicyReload(err)
	return errors.Join(err, auditErr)
}

func (s *Server) Close() error {
	s.Stop()
	return errors.Join(closeListener(s.publicListener), closeListener(s.adminListener))
}

func closeListener(listener net.Listener) error {
	err := listener.Close()
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

type authorizationAuditSink struct {
	store *operations.Store
	now   func() time.Time
}

func (s authorizationAuditSink) RecordDeniedRequest(ctx context.Context, method string, decision authz.Decision) error {
	if s.store == nil {
		return nil
	}
	now := s.now()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	requestID, correlationID, traceID := requestMetadata(ctx)
	actor := decision.WorkloadIdentity
	if actor == "" {
		actor = "unknown-workload"
	}
	operationID := correlationID
	if operationID == "" {
		operationID = requestID
	}
	if operationID == "" {
		operationID = "authorization-denied"
	}
	metadata := auditMetadata(ctx)
	metadata["decision"] = "denied"
	metadata["rpc_method"] = method
	metadata["capability"] = string(decision.Capability)
	metadata["reason"] = decision.Reason
	metadata["reason_description"] = decision.ReasonDescription
	metadata["workload_identity"] = decision.WorkloadIdentity
	metadata["policy_version"] = decision.PolicyVersion
	metadata["policy_generation"] = strconv.FormatUint(decision.PolicyGeneration, 10)
	if decision.TenantID != "" {
		metadata["tenant_id"] = decision.TenantID
	}
	if traceID != "" {
		metadata["trace_id"] = traceID
	}
	return s.store.AppendAuditEvent(&adminv1.AuditEvent{
		EventId:       auditEventID("authorization_denied", method, actor, decision.Reason, requestID, correlationID, strconv.FormatInt(now.UnixNano(), 10)),
		EventType:     "authorization_denied",
		OperationId:   operationID,
		OperationType: method,
		ActorIdentity: actor,
		OccurredAt:    timestamppb.New(now),
		Metadata:      metadata,
	})
}

func (s *Server) auditAuthorizationPolicyReload(reloadErr error) error {
	if s.auditEvents == nil {
		return nil
	}
	eventType := "authorization_policy_reloaded"
	result := "succeeded"
	reason := ""
	if reloadErr != nil {
		eventType = "authorization_policy_reload_rejected"
		result = "denied"
		reason = reloadErr.Error()
	}
	now := time.Now().UTC()
	metadata := map[string]string{
		"decision":          result,
		"policy_path":       s.policyPath,
		"policy_version":    s.authorization.PolicyVersion(),
		"policy_generation": strconv.FormatUint(s.authorization.Generation(), 10),
	}
	if reason != "" {
		metadata["reason"] = reason
	}
	return s.auditEvents.AppendAuditEvent(&adminv1.AuditEvent{
		EventId:       auditEventID(eventType, s.policyPath, strconv.FormatInt(now.UnixNano(), 10)),
		EventType:     eventType,
		OperationId:   "authorization-policy",
		OperationType: "authorization-policy-reload",
		ActorIdentity: "scrapd",
		OccurredAt:    timestamppb.New(now),
		Metadata:      metadata,
	})
}

func auditMetadata(ctx context.Context) map[string]string {
	requestID, correlationID, _ := requestMetadata(ctx)
	metadata := make(map[string]string)
	if requestID != "" {
		metadata["request_id"] = requestID
	}
	if correlationID != "" {
		metadata["correlation_id"] = correlationID
	}
	return metadata
}

func requestMetadata(ctx context.Context) (string, string, string) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", "", ""
	}
	requestID := firstMetadataValue(md, "x-request-id")
	correlationID := firstMetadataValue(md, "x-correlation-id")
	if correlationID == "" {
		correlationID = requestID
	}
	return requestID, correlationID, firstMetadataValue(md, "traceparent")
}

func firstMetadataValue(md metadata.MD, key string) string {
	values := md.Get(key)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func auditEventID(parts ...string) string {
	hasher := sha256.New()
	for _, part := range parts {
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write([]byte(part))
	}
	return "audit:" + hex.EncodeToString(hasher.Sum(nil)[:16])
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

func publicTenantExtractors() map[string]authz.TenantExtractor {
	return map[string]authz.TenantExtractor{
		scrapv1.DocumentService_WriteDocument_FullMethodName:          tenantFromWriteDocumentRequest,
		scrapv1.DocumentService_HeadDocument_FullMethodName:           tenantFromHeadDocumentRequest,
		scrapv1.DocumentService_ReadDocument_FullMethodName:           tenantFromReadDocumentRequest,
		scrapv1.DocumentService_FindDocuments_FullMethodName:          tenantFromFindDocumentsRequest,
		scrapv1.TransactionService_CompleteTransaction_FullMethodName: tenantFromCompleteTransactionRequest,
		scrapv1.TransactionService_GetTransaction_FullMethodName:      tenantFromGetTransactionRequest,
	}
}

func tenantFromWriteDocumentRequest(req any) (string, bool) {
	request, ok := req.(*scrapv1.WriteDocumentRequest)
	if !ok {
		return "", false
	}
	init := request.GetInit()
	if init == nil {
		return "", false
	}
	return tenantFromDocumentIdentity(init.GetIdentity())
}

func tenantFromHeadDocumentRequest(req any) (string, bool) {
	request, ok := req.(*scrapv1.HeadDocumentRequest)
	if !ok {
		return "", false
	}
	return tenantFromDocumentIdentity(request.GetIdentity())
}

func tenantFromReadDocumentRequest(req any) (string, bool) {
	request, ok := req.(*scrapv1.ReadDocumentRequest)
	if !ok {
		return "", false
	}
	return tenantFromDocumentIdentity(request.GetIdentity())
}

func tenantFromFindDocumentsRequest(req any) (string, bool) {
	request, ok := req.(*scrapv1.FindDocumentsRequest)
	if !ok {
		return "", false
	}
	return tenantFromTransactionIdentity(request.GetTransaction())
}

func tenantFromCompleteTransactionRequest(req any) (string, bool) {
	request, ok := req.(*scrapv1.CompleteTransactionRequest)
	if !ok {
		return "", false
	}
	return tenantFromTransactionIdentity(request.GetTransaction())
}

func tenantFromGetTransactionRequest(req any) (string, bool) {
	request, ok := req.(*scrapv1.GetTransactionRequest)
	if !ok {
		return "", false
	}
	return tenantFromTransactionIdentity(request.GetTransaction())
}

func tenantFromDocumentIdentity(identity *scrapv1.DocumentIdentity) (string, bool) {
	if identity == nil || identity.GetTenantId() == "" {
		return "", false
	}
	return identity.GetTenantId(), true
}

func tenantFromTransactionIdentity(identity *scrapv1.TransactionIdentity) (string, bool) {
	if identity == nil || identity.GetTenantId() == "" {
		return "", false
	}
	return identity.GetTenantId(), true
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
