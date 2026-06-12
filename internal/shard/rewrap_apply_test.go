package shard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
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

func TestApplyRewrapRequeuesAlreadyAppliedReplay(t *testing.T) {
	blocksDir := t.TempDir()
	idx := openApplyTestIndex(t)
	replacementEnvelope := rewrapApplyEnvelope(t, 2)
	writeRewrapApplyIndex(t, blocksDir, replacementEnvelope)
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
	}, 99)
	if err != nil {
		t.Fatalf("apply rewrap replay: %v", err)
	}

	pending, err := idx.GetPendingUpload(1)
	if err != nil {
		t.Fatalf("GetPendingUpload: %v", err)
	}
	if pending.UploadGeneration != 99 {
		t.Fatalf("pending upload generation = %d, want replay entry index", pending.UploadGeneration)
	}
	got := readRewrapApplyEnvelope(t, blocksDir)
	if string(got) != string(replacementEnvelope) {
		t.Fatal("already-applied envelope changed during replay")
	}
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

func TestApplyRewrapReopensCurrentIndexAfterAlreadyAppliedEnvelope(t *testing.T) {
	blocksDir := t.TempDir()
	replacementEnvelope := rewrapApplyEnvelope(t, 2)
	writeRewrapApplyIndex(t, blocksDir, replacementEnvelope)
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
		EncryptionEnvelope: replacementEnvelope,
		OldKeyVersion:      1,
		NewKeyVersion:      2,
		RewrappedAtUs:      1716700002000000,
	}, 89)
	if err != nil {
		t.Fatalf("applyRewrapDocumentEnvelopeCommand: %v", err)
	}
	if s.idxWriter == nil {
		t.Fatal("idxWriter was not reopened after already-applied rewrap")
	}
	t.Cleanup(func() { _ = s.idxWriter.Close() })
	if err := s.idxWriter.Append(block.IndexEntry{
		TransactionID: "tx-after-replay",
		DocName:       "after.xml",
		CreatedAt:     time.UnixMicro(1716700004000000),
		FrameCount:    1,
		SHA256:        [32]byte{2},
	}); err != nil {
		t.Fatalf("Append after already-applied rewrap: %v", err)
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

func TestProposeRewrapDocumentForgetsProposalOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Shard{
		raft:      &evictionApplyRaftStub{leader: true},
		proposals: make(map[string]chan error),
	}
	cancel()

	err := s.proposeRewrapDocument(ctx, 1, rewrap.Request{
		TransactionID: "tx-rewrap",
		DocumentName:  "doc.xml",
	}, rewrap.Result{
		OldKeyVersion: 1,
		NewKeyVersion: 2,
		RewrappedAt:   time.Unix(1716700002, 0),
	}, rewrapApplyEnvelope(t, 2))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("proposeRewrapDocument error = %v, want context canceled", err)
	}
	if len(s.proposals) != 0 {
		t.Fatalf("proposal waiters = %d, want none after cancellation", len(s.proposals))
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
