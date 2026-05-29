package main

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/scrub"
	"github.com/petabytecl/scrap/internal/shard"
)

// TestAppRunCleanShutdown is a startup/shutdown smoke test. It builds the full
// App on OS-assigned ports, starts serving, then cancels the context (standing
// in for SIGTERM) and asserts Run drains the servers and returns without error.
// This exercises newApp -> Run -> the ordered Shutdown (which closes the shard
// before the outbound transport).
func TestAppRunCleanShutdown(t *testing.T) {
	cfg := Config{
		DataDir:           t.TempDir(),
		ListenAddr:        "127.0.0.1:0",
		PeerAddr:          "127.0.0.1:0",
		AdminAddr:         "127.0.0.1:0",
		BlockSealSize:     shard.DefaultBlockSealSize,
		UploadConcurrency: shard.DefaultUploadConcurrency,
		PeerPort:          defaultPeerPort,
		Namespace:         "default",
		Scrub:             scrub.ParseConfig(),
		UploadPressure:    shard.ParseUploadPressureConfigFromEnv(),
	}
	logger := slog.New(slog.DiscardHandler)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	app, err := newApp(ctx, cfg, logger)
	if err != nil {
		t.Fatalf("newApp: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()

	// Give the servers a moment to begin serving, then signal shutdown.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error on clean shutdown: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Run did not return within 15s after context cancel")
	}
}
