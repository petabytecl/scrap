package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"

	"google.golang.org/grpc"

	"github.com/petabytecl/scrap/internal/admin"
	"github.com/petabytecl/scrap/internal/encryption"
	"github.com/petabytecl/scrap/internal/peer"
	"github.com/petabytecl/scrap/internal/security"
	"github.com/petabytecl/scrap/internal/server"
	"github.com/petabytecl/scrap/internal/shard"
	storeapi "github.com/petabytecl/scrap/internal/store"
	"github.com/petabytecl/scrap/internal/telemetry"
)

const appShardID uint64 = 0

// App owns the long-lived scrapd components and their lifecycle. Construction
// (newApp) wires everything; Run serves until the context is cancelled or a
// server fails; Shutdown tears down in one explicit, documented order.
type App struct {
	cfg    Config
	logger *slog.Logger

	telemetry  *scrapdTelemetryRuntime
	shards     *shardSet
	transport  *peer.SharedTransport
	peerClient *peer.Client
	peerSrv    *peer.Server
	clientGS   *grpc.Server
	peerGS     *grpc.Server
	adminSrv   *admin.Server
	adminTLS   adminTLSConfig
	clientLis  net.Listener
	peerLis    net.Listener

	// Retained for the startup log line.
	backendType string
	peers       map[uint64]string
	raftID      uint64
	uploadCfg   shard.UploadConfig
	topology    startupTopology
	publicStore storeapi.Store
}

