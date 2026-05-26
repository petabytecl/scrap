package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/petabytecl/scrap/internal/server"
	"github.com/petabytecl/scrap/internal/spike"
	"google.golang.org/grpc"
)

func main() {
	dataDir := flag.String("data-dir", "/data", "storage root directory")
	listenAddr := flag.String("listen-addr", ":9090", "gRPC listen address")
	blockSealSize := flag.Int64("block-seal-size", spike.DefaultBlockSealSize, "block seal threshold in bytes")
	flag.Parse()

	s, err := spike.OpenWithConfig(*dataDir, spike.Config{BlockSealSize: *blockSealSize})
	if err != nil {
		log.Fatalf("failed to open store: %v", err)
	}

	lis, err := net.Listen("tcp", *listenAddr)
	if err != nil {
		s.Close()
		log.Fatalf("failed to listen: %v", err)
	}

	gs := grpc.NewServer()
	server.Register(gs, s)

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
		sig := <-sigCh
		fmt.Fprintf(os.Stderr, "received %v, shutting down\n", sig)
		gs.GracefulStop()
	}()

	log.Printf("scrapd starting: listen=%s data-dir=%s block-seal-size=%d", *listenAddr, *dataDir, *blockSealSize)

	if err := gs.Serve(lis); err != nil {
		s.Close()
		log.Fatalf("serve error: %v", err)
	}

	if err := s.Close(); err != nil {
		log.Fatalf("close error: %v", err)
	}
}
