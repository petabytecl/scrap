package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/petabytecl/scrap/internal/peer"
	scrapraft "github.com/petabytecl/scrap/internal/raft"
	"github.com/petabytecl/scrap/internal/server"
	"github.com/petabytecl/scrap/internal/shard"
	"google.golang.org/grpc"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		if err := runHealthcheck(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if err := run(); err != nil {
		log.Fatalf("scrapd: %v", err)
	}
}

func run() error {
	dataDir := flag.String("data-dir", "/data", "storage root directory")
	listenAddr := flag.String("listen-addr", ":9090", "gRPC client listen address")
	peerAddr := flag.String("peer-addr", ":9091", "gRPC peer listen address")
	blockSealSize := flag.Int64("block-seal-size", shard.DefaultBlockSealSize, "block seal threshold in bytes")
	peersFlag := flag.String("peers", "", "raft peers (e.g. 1=localhost:9091,2=localhost:9092)")
	flag.Parse()

	replicas := envInt("SCRAP_REPLICAS", 0)
	headlessSvc := os.Getenv("SCRAP_HEADLESS_SERVICE")
	cellID := os.Getenv("SCRAP_CELL_ID")
	peerPort := envInt("SCRAP_PEER_PORT", 9091)
	namespace := os.Getenv("POD_NAMESPACE")
	if namespace == "" {
		namespace = "default"
	}

	var peers map[uint64]string
	var raftID uint64

	if *peersFlag != "" {
		var err error
		peers, err = scrapraft.ParsePeersFlag(*peersFlag)
		if err != nil {
			return fmt.Errorf("invalid --peers: %w", err)
		}
		hostname, _ := os.Hostname()
		ord, err := scrapraft.ParseOrdinal(hostname)
		if err != nil {
			raftID = 1
		} else {
			raftID = scrapraft.OrdinalToRaftID(ord)
		}
	} else if replicas > 0 && headlessSvc != "" {
		peers = scrapraft.BuildK8sPeers(replicas, headlessSvc, namespace, peerPort)
		hostname, err := os.Hostname()
		if err != nil {
			return fmt.Errorf("hostname: %w", err)
		}
		ord, err := scrapraft.ParseOrdinal(hostname)
		if err != nil {
			return fmt.Errorf("parse ordinal from %q: %w", hostname, err)
		}
		raftID = scrapraft.OrdinalToRaftID(ord)
	} else {
		peers = map[uint64]string{1: "localhost:9091"}
		raftID = 1
	}

	s, err := shard.Open(shard.Config{
		DataDir:       *dataDir,
		ShardID:       0,
		RaftID:        raftID,
		Peers:         peers,
		BlockSealSize: *blockSealSize,
	})
	if err != nil {
		return fmt.Errorf("open shard: %w", err)
	}
	defer s.Close()

	clientLis, err := net.Listen("tcp", *listenAddr)
	if err != nil {
		return fmt.Errorf("listen client %s: %w", *listenAddr, err)
	}

	gs := grpc.NewServer()
	server.Register(gs, s)
	server.RegisterHealth(gs, s)

	peerLis, err := net.Listen("tcp", *peerAddr)
	if err != nil {
		return fmt.Errorf("listen peer %s: %w", *peerAddr, err)
	}
	peerGS := grpc.NewServer()
	peerSrv := peer.NewServer(*dataDir + "/blocks")
	peer.RegisterServer(peerGS, peerSrv)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go peerGS.Serve(peerLis)
	go gs.Serve(clientLis)

	log.Printf("scrapd starting: client=%s peer=%s data-dir=%s raft-id=%d cell=%s peers=%d",
		*listenAddr, *peerAddr, *dataDir, raftID, cellID, len(peers))

	<-ctx.Done()
	log.Printf("shutting down")
	peerGS.GracefulStop()
	gs.GracefulStop()

	return nil
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	var n int
	_, err := fmt.Sscanf(v, "%d", &n)
	if err != nil {
		return fallback
	}
	return n
}
