package writepath

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/petabytecl/scrap/internal/testutil"
)

func TestRunWritesClusterRaftReportWithReadBack(t *testing.T) {
	var out bytes.Buffer
	err := Run(context.Background(), RunnerConfig{
		Dir:                   t.TempDir(),
		Transactions:          1,
		Concurrency:           1,
		ChunkSize:             4096,
		Seed:                  7,
		ReadBack:              true,
		UseRaftClusterBarrier: true,
	}, &out)
	testutil.RequireNoErrorf(t, err, "run write path spike")

	report := out.String()
	for _, want := range []string{
		"S.C.R.A.P. write path spike",
		"documents_completed:",
		"raft_barrier: true",
		"raft_barrier_mode: three-node-cluster",
		"bytes_read:",
		"invariant_errors: 0",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
	}
}

func TestRunRejectsMultipleRaftBarrierModes(t *testing.T) {
	var out bytes.Buffer
	err := Run(context.Background(), RunnerConfig{
		Dir:                   t.TempDir(),
		UseRaftBarrier:        true,
		UseRaftClusterBarrier: true,
	}, &out)
	if err == nil || !strings.Contains(err.Error(), "choose only one raft barrier mode") {
		t.Fatalf("run error = %v, want multiple raft modes error", err)
	}
}

func TestNormalizeConfigAppliesDefaults(t *testing.T) {
	cfg := normalizeConfig(RunnerConfig{})

	if cfg.Transactions != 25 {
		t.Fatalf("transactions = %d, want 25", cfg.Transactions)
	}
	if cfg.Concurrency != 4 {
		t.Fatalf("concurrency = %d, want 4", cfg.Concurrency)
	}
	if cfg.ChunkSize != 1024*1024 {
		t.Fatalf("chunk size = %d, want 1048576", cfg.ChunkSize)
	}
	if cfg.Seed != 1 {
		t.Fatalf("seed = %d, want 1", cfg.Seed)
	}
}
