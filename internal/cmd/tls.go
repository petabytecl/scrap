package cmd

import (
	"crypto/tls"
	"fmt"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/petabytecl/scrap/internal/peer"
	"github.com/petabytecl/scrap/internal/security"
)

type appSecurityRuntime struct {
	transport         *peer.SharedTransport
	peerClient        *peer.Client
	publicGRPCOptions []grpc.ServerOption
	peerGRPCOptions   []grpc.ServerOption
	adminTLS          adminTLSConfig
}

type adminTLSConfig struct {
	config  *tls.Config
	enabled bool
}

func newAppSecurityRuntime(cfg Config, peers map[uint64]string) (appSecurityRuntime, error) {
	transport, err := newSharedTransport(cfg, peers)
	if err != nil {
		return appSecurityRuntime{}, err
	}

	peerClient, err := newPeerClient(cfg)
	if err != nil {
		transport.Close()
		return appSecurityRuntime{}, err
	}

	runtime, err := newAppSecurityRuntimeOptions(cfg)
	if err != nil {
		peerClient.Close()
		transport.Close()
		return appSecurityRuntime{}, err
	}
	runtime.transport = transport
	runtime.peerClient = peerClient
	return runtime, nil
}

func newAppSecurityRuntimeOptions(cfg Config) (appSecurityRuntime, error) {
	publicGRPCOptions, err := newPublicGRPCServerOptions(cfg)
	if err != nil {
		return appSecurityRuntime{}, err
	}
	peerGRPCOptions, err := newPeerGRPCServerOptions(cfg)
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
	}, nil
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

func newPublicGRPCServerOptions(cfg Config) ([]grpc.ServerOption, error) {
	opts := []grpc.ServerOption{grpc.StatsHandler(otelgrpc.NewServerHandler())}
	if !cfg.SecurityMode.IsProduction() {
		return opts, nil
	}
	serverTLS, err := security.BuildMTLSServerConfig("SCRAP_TLS_PUBLIC", cfg.ProductionGates.TLS.Public)
	if err != nil {
		return nil, fmt.Errorf("public gRPC TLS: %w", err)
	}
	return append(opts, grpc.Creds(credentials.NewTLS(serverTLS))), nil
}

func newPeerGRPCServerOptions(cfg Config) ([]grpc.ServerOption, error) {
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
		grpc.ChainUnaryInterceptor(security.PeerIdentityUnaryServerInterceptor()),
		grpc.ChainStreamInterceptor(security.PeerIdentityStreamServerInterceptor()),
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
