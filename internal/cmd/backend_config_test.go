package cmd

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/security"
	"github.com/petabytecl/scrap/internal/shard"
)

func TestOpenUploadBackendDefaultsToFS(t *testing.T) {
	store, backendType, err := openUploadBackend(context.Background(), Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("openUploadBackend: %v", err)
	}
	if backendType != "fs" {
		t.Fatalf("backendType = %q, want fs", backendType)
	}

	key := "cell/shard/block.blk"
	if _, err := store.PutObject(context.Background(), key, bytes.NewReader([]byte("payload")), 7, backend.PutOpts{}); err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	if _, err := store.HeadObject(context.Background(), key); err != nil {
		t.Fatalf("HeadObject: %v", err)
	}
}

func TestOpenUploadBackendRejectsUnknownType(t *testing.T) {
	t.Setenv("SCRAP_BACKEND_TYPE", "tape")

	_, _, err := openUploadBackend(context.Background(), Config{DataDir: t.TempDir()})
	if err == nil {
		t.Fatal("expected unsupported backend type error")
	}
}

func TestOpenUploadBackendS3RequiresConfig(t *testing.T) {
	t.Setenv("SCRAP_BACKEND_TYPE", "s3")

	_, _, err := openUploadBackend(context.Background(), Config{DataDir: t.TempDir()})
	if !errors.Is(err, backend.ErrPermanent) {
		t.Fatalf("error = %v, want ErrPermanent", err)
	}
}

func TestProductionRejectsFilesystemBackendForMultiMember(t *testing.T) {
	err := validateProductionBackend(Config{
		SecurityMode: security.ModeProduction,
		PeersFlag:    "1=localhost:9091,2=localhost:9092",
	}, "fs")
	if err == nil {
		t.Fatal("expected filesystem Backend rejection in production multi-Member Cell")
	}
}

func TestProductionRejectsFilesystemBackendWhenEvictionEnabled(t *testing.T) {
	err := validateProductionBackend(Config{
		SecurityMode: security.ModeProduction,
		Eviction:     shard.EvictionConfig{Enabled: true},
	}, "fs")
	if err == nil {
		t.Fatal("expected filesystem Backend rejection when eviction is enabled")
	}
}

func TestProductionAllowsFilesystemBackendForSingleMemberWithoutEviction(t *testing.T) {
	err := validateProductionBackend(Config{
		SecurityMode: security.ModeProduction,
		PeersFlag:    "1=localhost:9091",
	}, "fs")
	if err != nil {
		t.Fatalf("validateProductionBackend: %v", err)
	}
}
