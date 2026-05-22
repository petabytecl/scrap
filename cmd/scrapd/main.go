package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"

	"github.com/petabytecl/scrap/internal/config"
	"github.com/petabytecl/scrap/internal/node"
)

func main() {
	cfg := config.Default()
	flag.StringVar(&cfg.PublicListenAddress, "public-listen", cfg.PublicListenAddress, "public gRPC listen address")
	flag.StringVar(&cfg.AdminListenAddress, "admin-listen", cfg.AdminListenAddress, "admin gRPC listen address")
	flag.Parse()

	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid config: %v", err)
	}

	server, err := node.Listen(cfg, node.Applications{})
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
