package shard_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/encryption"
	"github.com/petabytecl/scrap/internal/index"
	"github.com/petabytecl/scrap/internal/rewrap"
	"github.com/petabytecl/scrap/internal/shard"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func TestReadDocumentRestoresEvictedBlockFromBackend(t *testing.T) {
	ctx := context.Background()
	backendStore := newCountingDiscoveryBackend(backend.NewFS(t.TempDir()))
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		Backend:     backendStore,
		CellID:      testCellID,
		Concurrency: 1,
	})

	content := bytes.Repeat([]byte("restore me "), 8)
	confirmed := stageEvictedConfirmedBlock(ctx, t, s, backendStore.countingGetBackend.Backend, content)
	blocksDir := filepath.Join(s.DataDirForTest(), "blocks")
	assertReadRestoreStartsFromEvictedConfirmedBlock(t, blocksDir, confirmed)
	backendStore.resetCalls()

	rc, meta, err := s.ReadDocument(ctx, "tx-restore", "doc-1.bin")
	if err != nil {
		t.Fatalf("ReadDocument: %v", err)
	}
	defer func() { _ = rc.Close() }()

	assertRestoredDocument(t, rc, meta, content)
	assertRestorePublishedHotBlock(t, blocksDir)
	assertRestoreUsedCommittedBackendObjectOnly(t, backendStore, confirmed)
}

func TestReadDocumentRestoreBackendTransientReturnsUnavailable(t *testing.T) {
	ctx := context.Background()
	for _, tt := range []struct {
		name string
		err  error
	}{
		{name: "throttled", err: backend.ErrThrottled},
		{name: "transient", err: backend.ErrTransient},
		{name: "auth", err: backend.ErrAuth},
	} {
		t.Run(tt.name, func(t *testing.T) {
			backendStore := &failingGetBackend{
				Backend: backend.NewFS(t.TempDir()),
				err:     tt.err,
			}
			s := openUploadTestShard(t, shard.UploadConfig{
				Enabled:               true,
				Backend:               backendStore,
				CellID:                testCellID,
				Concurrency:           1,
				RestoreMaxAttempts:    1,
				RestoreRetryBaseDelay: time.Nanosecond,
			})

			content := bytes.Repeat([]byte("transient restore "), 4)
			stageEvictedConfirmedBlock(ctx, t, s, backendStore.Backend, content)

			err := assertReadDocumentRestoreFailsClosed(ctx, t, s, storeapi.ErrUnavailable)
			reason, ok := storeapi.UnavailableReason(err)
			if !ok || reason != storeapi.UnavailableReasonBackendRestoreUnavailable {
				t.Fatalf("unavailable reason = %q/%v, want backend_restore_unavailable", reason, ok)
			}
		})
	}
}

func TestReadDocumentRestoreRetriesTransientBackendFailures(t *testing.T) {
	ctx := context.Background()
	backendStore := &retryingGetBackend{
		Backend: backend.NewFS(t.TempDir()),
		errs:    []error{backend.ErrTransient, backend.ErrThrottled},
	}
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:               true,
		Backend:               backendStore,
		CellID:                testCellID,
		Concurrency:           1,
		RestoreMaxAttempts:    3,
		RestoreRetryBaseDelay: time.Nanosecond,
	})

	content := bytes.Repeat([]byte("retry restore "), 4)
	stageEvictedConfirmedBlock(ctx, t, s, backendStore.Backend, content)

	rc, meta, err := s.ReadDocument(ctx, "tx-restore", "doc-1.bin")
	if err != nil {
		t.Fatalf("ReadDocument after retry: %v", err)
	}
	defer func() { _ = rc.Close() }()

	assertRestoredDocument(t, rc, meta, content)
	assertRestorePublishedHotBlock(t, filepath.Join(s.DataDirForTest(), "blocks"))
	if got := backendStore.calls.Load(); got != 3 {
		t.Fatalf("Backend GetObject calls = %d, want 3", got)
	}
}

func TestReadDocumentRestoreRetriesTransientBackendReadFailure(t *testing.T) {
	ctx := context.Background()
	backendStore := &transientReadGetBackend{
		Backend: backend.NewFS(t.TempDir()),
	}
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:               true,
		Backend:               backendStore,
		CellID:                testCellID,
		Concurrency:           1,
		RestoreMaxAttempts:    2,
		RestoreRetryBaseDelay: time.Nanosecond,
	})

	content := bytes.Repeat([]byte("retry read restore "), 4)
	stageEvictedConfirmedBlock(ctx, t, s, backendStore.Backend, content)

	rc, meta, err := s.ReadDocument(ctx, "tx-restore", "doc-1.bin")
	if err != nil {
		t.Fatalf("ReadDocument after read retry: %v", err)
	}
	defer func() { _ = rc.Close() }()

	assertRestoredDocument(t, rc, meta, content)
	assertRestorePublishedHotBlock(t, filepath.Join(s.DataDirForTest(), "blocks"))
	if got := backendStore.calls.Load(); got != 2 {
		t.Fatalf("Backend GetObject calls = %d, want 2", got)
	}
}

func TestReadDocumentRestoreRetryBudgetExhaustedFailsClosed(t *testing.T) {
	ctx := context.Background()
	backendStore := &retryingGetBackend{
		Backend: backend.NewFS(t.TempDir()),
		errs:    []error{backend.ErrTransient, backend.ErrThrottled, backend.ErrAuth},
	}
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:               true,
		Backend:               backendStore,
		CellID:                testCellID,
		Concurrency:           1,
		RestoreMaxAttempts:    2,
		RestoreRetryBaseDelay: time.Nanosecond,
	})

	content := bytes.Repeat([]byte("retry exhausted restore "), 4)
	stageEvictedConfirmedBlock(ctx, t, s, backendStore.Backend, content)

	err := assertReadDocumentRestoreFailsClosed(ctx, t, s, storeapi.ErrUnavailable)
	reason, ok := storeapi.UnavailableReason(err)
	if !ok || reason != storeapi.UnavailableReasonBackendRestoreUnavailable {
		t.Fatalf("unavailable reason = %q/%v, want backend_restore_unavailable", reason, ok)
	}
	if got := backendStore.calls.Load(); got != 2 {
		t.Fatalf("Backend GetObject calls = %d, want 2", got)
	}
}

func TestReadDocumentRestoreRetryBudgetCapsExplicitAttempts(t *testing.T) {
	ctx := context.Background()
	errs := make([]error, 10)
	for i := range errs {
		errs[i] = backend.ErrTransient
	}
	backendStore := &retryingGetBackend{
		Backend: backend.NewFS(t.TempDir()),
		errs:    errs,
	}
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:               true,
		Backend:               backendStore,
		CellID:                testCellID,
		Concurrency:           1,
		RestoreMaxAttempts:    99,
		RestoreRetryBaseDelay: time.Nanosecond,
	})

	content := bytes.Repeat([]byte("capped retry restore "), 4)
	stageEvictedConfirmedBlock(ctx, t, s, backendStore.Backend, content)

	err := assertReadDocumentRestoreFailsClosed(ctx, t, s, storeapi.ErrUnavailable)
	reason, ok := storeapi.UnavailableReason(err)
	if !ok || reason != storeapi.UnavailableReasonBackendRestoreUnavailable {
		t.Fatalf("unavailable reason = %q/%v, want backend_restore_unavailable", reason, ok)
	}
	if got := backendStore.calls.Load(); got != 5 {
		t.Fatalf("Backend GetObject calls = %d, want capped attempt count 5", got)
	}
}

