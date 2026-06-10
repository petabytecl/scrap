package cmd

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/audit"
	"github.com/petabytecl/scrap/internal/encryption"
	"github.com/petabytecl/scrap/internal/peer"
	"github.com/petabytecl/scrap/internal/security"
)

type appSecurityRuntime struct {
	transport         *peer.SharedTransport
	peerClient        *peer.Client
	publicGRPCOptions []grpc.ServerOption
	peerGRPCOptions   []grpc.ServerOption
	adminTLS          adminTLSConfig
	authorizer        *security.Authorizer
	auditSink         audit.Sink
	rateLimiter       *security.RateLimiter
	transit           encryption.Transit
}

type adminTLSConfig struct {
	config  *tls.Config
	enabled bool
}

type appAuthorizerConfig struct {
	authorizer *security.Authorizer
}

func newAppSecurityRuntime(cfg Config, peers map[uint64]string, logger *slog.Logger, rateObserver security.RateLimitObserver) (appSecurityRuntime, error) {
	transport, err := newSharedTransport(cfg, peers)
	if err != nil {
		return appSecurityRuntime{}, err
	}

	peerClient, err := newPeerClient(cfg)
	if err != nil {
		transport.Close()
		return appSecurityRuntime{}, err
	}

	runtime, err := newAppSecurityRuntimeOptions(cfg, logger, rateObserver)
	if err != nil {
		peerClient.Close()
		transport.Close()
		return appSecurityRuntime{}, err
	}
	runtime.transport = transport
	runtime.peerClient = peerClient
	return runtime, nil
}

func newAppSecurityRuntimeOptions(cfg Config, logger *slog.Logger, rateObserver security.RateLimitObserver) (appSecurityRuntime, error) {
	authorizerCfg, err := newAppAuthorizer(cfg)
	if err != nil {
		return appSecurityRuntime{}, err
	}
	authorizer := authorizerCfg.authorizer
	auditSink, err := newAppAuditSink(cfg, logger)
	if err != nil {
		return appSecurityRuntime{}, err
	}
	rateLimiter, err := newAppRateLimiter(cfg, rateObserver)
	if err != nil {
		return appSecurityRuntime{}, err
	}
	transit, err := newAppTransit(cfg)
	if err != nil {
		return appSecurityRuntime{}, err
	}
	publicGRPCOptions, err := newPublicGRPCServerOptions(cfg, authorizer, auditSink, rateLimiter)
	if err != nil {
		return appSecurityRuntime{}, err
	}
	peerGRPCOptions, err := newPeerGRPCServerOptions(cfg, authorizer, auditSink, rateLimiter)
	if err != nil {
		return appSecurityRuntime{}, err
	}
	adminTLS, err := newAdminTLSConfig(cfg)
	if err != nil {
		return appSecurityRuntime{}, err
	}
	return appSecurityRuntime{
		publicGRPCOptions: publicGRPCOptions,
		peerGRPCOptions:   peerGRPCOptions,
		adminTLS:          adminTLS,
		authorizer:        authorizer,
		auditSink:         auditSink,
		rateLimiter:       rateLimiter,
		transit:           transit,
	}, nil
}

func newAppAuthorizer(cfg Config) (appAuthorizerConfig, error) {
	if !appSecurityControlsEnabled(cfg) {
		return appAuthorizerConfig{}, nil
	}
	policy, err := security.LoadRolePolicy(cfg.ProductionGates.RolePolicyPath)
	if err != nil {
		return appAuthorizerConfig{}, fmt.Errorf("role policy: %w", err)
	}
	return appAuthorizerConfig{authorizer: security.NewAuthorizer(policy)}, nil
}

func newAppAuditSink(cfg Config, logger *slog.Logger) (audit.Sink, error) {
	if !appSecurityControlsEnabled(cfg) {
		return audit.NewNopSink(), nil
	}
	if cfg.SecurityMode == security.ModeTest && strings.TrimSpace(cfg.ProductionGates.AuditSink.PolicyPath) == "" {
		return audit.NewLoggerSink(logger), nil
	}
	if _, err := audit.LoadPolicy(cfg.ProductionGates.AuditSink.PolicyPath); err != nil {
		return nil, fmt.Errorf("audit policy: %w", err)
	}
	return audit.NewLoggerSink(logger), nil
}

func newAppRateLimiter(cfg Config, observer security.RateLimitObserver) (*security.RateLimiter, error) {
	if !appSecurityControlsEnabled(cfg) {
		return security.NewRateLimiter(security.RateLimitPolicy{}), nil
	}
	if cfg.SecurityMode == security.ModeTest && strings.TrimSpace(cfg.ProductionGates.RateLimits.PolicyPath) == "" {
		return security.NewRateLimiter(security.RateLimitPolicy{}, security.WithRateLimitObserver(observer)), nil
	}
	policy, err := security.LoadRateLimitPolicy(cfg.ProductionGates.RateLimits.PolicyPath)
	if err != nil {
		return nil, fmt.Errorf("rate-limit policy: %w", err)
	}
	return security.NewRateLimiter(policy, security.WithRateLimitObserver(observer)), nil
}

