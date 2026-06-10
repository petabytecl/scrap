package shard

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/encryption"
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
	})
	if !errors.Is(err, rewrap.ErrStaleEnvelope) {
		t.Fatalf("applyRewrapDocumentEnvelope error = %v, want ErrStaleEnvelope", err)
	}

	got := readRewrapApplyEnvelope(t, blocksDir)
	if string(got) != string(currentEnvelope) {
		t.Fatalf("stored envelope was replaced by stale command")
	}
}

func TestApplyRewrapRejectsMissingHistoricalBlockBeforeReplacingIndex(t *testing.T) {
	blocksDir := t.TempDir()
	currentEnvelope := rewrapApplyEnvelope(t, 1)
	replacementEnvelope := rewrapApplyEnvelope(t, 2)
	writeRewrapApplyIndex(t, blocksDir, currentEnvelope)

	s := &Shard{
		blocksDir: blocksDir,
		upload:    UploadConfig{Enabled: true},
		uploads:   newUploadController(nil, UploadConfig{}, 7, nil, nil, nil),
	}
	err := s.applyRewrapDocumentEnvelope(&scrapv1.RewrapDocumentEnvelope{
		TransactionId:      "tx-rewrap",
		DocumentName:       "doc.xml",
		BlockId:            1,
		EncryptionEnvelope: replacementEnvelope,
		OldKeyVersion:      1,
		NewKeyVersion:      2,
		RewrappedAtUs:      1716700002000000,
	})
	if err == nil {
		t.Fatal("applyRewrapDocumentEnvelope error = nil, want missing Block failure")
	}

	got := readRewrapApplyEnvelope(t, blocksDir)
	if string(got) != string(currentEnvelope) {
		t.Fatal("stored envelope was replaced even though upload requeue could not read the Block")
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
	})
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