func TestReadDocumentRestoreMissingBackendConfigReturnsUnavailable(t *testing.T) {
	ctx := context.Background()
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		CellID:      testCellID,
		Concurrency: 1,
	})

	content := bytes.Repeat([]byte("missing backend restore "), 4)
	if _, err := s.WriteDocument(ctx, "tx-restore", "doc-1.bin", "application/octet-stream", "", bytes.NewReader(content)); err != nil {
		t.Fatalf("WriteDocument doc-1: %v", err)
	}
	if _, err := s.WriteDocument(ctx, "tx-restore-next", "doc-2.bin", "application/octet-stream", "", bytes.NewReader([]byte("seal previous"))); err != nil {
		t.Fatalf("WriteDocument doc-2: %v", err)
	}
	pending := waitPendingUploads(t, s, 1)[0]
	confirmed := confirmedUploadForTest(pending.SealedSizeBytes)
	confirmed.UploadGeneration = pending.UploadGeneration
	if err := s.ConfirmUploadForTest(ctx, confirmed); err != nil {
		t.Fatalf("ConfirmUploadForTest: %v", err)
	}

	blocksDir := filepath.Join(s.DataDirForTest(), "blocks")
	if err := shard.WriteEvictionMarker(blocksDir, evictionMarkerFromConfirmed(confirmed)); err != nil {
		t.Fatalf("WriteEvictionMarker: %v", err)
	}
	if err := os.Remove(block.FilePath(blocksDir, 1)); err != nil {
		t.Fatalf("remove local Block: %v", err)
	}

	err := assertReadDocumentRestoreFailsClosed(ctx, t, s, storeapi.ErrUnavailable)
	reason, ok := storeapi.UnavailableReason(err)
	if !ok || reason != storeapi.UnavailableReasonBackendRestoreUnavailable {
		t.Fatalf("unavailable reason = %q/%v, want backend_restore_unavailable", reason, ok)
	}
}

func TestReadDocumentRestoreMissingBackendObjectReturnsDataLoss(t *testing.T) {
	ctx := context.Background()
	backendStore := backend.NewFS(t.TempDir())
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		Backend:     backendStore,
		CellID:      testCellID,
		Concurrency: 1,
	})

	content := bytes.Repeat([]byte("missing restore "), 4)
	confirmed := stageEvictedConfirmedBlock(ctx, t, s, backendStore, content)
	if err := backendStore.DeleteObject(ctx, confirmed.BlockObject.Key); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}

	err := assertReadDocumentRestoreFailsClosed(ctx, t, s, storeapi.ErrDataLoss)
	assertDataLossReason(t, err, storeapi.DataLossReasonBackendRestoreMissing)
}

func TestReadDocumentRestoreBackendInvariantFailuresReturnDataLoss(t *testing.T) {
	ctx := context.Background()
	for _, tt := range []struct {
		name string
		err  error
	}{
		{name: "corrupt", err: backend.ErrCorrupt},
		{name: "permanent", err: backend.ErrPermanent},
		{name: "conflict", err: backend.ErrConflict},
	} {
		t.Run(tt.name, func(t *testing.T) {
			backendStore := &retryingGetBackend{
				Backend: backend.NewFS(t.TempDir()),
				errs:    []error{tt.err},
			}
			s := openUploadTestShard(t, shard.UploadConfig{
				Enabled:               true,
				Backend:               backendStore,
				CellID:                testCellID,
				Concurrency:           1,
				RestoreMaxAttempts:    3,
				RestoreRetryBaseDelay: time.Nanosecond,
			})

			content := bytes.Repeat([]byte("backend invariant restore "), 4)
			stageEvictedConfirmedBlock(ctx, t, s, backendStore.Backend, content)

			err := assertReadDocumentRestoreFailsClosed(ctx, t, s, storeapi.ErrDataLoss)
			assertDataLossReason(t, err, storeapi.DataLossReasonBackendRestoreCorrupt)
			if got := backendStore.calls.Load(); got != 1 {
				t.Fatalf("Backend GetObject calls = %d, want 1", got)
			}
		})
	}
}

func TestReadDocumentRestoreSizeMismatchReturnsDataLoss(t *testing.T) {
	ctx := context.Background()
	backendStore := &metaMutatingGetBackend{
		Backend: backend.NewFS(t.TempDir()),
		mutate: func(meta backend.ObjectMeta) backend.ObjectMeta {
			meta.Size++
			return meta
		},
	}
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		Backend:     backendStore,
		CellID:      testCellID,
		Concurrency: 1,
	})

	content := bytes.Repeat([]byte("size mismatch restore "), 4)
	stageEvictedConfirmedBlock(ctx, t, s, backendStore.Backend, content)

	err := assertReadDocumentRestoreFailsClosed(ctx, t, s, storeapi.ErrDataLoss)
	assertDataLossReason(t, err, storeapi.DataLossReasonBackendRestoreMetadataMismatch)
}

func TestReadDocumentRestoreValidationTokenMismatchReturnsDataLoss(t *testing.T) {
	ctx := context.Background()
	backendStore := &metaMutatingGetBackend{
		Backend: backend.NewFS(t.TempDir()),
		mutate: func(meta backend.ObjectMeta) backend.ObjectMeta {
			meta.ETag = "mismatched-validation"
			return meta
		},
	}
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		Backend:     backendStore,
		CellID:      testCellID,
		Concurrency: 1,
	})

	content := bytes.Repeat([]byte("validation mismatch restore "), 4)
	stageEvictedConfirmedBlock(ctx, t, s, backendStore.Backend, content)

	err := assertReadDocumentRestoreFailsClosed(ctx, t, s, storeapi.ErrDataLoss)
	assertDataLossReason(t, err, storeapi.DataLossReasonBackendRestoreMetadataMismatch)
}

func TestReadDocumentRestoreCorruptBackendObjectReturnsDataLoss(t *testing.T) {
	ctx := context.Background()
	backendStore := &corruptingGetBackend{Backend: backend.NewFS(t.TempDir())}
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		Backend:     backendStore,
		CellID:      testCellID,
		Concurrency: 1,
	})

	content := bytes.Repeat([]byte("corrupt restore "), 4)
	stageEvictedConfirmedBlock(ctx, t, s, backendStore.Backend, content)

	err := assertReadDocumentRestoreFailsClosed(ctx, t, s, storeapi.ErrDataLoss)
	assertDataLossReason(t, err, storeapi.DataLossReasonBackendRestoreChecksumMismatch)
}

func TestReadDocumentRestoreCorruptHeaderReturnsDataLoss(t *testing.T) {
	ctx := context.Background()
	backendStore := &mutatingGetBackend{
		Backend: backend.NewFS(t.TempDir()),
		mutate: func(data []byte) {
			data[0] ^= 0xff
		},
	}
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		Backend:     backendStore,
		CellID:      testCellID,
		Concurrency: 1,
	})

	content := bytes.Repeat([]byte("header restore "), 4)
	stageEvictedConfirmedBlock(ctx, t, s, backendStore.Backend, content)

	err := assertReadDocumentRestoreFailsClosed(ctx, t, s, storeapi.ErrDataLoss)
	assertDataLossReason(t, err, storeapi.DataLossReasonBackendRestoreChecksumMismatch)
}

func TestReadDocumentRestoreCorruptFrameHeaderReturnsDataLoss(t *testing.T) {
	ctx := context.Background()
	backendStore := &mutatingGetBackend{
		Backend: backend.NewFS(t.TempDir()),
		mutate: func(data []byte) {
			frameStart := block.HeaderSize
			data[frameStart] ^= 0xff
			block.RecomputeFramePayloadCRC(data, frameStart)
		},
	}
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		Backend:     backendStore,
		CellID:      testCellID,
		Concurrency: 1,
	})

	content := bytes.Repeat([]byte("frame header restore "), 4)
	stageEvictedConfirmedBlock(ctx, t, s, backendStore.Backend, content)

	err := assertReadDocumentRestoreFailsClosed(ctx, t, s, storeapi.ErrDataLoss)
	assertDataLossReason(t, err, storeapi.DataLossReasonBackendRestoreChecksumMismatch)
}

func TestReadDocumentRestoreCorruptDocumentSHAReturnsDataLoss(t *testing.T) {
	ctx := context.Background()
	backendStore := &mutatingGetBackend{
		Backend: backend.NewFS(t.TempDir()),
		mutate: func(data []byte) {
			data[block.HeaderSize+block.FrameHeaderSize] ^= 0xff
			block.RecomputeFramePayloadCRC(data, block.HeaderSize)
		},
	}
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		Backend:     backendStore,
		CellID:      testCellID,
		Concurrency: 1,
	})

	content := bytes.Repeat([]byte("sha restore "), 4)
	stageEvictedConfirmedBlock(ctx, t, s, backendStore.Backend, content)

	err := assertReadDocumentRestoreFailsClosed(ctx, t, s, storeapi.ErrDataLoss)
	assertDataLossReason(t, err, storeapi.DataLossReasonBackendRestoreChecksumMismatch)
}

