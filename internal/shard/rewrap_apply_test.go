package shard

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/encryption"
	"github.com/petabytecl/scrap/internal/index"
	"github.com/petabytecl/scrap/internal/rewrap"
)

func TestApplyRewrapRejectsStaleEnvelopeWithoutReplacingIndex(t *testing.T) {
	blocksDir := t.TempDir()
	currentEnvelope := rewrapApplyEnvelope(t, 2)
	replacementEnvelope := rewrapApplyEnvelope(t, 3)
	writeRewrapApplyIndex(t, blocksDir, currentEnvelope)

	s := &Shard{blocksDir: blocksDir, uploads: newUploadController(nil, UploadConfig{}, 7, nil, nil, nil)}
	err := s.applyRewrapDocumentEnvelope(&scrapv1.RewrapDocumentEnvelope{
		TransactionId:      "tx-rewrap",
		DocumentName:       "doc.xml",
		BlockId:            1,
		EncryptionEnvelope: replacementEnvelope,
		OldKeyVersion:      1,
		NewKeyVersion:      3,
		RewrappedAtUs:      1716700003000000,
	}, 1716700003000000)
	if !errors.Is(err, rewrap.ErrStaleEnvelope) {
		t.Fatalf("applyRewrapDocumentEnvelope error = %v, want ErrStaleEnvelope", err)
	}

	got := readRewrapApplyEnvelope(t, blocksDir)
	if string(got) != string(currentEnvelope) {
		t.Fatalf("stored envelope was replaced by stale command")
	}
}

func TestApplyRewrapRequeuesWithEntryIndexGeneration(t *testing.T) {
	blocksDir := t.TempDir()
	idx := openApplyTestIndex(t)
	currentEnvelope := rewrapApplyEnvelope(t, 1)
	replacementEnvelope := rewrapApplyEnvelope(t, 2)
	writeRewrapApplyIndex(t, blocksDir, currentEnvelope)
	if err := os.WriteFile(block.FilePath(blocksDir, 1), []byte("block bytes"), 0o600); err != nil {
		t.Fatalf("WriteFile block: %v", err)
	}
	if err := idx.PutConfirmedUpload(index.ConfirmedUpload{
		BlockID:         1,
		ShardID:         7,
		ConfirmedAtUs:   1716700001000000,
		SealedSizeBytes: 67108864,
		BlockObject: index.BackendObjectMetadata{
			Key:             "cell-a/shards/0000000000000007/0000000000000001.blk",
			SizeBytes:       67108864,
			ValidationToken: "block-validation",
		},
		IndexObject: index.BackendObjectMetadata{
			Key:             "cell-a/shards/0000000000000007/0000000000000001.idx",
			SizeBytes:       4096,
			ValidationToken: "index-validation",
		},
	}); err != nil {
		t.Fatalf("PutConfirmedUpload: %v", err)
	}

	s := &Shard{
		blocksDir: blocksDir,
		idx:       idx,
		upload:    UploadConfig{Enabled: true},
		uploads:   newUploadController(nil, UploadConfig{}, 7, nil, nil, nil),
		shardID:   7,
	}
	err := s.applyEntryCommand(&scrapv1.RaftCommand{
		Command: &scrapv1.RaftCommand_RewrapDoc{
			RewrapDoc: &scrapv1.RewrapDocumentEnvelope{
				TransactionId:      "tx-rewrap",
				DocumentName:       "doc.xml",
				BlockId:            1,
				EncryptionEnvelope: replacementEnvelope,
				OldKeyVersion:      1,
				NewKeyVersion:      2,
				RewrappedAtUs:      1716700002000000,
			},
		},
	}, 77)
	if err != nil {
		t.Fatalf("applyRewrapDocumentEnvelope: %v", err)
	}

	got := readRewrapApplyEnvelope(t, blocksDir)
	if string(got) != string(replacementEnvelope) {
		t.Fatal("stored envelope was not replaced")
	}
	pending, err := idx.GetPendingUpload(1)
	if err != nil {
		t.Fatalf("GetPendingUpload: %v", err)
	}
	if pending.UploadGeneration != 77 || pending.SealedAtUs != 77 {
		t.Fatalf("pending generation = %d/%d, want apply entry index", pending.UploadGeneration, pending.SealedAtUs)
	}
	if pending.SealedSizeBytes != int64(len("block bytes")) {
		t.Fatalf("pending sealed size = %d, want local block size", pending.SealedSizeBytes)
	}
}