// newApp constructs the full component graph. If any step fails, the components
// already built are torn down in reverse order before returning, so a failed
// construction never leaks a shard, transport, or listener.
//
//nolint:cyclop // linear construction of all subsystems; complexity is inherent
func newApp(ctx context.Context, cfg Config, logger *slog.Logger, build BuildInfo) (*App, error) {
	memberIdentity, err := validateStartupSecurityGates(cfg)
	if err != nil {
		return nil, err
	}
	topology, err := validateStartupTopology(cfg)
	if err != nil {
		return nil, err
	}
	if _, err := localShardDataDirs(cfg.DataDir, topology); err != nil {
		return nil, err
	}

	var cleanup []startupCleanup
	fail := func(err error) (*App, error) {
		return nil, rollbackStartup(err, cleanup)
	}

	uploadBackend, backendType, err := openConfiguredUploadBackend(context.Background(), cfg.DataDir, cfg.UploadEnabled)
	if err != nil {
		return fail(err)
	}

	// Telemetry identifiers are hashed by default; raw Document identifiers are
	// emitted only in the reserved local non-production Cell, and the request is
	// refused (fail-closed) anywhere else. See ADR 0013 §4.
	identifierMode := telemetry.ResolveIdentifierMode(cfg.CellID, cfg.RawTelemetryIDs)
	logIdentifierMode(ctx, logger, cfg, identifierMode)

	peers, raftID, err := resolvePeers(cfg)
	if err != nil {
		return fail(err)
	}
	clientAddrs := resolveClientAddrs(cfg, peers)

	telemetryRuntime, err := newScrapdTelemetryWithSecurityLabel(
		context.Background(),
		memberIdentity.MemberHostname,
		memberIdentity.MemberID,
		raftID,
		appShardID,
		topologyTelemetryShardLabel(topology),
		build,
		cfg.SecurityMode,
	)
	if err != nil {
		return fail(err)
	}
	cleanup = append(cleanup, func() error { return telemetryRuntime.Shutdown(context.Background()) })

	uploadCfg := shard.UploadConfig{
		Enabled:     cfg.UploadEnabled,
		Backend:     uploadBackend,
		CellID:      cfg.CellID,
		Concurrency: cfg.UploadConcurrency,
		Pressure:    cfg.UploadPressure,
	}

	securityRuntime, err := newAppSecurityRuntimeForTelemetry(cfg, peers, logger, telemetryRuntime)
	if err != nil {
		return fail(err)
	}
	transport := securityRuntime.transport
	cleanup = append(cleanup, func() error {
		transport.Close()
		return nil
	})

	peerClient := securityRuntime.peerClient
	cleanup = append(cleanup, func() error {
		peerClient.Close()
		return nil
	})

	shards, err := openShardSet(shardSetOpenConfig{
		cfg:              cfg,
		topology:         topology,
		raftID:           raftID,
		peers:            peers,
		clientAddrs:      clientAddrs,
		transport:        transport,
		peerClient:       peerClient,
		upload:           uploadCfg,
		telemetryRuntime: telemetryRuntime,
		logger:           logger,
		identifierMode:   identifierMode,
		encryption:       appShardEncryptionConfig(cfg, securityRuntime.transit),
	})
	if err != nil {
		return fail(err)
	}
	cleanup = append(cleanup, shards.Close)

	lc := net.ListenConfig{}

	clientLis, err := lc.Listen(ctx, "tcp", cfg.ListenAddr)
	if err != nil {
		return fail(fmt.Errorf("listen client %s: %w", cfg.ListenAddr, err))
	}
	cleanup = append(cleanup, clientLis.Close)

	clientGS := grpc.NewServer(securityRuntime.publicGRPCOptions...)
	publicStore := publicStoreForTopology(shards, topology)
	server.Register(clientGS, publicStore,
		server.WithTelemetry(telemetryRuntime.server),
		server.WithIdentifierMode(identifierMode),
		server.WithLogger(logger.With(telemetryRuntime.logIdentityAttrs()...)),
		server.WithAuthorizer(securityRuntime.authorizer),
		server.WithAuditSink(securityRuntime.auditSink),
		server.WithRateLimiter(securityRuntime.rateLimiter),
	)
	server.RegisterHealth(clientGS, shards)

	peerLis, err := lc.Listen(ctx, "tcp", cfg.PeerAddr)
	if err != nil {
		return fail(fmt.Errorf("listen peer %s: %w", cfg.PeerAddr, err))
	}
	cleanup = append(cleanup, peerLis.Close)

	peerGS := grpc.NewServer(securityRuntime.peerGRPCOptions...)
	peerSrv := newPeerServer(cfg, shards, securityRuntime, memberIdentity)
	peerSrv.SetRaftRouter(shardSetRaftRouter{shards: shards})
	peer.RegisterServer(peerGS, peerSrv)

	adminOpts := []admin.Option{
		admin.WithLogger(logger),
		admin.WithSecurityStatus(cfg.SecurityMode, security.ProductionReadinessForMode(cfg.SecurityMode)),
		admin.WithMetrics(telemetryRuntime.metricsHandler),
		admin.WithAuthorizer(securityRuntime.authorizer),
		admin.WithAuditSink(securityRuntime.auditSink),
		admin.WithRateLimiter(securityRuntime.rateLimiter),
	}
	adminOpts = appendShardAdminOptions(adminOpts, cfg, topology, shards, securityRuntime.transit)
	if cfg.PprofEnabled {
		adminOpts = append(adminOpts, admin.WithPprof())
	}

	return &App{
		cfg:         cfg,
		logger:      logger,
		telemetry:   telemetryRuntime,
		shards:      shards,
		transport:   transport,
		peerClient:  peerClient,
		peerSrv:     peerSrv,
		clientGS:    clientGS,
		peerGS:      peerGS,
		adminSrv:    admin.New(adminOpts...),
		adminTLS:    securityRuntime.adminTLS,
		clientLis:   clientLis,
		peerLis:     peerLis,
		backendType: backendType,
		peers:       peers,
		raftID:      raftID,
		uploadCfg:   uploadCfg,
		topology:    topology,
		publicStore: publicStore,
	}, nil
}

type startupCleanup func() error

func rollbackStartup(err error, cleanup []startupCleanup) error {
	for i := len(cleanup) - 1; i >= 0; i-- {
		if cleanupErr := cleanup[i](); cleanupErr != nil {
			err = errors.Join(err, cleanupErr)
		}
	}
	return err
}

func appendShardAdminOptions(opts []admin.Option, cfg Config, topology startupTopology, shards *shardSet, transit any) []admin.Option {
	if !topology.SingleShardFallback {
		return opts
	}
	localShard, ok := shards.singleShard()
	if !ok {
		return opts
	}
	opts = append(opts,
		admin.WithUploadPressureProvider(localShard),
		admin.WithEvictionPlanner(localShard),
		admin.WithEvictionApplier(localShard),
		admin.WithEvictionPlanStatusProvider(localShard),
		admin.WithEvictionHealthProvider(localShard),
		admin.WithRewrapService(localShard),
	)
	return appendTestHookAdminOptions(opts, cfg, localShard, transit)
}