func TestReadDocumentRestoreRequiresCommittedConfirmUpload(t *testing.T) {
	ctx := context.Background()
	s := openTestShard(t)

	content := bytes.Repeat([]byte("no confirm upload "), 4)
	if _, err := s.WriteDocument(ctx, "tx-restore", "doc-1.bin", "application/octet-stream", "", bytes.NewReader(content)); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}

	blocksDir := filepath.Join(s.DataDirForTest(), "blocks")
	if err := shard.WriteEvictionMarker(blocksDir, shard.EvictionMarker{
		BlockID:         1,
		BackendKey:      "cell-a/shards/0000000000000007/0000000000000001.blk",
		SizeBytes:       123,
		ValidationToken: "validation",
		EvictedAtUs:     time.Now().UnixMicro(),
		Trigger:         shard.EvictionTriggerOperatorRequested,
		Reason:          shard.EvictionReasonEvidenceRun,
	}); err != nil {
		t.Fatalf("WriteEvictionMarker: %v", err)
	}
	if err := os.Remove(block.FilePath(blocksDir, 1)); err != nil {
		t.Fatalf("remove local Block: %v", err)
	}

	_ = assertReadDocumentRestoreFailsClosed(ctx, t, s, storeapi.ErrDataLoss)
}

func TestReadDocumentRestoreRequiresMatchingEvictionMarker(t *testing.T) {
	ctx := context.Background()
	countingBackend := newCountingGetBackend(backend.NewFS(t.TempDir()))
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		Backend:     countingBackend,
		CellID:      testCellID,
		Concurrency: 1,
	})

	content := bytes.Repeat([]byte("stale marker restore "), 4)
	confirmed := stageEvictedConfirmedBlock(ctx, t, s, countingBackend.Backend, content)
	blocksDir := filepath.Join(s.DataDirForTest(), "blocks")
	staleMarker := evictionMarkerFromConfirmed(confirmed)
	staleMarker.ValidationToken = "stale-" + confirmed.BlockObject.ValidationToken
	if err := shard.WriteEvictionMarker(blocksDir, staleMarker); err != nil {
		t.Fatalf("WriteEvictionMarker stale: %v", err)
	}
	countingBackend.resetGetCalls()

	_ = assertReadDocumentRestoreFailsClosed(ctx, t, s, storeapi.ErrDataLoss)
	if got := countingBackend.getCalls.Load(); got != 0 {
		t.Fatalf("Backend GetObject calls = %d, want 0", got)
	}
}

func TestEncryptedReadDocumentRestoresThenUsesEnvelopePath(t *testing.T) {
	ctx := context.Background()
	backendStore := newCountingDiscoveryBackend(backend.NewFS(t.TempDir()))
	transit := encryption.NewFakeTransit(encryption.FakeConfig{KeyName: testTransitKey})
	s := openEncryptedUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		Backend:     backendStore,
		CellID:      testCellID,
		Concurrency: 1,
	}, transit)

	plaintextMarker := []byte("encrypted restore plaintext:")
	content := bytes.Repeat(plaintextMarker, 64)
	confirmed := stageEvictedConfirmedBlock(ctx, t, s, backendStore.countingGetBackend.Backend, content)
	blocksDir := filepath.Join(s.DataDirForTest(), "blocks")
	assertReadRestoreStartsFromEvictedConfirmedBlock(t, blocksDir, confirmed)
	assertBackendObjectOmitsPlaintext(ctx, t, backendStore.countingGetBackend.Backend, confirmed.BlockObject.Key, content, plaintextMarker)
	backendStore.resetCalls()

	rc, meta, err := s.ReadDocument(ctx, "tx-restore", "doc-1.bin")
	if err != nil {
		t.Fatalf("ReadDocument encrypted restore: %v", err)
	}
	defer func() { _ = rc.Close() }()

	assertRestoredDocument(t, rc, meta, content)
	assertRestorePublishedHotBlock(t, blocksDir)
	assertBlockOmitsPlaintext(t, s.DataDirForTest(), content)
	assertBlockOmitsPlaintext(t, s.DataDirForTest(), plaintextMarker)
	assertRestoreUsedCommittedBackendObjectOnly(t, backendStore, confirmed)
	entry := readOnlyIndexEntry(t, s.DataDirForTest(), "tx-restore", "doc-1.bin")
	assertEnvelopeMetadata(t, entry, len(content))
}

func TestReadDocumentEncryptedRestoreFailsClosedWhenKeyMaterialUnavailable(t *testing.T) {
	ctx := context.Background()
	for _, tt := range []struct {
		name      string
		unwrapErr error
	}{
		{name: "unavailable", unwrapErr: encryption.ErrUnavailable},
		{name: "auth denied", unwrapErr: encryption.ErrAuthDenied},
		{name: "missing key", unwrapErr: encryption.ErrMissingKey},
	} {
		t.Run(tt.name, func(t *testing.T) {
			backendStore := backend.NewFS(t.TempDir())
			transit := &mutableTransit{
				delegate: encryption.NewFakeTransit(encryption.FakeConfig{KeyName: testTransitKey}),
			}
			s := openEncryptedUploadTestShard(t, shard.UploadConfig{
				Enabled:     true,
				Backend:     backendStore,
				CellID:      testCellID,
				Concurrency: 1,
			}, transit)

			plaintextMarker := []byte("encrypted unavailable restore plaintext:")
			content := bytes.Repeat(plaintextMarker, 32)
			confirmed := stageEvictedConfirmedBlock(ctx, t, s, backendStore, content)
			transit.unwrapErr = tt.unwrapErr

			err := assertEncryptedRestoreCryptoUnavailable(ctx, t, s)
			assertCryptoRestoreErrorSanitized(t, err, confirmed.BlockObject.Key, content, plaintextMarker)
			assertRestorePublishedHotBlock(t, filepath.Join(s.DataDirForTest(), "blocks"))
			assertBlockOmitsPlaintext(t, s.DataDirForTest(), content)
			assertBlockOmitsPlaintext(t, s.DataDirForTest(), plaintextMarker)
		})
	}
}

func TestReadDocumentEncryptedRestoreFailsClosedWhenKeyVersionRejected(t *testing.T) {
	ctx := context.Background()
	backendStore := backend.NewFS(t.TempDir())
	transit := encryption.NewFakeTransit(encryption.FakeConfig{KeyName: testTransitKey})
	s := openEncryptedUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		Backend:     backendStore,
		CellID:      testCellID,
		Concurrency: 1,
	}, transit)

	plaintextMarker := []byte("encrypted wrong version restore plaintext:")
	content := bytes.Repeat(plaintextMarker, 32)
	confirmed := stageEvictedConfirmedBlock(ctx, t, s, backendStore, content)
	transit.RequireMinimumVersion(2)

	err := assertEncryptedRestoreCryptoUnavailable(ctx, t, s)
	assertCryptoRestoreErrorSanitized(t, err, confirmed.BlockObject.Key, content, plaintextMarker)
	assertRestorePublishedHotBlock(t, filepath.Join(s.DataDirForTest(), "blocks"))
	assertBlockOmitsPlaintext(t, s.DataDirForTest(), content)
	assertBlockOmitsPlaintext(t, s.DataDirForTest(), plaintextMarker)
}