func TestUploadProcessorSkipsPendingUploadWithoutLocalBlock(t *testing.T) {
	ctx := context.Background()
	backendStore := newCountingBackend()
	core := &uploadCoreStub{
		leader: true,
		pending: []PendingUpload{{
			BlockID:          1,
			ShardID:          7,
			SealedSizeBytes:  10,
			SealedAtUs:       77,
			UploadGeneration: 77,
		}},
	}
	controller := newUploadController(core, UploadConfig{
		Enabled:     true,
		Backend:     backendStore,
		CellID:      "cell-a",
		Concurrency: 1,
	}, 7, nil, nil, nil)

	controller.processPendingOnce(ctx)

	if backendStore.puts != 0 {
		t.Fatalf("backend puts = %d, want 0 for missing local Block", backendStore.puts)
	}
}

type uploadCoreStub struct {
	leader  bool
	pending []PendingUpload
}

func (c *uploadCoreStub) Propose(context.Context, []byte) error { return nil }

func (c *uploadCoreStub) IsLeader() bool { return c.leader }

func (c *uploadCoreStub) retryUploadObligations(context.Context) {}

func (c *uploadCoreStub) pendingUploads() ([]PendingUpload, error) {
	return append([]PendingUpload(nil), c.pending...), nil
}

func (c *uploadCoreStub) hasLocalBlock(uint64) bool { return false }

func (c *uploadCoreStub) blockPath(uint64) string { return "" }

func (c *uploadCoreStub) idxPath(uint64) string { return "" }

type countingBackend struct {
	puts int
}

func newCountingBackend() *countingBackend {
	return &countingBackend{}
}

func (b *countingBackend) PutObject(context.Context, string, io.Reader, int64, backend.PutOpts) (backend.PutResult, error) {
	b.puts++
	return backend.PutResult{}, nil
}

func (b *countingBackend) HeadObject(context.Context, string) (backend.ObjectMeta, error) {
	return backend.ObjectMeta{}, backend.ErrNotFound
}

func (b *countingBackend) GetObject(context.Context, string, backend.GetOpts) (io.ReadCloser, backend.ObjectMeta, error) {
	return nil, backend.ObjectMeta{}, backend.ErrNotFound
}

func (b *countingBackend) DeleteObject(context.Context, string) error { return nil }

func (b *countingBackend) ListObjects(context.Context, string, backend.ListOpts) (backend.ObjectIterator, error) {
	return emptyBackendIterator{}, nil
}

type emptyBackendIterator struct{}

func (emptyBackendIterator) Next() (backend.ObjectInfo, error) {
	return backend.ObjectInfo{}, io.EOF
}

func TestApplyRewrapReopensCurrentIndexAfterStaleEnvelope(t *testing.T) {
	blocksDir := t.TempDir()
	currentEnvelope := rewrapApplyEnvelope(t, 2)
	writeRewrapApplyIndex(t, blocksDir, currentEnvelope)
	blockWriter, err := block.NewWriter(block.FilePath(blocksDir, 1), 7, 1)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	t.Cleanup(func() { _ = blockWriter.Close() })
	idxWriter, err := block.OpenIndexWriter(block.IdxFilePath(blocksDir, 1))
	if err != nil {
		t.Fatalf("OpenIndexWriter: %v", err)
	}

	s := &Shard{
		blocksDir:   blocksDir,
		blockWriter: blockWriter,
		idxWriter:   idxWriter,
		uploads:     newUploadController(nil, UploadConfig{}, 7, nil, nil, nil),
	}
	err = s.applyRewrapDocumentEnvelopeCommand(&scrapv1.RewrapDocumentEnvelope{
		TransactionId:      "tx-rewrap",
		DocumentName:       "doc.xml",
		BlockId:            1,
		EncryptionEnvelope: rewrapApplyEnvelope(t, 3),
		OldKeyVersion:      1,
		NewKeyVersion:      3,
		RewrappedAtUs:      1716700003000000,
	}, 88)
	if err != nil {
		t.Fatalf("applyRewrapDocumentEnvelopeCommand: %v", err)
	}
	if s.idxWriter == nil {
		t.Fatal("idxWriter was not reopened after stale rewrap")
	}
	t.Cleanup(func() { _ = s.idxWriter.Close() })
	if err := s.idxWriter.Append(block.IndexEntry{
		TransactionID: "tx-after-stale",
		DocName:       "after.xml",
		CreatedAt:     time.UnixMicro(1716700004000000),
		FrameCount:    1,
		SHA256:        [32]byte{2},
	}); err != nil {
		t.Fatalf("Append after stale rewrap: %v", err)
	}
}

func TestProposeRewrapRejectsMissingHistoricalBlockBeforeConsensus(t *testing.T) {
	s := &Shard{
		blocksDir: t.TempDir(),
		upload:    UploadConfig{Enabled: true},
	}
	err := s.proposeRewrapDocument(
		context.Background(),
		1,
		rewrap.Request{TransactionID: "tx-rewrap", DocumentName: "doc.xml"},
		rewrap.Result{
			OldKeyVersion: 1,
			NewKeyVersion: 2,
			RewrappedAt:   time.UnixMicro(1716700002000000),
		},
		rewrapApplyEnvelope(t, 2),
	)
	if err == nil || !strings.Contains(err.Error(), "local Block data") {
		t.Fatalf("proposeRewrapDocument error = %v, want local Block preflight failure", err)
	}
}

