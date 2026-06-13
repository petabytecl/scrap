package cmd

import (
	"context"
	"testing"
)

func TestOpenConfiguredUploadBackendDisabled(t *testing.T) {
	backend, backendType, err := openConfiguredUploadBackend(context.Background(), t.TempDir(), false)
	if err != nil {
		t.Fatalf("openConfiguredUploadBackend: %v", err)
	}
	if backend != nil {
		t.Fatal("backend is not nil")
	}
	if backendType != "" {
		t.Fatalf("backendType = %q, want empty", backendType)
	}
}

func TestOpenConfiguredUploadBackendEnabledFS(t *testing.T) {
	t.Setenv("SCRAP_BACKEND_TYPE", "fs")

	backend, backendType, err := openConfiguredUploadBackend(context.Background(), t.TempDir(), true)
	if err != nil {
		t.Fatalf("openConfiguredUploadBackend: %v", err)
	}
	if backend == nil {
		t.Fatal("backend is nil")
	}
	if backendType != "fs" {
		t.Fatalf("backendType = %q, want fs", backendType)
	}
}