func TestReadDocumentEncryptedRestoreUsesRewrappedEnvelope(t *testing.T) {
	ctx := context.Background()
	backendStore := backend.NewFS(t.TempDir())
	transit := encryption.NewFakeTransit(encryption.FakeConfig{KeyName: testTransitKey})
	s := openEncryptedUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		Backend:     backendStore,
		CellID:      testCellID,
		Concurrency: 1,
	}, transit)

	plaintextMarker := []byte("encrypted rewrap restore plaintext:")
	content := bytes.Repeat(plaintextMarker, 48)
	if _, err := s.WriteDocument(ctx, "tx-restore", "doc-1.bin", "application/octet-stream", "", bytes.NewReader(content)); err != nil {
		t.Fatalf("WriteDocument doc-1: %v", err)
	}
	if _, err := s.WriteDocument(ctx, "tx-restore-next", "doc-2.bin", "application/octet-stream", "", bytes.NewReader([]byte("seal previous"))); err != nil {
		t.Fatalf("WriteDocument doc-2: %v", err)
	}
	waitBackendObject(ctx, t, backendStore, backendObjectKey(1, "blk"))
	waitBackendObject(ctx, t, backendStore, backendObjectKey(1, "idx"))
	waitPendingUploads(t, s, 0)
	initialConfirmed := waitConfirmedUpload(t, s)
	beforeBlock := readTestBlockFile(t, s.DataDirForTest())

	transit.Rotate()
	result, err := s.RewrapDocument(ctx, rewrap.Request{
		TransactionID: "tx-restore",
		DocumentName:  "doc-1.bin",
		KeyVersion:    2,
		Reason:        "test",
	})
	if err != nil {
		t.Fatalf("RewrapDocument: %v", err)
	}
	if !result.Changed || result.OldKeyVersion != 1 || result.NewKeyVersion != 2 {
		t.Fatalf("RewrapDocument result = %+v, want changed 1->2", result)
	}
	assertBlockPayloadUnchanged(t, s.DataDirForTest(), beforeBlock)
	assertIndexEnvelopeVersion(t, s.DataDirForTest(), "tx-restore", "doc-1.bin", 2)

	waitPendingUploads(t, s, 0)
	confirmed := waitConfirmedUploadGeneration(t, s, 1, initialConfirmed.UploadGeneration)
	blocksDir := filepath.Join(s.DataDirForTest(), "blocks")
	if err := shard.WriteEvictionMarker(blocksDir, evictionMarkerFromConfirmed(confirmed)); err != nil {
		t.Fatalf("WriteEvictionMarker: %v", err)
	}
	if err := os.Remove(block.FilePath(blocksDir, 1)); err != nil {
		t.Fatalf("remove local Block: %v", err)
	}
	assertReadRestoreStartsFromEvictedConfirmedBlock(t, blocksDir, confirmed)

	rc, meta, err := s.ReadDocument(ctx, "tx-restore", "doc-1.bin")
	if err != nil {
		t.Fatalf("ReadDocument after rewrapped restore: %v", err)
	}
	defer func() { _ = rc.Close() }()

	assertRestoredDocument(t, rc, meta, content)
	assertRestorePublishedHotBlock(t, blocksDir)
	assertBlockPayloadUnchanged(t, s.DataDirForTest(), beforeBlock)
	assertIndexEnvelopeVersion(t, s.DataDirForTest(), "tx-restore", "doc-1.bin", 2)
	assertBlockOmitsPlaintext(t, s.DataDirForTest(), content)
	assertBlockOmitsPlaintext(t, s.DataDirForTest(), plaintextMarker)
}

func TestReadDocumentJoinsConcurrentBlockRestore(t *testing.T) {
	ctx := context.Background()
	countingBackend := newCountingGetBackend(backend.NewFS(t.TempDir()))
	countingBackend.started = make(chan struct{})
	countingBackend.release = make(chan struct{})
	releaseBackend := releaseBackendOnce(countingBackend.release)
	defer releaseBackend()
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		Backend:     countingBackend,
		CellID:      testCellID,
		Concurrency: 1,
	})

	content := bytes.Repeat([]byte("shared restore "), 8)
	stageEvictedConfirmedBlock(ctx, t, s, countingBackend.Backend, content)

	const readers = 5
	waiters := make(chan uint64, readers)
	s.SetRestoreWaiterEnteredForTest(waiters)
	t.Cleanup(func() { s.SetRestoreWaiterEnteredForTest(nil) })

	var ready sync.WaitGroup
	ready.Add(readers)
	start := make(chan struct{})
	errs := make(chan error, readers)
	for range readers {
		go func() {
			ready.Done()
			<-start
			rc, _, err := s.ReadDocument(ctx, "tx-restore", "doc-1.bin")
			if err != nil {
				errs <- err
				return
			}
			defer func() { _ = rc.Close() }()
			got, err := io.ReadAll(rc)
			if err != nil {
				errs <- err
				return
			}
			if !bytes.Equal(got, content) {
				errs <- errors.New("content mismatch")
				return
			}
			errs <- nil
		}()
	}
	ready.Wait()
	close(start)
	waitRestoreBackendStarted(t, countingBackend.started)
	waitRestoreWaitersEntered(t, waiters, 1, readers-1)
	releaseBackend()

	for range readers {
		if err := waitErrorResult(t, errs, "concurrent ReadDocument"); err != nil {
			t.Fatalf("concurrent ReadDocument: %v", err)
		}
	}
	if got := countingBackend.getCalls.Load(); got != 1 {
		t.Fatalf("Backend GetObject calls = %d, want 1", got)
	}
}

func TestReadDocumentSharedRestoreSurvivesLeaderReaderCancellation(t *testing.T) {
	ctx := context.Background()
	countingBackend := newCountingGetBackend(backend.NewFS(t.TempDir()))
	countingBackend.started = make(chan struct{})
	countingBackend.release = make(chan struct{})
	releaseBackend := releaseBackendOnce(countingBackend.release)
	defer releaseBackend()
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		Backend:     countingBackend,
		CellID:      testCellID,
		Concurrency: 1,
	})

	content := bytes.Repeat([]byte("cancelled leader restore "), 4)
	stageEvictedConfirmedBlock(ctx, t, s, countingBackend.Backend, content)

	waiters := make(chan uint64, 1)
	s.SetRestoreWaiterEnteredForTest(waiters)
	t.Cleanup(func() { s.SetRestoreWaiterEnteredForTest(nil) })

	leaderCtx, cancelLeader := context.WithCancel(ctx)
	leaderResult := make(chan readResult, 1)
	go func() {
		rc, meta, err := s.ReadDocument(leaderCtx, "tx-restore", "doc-1.bin")
		if rc != nil {
			_ = rc.Close()
		}
		leaderResult <- readResult{meta: meta, err: err, readerReturned: rc != nil}
	}()

	waitRestoreBackendStarted(t, countingBackend.started)
	followerErr := make(chan error, 1)
	go func() {
		rc, _, err := s.ReadDocument(ctx, "tx-restore", "doc-1.bin")
		if err != nil {
			followerErr <- err
			return
		}
		defer func() { _ = rc.Close() }()
		got, err := io.ReadAll(rc)
		if err != nil {
			followerErr <- err
			return
		}
		if !bytes.Equal(got, content) {
			followerErr <- errors.New("content mismatch")
			return
		}
		followerErr <- nil
	}()

	waitRestoreWaitersEntered(t, waiters, 1, 1)
	cancelLeader()
	releaseBackend()

	leader := waitReadResult(t, leaderResult, "leader ReadDocument cancellation")
	if !errors.Is(leader.err, context.Canceled) {
		t.Fatalf("leader ReadDocument error = %v, want context.Canceled", leader.err)
	}
	if leader.readerReturned {
		t.Fatal("leader ReadDocument returned reader after cancellation")
	}
	if leader.meta != (storeapi.DocumentMeta{}) {
		t.Fatalf("leader ReadDocument metadata = %+v, want zero value after cancellation", leader.meta)
	}
	if err := waitErrorResult(t, followerErr, "follower ReadDocument"); err != nil {
		t.Fatalf("follower ReadDocument: %v", err)
	}
	if got := countingBackend.getCalls.Load(); got != 1 {
		t.Fatalf("Backend GetObject calls = %d, want 1", got)
	}
}

