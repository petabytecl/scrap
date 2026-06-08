package cmd

import (
	"crypto/tls"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/petabytecl/scrap/internal/audit"
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
	publicGRPCOptions, err := newPublicGRPCServerOptions(cfg, authorizer)
	if err != nil {
		return appSecurityRuntime{}, err
	}
	peerGRPCOptions, err := newPeerGRPCServerOptions(cfg, authorizer)
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
	}, nil
}

func newAppAuthorizer(cfg Config) (appAuthorizerConfig, error) {
	if !cfg.SecurityMode.IsProduction() {
		return appAuthorizerConfig{}, nil
	}
	policy, err := security.LoadRolePolicy(cfg.ProductionGates.RolePolicyPath)
	if err != nil {
		return appAuthorizerConfig{}, fmt.Errorf("role policy: %w", err)
	}
	return appAuthorizerConfig{authorizer: security.NewAuthorizer(policy)}, nil
}

func newAppAuditSink(cfg Config, logger *slog.Logger) (audit.Sink, error) {
	if !cfg.SecurityMode.IsProduction() {
		return audit.NewNopSink(), nil
	}
	if _, err := audit.LoadPolicy(cfg.ProductionGates.AuditSink.PolicyPath); err != nil {
		return nil, fmt.Errorf("audit policy: %w", err)
	}
	return audit.NewLoggerSink(logger), nil
}

func newAppRateLimiter(cfg Config, observer security.RateLimitObserver) (*security.RateLimiter, error) {
	if !cfg.SecurityMode.IsProduction() {
		return security.NewRateLimiter(security.RateLimitPolicy{}), nil
	}
	policy, err := security.LoadRateLimitPolicy(cfg.ProductionGates.RateLimits.PolicyPath)
	if err != nil {
		return nil, fmt.Errorf("rate-limit policy: %w", err)
	}
	return security.NewRateLimiter(policy, security.WithRateLimitObserver(observer)), nil
}

func newSharedTransport(cfg Config, peers map[uint64]string) (*peer.SharedTransport, error) {
	if !cfg.SecurityMode.IsProduction() {
		return peer.NewSharedTransport(peers), nil
	}
	clientTLS, err := security.BuildMTLSClientConfig("SCRAP_TLS_PEER", security.ClientTLSFilesFromSurface(cfg.ProductionGates.TLS.Peer))
	if err != nil {
		return nil, fmt.Errorf("peer transport TLS: %w", err)
	}
	return peer.NewSharedTransport(peers, peer.WithSharedTransportCredentials(credentials.NewTLS(clientTLS))), nil
}

func newPeerClient(cfg Config) (*peer.Client, error) {
	if !cfg.SecurityMode.IsProduction() {
		return peer.NewClient(), nil
	}
	clientTLS, err := security.BuildMTLSClientConfig("SCRAP_TLS_PEER", security.ClientTLSFilesFromSurface(cfg.ProductionGates.TLS.Peer))
	if err != nil {
		return nil, fmt.Errorf("peer client TLS: %w", err)
	}
	return peer.NewClient(peer.WithClientTransportCredentials(credentials.NewTLS(clientTLS))), nil
}

func newPublicGRPCServerOptions(cfg Config, authorizer *security.Authorizer) ([]grpc.ServerOption, error) {
	opts := []grpc.ServerOption{grpc.StatsHandler(otelgrpc.NewServerHandler())}
	if !cfg.SecurityMode.IsProduction() {
		return opts, nil
	}
	serverTLS, err := security.BuildMTLSServerConfig("SCRAP_TLS_PUBLIC", cfg.ProductionGates.TLS.Public)
	if err != nil {
		return nil, fmt.Errorf("public gRPC TLS: %w", err)
	}
	return append(opts,
		grpc.Creds(credentials.NewTLS(serverTLS)),
		grpc.ChainUnaryInterceptor(security.PrincipalUnaryServerInterceptor(authorizer)),
		grpc.ChainStreamInterceptor(security.PrincipalStreamServerInterceptor(authorizer)),
	), nil
}

func newPeerGRPCServerOptions(cfg Config, authorizer *security.Authorizer) ([]grpc.ServerOption, error) {
	opts := []grpc.ServerOption{grpc.StatsHandler(otelgrpc.NewServerHandler())}
	if !cfg.SecurityMode.IsProduction() {
		return opts, nil
	}
	serverTLS, err := security.BuildMTLSServerConfig("SCRAP_TLS_PEER", cfg.ProductionGates.TLS.Peer)
	if err != nil {
		return nil, fmt.Errorf("peer gRPC TLS: %w", err)
	}
	return append(opts,
		grpc.Creds(credentials.NewTLS(serverTLS)),
		grpc.ChainUnaryInterceptor(
			security.PrincipalUnaryServerInterceptor(authorizer),
			security.PeerIdentityUnaryServerInterceptor(),
		),
		grpc.ChainStreamInterceptor(
			security.PrincipalStreamServerInterceptor(authorizer),
			security.PeerIdentityStreamServerInterceptor(),
		),
	), nil
}

func newAdminTLSConfig(cfg Config) (adminTLSConfig, error) {
	if !cfg.SecurityMode.IsProduction() {
		return adminTLSConfig{}, nil
	}
	adminTLS, err := security.BuildMTLSServerConfig("SCRAP_TLS_ADMIN", cfg.ProductionGates.TLS.Admin)
	if err != nil {
		return adminTLSConfig{}, fmt.Errorf("admin TLS: %w", err)
	}
	return adminTLSConfig{config: adminTLS, enabled: true}, nil
}
