package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/petabytecl/scrap/internal/spike/writepath"
)

func main() {
	var cfg writepath.RunnerConfig

	flag.StringVar(&cfg.Dir, "dir", "", "scratch data directory; defaults to a temporary directory")
	flag.IntVar(&cfg.Transactions, "transactions", 25, "synthetic transaction count")
	flag.IntVar(&cfg.Concurrency, "concurrency", 4, "concurrent document writers")
	flag.IntVar(&cfg.ChunkSize, "chunk-size", 1024*1024, "streaming chunk size in bytes")
	flag.Int64Var(&cfg.Seed, "seed", 1, "synthetic workload seed")
	flag.BoolVar(&cfg.ReadBack, "read-back", true, "read every committed document and verify its checksum")
	flag.BoolVar(&cfg.UseRaftBarrier, "raft-barrier", false, "gate visible metadata behind an in-process single-node Raft commit barrier")
	flag.BoolVar(&cfg.UseDurableRaftBarrier, "raft-durable-barrier", false, "gate visible metadata behind an fsynced single-node Raft commit barrier")
	flag.BoolVar(&cfg.UseRaftClusterBarrier, "raft-cluster-barrier", false, "gate visible metadata behind an in-process three-node Raft commit barrier")
	flag.Parse()

	if err := writepath.Run(context.Background(), cfg, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "scrap-spike failed: %v\n", err)
		os.Exit(1)
	}
}