func TestReadDocumentRestoreWaiterDeadlineDoesNotCancelSharedRestore(t *testing.T) {
	ctx := context.Background()
	countingBackend := newCountingGetBackend(backend.NewFS(t.TempDir()))
	countingBackend.started = make(chan struct{})
	countingBackend.release = make(chan struct{})
	releaseBackend := releaseBackendOnce(countingBackend.release)
	defer releaseBackend()
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		Backend:     countingBackend,
		CellID:      testCellID,
		Concurrency: 1,
	})

	content := bytes.Repeat([]byte("waiter timeout restore "), 4)
	stageEvictedConfirmedBlock(ctx, t, s, countingBackend.Backend, content)

	leaderResult := make(chan error, 1)
	go func() {
		rc, _, err := s.ReadDocument(ctx, "tx-restore", "doc-1.bin")
		if err != nil {
			leaderResult <- err
			return
		}
		defer func() { _ = rc.Close() }()
		got, err := io.ReadAll(rc)
		if err != nil {
			leaderResult <- err
			return
		}
		if !bytes.Equal(got, content) {
			leaderResult <- errors.New("content mismatch")
			return
		}
		leaderResult <- nil
	}()

	waitRestoreBackendStarted(t, countingBackend.started)
	waiterCtx, cancelWaiter := context.WithTimeout(ctx, 25*time.Millisecond)
	defer cancelWaiter()
	rc, meta, err := s.ReadDocument(waiterCtx, "tx-restore", "doc-1.bin")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiter ReadDocument error = %v, want context deadline", err)
	}
	if rc != nil {
		_ = rc.Close()
		t.Fatal("waiter ReadDocument returned reader after deadline")
	}
	if meta != (storeapi.DocumentMeta{}) {
		t.Fatalf("waiter ReadDocument metadata = %+v, want zero value after deadline", meta)
	}

	releaseBackend()
	if err := waitErrorResult(t, leaderResult, "leader ReadDocument after waiter deadline"); err != nil {
		t.Fatalf("leader ReadDocument after waiter deadline: %v", err)
	}
	if got := countingBackend.getCalls.Load(); got != 1 {
		t.Fatalf("Backend GetObject calls = %d, want 1", got)
	}
	assertRestorePublishedHotBlock(t, filepath.Join(s.DataDirForTest(), "blocks"))
}

func TestReadDocumentRestoreLeaderDeadlineFailsClosed(t *testing.T) {
	ctx := context.Background()
	countingBackend := newCountingGetBackend(backend.NewFS(t.TempDir()))
	countingBackend.started = make(chan struct{})
	countingBackend.release = make(chan struct{})
	releaseBackend := releaseBackendOnce(countingBackend.release)
	defer releaseBackend()
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		Backend:     countingBackend,
		CellID:      testCellID,
		Concurrency: 1,
	})

	content := bytes.Repeat([]byte("leader deadline restore "), 4)
	stageEvictedConfirmedBlock(ctx, t, s, countingBackend.Backend, content)

	leaderCtx, cancelLeader := context.WithTimeout(ctx, 25*time.Millisecond)
	defer cancelLeader()
	leaderResult := make(chan readResult, 1)
	go func() {
		rc, meta, err := s.ReadDocument(leaderCtx, "tx-restore", "doc-1.bin")
		if rc != nil {
			_ = rc.Close()
		}
		leaderResult <- readResult{meta: meta, err: err, readerReturned: rc != nil}
	}()

	waitRestoreBackendStarted(t, countingBackend.started)
	leader := waitReadResult(t, leaderResult, "leader ReadDocument deadline")
	if !errors.Is(leader.err, context.DeadlineExceeded) {
		t.Fatalf("leader ReadDocument error = %v, want context deadline", leader.err)
	}
	if leader.readerReturned {
		t.Fatal("leader ReadDocument returned reader after deadline")
	}
	if leader.meta != (storeapi.DocumentMeta{}) {
		t.Fatalf("leader ReadDocument metadata = %+v, want zero value after deadline", leader.meta)
	}
	assertRestoreFailureLeftEvicted(t, s)
	if got := countingBackend.getCalls.Load(); got != 1 {
		t.Fatalf("Backend GetObject calls = %d, want 1", got)
	}
}

func TestReadDocumentRestoreDoesNotBlockMetadataReadsWhileDownloading(t *testing.T) {
	ctx := context.Background()
	countingBackend := newCountingGetBackend(backend.NewFS(t.TempDir()))
	countingBackend.started = make(chan struct{})
	countingBackend.release = make(chan struct{})
	releaseBackend := releaseBackendOnce(countingBackend.release)
	defer releaseBackend()
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		Backend:     countingBackend,
		CellID:      testCellID,
		Concurrency: 1,
	})

	content := bytes.Repeat([]byte("metadata during restore "), 4)
	stageEvictedConfirmedBlock(ctx, t, s, countingBackend.Backend, content)

	restoreResult := make(chan error, 1)
	go func() {
		rc, _, err := s.ReadDocument(ctx, "tx-restore", "doc-1.bin")
		if err != nil {
			restoreResult <- err
			return
		}
		defer func() { _ = rc.Close() }()
		_, err = io.ReadAll(rc)
		restoreResult <- err
	}()

	waitRestoreBackendStarted(t, countingBackend.started)
	headResult := make(chan error, 1)
	go func() {
		meta, err := s.HeadDocument(ctx, "tx-restore", "doc-1.bin")
		if err != nil {
			headResult <- err
			return
		}
		if meta.Size != int64(len(content)) {
			headResult <- errors.New("metadata size mismatch")
			return
		}
		headResult <- nil
	}()

	if err := waitErrorResult(t, headResult, "HeadDocument while restore download is blocked"); err != nil {
		t.Fatalf("HeadDocument while restore download is blocked: %v", err)
	}

	releaseBackend()
	if err := waitErrorResult(t, restoreResult, "ReadDocument restore"); err != nil {
		t.Fatalf("ReadDocument restore: %v", err)
	}
}

func TestRestoreBlockForRepairRestoresQuarantinedBlockFromBackend(t *testing.T) {
	ctx := context.Background()
	backendStore := backend.NewFS(t.TempDir())
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		Backend:     backendStore,
		CellID:      testCellID,
		Concurrency: 1,
	})

	content := bytes.Repeat([]byte("repair restore "), 8)
	if _, err := s.WriteDocument(ctx, "tx-repair-restore", "doc-1.bin", "application/octet-stream", "", bytes.NewReader(content)); err != nil {
		t.Fatalf("WriteDocument doc-1: %v", err)
	}
	if _, err := s.WriteDocument(ctx, "tx-repair-seal", "doc-2.bin", "application/octet-stream", "", bytes.NewReader([]byte("seal previous"))); err != nil {
		t.Fatalf("WriteDocument doc-2: %v", err)
	}
	waitBackendObject(ctx, t, backendStore, backendObjectKey(1, "blk"))
	waitBackendObject(ctx, t, backendStore, backendObjectKey(1, "idx"))
	waitPendingUploads(t, s, 0)
	_ = waitConfirmedUpload(t, s)

	blocksDir := filepath.Join(s.DataDirForTest(), "blocks")
	if err := block.Quarantine(block.FilePath(blocksDir, 1)); err != nil {
		t.Fatalf("Quarantine: %v", err)
	}

	if err := s.RestoreBlockForRepair(ctx, 1); err != nil {
		t.Fatalf("RestoreBlockForRepair: %v", err)
	}

	assertRepairRestorePublishedHotBlock(t, blocksDir)
}

