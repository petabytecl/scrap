package shard

import (
	"context"
	"errors"
	"testing"
	"time"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/admission"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func TestEnsureCommitDocumentBytesReadyFailsClosedWhenBytesAreMissing(t *testing.T) {
	s := &Shard{blocksDir: t.TempDir()}

	err := s.ensureCommitDocumentBytesReadyLocked(&scrapv1.CommitDocument{
		BlockId:       7,
		FirstFrameOff: 40,
		FrameCount:    1,
		TotalBytes:    4,
		Sha256:        make([]byte, 32),
	})
	if err == nil {
		t.Fatal("ensureCommitDocumentBytesReadyLocked succeeded without local Block bytes")
	}
	if !errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("error = %v, want ErrDataLoss", err)
	}
}

func TestProcessByteAdmissionCapsConcurrentDocumentBuffers(t *testing.T) {
	budget := admission.New(storeapi.MaxDocumentBytes)
	if err := budget.Acquire(context.Background(), storeapi.MaxDocumentBytes); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	t.Cleanup(func() { budget.Release(storeapi.MaxDocumentBytes) })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := budget.Acquire(ctx, 1)
	if !errors.Is(err, storeapi.ErrResourceExhausted) {
		t.Fatalf("Acquire = %v, want ErrResourceExhausted", err)
	}
}