func newAppTransit(cfg Config) (encryption.Transit, error) {
	transitCfg := cfg.ProductionGates.Transit
	if !cfg.SecurityMode.IsProduction() {
		return encryption.NewFakeTransit(encryption.FakeConfig{KeyName: transitCfg.KeyName}), nil
	}
	if transitCfg.Fake {
		return nil, fmt.Errorf("transit config: fake Transit cannot satisfy production: %w", encryption.ErrInvalidConfig)
	}
	tokenEnv := strings.TrimSpace(transitCfg.TokenEnv)
	if tokenEnv == "" {
		return nil, fmt.Errorf("transit config: token env is required: %w", encryption.ErrInvalidConfig)
	}
	token := strings.TrimSpace(os.Getenv(tokenEnv))
	if token == "" {
		return nil, fmt.Errorf("transit config: referenced token env is empty: %w", encryption.ErrInvalidConfig)
	}
	transit, err := encryption.NewOpenBaoTransit(encryption.OpenBaoConfig{
		Address:   transitCfg.Address,
		MountPath: transitCfg.MountPath,
		KeyName:   transitCfg.KeyName,
		Token:     token,
	})
	if err != nil {
		return nil, fmt.Errorf("transit config: %w", err)
	}
	return transit, nil
}

func newSharedTransport(cfg Config, peers map[uint64]string) (*peer.SharedTransport, error) {
	if !appSecurityControlsEnabled(cfg) {
		return peer.NewSharedTransport(peers), nil
	}
	clientTLS, err := security.BuildMTLSClientConfig("SCRAP_TLS_PEER", security.ClientTLSFilesFromSurface(cfg.ProductionGates.TLS.Peer))
	if err != nil {
		return nil, fmt.Errorf("peer transport TLS: %w", err)
	}
	return peer.NewSharedTransport(peers, peer.WithSharedTransportCredentials(credentials.NewTLS(clientTLS))), nil
}

func newPeerClient(cfg Config) (*peer.Client, error) {
	if !appSecurityControlsEnabled(cfg) {
		return peer.NewClient(), nil
	}
	clientTLS, err := security.BuildMTLSClientConfig("SCRAP_TLS_PEER", security.ClientTLSFilesFromSurface(cfg.ProductionGates.TLS.Peer))
	if err != nil {
		return nil, fmt.Errorf("peer client TLS: %w", err)
	}
	return peer.NewClient(peer.WithClientTransportCredentials(credentials.NewTLS(clientTLS))), nil
}

func newPublicGRPCServerOptions(cfg Config, authorizer *security.Authorizer, auditSink audit.Sink, rateLimiter *security.RateLimiter) ([]grpc.ServerOption, error) {
	limiter := newPublicRPCLimiter()
	unaryInterceptors := []grpc.UnaryServerInterceptor{limiter.unary}
	streamInterceptors := []grpc.StreamServerInterceptor{limiter.stream}
	opts := []grpc.ServerOption{grpc.StatsHandler(otelgrpc.NewServerHandler())}
	opts = append(opts, publicGRPCTransportOptions()...)
	if !appSecurityControlsEnabled(cfg) {
		return append(opts,
			grpc.ChainUnaryInterceptor(unaryInterceptors...),
			grpc.ChainStreamInterceptor(streamInterceptors...),
		), nil
	}
	serverTLS, err := security.BuildMTLSServerConfig("SCRAP_TLS_PUBLIC", cfg.ProductionGates.TLS.Public)
	if err != nil {
		return nil, fmt.Errorf("public gRPC TLS: %w", err)
	}
	unaryInterceptors = append(unaryInterceptors, security.PrincipalUnaryServerInterceptor(authorizer,
		security.WithPrincipalAudit(auditSink, rateLimiter, security.RateLimitSurfacePublic, publicGRPCAuditInfo),
	))
	streamInterceptors = append(streamInterceptors, security.PrincipalStreamServerInterceptor(authorizer,
		security.WithPrincipalAudit(auditSink, rateLimiter, security.RateLimitSurfacePublic, publicGRPCAuditInfo),
	))
	return append(opts,
		grpc.Creds(credentials.NewTLS(serverTLS)),
		grpc.ChainUnaryInterceptor(unaryInterceptors...),
		grpc.ChainStreamInterceptor(streamInterceptors...),
	), nil
}