func TestRestoreBlockForRepairRestoresCorruptQuarantinedIndexFromBackend(t *testing.T) {
	ctx := context.Background()
	backendStore := backend.NewFS(t.TempDir())
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		Backend:     backendStore,
		CellID:      testCellID,
		Concurrency: 1,
	})

	content := bytes.Repeat([]byte("repair corrupt index "), 8)
	if _, err := s.WriteDocument(ctx, "tx-repair-index", "doc-1.bin", "application/octet-stream", "", bytes.NewReader(content)); err != nil {
		t.Fatalf("WriteDocument doc-1: %v", err)
	}
	if _, err := s.WriteDocument(ctx, "tx-repair-index-seal", "doc-2.bin", "application/octet-stream", "", bytes.NewReader([]byte("seal previous"))); err != nil {
		t.Fatalf("WriteDocument doc-2: %v", err)
	}
	waitBackendObject(ctx, t, backendStore, backendObjectKey(1, "blk"))
	waitBackendObject(ctx, t, backendStore, backendObjectKey(1, "idx"))
	waitPendingUploads(t, s, 0)
	_ = waitConfirmedUpload(t, s)

	blocksDir := filepath.Join(s.DataDirForTest(), "blocks")
	if err := block.Quarantine(block.FilePath(blocksDir, 1)); err != nil {
		t.Fatalf("Quarantine: %v", err)
	}
	if err := os.WriteFile(block.IdxFilePath(blocksDir, 1)+block.QuarantineSuffix, []byte("corrupt local index"), 0o600); err != nil {
		t.Fatalf("corrupt quarantined index: %v", err)
	}

	if err := s.RestoreBlockForRepair(ctx, 1); err != nil {
		t.Fatalf("RestoreBlockForRepair: %v", err)
	}

	assertRepairRestorePublishedHotBlock(t, blocksDir)
}

func TestRestoreBlockForRepairCorruptBackendLeavesQuarantine(t *testing.T) {
	ctx := context.Background()
	backendStore := &mutatingGetBackend{
		Backend: backend.NewFS(t.TempDir()),
		mutate: func(data []byte) {
			data[block.HeaderSize+block.FrameHeaderSize] ^= 0xff
		},
	}
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		Backend:     backendStore,
		CellID:      testCellID,
		Concurrency: 1,
	})

	content := bytes.Repeat([]byte("corrupt repair restore "), 8)
	if _, err := s.WriteDocument(ctx, "tx-repair-corrupt", "doc-1.bin", "application/octet-stream", "", bytes.NewReader(content)); err != nil {
		t.Fatalf("WriteDocument doc-1: %v", err)
	}
	if _, err := s.WriteDocument(ctx, "tx-repair-corrupt-seal", "doc-2.bin", "application/octet-stream", "", bytes.NewReader([]byte("seal previous"))); err != nil {
		t.Fatalf("WriteDocument doc-2: %v", err)
	}
	waitBackendObject(ctx, t, backendStore.Backend, backendObjectKey(1, "blk"))
	waitBackendObject(ctx, t, backendStore.Backend, backendObjectKey(1, "idx"))
	waitPendingUploads(t, s, 0)
	_ = waitConfirmedUpload(t, s)

	blocksDir := filepath.Join(s.DataDirForTest(), "blocks")
	if err := block.Quarantine(block.FilePath(blocksDir, 1)); err != nil {
		t.Fatalf("Quarantine: %v", err)
	}

	err := s.RestoreBlockForRepair(ctx, 1)
	if !errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("RestoreBlockForRepair error = %v, want ErrDataLoss", err)
	}

	assertRepairRestoreFailureLeftQuarantined(t, blocksDir)
}

func assertRestoredDocument(t *testing.T, rc io.Reader, meta storeapi.DocumentMeta, want []byte) {
	t.Helper()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("restored content mismatch: got %q want %q", got, want)
	}
	if meta.Size != int64(len(want)) {
		t.Fatalf("meta size = %d, want %d", meta.Size, len(want))
	}
}

func assertReadRestoreStartsFromEvictedConfirmedBlock(t *testing.T, blocksDir string, confirmed index.ConfirmedUpload) {
	t.Helper()

	if _, err := os.Stat(block.FilePath(blocksDir, confirmed.BlockID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("evicted Block stat error = %v, want not exist", err)
	}
	if _, err := os.Stat(block.IdxFilePath(blocksDir, confirmed.BlockID)); err != nil {
		t.Fatalf("retained index should exist before restore: %v", err)
	}
	marker, err := shard.ReadEvictionMarker(blocksDir, confirmed.BlockID)
	if err != nil {
		t.Fatalf("ReadEvictionMarker: %v", err)
	}
	if marker.BackendKey != confirmed.BlockObject.Key {
		t.Fatalf("eviction marker Backend key = %q, want committed key %q", marker.BackendKey, confirmed.BlockObject.Key)
	}
	if marker.SizeBytes != confirmed.BlockObject.SizeBytes {
		t.Fatalf("eviction marker size = %d, want committed size %d", marker.SizeBytes, confirmed.BlockObject.SizeBytes)
	}
	if marker.ValidationToken != confirmed.BlockObject.ValidationToken {
		t.Fatalf("eviction marker validation token mismatch")
	}
}

func assertReadDocumentRestoreFailsClosed(ctx context.Context, t *testing.T, s *shard.Shard, want error) error {
	t.Helper()

	rc, meta, err := s.ReadDocument(ctx, "tx-restore", "doc-1.bin")
	if !errors.Is(err, want) {
		t.Fatalf("ReadDocument error = %v, want %v", err, want)
	}
	if rc != nil {
		_ = rc.Close()
		t.Fatal("ReadDocument returned reader after failed restore")
	}
	if meta != (storeapi.DocumentMeta{}) {
		t.Fatalf("ReadDocument metadata = %+v, want zero value after failed restore", meta)
	}
	assertRestoreFailureLeftEvicted(t, s)
	return err
}

func assertDataLossReason(t *testing.T, err error, want string) {
	t.Helper()

	reason, ok := storeapi.DataLossReason(err)
	if !ok || reason != want {
		t.Fatalf("data-loss reason = %q/%v, want %s", reason, ok, want)
	}
}

func assertRestorePublishedHotBlock(t *testing.T, blocksDir string) {
	t.Helper()

	assertRestoredBlockVerified(t, blocksDir)
	assertRestoreMarkersPublished(t, blocksDir)
	lifecycle, err := shard.ClassifyLocalBlock(blocksDir, 1)
	if err != nil {
		t.Fatalf("ClassifyLocalBlock: %v", err)
	}
	if lifecycle.State != shard.LocalBlockStateHot || !lifecycle.ServingAllowed {
		t.Fatalf("lifecycle = %+v, want hot serving-allowed", lifecycle)
	}
}

func assertRestoredBlockVerified(t *testing.T, blocksDir string) {
	t.Helper()

	if _, err := os.Stat(block.FilePath(blocksDir, 1)); err != nil {
		t.Fatalf("restored Block not published: %v", err)
	}
	result, err := block.VerifyBlock(block.FilePath(blocksDir, 1), block.IdxFilePath(blocksDir, 1))
	if err != nil {
		t.Fatalf("VerifyBlock restored Block: %v", err)
	}
	if len(result.CorruptFrames) != 0 {
		t.Fatalf("restored Block has corrupt frames: %+v", result.CorruptFrames)
	}
}

func assertRestoreMarkersPublished(t *testing.T, blocksDir string) {
	t.Helper()

	if _, err := os.Stat(shard.EvictionMarkerPath(blocksDir, 1)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("eviction marker stat error = %v, want not exist", err)
	}
	restore, err := shard.ReadRestoreMarker(blocksDir, 1)
	if err != nil {
		t.Fatalf("ReadRestoreMarker: %v", err)
	}
	if restore.Source != shard.RestoreSourceBackend || restore.Reason != shard.RestoreReasonRead {
		t.Fatalf("restore marker = %+v, want backend/read", restore)
	}
}

func assertRestoreUsedCommittedBackendObjectOnly(t *testing.T, backendStore *countingDiscoveryBackend, confirmed index.ConfirmedUpload) {
	t.Helper()

	if got := backendStore.getCalls.Load(); got != 1 {
		t.Fatalf("Backend GetObject calls = %d, want 1", got)
	}
	keys := backendStore.objectGetKeys()
	if len(keys) != 1 {
		t.Fatalf("Backend GetObject keys = %v, want one committed key", keys)
	}
	if keys[0] != confirmed.BlockObject.Key {
		t.Fatalf("Backend GetObject key = %q, want committed key %q", keys[0], confirmed.BlockObject.Key)
	}
	opts := backendStore.objectGetOpts()
	if len(opts) != 1 {
		t.Fatalf("Backend GetObject opts = %v, want one full-object read", opts)
	}
	if opts[0].Range.Enabled {
		t.Fatalf("Backend GetObject range = %+v, want full Block object", opts[0].Range)
	}
	if got := backendStore.headCalls.Load(); got != 0 {
		t.Fatalf("Backend HeadObject calls = %d, want 0", got)
	}
	if got := backendStore.listCalls.Load(); got != 0 {
		t.Fatalf("Backend ListObjects calls = %d, want 0", got)
	}
}

func assertBackendObjectOmitsPlaintext(
	ctx context.Context,
	t *testing.T,
	backendStore backend.Backend,
	key string,
	plaintextNeedles ...[]byte,
) {
	t.Helper()

	rc, _, err := backendStore.GetObject(ctx, key, backend.GetOpts{})
	if err != nil {
		t.Fatalf("Backend GetObject for plaintext check: %v", err)
	}
	defer func() { _ = rc.Close() }()

	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll Backend object: %v", err)
	}
	for _, plaintext := range plaintextNeedles {
		if len(plaintext) != 0 && bytes.Contains(data, plaintext) {
			t.Fatalf("Backend object contains plaintext Document bytes %q", plaintext)
		}
	}
}

