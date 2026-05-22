package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"

	"github.com/petabytecl/scrap/internal/config"
	"github.com/petabytecl/scrap/internal/localstorage"
	"github.com/petabytecl/scrap/internal/node"
)

func main() {
	cfg := config.Default()
	flag.StringVar(&cfg.PublicListenAddress, "public-listen", cfg.PublicListenAddress, "public gRPC listen address")
	flag.StringVar(&cfg.AdminListenAddress, "admin-listen", cfg.AdminListenAddress, "admin gRPC listen address")
	flag.StringVar(&cfg.LocalDataDir, "local-data-dir", cfg.LocalDataDir, "local data directory for explicitly enabled non-production storage")
	flag.BoolVar(&cfg.EnableLocalNonProductionStorage, "enable-local-non-production-storage", cfg.EnableLocalNonProductionStorage, "enable single-member local storage; not a production durability mode")
	flag.Parse()

	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid config: %v", err)
	}

	apps := node.Applications{}
	if cfg.EnableLocalNonProductionStorage {
		localApp, err := localstorage.Open(cfg.LocalDataDir)
		if err != nil {
			log.Fatalf("open local non-production storage: %v", err)
		}
		defer localApp.Close()
		apps.Documents = localApp
		apps.Transactions = localApp
		log.Printf("WARNING: local non-production storage enabled at %s; this does not satisfy the production write ACK contract", cfg.LocalDataDir)
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
	if err := server.Serve(ctx); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
