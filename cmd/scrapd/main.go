package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	backendfs "github.com/petabytecl/scrap/internal/backend/fs"
	"github.com/petabytecl/scrap/internal/backendupload"
	"github.com/petabytecl/scrap/internal/closeutil"
	"github.com/petabytecl/scrap/internal/config"
	"github.com/petabytecl/scrap/internal/localstorage"
	"github.com/petabytecl/scrap/internal/node"
	"github.com/petabytecl/scrap/internal/operations"
)

func main() {
	cfg := config.Default()
	flag.StringVar(&cfg.PublicListenAddress, "public-listen", cfg.PublicListenAddress, "public gRPC listen address")
	flag.StringVar(&cfg.AdminListenAddress, "admin-listen", cfg.AdminListenAddress, "admin gRPC listen address")
	flag.StringVar(&cfg.AuthorizationPolicyPath, "authorization-policy", cfg.AuthorizationPolicyPath, "authorization policy JSON path; required for public and admin APIs")
	flag.StringVar(&cfg.LocalDataDir, "local-data-dir", cfg.LocalDataDir, "local data directory for explicitly enabled non-production storage")
	flag.BoolVar(&cfg.EnableLocalNonProductionStorage, "enable-local-non-production-storage", cfg.EnableLocalNonProductionStorage, "enable single-member local storage; not a production durability mode")
	flag.BoolVar(&cfg.EnableLocalFilesystemBackend, "enable-local-filesystem-backend", cfg.EnableLocalFilesystemBackend, "enable local filesystem backend upload for non-production storage")
	flag.StringVar(&cfg.LocalBackendDataDir, "local-backend-data-dir", cfg.LocalBackendDataDir, "local filesystem backend data directory for explicitly enabled non-production backend upload")
	flag.DurationVar(&cfg.BackendUploadInterval, "backend-upload-interval", cfg.BackendUploadInterval, "interval for non-production backend upload scans")
	flag.DurationVar(&cfg.OperationRunInterval, "operation-run-interval", cfg.OperationRunInterval, "interval for non-production queued operation scans")
	flag.Uint64Var(&cfg.LocalSealBlockAtBytes, "local-seal-block-at-bytes", cfg.LocalSealBlockAtBytes, "local non-production block size threshold before backend upload sealing")
	flag.BoolVar(&cfg.EnableProductionWriteACK, "enable-production-write-ack", cfg.EnableProductionWriteACK, "attempt production write ACK mode; fails closed until target-profile evidence, owner signoff, implementation evidence, and downstream deployment approval exist")
	flag.StringVar(&cfg.ProductionReadinessEvidence.TargetReleaseProfileID, "production-readiness-profile-id", cfg.ProductionReadinessEvidence.TargetReleaseProfileID, "target production release profile id; currently scrap-prod-v1")
	flag.BoolVar(&cfg.ProductionReadinessEvidence.MetadataCompatibilityBoundary, "production-readiness-metadata-compatibility", cfg.ProductionReadinessEvidence.MetadataCompatibilityBoundary, "readiness evidence: metadata, block, index, envelope, and published schema compatibility boundary passed")
	flag.StringVar(&cfg.ProductionReadinessEvidence.MetadataCompatibilityArtifact, "production-readiness-metadata-compatibility-artifact", cfg.ProductionReadinessEvidence.MetadataCompatibilityArtifact, "release artifact URI for metadata compatibility readiness evidence")
	flag.BoolVar(&cfg.ProductionReadinessEvidence.RaftMetadataDurability, "production-readiness-raft-metadata", cfg.ProductionReadinessEvidence.RaftMetadataDurability, "readiness evidence: durable Raft metadata restart, snapshot, and stale-leader gates passed")
	flag.StringVar(&cfg.ProductionReadinessEvidence.RaftMetadataDurabilityArtifact, "production-readiness-raft-metadata-artifact", cfg.ProductionReadinessEvidence.RaftMetadataDurabilityArtifact, "release artifact URI for Raft metadata durability readiness evidence")
	flag.BoolVar(&cfg.ProductionReadinessEvidence.PeerByteDurability, "production-readiness-peer-durability", cfg.ProductionReadinessEvidence.PeerByteDurability, "readiness evidence: peer byte durability and placement gates passed")
	flag.StringVar(&cfg.ProductionReadinessEvidence.PeerByteDurabilityArtifact, "production-readiness-peer-durability-artifact", cfg.ProductionReadinessEvidence.PeerByteDurabilityArtifact, "release artifact URI for peer byte durability readiness evidence")
	flag.BoolVar(&cfg.ProductionReadinessEvidence.BackendRestoreWorkflow, "production-readiness-backend-restore", cfg.ProductionReadinessEvidence.BackendRestoreWorkflow, "readiness evidence: backend upload, restore, and DR rebuild gates passed")
	flag.StringVar(&cfg.ProductionReadinessEvidence.BackendRestoreArtifact, "production-readiness-backend-restore-artifact", cfg.ProductionReadinessEvidence.BackendRestoreArtifact, "release artifact URI for backend restore workflow readiness evidence")
	flag.BoolVar(&cfg.ProductionReadinessEvidence.OpenBaoEnvelopeWorkflow, "production-readiness-openbao-envelope", cfg.ProductionReadinessEvidence.OpenBaoEnvelopeWorkflow, "readiness evidence: OpenBao envelope restore and key-retention gates passed")
	flag.StringVar(&cfg.ProductionReadinessEvidence.OpenBaoEnvelopeArtifact, "production-readiness-openbao-envelope-artifact", cfg.ProductionReadinessEvidence.OpenBaoEnvelopeArtifact, "release artifact URI for OpenBao envelope readiness evidence")
	flag.BoolVar(&cfg.ProductionReadinessEvidence.CapacityAdmission, "production-readiness-capacity-admission", cfg.ProductionReadinessEvidence.CapacityAdmission, "readiness evidence: capacity, disk runway, and admission-control gates passed")
	flag.StringVar(&cfg.ProductionReadinessEvidence.CapacityAdmissionArtifact, "production-readiness-capacity-admission-artifact", cfg.ProductionReadinessEvidence.CapacityAdmissionArtifact, "release artifact URI for capacity admission readiness evidence")
	flag.BoolVar(&cfg.ProductionReadinessEvidence.OperatorReadiness, "production-readiness-operator", cfg.ProductionReadinessEvidence.OperatorReadiness, "readiness evidence: operator runbooks, dashboards, alerts, and signoff gates passed")
	flag.StringVar(&cfg.ProductionReadinessEvidence.OperatorReadinessArtifact, "production-readiness-operator-artifact", cfg.ProductionReadinessEvidence.OperatorReadinessArtifact, "release artifact URI for operator readiness evidence")
	flag.BoolVar(&cfg.ProductionReadinessEvidence.ProductionWriteACKImplementation, "production-readiness-write-ack-implementation", cfg.ProductionReadinessEvidence.ProductionWriteACKImplementation, "readiness evidence: final production write ACK implementation gate passed for the target profile")
	flag.StringVar(&cfg.ProductionReadinessEvidence.ProductionImplementationArtifact, "production-readiness-write-ack-implementation-artifact", cfg.ProductionReadinessEvidence.ProductionImplementationArtifact, "release artifact URI for production write ACK implementation evidence")
	flag.BoolVar(&cfg.ProductionReadinessEvidence.ReleaseOwnerSignoff, "production-readiness-owner-signoff", cfg.ProductionReadinessEvidence.ReleaseOwnerSignoff, "readiness evidence: release owner approved the target-profile evidence bundle")
	flag.StringVar(&cfg.ProductionReadinessEvidence.ReleaseOwnerSignoffArtifact, "production-readiness-owner-signoff-artifact", cfg.ProductionReadinessEvidence.ReleaseOwnerSignoffArtifact, "release artifact URI for owner signoff evidence")
	flag.BoolVar(&cfg.ProductionReadinessEvidence.DownstreamDeploymentApproval, "production-readiness-downstream-deployment", cfg.ProductionReadinessEvidence.DownstreamDeploymentApproval, "readiness evidence: downstream deployment owner approved live production deployment-specific requirements")
	flag.StringVar(&cfg.ProductionReadinessEvidence.DownstreamDeploymentArtifact, "production-readiness-downstream-deployment-artifact", cfg.ProductionReadinessEvidence.DownstreamDeploymentArtifact, "release artifact URI for downstream deployment approval evidence")
	flag.StringVar(&cfg.ProductionReadinessEvidence.DownstreamDeploymentDeferral, "production-readiness-downstream-deployment-deferral", cfg.ProductionReadinessEvidence.DownstreamDeploymentDeferral, "optional downstream deployment deferral reason to report when approval is missing")
	flag.Parse()

	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid config: %v", err)
	}

	apps := node.Applications{}
	var uploadRunner *backendupload.Runner
	if cfg.EnableLocalNonProductionStorage {
		localApp, err := localstorage.Open(cfg.LocalDataDir)
		if err != nil {
			log.Fatalf("open local non-production storage: %v", err)
		}
		defer closeutil.Log("local storage application", log.Printf, localApp)
		operationStore, err := operations.Open(cfg.LocalDataDir)
		if err != nil {
			log.Fatalf("open operation store: %v", err)
		}
		defer closeutil.Log("operation store", log.Printf, operationStore)
		localApp.SetOperationStore(operationStore)
		localApp.SetSealBlockAtBytes(cfg.LocalSealBlockAtBytes)
		apps.Documents = localApp
		apps.Transactions = localApp
		apps.Inspect = localApp
		apps.Repair = localApp
		apps.Member = localApp
		apps.DR = localApp
		apps.Operations = operationStore
		log.Printf("WARNING: local non-production storage enabled at %s; this does not satisfy the production write ACK contract", cfg.LocalDataDir)
		if cfg.EnableLocalFilesystemBackend {
			backendStore, err := backendfs.Open(cfg.LocalBackendDataDir)
			if err != nil {
				log.Fatalf("open local filesystem backend: %v", err)
			}
			localApp.SetBackendStore(backendStore)
			uploadRunner = &backendupload.Runner{
				RunOnceFunc: func(ctx context.Context) (backendupload.RunResult, error) {
					result, err := localApp.RunBackendUploadOnce(ctx, backendStore)
					if result.Sealed {
						log.Printf("backend upload sealed block %s", result.SealedBlockID)
					}
					if result.MetadataPublished && result.MetadataPublication != nil {
						log.Printf("published metadata checkpoint %s", result.MetadataPublication.Manifest.GetManifestId())
					}
					return result.Upload, err
				},
				Interval: cfg.BackendUploadInterval,
				Report:   logBackendUploadReport,
			}
			log.Printf("WARNING: local filesystem backend enabled at %s; this is a non-production backend adapter", cfg.LocalBackendDataDir)
		}
	}

	server, err := node.Listen(cfg, apps)
	if err != nil {
		log.Fatalf("start listeners: %v", err)
	}
	defer closeutil.Log("server", log.Printf, server)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Printf("public grpc listening on %s", server.PublicAddress())
	log.Printf("admin grpc listening on %s", server.AdminAddress())
	go reloadAuthorizationPolicy(ctx, server)
	if cfg.EnableLocalNonProductionStorage {
		go runOperationLoop(ctx, apps, cfg.OperationRunInterval)
	}
	if uploadRunner != nil {
		go func() {
			if err := uploadRunner.Run(ctx); err != nil {
				log.Printf("backend upload runner stopped: %v", err)
			}
		}()
	}
	if err := server.Serve(ctx); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