func appendTestHookAdminOptions(opts []admin.Option, cfg Config, localShard *shard.Shard, transit any) []admin.Option {
	if !cfg.TestHooks {
		return opts
	}
	opts = append(opts, admin.WithProjectionInjector(localShard))
	opts = append(opts, admin.WithLightScrubber(localShard))
	if rotator, ok := transit.(interface{ Rotate() }); ok {
		opts = append(opts, admin.WithTransitRotator(appTransitRotator{transit: rotator}))
	}
	return opts
}

type appTransitRotator struct {
	transit interface{ Rotate() }
}

func (r appTransitRotator) RotateTransitKey(context.Context) error {
	r.transit.Rotate()
	return nil
}

func newAppSecurityRuntimeForTelemetry(cfg Config, peers map[uint64]string, logger *slog.Logger, telemetryRuntime *scrapdTelemetryRuntime) (appSecurityRuntime, error) {
	rateLimitMetrics, err := telemetryRuntime.newRateLimitMetrics()
	if err != nil {
		return appSecurityRuntime{}, fmt.Errorf("create rate-limit metrics: %w", err)
	}
	return newAppSecurityRuntime(cfg, peers, logger.With(telemetryRuntime.logIdentityAttrs()...), rateLimitMetrics)
}

func newPeerServer(cfg Config, shards *shardSet, securityRuntime appSecurityRuntime, memberIdentity scrapdMemberIdentity) *peer.Server {
	peerOpts := []peer.ServerOption{
		peer.WithReplicationSink(shardSetReplicationSink{shards: shards}),
		peer.WithBlockDirResolver(shards),
		peer.WithAuthorizedShards(shards.IDs()...),
	}
	if localShard, ok := shards.singleShard(); ok {
		peerOpts = append(peerOpts,
			peer.WithScrubCache(localShard),
			peer.WithRebuildHandler(localShard),
		)
	}
	if securityRuntime.authorizer != nil {
		peerOpts = append(peerOpts, peer.WithAuthorizer(securityRuntime.authorizer, security.PeerIdentityConfig{
			CellID:         cfg.CellID,
			MemberHostname: memberIdentity.MemberHostname,
			MemberID:       memberIdentity.MemberID,
		}))
	}
	if securityRuntime.auditSink != nil {
		peerOpts = append(peerOpts, peer.WithAuditSink(securityRuntime.auditSink))
	}
	if securityRuntime.rateLimiter != nil {
		peerOpts = append(peerOpts, peer.WithRateLimiter(securityRuntime.rateLimiter))
	}
	return peer.NewServer(cfg.DataDir+"/blocks", peerOpts...)
}

func logIdentifierMode(ctx context.Context, logger *slog.Logger, cfg Config, identifierMode telemetry.IdentifierMode) {
	switch {
	case identifierMode == telemetry.RawIdentifiersForLocalDebug:
		logger.WarnContext(ctx, "telemetry raw identifiers ENABLED for local debugging (local non-production Cell only)", "cell_id", cfg.CellID)
	case cfg.RawTelemetryIDs:
		logger.WarnContext(ctx, "SCRAP_TELEMETRY_RAW_IDS ignored: raw telemetry identifiers are permitted only in the local non-production Cell", "cell_id", cfg.CellID)
	}
}

func validateStartupSecurityGates(cfg Config) (scrapdMemberIdentity, error) {
	memberIdentity, err := resolveScrapdMemberIdentity(cfg.DataDir, cfg.CellID)
	if err != nil {
		return scrapdMemberIdentity{}, err
	}
	peerIdentity := security.PeerIdentityConfig{
		CellID:         cfg.CellID,
		MemberHostname: memberIdentity.MemberHostname,
		MemberID:       memberIdentity.MemberID,
	}
	if err := security.ValidateStartupGates(cfg.startupGateConfig(peerIdentity)); err != nil {
		return scrapdMemberIdentity{}, fmt.Errorf("production security gates: %w", err)
	}
	return memberIdentity, nil
}

