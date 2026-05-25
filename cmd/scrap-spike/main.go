package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/petabytecl/scrap/internal/spike/writepath"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	var cfg writepath.RunnerConfig

	flags := flag.NewFlagSet("scrap-spike", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&cfg.Dir, "dir", "", "scratch data directory; defaults to a temporary directory")
	flags.IntVar(&cfg.Transactions, "transactions", 25, "synthetic transaction count")
	flags.IntVar(&cfg.Concurrency, "concurrency", 4, "concurrent document writers")
	flags.IntVar(&cfg.ChunkSize, "chunk-size", 1024*1024, "streaming chunk size in bytes")
	flags.Int64Var(&cfg.Seed, "seed", 1, "synthetic workload seed")
	flags.BoolVar(&cfg.ReadBack, "read-back", true, "read every committed document and verify its checksum")
	flags.BoolVar(&cfg.UseRaftBarrier, "raft-barrier", false, "gate visible metadata behind an in-process single-node Raft commit barrier")
	flags.BoolVar(&cfg.UseDurableRaftBarrier, "raft-durable-barrier", false, "gate visible metadata behind an fsynced single-node Raft commit barrier")
	flags.BoolVar(&cfg.UseRaftClusterBarrier, "raft-cluster-barrier", false, "gate visible metadata behind an in-process three-node Raft commit barrier")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		_, _ = fmt.Fprintf(stderr, "unexpected arguments: %v\n", flags.Args())
		return 2
	}

	if err := writepath.Run(context.Background(), cfg, stdout); err != nil {
		_, _ = fmt.Fprintf(stderr, "scrap-spike failed: %v\n", err)
		return 1
	}
	return 0
}
