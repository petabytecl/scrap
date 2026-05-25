package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	backendfs "github.com/petabytecl/scrap/internal/backend/fs"
	"github.com/petabytecl/scrap/internal/backendupload"
	"github.com/petabytecl/scrap/internal/closeutil"
	"github.com/petabytecl/scrap/internal/config"
	"github.com/petabytecl/scrap/internal/localstorage"
	"github.com/petabytecl/scrap/internal/node"
	"github.com/petabytecl/scrap/internal/observe"
	"github.com/petabytecl/scrap/internal/operations"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(logger); err != nil {
		logger.Error("scrapd stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		return runHealthcheck(os.Args[2:], os.Stderr)
	}

	cfg := config.Default()
	registerFlags(&cfg)
	flag.Parse()

	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	logPrintf := logPrintfFunc(logger)
	apps, uploadRunner, operationExecutor, cleanup, err := buildApplications(cfg, logger, logPrintf)
	if err != nil {
		return err
	}
	defer cleanup()

	server, err := node.Listen(cfg, apps)
	if err != nil {
		return fmt.Errorf("start listeners: %w", err)
	}
	defer closeutil.Log("server", logPrintf, server)
	metricsEndpoint, err := listenMetricsEndpoint(cfg.MetricsListenAddress)
	if err != nil {
		return fmt.Errorf("start metrics listener: %w", err)
	}
	defer closeutil.Log("metrics endpoint", logPrintf, metricsEndpoint)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger.Info("public grpc listening", "address", server.PublicAddress())
	logger.Info("admin grpc listening", "address", server.AdminAddress())
	logger.Info("metrics http listening", "address", metricsEndpoint.Address())
	go reloadAuthorizationPolicy(ctx, server, logger)
	startBackgroundRunners(ctx, cfg, apps, uploadRunner, operationExecutor, logger)
	if err := serveUntilStopped(ctx, server, metricsEndpoint); err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

func registerFlags(cfg *config.Config) {
	registerFlagSet(flag.CommandLine, cfg)
}

func registerFlagSet(flags *flag.FlagSet, cfg *config.Config) {
	flags.StringVar(&cfg.PublicListenAddress, "public-listen", cfg.PublicListenAddress, "public gRPC listen address")
	flags.StringVar(&cfg.AdminListenAddress, "admin-listen", cfg.AdminListenAddress, "admin gRPC listen address")
	flags.StringVar(&cfg.MetricsListenAddress, "metrics-listen", cfg.MetricsListenAddress, "metrics HTTP listen address")
	flags.StringVar(&cfg.AuthorizationPolicyPath, "authorization-policy", cfg.AuthorizationPolicyPath, "authorization policy JSON path; required for public and admin APIs")
	flags.BoolVar(&cfg.TLSEnabled, "grpc-tls-enabled", cfg.TLSEnabled, "enable mutual TLS on public and admin gRPC listeners")
	flags.StringVar(&cfg.TLSCertFile, "grpc-tls-cert-file", cfg.TLSCertFile, "server TLS certificate PEM path for public and admin gRPC listeners")
	flags.StringVar(&cfg.TLSKeyFile, "grpc-tls-key-file", cfg.TLSKeyFile, "server TLS private key PEM path for public and admin gRPC listeners")
	flags.StringVar(&cfg.TLSCACertFile, "grpc-tls-ca-cert-file", cfg.TLSCACertFile, "client CA certificate PEM path for mutual TLS client verification")
	flags.BoolVar(&cfg.RequireCertificateIdentity, "require-certificate-identity", cfg.RequireCertificateIdentity, "require workload identity to come from the mTLS client certificate instead of request metadata")
	flags.StringVar(&cfg.LocalDataDir, "local-data-dir", cfg.LocalDataDir, "local data directory for explicitly enabled non-production storage")
	flags.BoolVar(&cfg.EnableLocalNonProductionStorage, "enable-local-non-production-storage", cfg.EnableLocalNonProductionStorage, "enable single-member local storage; not a production durability mode")
	flags.BoolVar(&cfg.EnableLocalFilesystemBackend, "enable-local-filesystem-backend", cfg.EnableLocalFilesystemBackend, "enable local filesystem backend upload for non-production storage")
	flags.StringVar(&cfg.LocalBackendDataDir, "local-backend-data-dir", cfg.LocalBackendDataDir, "local filesystem backend data directory for explicitly enabled non-production backend upload")
	flags.DurationVar(&cfg.BackendUploadInterval, "backend-upload-interval", cfg.BackendUploadInterval, "interval for non-production backend upload scans")
	flags.DurationVar(&cfg.OperationRunInterval, "operation-run-interval", cfg.OperationRunInterval, "interval for non-production queued operation scans")
	flags.Uint64Var(&cfg.LocalSealBlockAtBytes, "local-seal-block-at-bytes", cfg.LocalSealBlockAtBytes, "local non-production block size threshold before backend upload sealing")
	flags.Uint64Var(&cfg.GRPCServerLimits.MaxConcurrentStreams, "grpc-max-concurrent-streams", cfg.GRPCServerLimits.MaxConcurrentStreams, "maximum concurrent HTTP/2 streams per gRPC server transport")
	flags.IntVar(&cfg.GRPCServerLimits.MaxRecvMsgSizeBytes, "grpc-max-recv-msg-size-bytes", cfg.GRPCServerLimits.MaxRecvMsgSizeBytes, "maximum inbound gRPC message size in bytes")
	flags.IntVar(&cfg.GRPCServerLimits.MaxSendMsgSizeBytes, "grpc-max-send-msg-size-bytes", cfg.GRPCServerLimits.MaxSendMsgSizeBytes, "maximum outbound gRPC message size in bytes")
	flags.DurationVar(&cfg.GRPCServerLimits.KeepaliveMinTime, "grpc-keepalive-min-time", cfg.GRPCServerLimits.KeepaliveMinTime, "minimum client keepalive ping interval")
	flags.BoolVar(&cfg.GRPCServerLimits.KeepalivePermitWithoutStream, "grpc-keepalive-permit-without-stream", cfg.GRPCServerLimits.KeepalivePermitWithoutStream, "permit client keepalive pings with no active streams")
	flags.DurationVar(&cfg.GRPCServerLimits.KeepaliveMaxConnectionIdle, "grpc-keepalive-max-connection-idle", cfg.GRPCServerLimits.KeepaliveMaxConnectionIdle, "idle time before gRPC sends GOAWAY")
	flags.DurationVar(&cfg.GRPCServerLimits.KeepaliveMaxConnectionAge, "grpc-keepalive-max-connection-age", cfg.GRPCServerLimits.KeepaliveMaxConnectionAge, "maximum gRPC connection age before GOAWAY")
	flags.DurationVar(&cfg.GRPCServerLimits.KeepaliveMaxConnectionAgeGrace, "grpc-keepalive-max-connection-age-grace", cfg.GRPCServerLimits.KeepaliveMaxConnectionAgeGrace, "grace period after maximum gRPC connection age")
	flags.DurationVar(&cfg.GRPCServerLimits.KeepaliveTime, "grpc-keepalive-time", cfg.GRPCServerLimits.KeepaliveTime, "server keepalive ping interval for idle gRPC transports")
	flags.DurationVar(&cfg.GRPCServerLimits.KeepaliveTimeout, "grpc-keepalive-timeout", cfg.GRPCServerLimits.KeepaliveTimeout, "server keepalive ping acknowledgement timeout")
	flags.BoolVar(&cfg.EnableProductionWriteACK, "enable-production-write-ack", cfg.EnableProductionWriteACK, "attempt production write ACK mode; fails closed until target-profile evidence, owner signoff, implementation evidence, and downstream deployment approval exist")
	flags.StringVar(&cfg.ProductionReadinessEvidence.TargetReleaseProfileID, "production-readiness-profile-id", cfg.ProductionReadinessEvidence.TargetReleaseProfileID, "required target production release profile id when production write ACK is enabled; only scrap-prod-v1 is supported")
	flags.BoolVar(&cfg.ProductionReadinessEvidence.MetadataCompatibilityBoundary, "production-readiness-metadata-compatibility", cfg.ProductionReadinessEvidence.MetadataCompatibilityBoundary, "readiness evidence: metadata, block, index, envelope, and published schema compatibility boundary passed")
	flags.StringVar(&cfg.ProductionReadinessEvidence.MetadataCompatibilityArtifact, "production-readiness-metadata-compatibility-artifact", cfg.ProductionReadinessEvidence.MetadataCompatibilityArtifact, "release artifact URI for metadata compatibility readiness evidence")
	flags.BoolVar(&cfg.ProductionReadinessEvidence.RaftMetadataDurability, "production-readiness-raft-metadata", cfg.ProductionReadinessEvidence.RaftMetadataDurability, "readiness evidence: durable Raft metadata restart, snapshot, and stale-leader gates passed")
	flags.StringVar(&cfg.ProductionReadinessEvidence.RaftMetadataDurabilityArtifact, "production-readiness-raft-metadata-artifact", cfg.ProductionReadinessEvidence.RaftMetadataDurabilityArtifact, "release artifact URI for Raft metadata durability readiness evidence")
	flags.BoolVar(&cfg.ProductionReadinessEvidence.PeerByteDurability, "production-readiness-peer-durability", cfg.ProductionReadinessEvidence.PeerByteDurability, "readiness evidence: peer byte durability and placement gates passed")
	flags.StringVar(&cfg.ProductionReadinessEvidence.PeerByteDurabilityArtifact, "production-readiness-peer-durability-artifact", cfg.ProductionReadinessEvidence.PeerByteDurabilityArtifact, "release artifact URI for peer byte durability readiness evidence")
	flags.BoolVar(&cfg.ProductionReadinessEvidence.BackendRestoreWorkflow, "production-readiness-backend-restore", cfg.ProductionReadinessEvidence.BackendRestoreWorkflow, "readiness evidence: backend upload, restore, and DR rebuild gates passed")
	flags.StringVar(&cfg.ProductionReadinessEvidence.BackendRestoreArtifact, "production-readiness-backend-restore-artifact", cfg.ProductionReadinessEvidence.BackendRestoreArtifact, "release artifact URI for backend restore workflow readiness evidence")
	flags.BoolVar(&cfg.ProductionReadinessEvidence.OpenBaoEnvelopeWorkflow, "production-readiness-openbao-envelope", cfg.ProductionReadinessEvidence.OpenBaoEnvelopeWorkflow, "readiness evidence: OpenBao envelope restore and key-retention gates passed")
	flags.StringVar(&cfg.ProductionReadinessEvidence.OpenBaoEnvelopeArtifact, "production-readiness-openbao-envelope-artifact", cfg.ProductionReadinessEvidence.OpenBaoEnvelopeArtifact, "release artifact URI for OpenBao envelope readiness evidence")
	flags.BoolVar(&cfg.ProductionReadinessEvidence.CapacityAdmission, "production-readiness-capacity-admission", cfg.ProductionReadinessEvidence.CapacityAdmission, "readiness evidence: capacity, disk runway, and admission-control gates passed")
	flags.StringVar(&cfg.ProductionReadinessEvidence.CapacityAdmissionArtifact, "production-readiness-capacity-admission-artifact", cfg.ProductionReadinessEvidence.CapacityAdmissionArtifact, "release artifact URI for capacity admission readiness evidence")
	flags.BoolVar(&cfg.ProductionReadinessEvidence.OperatorReadiness, "production-readiness-operator", cfg.ProductionReadinessEvidence.OperatorReadiness, "readiness evidence: operator runbooks, dashboards, alerts, and signoff gates passed")
	flags.StringVar(&cfg.ProductionReadinessEvidence.OperatorReadinessArtifact, "production-readiness-operator-artifact", cfg.ProductionReadinessEvidence.OperatorReadinessArtifact, "release artifact URI for operator readiness evidence")
	flags.BoolVar(&cfg.ProductionReadinessEvidence.ProductionWriteACKImplementation, "production-readiness-write-ack-implementation", cfg.ProductionReadinessEvidence.ProductionWriteACKImplementation, "readiness evidence: final production write ACK implementation gate passed for the target profile")
	flags.StringVar(&cfg.ProductionReadinessEvidence.ProductionImplementationArtifact, "production-readiness-write-ack-implementation-artifact", cfg.ProductionReadinessEvidence.ProductionImplementationArtifact, "release artifact URI for production write ACK implementation evidence")
	flags.BoolVar(&cfg.ProductionReadinessEvidence.ReleaseOwnerSignoff, "production-readiness-owner-signoff", cfg.ProductionReadinessEvidence.ReleaseOwnerSignoff, "readiness evidence: release owner approved the target-profile evidence bundle")
	flags.StringVar(&cfg.ProductionReadinessEvidence.ReleaseOwnerSignoffArtifact, "production-readiness-owner-signoff-artifact", cfg.ProductionReadinessEvidence.ReleaseOwnerSignoffArtifact, "release artifact URI for owner signoff evidence")
	flags.BoolVar(&cfg.ProductionReadinessEvidence.DownstreamDeploymentApproval, "production-readiness-downstream-deployment", cfg.ProductionReadinessEvidence.DownstreamDeploymentApproval, "readiness evidence: downstream deployment owner approved live production deployment-specific requirements")
	flags.StringVar(&cfg.ProductionReadinessEvidence.DownstreamDeploymentArtifact, "production-readiness-downstream-deployment-artifact", cfg.ProductionReadinessEvidence.DownstreamDeploymentArtifact, "release artifact URI for downstream deployment approval evidence")
	flags.StringVar(&cfg.ProductionReadinessEvidence.DownstreamDeploymentDeferral, "production-readiness-downstream-deployment-deferral", cfg.ProductionReadinessEvidence.DownstreamDeploymentDeferral, "optional downstream deployment deferral reason to report when approval is missing")
}

type metricsEndpoint struct {
	listener net.Listener
	server   *http.Server
	once     sync.Once
	closeErr error
}

func listenMetricsEndpoint(address string) (*metricsEndpoint, error) {
	listenConfig := net.ListenConfig{}
	listener, err := listenConfig.Listen(context.Background(), "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("listen metrics http: %w", err)
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", observe.Handler())
	return &metricsEndpoint{
		listener: listener,
		server: &http.Server{
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
		},
	}, nil
}

func (e *metricsEndpoint) Address() string {
	return e.listener.Addr().String()
}

func (e *metricsEndpoint) Serve() <-chan error {
	errCh := make(chan error, 1)
	go func() {
		err := e.server.Serve(e.listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()
	return errCh
}

func (e *metricsEndpoint) Close() error {
	e.once.Do(func() {
		err := e.server.Close()
		if closeErr := e.listener.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
			err = errors.Join(err, closeErr)
		}
		e.closeErr = err
	})
	return e.closeErr
}

func serveUntilStopped(ctx context.Context, server *node.Server, metricsEndpoint *metricsEndpoint) error {
	metricsErrCh := metricsEndpoint.Serve()
	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- server.Serve(ctx)
	}()

	select {
	case err := <-serverErrCh:
		closeErr := metricsEndpoint.Close()
		return errors.Join(err, closeErr)
	case err := <-metricsErrCh:
		server.Stop()
		if err != nil {
			return fmt.Errorf("metrics http: %w", err)
		}
		return <-serverErrCh
	}
}

func buildApplications(
	cfg config.Config,
	logger *slog.Logger,
	logPrintf closeutil.Logger,
) (node.Applications, *backendupload.Runner, localstorage.OperationExecution, func(), error) {
	if !cfg.EnableLocalNonProductionStorage {
		return node.Applications{}, nil, nil, func() {}, nil
	}
	apps, localApp, operationStore, err := buildLocalApplications(cfg)
	if err != nil {
		return node.Applications{}, nil, nil, nil, err
	}
	cleanup := func() {
		closeutil.Log("operation store", logPrintf, operationStore)
		closeutil.Log("local storage application", logPrintf, localApp)
	}
	logger.Warn("local non-production storage enabled; this does not satisfy the production write ACK contract", "path", cfg.LocalDataDir)
	var uploadRunner *backendupload.Runner
	if cfg.EnableLocalFilesystemBackend {
		uploadRunner, err = buildUploadRunner(cfg, localApp, logger)
		if err != nil {
			cleanup()
			return node.Applications{}, nil, nil, nil, err
		}
	}
	return apps, uploadRunner, localApp.OperationExecutor(), cleanup, nil
}

func buildLocalApplications(cfg config.Config) (node.Applications, *localstorage.Application, *operations.Store, error) {
	localApp, err := localstorage.Open(cfg.LocalDataDir)
	if err != nil {
		return node.Applications{}, nil, nil, fmt.Errorf("open local non-production storage: %w", err)
	}
	operationStore, err := operations.Open(cfg.LocalDataDir)
	if err != nil {
		closeutil.Ignore(localApp)
		return node.Applications{}, nil, nil, fmt.Errorf("open operation store: %w", err)
	}
	localApp.SetOperationStore(operationStore)
	localApp.SetSealBlockAtBytes(cfg.LocalSealBlockAtBytes)
	return node.Applications{
		Documents:    localApp.Documents(),
		Transactions: localApp.Transactions(),
		Inspect:      localstorage.Inspect(localApp),
		Repair:       localstorage.Repair(localApp),
		Member:       localstorage.Members(localApp),
		DR:           localstorage.DisasterRecovery(localApp),
		Operations:   operationStore,
		Health:       localstorage.Health(localApp),
	}, localApp, operationStore, nil
}

func buildUploadRunner(cfg config.Config, localApp *localstorage.Application, logger *slog.Logger) (*backendupload.Runner, error) {
	backendStore, err := backendfs.Open(cfg.LocalBackendDataDir)
	if err != nil {
		return nil, fmt.Errorf("open local filesystem backend: %w", err)
	}
	localApp.SetBackendStore(backendStore)
	logger.Warn("local filesystem backend enabled; this is a non-production backend adapter", "path", cfg.LocalBackendDataDir)
	uploads := localstorage.BackendUploads(localApp)
	return &backendupload.Runner{
		RunOnceFunc: func(ctx context.Context) (backendupload.RunResult, error) {
			result, err := uploads.RunOnce(ctx, backendStore)
			logBackendUploadPublication(logger, result)
			return result.Upload, err
		},
		Interval: cfg.BackendUploadInterval,
		Report: func(result backendupload.RunResult, err error) {
			logBackendUploadReport(logger, result, err)
		},
	}, nil
}

func logBackendUploadPublication(logger *slog.Logger, result localstorage.BackendUploadOnceResult) {
	if result.Sealed {
		logger.Info("backend upload sealed block", "sealed_block_id", result.SealedBlockID)
	}
	if result.MetadataPublished && result.MetadataPublication != nil {
		logger.Info("published metadata checkpoint", "manifest_id", result.MetadataPublication.Manifest.GetManifestId())
	}
}

func startBackgroundRunners(
	ctx context.Context,
	cfg config.Config,
	apps node.Applications,
	uploadRunner *backendupload.Runner,
	operationExecutor localstorage.OperationExecution,
	logger *slog.Logger,
) {
	if cfg.EnableLocalNonProductionStorage {
		go runOperationLoop(ctx, apps, operationExecutor, cfg.OperationRunInterval, logger)
	}
	if uploadRunner != nil {
		go func() {
			if err := uploadRunner.Run(ctx); err != nil {
				logger.Info("backend upload runner stopped", "error", err)
			}
		}()
	}
}

func logPrintfFunc(logger *slog.Logger) func(string, ...any) {
	return func(format string, args ...any) {
		logger.Info(fmt.Sprintf(format, args...))
	}
}

func reloadAuthorizationPolicy(ctx context.Context, server *node.Server, logger *slog.Logger) {
	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, syscall.SIGHUP)
	defer signal.Stop(signalCh)
	for {
		select {
		case <-ctx.Done():
			return
		case <-signalCh:
			if err := server.ReloadAuthorizationPolicy(); err != nil {
				logger.WarnContext(ctx, "authorization policy reload rejected", "error", err)
				continue
			}
			logger.InfoContext(ctx, "authorization policy reloaded")
		}
	}
}

func runOperationLoop(
	ctx context.Context,
	apps node.Applications,
	operationExecutor localstorage.OperationExecution,
	interval time.Duration,
	logger *slog.Logger,
) {
	if apps.Operations == nil || operationExecutor == nil {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	recovery, err := operationExecutor.RecoverInterruptedOperations(ctx, apps.Operations)
	logOperationRecoveryReport(logger, recovery, err)
	if err != nil {
		return
	}
	for {
		result, err := operationExecutor.RunQueuedOperationsOnce(ctx, apps.Operations)
		if err != nil && ctx.Err() != nil {
			return
		}
		logOperationRunReport(logger, result, err)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func logOperationRecoveryReport(logger *slog.Logger, result operations.RecoveryResult, err error) {
	if err != nil {
		logger.Warn("operation recovery failed", "error", err)
		return
	}
	if result.Requeued == 0 && result.FailedUnsupported == 0 {
		return
	}
	logger.Info(
		"operation recovery",
		"scanned", result.Scanned,
		"queued", result.Queued,
		"requeued", result.Requeued,
		"failed_unsupported", result.FailedUnsupported,
		"terminal", result.Terminal,
		"ignored", result.Ignored,
	)
}

func logOperationRunReport(logger *slog.Logger, result localstorage.OperationRunResult, err error) {
	if err != nil {
		logger.Warn("operation scan failed", "error", err)
		return
	}
	if result.Scanned == 0 {
		return
	}
	logger.Info(
		"operation scan",
		"scanned", result.Scanned,
		"skipped", result.Skipped,
		"pending", result.Pending,
		"succeeded", result.Succeeded,
		"failed", result.Failed,
	)
}

func logBackendUploadReport(logger *slog.Logger, result backendupload.RunResult, err error) {
	if err != nil {
		logger.Warn("backend upload scan failed", "error", err)
		return
	}
	if result.Scanned == 0 {
		return
	}
	logger.Info(
		"backend upload scan",
		"scanned", result.Scanned,
		"skipped", result.Skipped,
		"deferred", result.Deferred,
		"uploaded", result.Uploaded,
		"failed", result.Failed,
	)
}
