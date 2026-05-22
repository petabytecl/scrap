package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"
	"time"

	backendfs "github.com/petabytecl/scrap/internal/backend/fs"
	"github.com/petabytecl/scrap/internal/backendupload"
	"github.com/petabytecl/scrap/internal/config"
	"github.com/petabytecl/scrap/internal/localstorage"
	"github.com/petabytecl/scrap/internal/node"
	"github.com/petabytecl/scrap/internal/operations"
)

func main() {
	cfg := config.Default()
	flag.StringVar(&cfg.PublicListenAddress, "public-listen", cfg.PublicListenAddress, "public gRPC listen address")
	flag.StringVar(&cfg.AdminListenAddress, "admin-listen", cfg.AdminListenAddress, "admin gRPC listen address")
	flag.StringVar(&cfg.LocalDataDir, "local-data-dir", cfg.LocalDataDir, "local data directory for explicitly enabled non-production storage")
	flag.BoolVar(&cfg.EnableLocalNonProductionStorage, "enable-local-non-production-storage", cfg.EnableLocalNonProductionStorage, "enable single-member local storage; not a production durability mode")
	flag.BoolVar(&cfg.EnableLocalFilesystemBackend, "enable-local-filesystem-backend", cfg.EnableLocalFilesystemBackend, "enable local filesystem backend upload for non-production storage")
	flag.StringVar(&cfg.LocalBackendDataDir, "local-backend-data-dir", cfg.LocalBackendDataDir, "local filesystem backend data directory for explicitly enabled non-production backend upload")
	flag.DurationVar(&cfg.BackendUploadInterval, "backend-upload-interval", cfg.BackendUploadInterval, "interval for non-production backend upload scans")
	flag.DurationVar(&cfg.OperationRunInterval, "operation-run-interval", cfg.OperationRunInterval, "interval for non-production queued operation scans")
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
		defer localApp.Close()
		operationStore, err := operations.Open(cfg.LocalDataDir)
		if err != nil {
			log.Fatalf("open operation store: %v", err)
		}
		defer operationStore.Close()
		apps.Documents = localApp
		apps.Transactions = localApp
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
	defer server.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Printf("public grpc listening on %s", server.PublicAddress())
	log.Printf("admin grpc listening on %s", server.AdminAddress())
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

func logOperationRunReport(result localstorage.OperationRunResult, err error) {
	if err != nil {
		log.Printf("operation scan failed: %v", err)
		return
	}
	if result.Scanned == 0 {
		return
	}
	log.Printf(
		"operation scan: scanned=%d skipped=%d succeeded=%d failed=%d",
		result.Scanned,
		result.Skipped,
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