func assertEncryptedRestoreCryptoUnavailable(ctx context.Context, t *testing.T, s *shard.Shard) error {
	t.Helper()

	rc, meta, err := s.ReadDocument(ctx, "tx-restore", "doc-1.bin")
	if err == nil {
		if rc != nil {
			_ = rc.Close()
		}
		t.Fatal("ReadDocument encrypted restore error = nil, want crypto unavailable")
	}
	if rc != nil {
		t.Fatal("ReadDocument encrypted restore returned reader on crypto failure")
	}
	if meta != (storeapi.DocumentMeta{}) {
		t.Fatalf("ReadDocument encrypted restore meta = %+v, want zero", meta)
	}
	if !errors.Is(err, storeapi.ErrUnavailable) {
		t.Fatalf("ReadDocument encrypted restore error = %v, want ErrUnavailable", err)
	}
	reason, ok := storeapi.UnavailableReason(err)
	if !ok || reason != storeapi.UnavailableReasonCryptoUnavailable {
		t.Fatalf("UnavailableReason = %q/%v, want crypto_unavailable", reason, ok)
	}
	return err
}

func assertCryptoRestoreErrorSanitized(t *testing.T, err error, backendKey string, plaintextNeedles ...[]byte) {
	t.Helper()

	message := err.Error()
	forbidden := []string{
		backendKey,
		"fake-transit:",
		"transit unavailable",
		"transit auth denied",
		"transit missing key",
		"transit minimum version",
		"/tmp",
		"tx-restore",
		"doc-1.bin",
	}
	for _, plaintext := range plaintextNeedles {
		forbidden = append(forbidden, string(plaintext))
	}
	for _, forbidden := range forbidden {
		if forbidden != "" && strings.Contains(message, forbidden) {
			t.Fatalf("crypto restore error %q contains forbidden detail %q", message, forbidden)
		}
	}
}

func assertRepairRestorePublishedHotBlock(t *testing.T, blocksDir string) {
	t.Helper()

	result, err := block.VerifyBlock(block.FilePath(blocksDir, 1), block.IdxFilePath(blocksDir, 1))
	if err != nil {
		t.Fatalf("VerifyBlock: %v", err)
	}
	if len(result.CorruptFrames) != 0 {
		t.Fatalf("restored repair Block has corrupt frames: %+v", result.CorruptFrames)
	}
	if _, err := os.Stat(block.FilePath(blocksDir, 1) + block.QuarantineSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("block quarantine stat = %v, want not exist", err)
	}
	if _, err := os.Stat(block.IdxFilePath(blocksDir, 1) + block.QuarantineSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("index quarantine stat = %v, want not exist", err)
	}
	restore, err := shard.ReadRestoreMarker(blocksDir, 1)
	if err != nil {
		t.Fatalf("ReadRestoreMarker: %v", err)
	}
	if restore.Source != shard.RestoreSourceBackend || restore.Reason != shard.RestoreReasonRepair {
		t.Fatalf("restore marker = %+v, want backend/repair", restore)
	}
}