// Run serves the client, peer, and admin servers until ctx is cancelled or one
// of them fails, then performs an ordered Shutdown. A failed Serve propagates so
// the process exits non-zero instead of running blocked-but-not-serving.
func (a *App) Run(ctx context.Context) error {
	a.logStarting(ctx)

	const serverCount = 3
	serveErrs := make(chan error, serverCount)
	go func() { serveErrs <- a.serveClientGRPC() }()
	go func() { serveErrs <- a.servePeerGRPC() }()
	// admin.ListenAndServe already treats http.ErrServerClosed as a clean stop.
	go func() { serveErrs <- a.serveAdmin() }()

	// Wait for a shutdown signal or the first server to fail.
	var serveErr error
	consumed := 0
	select {
	case <-ctx.Done():
	case serveErr = <-serveErrs:
		consumed = 1
	}

	a.logger.InfoContext(ctx, "shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	shutdownErr := a.Shutdown(shutdownCtx)

	// Stopping the servers makes the remaining Serve goroutines return; collect
	// the first real failure, which takes precedence over shutdown errors.
	for range serverCount - consumed {
		if err := <-serveErrs; err != nil && serveErr == nil {
			serveErr = err
		}
	}
	if serveErr != nil {
		return serveErr
	}
	return shutdownErr
}

func (a *App) serveClientGRPC() error {
	if err := a.clientGS.Serve(a.clientLis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return fmt.Errorf("client server: %w", err)
	}
	return nil
}

func (a *App) servePeerGRPC() error {
	if err := a.peerGS.Serve(a.peerLis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return fmt.Errorf("peer server: %w", err)
	}
	return nil
}

func (a *App) serveAdmin() error {
	if a.adminTLS.enabled {
		return a.adminSrv.ListenAndServeTLS(a.cfg.AdminAddr, a.adminTLS.config)
	}
	return a.adminSrv.ListenAndServe(a.cfg.AdminAddr)
}

func appShardEncryptionConfig(cfg Config, transit encryption.Transit) shard.EncryptionConfig {
	if !encryption.ProductionCapable(transit) && !testModeFakeTransitEncryptionEnabled(cfg) {
		return shard.EncryptionConfig{}
	}
	return shard.EncryptionConfig{
		Transit:      transit,
		TransitMount: cfg.ProductionGates.Transit.MountPath,
		TransitKey:   cfg.ProductionGates.Transit.KeyName,
	}
}

func testModeFakeTransitEncryptionEnabled(cfg Config) bool {
	return cfg.SecurityMode == security.ModeTest && cfg.ProductionGates.Transit.Fake
}

// Shutdown tears the App down in the reverse order of startup, documented here
// in one place rather than emerging from deferred ordering:
//
//	admin -> peer gRPC -> peer writers -> client gRPC -> shard -> peer client ->
//	transport -> telemetry
//
// The shard (projection authority) is closed before the outbound transport and
// peer client it depends on. It is safe to call once after Run returns.
func (a *App) Shutdown(ctx context.Context) error {
	var errs []error

	if err := a.adminSrv.Shutdown(ctx); err != nil {
		a.logger.ErrorContext(ctx, "admin shutdown failed", "err", err)
		errs = append(errs, err)
	}

	a.peerGS.GracefulStop()
	// peerGS has stopped accepting RPCs, so no new peer writers can be created;
	// flush and close any block/index writers the peer server still owns.
	if err := a.peerSrv.Close(); err != nil {
		a.logger.ErrorContext(ctx, "peer writer close failed", "err", err)
		errs = append(errs, err)
	}

	a.clientGS.GracefulStop()

	if err := a.shards.Close(); err != nil {
		a.logger.ErrorContext(ctx, "shard set close failed", "err", err)
		errs = append(errs, err)
	}

	a.peerClient.Close()
	a.transport.Close()

	if err := a.telemetry.Shutdown(ctx); err != nil {
		a.logger.ErrorContext(ctx, "telemetry shutdown failed", "err", err)
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}

func (a *App) logStarting(ctx context.Context) {
	a.logger.InfoContext(ctx, "scrapd starting",
		"raft_id", a.raftID,
		"cell_id", a.cfg.CellID,
		"peers", len(a.peers),
		"scrub_enabled", a.cfg.Scrub.Enabled,
		"backend_type", a.backendType,
		"route_map", a.topology.RouteMapSummary,
		"local_shards", a.shards.IDs(),
		"shard_status", a.shards.StartupStatus(a.topology),
		"upload_enabled", a.uploadCfg.Enabled,
		"upload_concurrency", a.uploadCfg.Concurrency,
		"upload_budget_bytes", a.uploadCfg.Pressure.BudgetBytes,
	)
}