func reloadAuthorizationPolicy(ctx context.Context, server *node.Server) {
	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, syscall.SIGHUP)
	defer signal.Stop(signalCh)
	for {
		select {
		case <-ctx.Done():
			return
		case <-signalCh:
			if err := server.ReloadAuthorizationPolicy(); err != nil {
				log.Printf("authorization policy reload rejected: %v", err)
				continue
			}
			log.Printf("authorization policy reloaded")
		}
	}
}

func runOperationLoop(ctx context.Context, apps node.Applications, interval time.Duration) {
	if apps.Operations == nil {
		return
	}
	localApp, ok := apps.Documents.(*localstorage.Application)
	if !ok || localApp == nil {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	recovery, err := localApp.RecoverInterruptedOperations(ctx, apps.Operations)
	logOperationRecoveryReport(recovery, err)
	if err != nil {
		return
	}
	for {
		result, err := localApp.RunQueuedOperationsOnce(ctx, apps.Operations)
		if err != nil && ctx.Err() != nil {
			return
		}
		logOperationRunReport(result, err)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func logOperationRecoveryReport(result operations.RecoveryResult, err error) {
	if err != nil {
		log.Printf("operation recovery failed: %v", err)
		return
	}
	if result.Requeued == 0 && result.FailedUnsupported == 0 {
		return
	}
	log.Printf(
		"operation recovery: scanned=%d queued=%d requeued=%d failed_unsupported=%d terminal=%d ignored=%d",
		result.Scanned,
		result.Queued,
		result.Requeued,
		result.FailedUnsupported,
		result.Terminal,
		result.Ignored,
	)
}

func logOperationRunReport(result localstorage.OperationRunResult, err error) {
	if err != nil {
		log.Printf("operation scan failed: %v", err)
		return
	}
	if result.Scanned == 0 {
		return
	}
	log.Printf(
		"operation scan: scanned=%d skipped=%d pending=%d succeeded=%d failed=%d",
		result.Scanned,
		result.Skipped,
		result.Pending,
		result.Succeeded,
		result.Failed,
	)
}

func logBackendUploadReport(result backendupload.RunResult, err error) {
	if err != nil {
		log.Printf("backend upload scan failed: %v", err)
		return
	}
	if result.Scanned == 0 {
		return
	}
	log.Printf(
		"backend upload scan: scanned=%d skipped=%d deferred=%d uploaded=%d failed=%d",
		result.Scanned,
		result.Skipped,
		result.Deferred,
		result.Uploaded,
		result.Failed,
	)
}