func assertRepairRestoreFailureLeftQuarantined(t *testing.T, blocksDir string) {
	t.Helper()

	if _, err := os.Stat(block.FilePath(blocksDir, 1)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Block stat after repair failure = %v, want not exist", err)
	}
	if _, err := os.Stat(block.IdxFilePath(blocksDir, 1)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("index stat after repair failure = %v, want not exist", err)
	}
	if _, err := os.Stat(block.FilePath(blocksDir, 1) + block.QuarantineSuffix); err != nil {
		t.Fatalf("quarantined Block should remain: %v", err)
	}
	if _, err := os.Stat(block.IdxFilePath(blocksDir, 1) + block.QuarantineSuffix); err != nil {
		t.Fatalf("quarantined index should remain: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(blocksDir, ".0000000000000001.blk.restore-*"))
	if err != nil {
		t.Fatalf("glob repair restore staging: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("repair restore staging files remain: %v", matches)
	}
	matches, err = filepath.Glob(filepath.Join(blocksDir, ".0000000000000001.idx.restore-*"))
	if err != nil {
		t.Fatalf("glob repair index restore staging: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("repair index restore staging files remain: %v", matches)
	}
}

func stageEvictedConfirmedBlock(ctx context.Context, t *testing.T, s *shard.Shard, backendStore backend.Backend, content []byte) index.ConfirmedUpload {
	t.Helper()

	if _, err := s.WriteDocument(ctx, "tx-restore", "doc-1.bin", "application/octet-stream", "", bytes.NewReader(content)); err != nil {
		t.Fatalf("WriteDocument doc-1: %v", err)
	}
	if _, err := s.WriteDocument(ctx, "tx-restore-next", "doc-2.bin", "application/octet-stream", "", bytes.NewReader([]byte("seal previous"))); err != nil {
		t.Fatalf("WriteDocument doc-2: %v", err)
	}

	waitBackendObject(ctx, t, backendStore, backendObjectKey(1, "blk"))
	waitBackendObject(ctx, t, backendStore, backendObjectKey(1, "idx"))
	waitPendingUploads(t, s, 0)
	confirmed := waitConfirmedUpload(t, s)

	blocksDir := filepath.Join(s.DataDirForTest(), "blocks")
	if err := shard.WriteEvictionMarker(blocksDir, evictionMarkerFromConfirmed(confirmed)); err != nil {
		t.Fatalf("WriteEvictionMarker: %v", err)
	}
	if err := os.Remove(block.FilePath(blocksDir, 1)); err != nil {
		t.Fatalf("remove local Block: %v", err)
	}
	return confirmed
}

func waitConfirmedUpload(t *testing.T, s *shard.Shard) index.ConfirmedUpload {
	t.Helper()

	const blockID uint64 = 1
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		confirmed, err := s.ConfirmedUploadForTest(blockID)
		if err == nil {
			return confirmed
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for confirmed upload block %d", blockID)
	return index.ConfirmedUpload{}
}

func waitConfirmedUploadGeneration(
	t *testing.T,
	s *shard.Shard,
	blockID uint64,
	previousGeneration int64,
) index.ConfirmedUpload {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		confirmed, err := s.ConfirmedUploadForTest(blockID)
		if err == nil && confirmed.UploadGeneration > previousGeneration {
			return confirmed
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf(
		"timed out waiting for confirmed upload block %d generation greater than %d",
		blockID,
		previousGeneration,
	)
	return index.ConfirmedUpload{}
}

func evictionMarkerFromConfirmed(confirmed index.ConfirmedUpload) shard.EvictionMarker {
	return shard.EvictionMarker{
		BlockID:         confirmed.BlockID,
		BackendKey:      confirmed.BlockObject.Key,
		SizeBytes:       confirmed.BlockObject.SizeBytes,
		ValidationToken: confirmed.BlockObject.ValidationToken,
		EvictedAtUs:     time.Now().UnixMicro(),
		Trigger:         shard.EvictionTriggerOperatorRequested,
		Reason:          shard.EvictionReasonEvidenceRun,
	}
}

func assertRestoreFailureLeftEvicted(t *testing.T, s *shard.Shard) {
	t.Helper()

	blocksDir := filepath.Join(s.DataDirForTest(), "blocks")
	if _, err := os.Stat(block.FilePath(blocksDir, 1)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Block stat error = %v, want not exist", err)
	}
	if _, err := os.Stat(shard.EvictionMarkerPath(blocksDir, 1)); err != nil {
		t.Fatalf("eviction marker should remain: %v", err)
	}
	if _, err := os.Stat(shard.RestoreMarkerPath(blocksDir, 1)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("restore marker stat error = %v, want not exist", err)
	}
	matches, err := filepath.Glob(filepath.Join(blocksDir, ".0000000000000001.blk.restore-*"))
	if err != nil {
		t.Fatalf("glob restore staging: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("restore staging files remain: %v", matches)
	}
}

func openEncryptedUploadTestShard(t *testing.T, upload shard.UploadConfig, transit encryption.Transit) *shard.Shard {
	t.Helper()

	s, err := shard.Open(shard.Config{
		DataDir:        t.TempDir(),
		ShardID:        testShardID,
		RaftID:         1,
		Peers:          map[uint64]string{1: "localhost:9091"},
		BlockSealSize:  41,
		TickInterval:   10 * time.Millisecond,
		BootstrapGrace: time.Second,
		Upload:         upload,
		Encryption: shard.EncryptionConfig{
			Transit:      transit,
			TransitMount: testTransitMount,
			TransitKey:   testTransitKey,
		},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.IsLeader() {
			return s
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("shard did not become leader")
	return nil
}

type countingGetBackend struct {
	backend.Backend
	getCalls atomic.Int32
	mu       sync.Mutex
	getKeys  []string
	getOpts  []backend.GetOpts
	once     sync.Once
	started  chan struct{}
	release  chan struct{}
}

type readResult struct {
	meta           storeapi.DocumentMeta
	err            error
	readerReturned bool
}

func releaseBackendOnce(release chan struct{}) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			close(release)
		})
	}
}

func waitRestoreBackendStarted(t *testing.T, started <-chan struct{}) {
	t.Helper()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for restore Backend GetObject")
	}
}

func waitRestoreWaitersEntered(t *testing.T, waiters <-chan uint64, blockID uint64, want int) {
	t.Helper()

	for range want {
		select {
		case got := <-waiters:
			if got != blockID {
				t.Fatalf("restore waiter block ID = %d, want %d", got, blockID)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for %d restore waiters to join Block %d", want, blockID)
		}
	}
}

func waitReadResult(t *testing.T, results <-chan readResult, label string) readResult {
	t.Helper()

	select {
	case result := <-results:
		return result
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", label)
		return readResult{}
	}
}

func waitErrorResult(t *testing.T, results <-chan error, label string) error {
	t.Helper()

	select {
	case err := <-results:
		return err
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", label)
		return nil
	}
}

func newCountingGetBackend(base backend.Backend) *countingGetBackend {
	return &countingGetBackend{Backend: base}
}

func (b *countingGetBackend) GetObject(ctx context.Context, key string, opts backend.GetOpts) (io.ReadCloser, backend.ObjectMeta, error) {
	b.getCalls.Add(1)
	b.mu.Lock()
	b.getKeys = append(b.getKeys, key)
	b.getOpts = append(b.getOpts, opts)
	b.mu.Unlock()
	if b.started != nil {
		b.once.Do(func() { close(b.started) })
	}
	if b.release != nil {
		select {
		case <-b.release:
		case <-ctx.Done():
			return nil, backend.ObjectMeta{}, ctx.Err()
		}
	}
	return b.Backend.GetObject(ctx, key, opts)
}

func (b *countingGetBackend) resetGetCalls() {
	b.getCalls.Store(0)
	b.mu.Lock()
	b.getKeys = nil
	b.getOpts = nil
	b.mu.Unlock()
}

func (b *countingGetBackend) objectGetKeys() []string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return append([]string(nil), b.getKeys...)
}

func (b *countingGetBackend) objectGetOpts() []backend.GetOpts {
	b.mu.Lock()
	defer b.mu.Unlock()

	return append([]backend.GetOpts(nil), b.getOpts...)
}

type failingGetBackend struct {
	backend.Backend
	err error
}

func (b *failingGetBackend) GetObject(context.Context, string, backend.GetOpts) (io.ReadCloser, backend.ObjectMeta, error) {
	return nil, backend.ObjectMeta{}, b.err
}

type retryingGetBackend struct {
	backend.Backend
	errs  []error
	calls atomic.Int32
}

func (b *retryingGetBackend) GetObject(ctx context.Context, key string, opts backend.GetOpts) (io.ReadCloser, backend.ObjectMeta, error) {
	call := int(b.calls.Add(1)) - 1
	if call < len(b.errs) {
		return nil, backend.ObjectMeta{}, b.errs[call]
	}
	return b.Backend.GetObject(ctx, key, opts)
}

type transientReadGetBackend struct {
	backend.Backend
	calls atomic.Int32
}

func (b *transientReadGetBackend) GetObject(ctx context.Context, key string, opts backend.GetOpts) (io.ReadCloser, backend.ObjectMeta, error) {
	rc, meta, err := b.Backend.GetObject(ctx, key, opts)
	if err != nil {
		return nil, backend.ObjectMeta{}, err
	}
	if b.calls.Add(1) == 1 {
		return &transientReadCloser{ReadCloser: rc}, meta, nil
	}
	return rc, meta, nil
}

type transientReadCloser struct {
	io.ReadCloser
	failed bool
}

func (r *transientReadCloser) Read(p []byte) (int, error) {
	if !r.failed {
		r.failed = true
		if len(p) > 8 {
			p = p[:8]
		}
		n, _ := r.ReadCloser.Read(p)
		return n, backend.ErrTransient
	}
	return r.ReadCloser.Read(p)
}

type corruptingGetBackend struct {
	backend.Backend
}

func (b *corruptingGetBackend) GetObject(ctx context.Context, key string, opts backend.GetOpts) (io.ReadCloser, backend.ObjectMeta, error) {
	return mutateGetObject(ctx, b.Backend, key, opts, func(data []byte) {
		if len(data) > block.HeaderSize+block.FrameHeaderSize {
			data[block.HeaderSize+block.FrameHeaderSize] ^= 0xff
		}
	})
}

type mutatingGetBackend struct {
	backend.Backend
	mutate func([]byte)
}

func (b *mutatingGetBackend) GetObject(ctx context.Context, key string, opts backend.GetOpts) (io.ReadCloser, backend.ObjectMeta, error) {
	return mutateGetObject(ctx, b.Backend, key, opts, b.mutate)
}

type metaMutatingGetBackend struct {
	backend.Backend
	mutate func(backend.ObjectMeta) backend.ObjectMeta
}

func (b *metaMutatingGetBackend) GetObject(ctx context.Context, key string, opts backend.GetOpts) (io.ReadCloser, backend.ObjectMeta, error) {
	rc, meta, err := b.Backend.GetObject(ctx, key, opts)
	if err != nil {
		return nil, backend.ObjectMeta{}, err
	}
	if b.mutate != nil {
		meta = b.mutate(meta)
	}
	return rc, meta, nil
}

func mutateGetObject(
	ctx context.Context,
	base backend.Backend,
	key string,
	opts backend.GetOpts,
	mutate func([]byte),
) (io.ReadCloser, backend.ObjectMeta, error) {
	rc, meta, err := base.GetObject(ctx, key, opts)
	if err != nil {
		return nil, backend.ObjectMeta{}, err
	}
	defer func() { _ = rc.Close() }()

	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, backend.ObjectMeta{}, err
	}
	if mutate != nil {
		mutate(data)
	}
	return io.NopCloser(bytes.NewReader(data)), meta, nil
}