func newPeerGRPCServerOptions(cfg Config, authorizer *security.Authorizer, auditSink audit.Sink, rateLimiter *security.RateLimiter) ([]grpc.ServerOption, error) {
	opts := []grpc.ServerOption{grpc.StatsHandler(otelgrpc.NewServerHandler())}
	if !appSecurityControlsEnabled(cfg) {
		return opts, nil
	}
	serverTLS, err := security.BuildMTLSServerConfig("SCRAP_TLS_PEER", cfg.ProductionGates.TLS.Peer)
	if err != nil {
		return nil, fmt.Errorf("peer gRPC TLS: %w", err)
	}
	return append(opts,
		grpc.Creds(credentials.NewTLS(serverTLS)),
		grpc.ChainUnaryInterceptor(
			security.PrincipalUnaryServerInterceptor(authorizer,
				security.WithPrincipalAudit(auditSink, rateLimiter, security.RateLimitSurfacePeer, peerGRPCAuditInfo),
			),
			security.PeerIdentityUnaryServerInterceptor(
				security.WithPrincipalAudit(auditSink, rateLimiter, security.RateLimitSurfacePeer, peerGRPCAuditInfo),
			),
		),
		grpc.ChainStreamInterceptor(
			security.PrincipalStreamServerInterceptor(authorizer,
				security.WithPrincipalAudit(auditSink, rateLimiter, security.RateLimitSurfacePeer, peerGRPCAuditInfo),
			),
			security.PeerIdentityStreamServerInterceptor(
				security.WithPrincipalAudit(auditSink, rateLimiter, security.RateLimitSurfacePeer, peerGRPCAuditInfo),
			),
		),
	), nil
}

func publicGRPCAuditInfo(fullMethod string) (security.GRPCAuditInfo, bool) {
	switch fullMethod {
	case scrapv1.DocumentService_WriteDocument_FullMethodName:
		return security.GRPCAuditInfo{Role: security.RoleDocumentWriter, Operation: audit.OperationWriteDocument, Target: audit.TargetDocument}, true
	case scrapv1.DocumentService_HeadDocument_FullMethodName:
		return security.GRPCAuditInfo{Role: security.RoleDocumentReader, Operation: audit.OperationHeadDocument, Target: audit.TargetDocument}, true
	case scrapv1.DocumentService_ReadDocument_FullMethodName:
		return security.GRPCAuditInfo{Role: security.RoleDocumentReader, Operation: audit.OperationReadDocument, Target: audit.TargetDocument}, true
	case scrapv1.DocumentService_FindDocuments_FullMethodName:
		return security.GRPCAuditInfo{Role: security.RoleDocumentReader, Operation: audit.OperationFindDocuments, Target: audit.TargetDocument}, true
	default:
		return security.GRPCAuditInfo{}, false
	}
}

func peerGRPCAuditInfo(fullMethod string) (security.GRPCAuditInfo, bool) {
	switch fullMethod {
	case scrapv1.PeerService_ReplicateDocument_FullMethodName:
		return security.GRPCAuditInfo{Role: security.RolePeerMember, Operation: audit.OperationReplicateDocument, Target: audit.TargetDocument}, true
	case scrapv1.PeerService_TransferBlock_FullMethodName:
		return security.GRPCAuditInfo{Role: security.RolePeerMember, Operation: audit.OperationTransferBlock, Target: audit.TargetBlock}, true
	case scrapv1.PeerService_ConsistencyCheck_FullMethodName:
		return security.GRPCAuditInfo{Role: security.RolePeerMember, Operation: audit.OperationConsistencyCheck, Target: audit.TargetPeer}, true
	case scrapv1.PeerService_RequestIndexRebuild_FullMethodName:
		return security.GRPCAuditInfo{Role: security.RolePeerMember, Operation: audit.OperationRequestIndexRebuild, Target: audit.TargetPeer}, true
	case scrapv1.PeerService_ForwardRaft_FullMethodName:
		return security.GRPCAuditInfo{Role: security.RolePeerMember, Operation: audit.OperationForwardRaft, Target: audit.TargetPeer}, true
	case scrapv1.PeerService_ForwardRaftStream_FullMethodName:
		return security.GRPCAuditInfo{Role: security.RolePeerMember, Operation: audit.OperationForwardRaftStream, Target: audit.TargetPeer}, true
	default:
		return security.GRPCAuditInfo{}, false
	}
}

func newAdminTLSConfig(cfg Config) (adminTLSConfig, error) {
	if !appSecurityControlsEnabled(cfg) {
		return adminTLSConfig{}, nil
	}
	adminTLS, err := security.BuildMTLSServerConfig("SCRAP_TLS_ADMIN", cfg.ProductionGates.TLS.Admin)
	if err != nil {
		return adminTLSConfig{}, fmt.Errorf("admin TLS: %w", err)
	}
	return adminTLSConfig{config: adminTLS, enabled: true}, nil
}

func appSecurityControlsEnabled(cfg Config) bool {
	if cfg.SecurityMode.IsProduction() {
		return true
	}
	return cfg.SecurityMode == security.ModeTest && strings.TrimSpace(cfg.ProductionGates.RolePolicyPath) != ""
}
