package index_test

import (
	"errors"
	"io"
	"testing"

	"github.com/petabytecl/scrap/internal/index"
)

func TestPendingUploadsRoundTrip(t *testing.T) {
	idx, err := index.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })

	putPendingUpload(t, idx, 3, 4096, 1716700000000003)
	putPendingUpload(t, idx, 1, 1024, 1716700000000001)
	putPendingUpload(t, idx, 2, 2048, 1716700000000002)

	if err := idx.DeletePendingUpload(2); err != nil {
		t.Fatalf("DeletePendingUpload block 2: %v", err)
	}

	iter, err := idx.PendingUploads()
	if err != nil {
		t.Fatalf("PendingUploads: %v", err)
	}

	assertPendingUpload(t, nextPendingUpload(t, iter, "first"), 1, 1024, 1716700000000001)
	assertPendingUpload(t, nextPendingUpload(t, iter, "second"), 3, 4096, 1716700000000003)

	if _, err := iter.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next after final error = %v, want EOF", err)
	}
}

func nextPendingUpload(t *testing.T, iter index.PendingUploadIterator, label string) index.PendingUpload {
	t.Helper()

	upload, err := iter.Next()
	if err != nil {
		t.Fatalf("Next %s: %v", label, err)
	}
	return upload
}

func assertPendingUpload(t *testing.T, upload index.PendingUpload, blockID uint64, sealedSize, sealedAt int64) {
	t.Helper()

	if upload.BlockID != blockID {
		t.Fatalf("BlockID = %d, want %d", upload.BlockID, blockID)
	}
	if upload.ShardID != 7 {
		t.Fatalf("ShardID = %d, want 7", upload.ShardID)
	}
	if upload.SealedSizeBytes != sealedSize {
		t.Fatalf("SealedSizeBytes = %d, want %d", upload.SealedSizeBytes, sealedSize)
	}
	if upload.SealedAtUs != sealedAt {
		t.Fatalf("SealedAtUs = %d, want %d", upload.SealedAtUs, sealedAt)
	}
}

func putPendingUpload(t *testing.T, idx *index.Index, blockID uint64, sealedSize, sealedAt int64) {
	t.Helper()

	if err := idx.PutPendingUpload(index.PendingUpload{
		BlockID:         blockID,
		ShardID:         7,
		SealedSizeBytes: sealedSize,
		SealedAtUs:      sealedAt,
	}); err != nil {
		t.Fatalf("PutPendingUpload block %d: %v", blockID, err)
	}
}