func TestApplyEntryCommandReturnsRewrapApplyFailures(t *testing.T) {
	s := &Shard{blocksDir: t.TempDir(), uploads: newUploadController(nil, UploadConfig{}, 7, nil, nil, nil)}

	err := s.applyEntryCommand(&scrapv1.RaftCommand{
		Command: &scrapv1.RaftCommand_RewrapDoc{
			RewrapDoc: &scrapv1.RewrapDocumentEnvelope{
				TransactionId:      "tx-rewrap",
				DocumentName:       "doc.xml",
				BlockId:            1,
				EncryptionEnvelope: rewrapApplyEnvelope(t, 2),
				OldKeyVersion:      1,
				NewKeyVersion:      2,
				RewrappedAtUs:      1716700002000000,
			},
		},
	}, 10)
	if err == nil {
		t.Fatal("applyEntryCommand error = nil, want missing index failure")
	}
}

func TestApplyRewrapNotifiesMatchingProposalID(t *testing.T) {
	firstCh := make(chan error, 1)
	secondCh := make(chan error, 1)
	s := &Shard{
		blocksDir: t.TempDir(),
		proposals: map[string]chan error{
			rewrapProposalKey("proposal-first", "tx-rewrap", "doc.xml"):  firstCh,
			rewrapProposalKey("proposal-second", "tx-rewrap", "doc.xml"): secondCh,
		},
		uploads: newUploadController(nil, UploadConfig{}, 7, nil, nil, nil),
	}

	err := s.applyRewrapDocumentEnvelopeCommand(&scrapv1.RewrapDocumentEnvelope{
		ProposalId:         "proposal-first",
		TransactionId:      "tx-rewrap",
		DocumentName:       "doc.xml",
		BlockId:            1,
		EncryptionEnvelope: rewrapApplyEnvelope(t, 2),
		OldKeyVersion:      1,
		NewKeyVersion:      2,
		RewrappedAtUs:      1716700002000000,
	}, 11)
	if err == nil {
		t.Fatal("applyRewrapDocumentEnvelopeCommand error = nil, want missing index failure")
	}

	select {
	case <-firstCh:
	default:
		t.Fatal("matching proposal was not notified")
	}
	select {
	case err := <-secondCh:
		t.Fatalf("non-matching proposal was notified: %v", err)
	default:
	}
	if _, ok := s.proposals[rewrapProposalKey("proposal-second", "tx-rewrap", "doc.xml")]; !ok {
		t.Fatal("non-matching proposal was removed")
	}
}

func writeRewrapApplyIndex(t *testing.T, blocksDir string, envelope []byte) {
	t.Helper()

	path := filepath.Join(blocksDir, "0000000000000001.idx")
	iw, err := block.NewIndexWriter(path)
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}
	if err := iw.Append(block.IndexEntry{
		TransactionID:      "tx-rewrap",
		DocName:            "doc.xml",
		ContentType:        "text/xml",
		CreatedAt:          time.UnixMicro(1716700000000000),
		FirstFrameOff:      block.HeaderSize,
		FrameCount:         1,
		TotalBytes:         1,
		SHA256:             [32]byte{1},
		EncryptionEnvelope: envelope,
	}); err != nil {
		t.Fatalf("Append index entry: %v", err)
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("Close index writer: %v", err)
	}
}

func readRewrapApplyEnvelope(t *testing.T, blocksDir string) []byte {
	t.Helper()

	ir, err := block.OpenIndexReader(filepath.Join(blocksDir, "0000000000000001.idx"))
	if err != nil {
		t.Fatalf("OpenIndexReader: %v", err)
	}
	defer func() { _ = ir.Close() }()

	entry, err := ir.Find("tx-rewrap", "doc.xml")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	return entry.EncryptionEnvelope
}

func rewrapApplyEnvelope(t *testing.T, keyVersion int) []byte {
	t.Helper()

	envelope, err := encryption.MarshalEnvelope(encryption.Envelope{
		Version:          encryption.EnvelopeVersion,
		TransitMount:     encryption.DefaultTransitMountPath,
		TransitKey:       encryption.DefaultTransitKeyName,
		KeyVersion:       keyVersion,
		WrappedDataKey:   "wrapped-key",
		PayloadAlgorithm: encryption.PayloadAlgorithmAES256GCM,
		NoncePrefix:      make([]byte, 8),
		PlaintextSHA256:  make([]byte, 32),
		PlaintextLength:  1,
		CiphertextLength: 17,
	})
	if err != nil {
		t.Fatalf("MarshalEnvelope: %v", err)
	}
	return envelope
}
