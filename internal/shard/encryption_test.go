package shard_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/encryption"
	"github.com/petabytecl/scrap/internal/rewrap"
	"github.com/petabytecl/scrap/internal/shard"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

const (
	testTransitMount = "transit"
	testTransitKey   = "scrap-documents"
)

func TestEncryptedShardWriteReadPersistsCiphertextAndEnvelope(t *testing.T) {
	transit := encryption.NewFakeTransit(encryption.FakeConfig{KeyName: testTransitKey})
	s, dataDir := openEncryptedTestShard(t, transit)
	ctx := context.Background()

	content := bytes.Repeat([]byte("plaintext-marker-406:"), 512)
	result, err := s.WriteDocument(ctx, "tx-encrypted", "invoice.xml", "application/xml", "", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}

	rc, meta, err := s.ReadDocument(ctx, "tx-encrypted", "invoice.xml")
	if err != nil {
		t.Fatalf("ReadDocument: %v", err)
	}
	defer func() { _ = rc.Close() }()
	readBack, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(readBack, content) {
		t.Fatalf("read content mismatch")
	}
	if meta.SHA256 != result.SHA256 || meta.Size != int64(len(content)) {
		t.Fatalf("meta = %+v, write result = %+v", meta, result)
	}

	assertBlockOmitsPlaintext(t, dataDir, content)
	entry := readOnlyIndexEntry(t, dataDir, "tx-encrypted", "invoice.xml")
	assertEnvelopeMetadata(t, entry, len(content))
}

func TestEncryptedShardWriteFailsClosedWhenTransitUnavailable(t *testing.T) {
	transit := &mutableTransit{
		delegate:    encryption.NewFakeTransit(encryption.FakeConfig{KeyName: testTransitKey}),
		generateErr: fmt.Errorf("test transit outage: %w", encryption.ErrUnavailable),
	}
	s, dataDir := openEncryptedTestShard(t, transit)

	content := []byte("must not be written as plaintext")
	_, err := s.WriteDocument(context.Background(), "tx-outage", "doc.xml", "text/xml", "", bytes.NewReader(content))
	if !errors.Is(err, storeapi.ErrUnavailable) {
		t.Fatalf("WriteDocument error = %v, want ErrUnavailable", err)
	}
	assertCryptoUnavailableReason(t, err)

	assertBlockOmitsPlaintext(t, dataDir, content)
}

func TestEncryptedShardReadFailsClosedWhenKeyMaterialUnavailable(t *testing.T) {
	tests := []struct {
		name      string
		unwrapErr error
	}{
		{name: "missing key", unwrapErr: fmt.Errorf("missing key: %w", encryption.ErrMissingKey)},
		{name: "auth denied", unwrapErr: fmt.Errorf("auth denied: %w", encryption.ErrAuthDenied)},
		{name: "outage", unwrapErr: fmt.Errorf("outage: %w", encryption.ErrUnavailable)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transit := &mutableTransit{delegate: encryption.NewFakeTransit(encryption.FakeConfig{KeyName: testTransitKey})}
			s, _ := openEncryptedTestShard(t, transit)
			if _, err := s.WriteDocument(context.Background(), "tx-read-fail", "doc.xml", "text/xml", "", bytes.NewReader([]byte("payload"))); err != nil {
				t.Fatalf("WriteDocument: %v", err)
			}

			transit.unwrapErr = tt.unwrapErr
			_, _, err := s.ReadDocument(context.Background(), "tx-read-fail", "doc.xml")
			if !errors.Is(err, storeapi.ErrUnavailable) {
				t.Fatalf("ReadDocument error = %v, want ErrUnavailable", err)
			}
			assertCryptoUnavailableReason(t, err)
		})
	}
}

func TestEncryptedShardReadReportsDataLossOnCiphertextCorruption(t *testing.T) {
	transit := encryption.NewFakeTransit(encryption.FakeConfig{KeyName: testTransitKey})
	s, dataDir := openEncryptedTestShard(t, transit)
	content := bytes.Repeat([]byte("ciphertext-integrity:"), 64)
	if _, err := s.WriteDocument(context.Background(), "tx-corrupt", "doc.xml", "text/xml", "", bytes.NewReader(content)); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}

	entry := readOnlyIndexEntry(t, dataDir, "tx-corrupt", "doc.xml")
	corruptBlockPayload(t, filepath.Join(dataDir, "blocks", "0000000000000001.blk"), entry.FirstFrameOff)

	_, _, err := s.ReadDocument(context.Background(), "tx-corrupt", "doc.xml")
	if !errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("ReadDocument error = %v, want ErrDataLoss", err)
	}
}

func TestEncryptedShardReadMapsInvalidTransitRequestToDataLoss(t *testing.T) {
	transit := &mutableTransit{delegate: encryption.NewFakeTransit(encryption.FakeConfig{KeyName: testTransitKey})}
	s, _ := openEncryptedTestShard(t, transit)
	if _, err := s.WriteDocument(context.Background(), "tx-invalid-transit", "doc.xml", "text/xml", "", bytes.NewReader([]byte("payload"))); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}

	transit.unwrapErr = fmt.Errorf("malformed transit ciphertext: %w", encryption.ErrInvalidRequest)
	_, _, err := s.ReadDocument(context.Background(), "tx-invalid-transit", "doc.xml")
	if !errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("ReadDocument error = %v, want ErrDataLoss", err)
	}
	if errors.Is(err, rewrap.ErrInvalidRequest) {
		t.Fatalf("ReadDocument error = %v, must not expose rewrap invalid request sentinel", err)
	}
}

func TestEncryptedShardRewrapUpdatesEnvelopeWithoutRewritingBlock(t *testing.T) {
	transit := encryption.NewFakeTransit(encryption.FakeConfig{KeyName: testTransitKey})
	s, dataDir := openEncryptedTestShard(t, transit)
	content := bytes.Repeat([]byte("rewrap plaintext:"), 64)
	if _, err := s.WriteDocument(context.Background(), "tx-rewrap", "doc.xml", "text/xml", "", bytes.NewReader(content)); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	beforeBlock := readTestBlockFile(t, dataDir)

	transit.Rotate()
	result, err := s.RewrapDocument(context.Background(), rewrap.Request{
		TransactionID: "tx-rewrap",
		DocumentName:  "doc.xml",
		KeyVersion:    2,
		Reason:        "test",
	})
	if err != nil {
		t.Fatalf("RewrapDocument: %v", err)
	}
	if !result.Changed || result.OldKeyVersion != 1 || result.NewKeyVersion != 2 {
		t.Fatalf("RewrapDocument result = %+v, want changed 1->2", result)
	}
	assertBlockPayloadUnchanged(t, dataDir, beforeBlock)
	assertIndexEnvelopeVersion(t, dataDir, "tx-rewrap", "doc.xml", 2)
	assertRewrappedDocumentReadable(t, s, "tx-rewrap", "doc.xml", content)
	assertRewrapIsIdempotent(t, s, "tx-rewrap", "doc.xml", 2)
}

func TestEncryptedShardRewrapFailureRecordsHealthAndPreservesRead(t *testing.T) {
	baseTransit := encryption.NewFakeTransit(encryption.FakeConfig{KeyName: testTransitKey})
	transit := &mutableTransit{delegate: baseTransit}
	s, _ := openEncryptedTestShard(t, transit)
	content := []byte("readable after failed rewrap")
	if _, err := s.WriteDocument(context.Background(), "tx-rewrap-fail", "doc.xml", "text/xml", "", bytes.NewReader(content)); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}

	baseTransit.Rotate()
	transit.rewrapErr = fmt.Errorf("transit outage: %w", encryption.ErrUnavailable)
	result, err := s.RewrapDocument(context.Background(), rewrap.Request{
		TransactionID: "tx-rewrap-fail",
		DocumentName:  "doc.xml",
		KeyVersion:    2,
		Reason:        "test",
	})
	if err == nil {
		t.Fatal("RewrapDocument error = nil, want unavailable")
	}
	assertCryptoUnavailableReason(t, err)
	if result.Status != rewrap.StatusFailed || result.Reason != rewrap.ReasonCryptoUnavailable || result.Changed {
		t.Fatalf("RewrapDocument result = %+v, want crypto unavailable failure without change", result)
	}

	snapshot := s.RewrapHealthSnapshot()
	if snapshot.Status != rewrap.StatusDegraded || snapshot.LastReason != rewrap.ReasonCryptoUnavailable {
		t.Fatalf("RewrapHealthSnapshot = %+v, want degraded crypto unavailable", snapshot)
	}
	if snapshot.FailuresByReason[rewrap.ReasonCryptoUnavailable] != 1 {
		t.Fatalf("crypto unavailable failures = %d, want 1", snapshot.FailuresByReason[rewrap.ReasonCryptoUnavailable])
	}
	assertRewrappedDocumentReadable(t, s, "tx-rewrap-fail", "doc.xml", content)
}

func TestShardRewrapRejectsInvalidAndUnavailableRequests(t *testing.T) {
	s := openTestShard(t)

	result, err := s.RewrapDocument(context.Background(), rewrap.Request{DocumentName: "doc.xml"})
	if !errors.Is(err, rewrap.ErrInvalidRequest) {
		t.Fatalf("invalid RewrapDocument error = %v, want ErrInvalidRequest", err)
	}
	if result.Reason != rewrap.ReasonInvalidRequest {
		t.Fatalf("invalid RewrapDocument reason = %q, want invalid_request", result.Reason)
	}

	if _, err := s.WriteDocument(context.Background(), "tx-plain", "doc.xml", "text/xml", "", bytes.NewReader([]byte("plain"))); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	result, err = s.RewrapDocument(context.Background(), rewrap.Request{TransactionID: "tx-plain", DocumentName: "doc.xml"})
	if err == nil {
		t.Fatal("RewrapDocument on unencrypted shard error = nil, want unavailable")
	}
	assertCryptoUnavailableReason(t, err)
	if result.Reason != rewrap.ReasonCryptoUnavailable {
		t.Fatalf("unencrypted RewrapDocument reason = %q, want crypto_unavailable", result.Reason)
	}
}

func TestEncryptedShardRewrapMapsInvalidTransitRequest(t *testing.T) {
	transit := encryption.NewFakeTransit(encryption.FakeConfig{KeyName: testTransitKey})
	s, _ := openEncryptedTestShard(t, transit)
	if _, err := s.WriteDocument(context.Background(), "tx-rewrap-invalid", "doc.xml", "text/xml", "", bytes.NewReader([]byte("payload"))); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}

	result, err := s.RewrapDocument(context.Background(), rewrap.Request{
		TransactionID: "tx-rewrap-invalid",
		DocumentName:  "doc.xml",
		KeyVersion:    99,
		Reason:        "test",
	})
	if !errors.Is(err, rewrap.ErrInvalidRequest) {
		t.Fatalf("RewrapDocument error = %v, want ErrInvalidRequest", err)
	}
	if result.Reason != rewrap.ReasonInvalidRequest {
		t.Fatalf("RewrapDocument reason = %q, want invalid_request", result.Reason)
	}
}

func assertBlockPayloadUnchanged(t *testing.T, dataDir string, before []byte) {
	t.Helper()

	if !bytes.Equal(before, readTestBlockFile(t, dataDir)) {
		t.Fatal("rewrap changed Block payload bytes")
	}
}

func assertIndexEnvelopeVersion(t *testing.T, dataDir, txID, docName string, want int) {
	t.Helper()

	entry := readOnlyIndexEntry(t, dataDir, txID, docName)
	envelope, err := encryption.ParseEnvelope(entry.EncryptionEnvelope)
	if err != nil {
		t.Fatalf("ParseEnvelope: %v", err)
	}
	if envelope.KeyVersion != want {
		t.Fatalf("envelope key version = %d, want %d", envelope.KeyVersion, want)
	}
}

func assertRewrappedDocumentReadable(t *testing.T, s *shard.Shard, txID, docName string, want []byte) {
	t.Helper()

	rc, _, err := s.ReadDocument(context.Background(), txID, docName)
	if err != nil {
		t.Fatalf("ReadDocument after rewrap: %v", err)
	}
	defer func() { _ = rc.Close() }()
	readBack, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(readBack, want) {
		t.Fatal("read content mismatch after rewrap")
	}
}

func assertRewrapIsIdempotent(t *testing.T, s *shard.Shard, txID, docName string, keyVersion int) {
	t.Helper()

	idempotent, err := s.RewrapDocument(context.Background(), rewrap.Request{
		TransactionID: txID,
		DocumentName:  docName,
		KeyVersion:    keyVersion,
		Reason:        "test",
	})
	if err != nil {
		t.Fatalf("idempotent RewrapDocument: %v", err)
	}
	if idempotent.Changed {
		t.Fatalf("idempotent RewrapDocument changed envelope: %+v", idempotent)
	}
}

func TestEnvelopeDecryptVerifiesPlaintextSHA256(t *testing.T) {
	transit := encryption.NewFakeTransit(encryption.FakeConfig{KeyName: testTransitKey})
	content := []byte("verify plaintext digest")
	encrypted, err := encryption.EncryptDocument(context.Background(), encryption.DocumentConfig{
		Transit:      transit,
		TransitMount: testTransitMount,
		TransitKey:   testTransitKey,
	}, encryption.DocumentIdentity{TransactionID: "tx-sha", DocumentName: "doc.xml"}, bytes.NewReader(content))
	if err != nil {
		t.Fatalf("EncryptDocument: %v", err)
	}

	wrongSHA := encrypted.PlaintextSHA256
	wrongSHA[0] ^= 0xff
	_, err = encryption.DecryptDocument(context.Background(), transit, encryption.DocumentIdentity{
		TransactionID: "tx-sha",
		DocumentName:  "doc.xml",
	}, encrypted.Envelope, encrypted.Frames, wrongSHA, int64(len(content)))
	if !errors.Is(err, encryption.ErrIntegrity) {
		t.Fatalf("DecryptDocument error = %v, want ErrIntegrity", err)
	}
}

func TestEncryptedReplicationAuthenticatesCiphertext(t *testing.T) {
	transit := encryption.NewFakeTransit(encryption.FakeConfig{KeyName: testTransitKey})
	content := bytes.Repeat([]byte("replicated encrypted payload:"), 8)
	encrypted, err := encryption.EncryptDocument(context.Background(), encryption.DocumentConfig{
		Transit:      transit,
		TransitMount: testTransitMount,
		TransitKey:   testTransitKey,
	}, encryption.DocumentIdentity{TransactionID: "tx-repl-encrypted", DocumentName: "doc.xml"}, bytes.NewReader(content))
	if err != nil {
		t.Fatalf("EncryptDocument: %v", err)
	}

	tamperedCiphertext := flattenFramesForTest(encrypted.Frames)
	tamperedCiphertext[0] ^= 0xff
	s, _ := openEncryptedTestShard(t, transit)

	_, err = s.AppendReplicatedDocument(context.Background(), &scrapv1.ReplicateDocumentInit{
		TransactionId:      "tx-repl-encrypted",
		DocumentName:       "doc.xml",
		ContentType:        "text/xml",
		BlockId:            1,
		StartOffset:        block.HeaderSize,
		FrameCount:         uint32(len(encrypted.Frames)), //nolint:gosec // test-generated frames are bounded by in-memory fixture size.
		TotalBytes:         encrypted.PlaintextSize,
		Sha256:             encrypted.PlaintextSHA256[:],
		EncryptionEnvelope: encrypted.Envelope,
	}, bytes.NewReader(tamperedCiphertext))
	if !errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("AppendReplicatedDocument error = %v, want ErrDataLoss", err)
	}
}

func openEncryptedTestShard(t *testing.T, transit encryption.Transit) (*shard.Shard, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := shard.Open(shard.Config{
		DataDir:      dir,
		ShardID:      0,
		RaftID:       1,
		Peers:        map[uint64]string{1: "localhost:9091"},
		TickInterval: 10 * time.Millisecond,
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
			return s, dir
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("shard did not become leader")
	return nil, ""
}

func readOnlyIndexEntry(t *testing.T, dataDir, txID, docName string) block.IndexEntry {
	t.Helper()
	ir, err := block.OpenIndexReader(filepath.Join(dataDir, "blocks", "0000000000000001.idx"))
	if err != nil {
		t.Fatalf("OpenIndexReader: %v", err)
	}
	defer func() { _ = ir.Close() }()

	entry, err := ir.Find(txID, docName)
	if err != nil {
		t.Fatalf("Find index entry: %v", err)
	}
	return entry
}

func flattenFramesForTest(frames [][]byte) []byte {
	var total int
	for _, frame := range frames {
		total += len(frame)
	}
	out := make([]byte, 0, total)
	for _, frame := range frames {
		out = append(out, frame...)
	}
	return out
}

func assertBlockOmitsPlaintext(t *testing.T, dataDir string, content []byte) {
	t.Helper()
	blockBytes := readTestBlockFile(t, dataDir)
	if bytes.Contains(blockBytes, content) {
		t.Fatal("Block contains plaintext Document bytes")
	}
}

func readTestBlockFile(t *testing.T, dataDir string) []byte {
	t.Helper()
	blockBytes, err := os.ReadFile(filepath.Join(dataDir, "blocks", "0000000000000001.blk")) //nolint:gosec // test path is under t.TempDir.
	if err != nil {
		t.Fatalf("ReadFile block: %v", err)
	}
	return blockBytes
}

func assertEnvelopeMetadata(t *testing.T, entry block.IndexEntry, plaintextLength int) {
	t.Helper()
	if len(entry.EncryptionEnvelope) == 0 {
		t.Fatal("index entry missing encryption envelope")
	}
	envelope, err := encryption.ParseEnvelope(entry.EncryptionEnvelope)
	if err != nil {
		t.Fatalf("ParseEnvelope: %v", err)
	}
	if envelope.TransitMount != testTransitMount || envelope.TransitKey != testTransitKey {
		t.Fatalf("envelope Transit identity = %s/%s", envelope.TransitMount, envelope.TransitKey)
	}
	if envelope.PlaintextLength != int64(plaintextLength) || envelope.CiphertextLength <= int64(plaintextLength) {
		t.Fatalf("envelope lengths = plaintext %d ciphertext %d", envelope.PlaintextLength, envelope.CiphertextLength)
	}
}

func corruptBlockPayload(t *testing.T, path string, firstFrameOffset int64) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR, 0) //nolint:gosec // test path is under t.TempDir.
	if err != nil {
		t.Fatalf("OpenFile block: %v", err)
	}
	defer func() { _ = f.Close() }()

	if _, err := f.Seek(firstFrameOffset+int64(block.FrameHeaderSize), io.SeekStart); err != nil {
		t.Fatalf("Seek payload: %v", err)
	}
	one := []byte{0}
	if _, err := f.Read(one); err != nil {
		t.Fatalf("Read payload byte: %v", err)
	}
	one[0] ^= 0xff
	if _, err := f.Seek(firstFrameOffset+int64(block.FrameHeaderSize), io.SeekStart); err != nil {
		t.Fatalf("Seek payload write: %v", err)
	}
	if _, err := f.Write(one); err != nil {
		t.Fatalf("Write payload byte: %v", err)
	}
	if err := f.Sync(); err != nil {
		t.Fatalf("Sync block: %v", err)
	}
}

func assertCryptoUnavailableReason(t *testing.T, err error) {
	t.Helper()
	reason, ok := storeapi.UnavailableReason(err)
	if !ok || reason != storeapi.UnavailableReasonCryptoUnavailable {
		t.Fatalf("UnavailableReason = %q, %v; want %q, true", reason, ok, storeapi.UnavailableReasonCryptoUnavailable)
	}
}

type mutableTransit struct {
	delegate    encryption.Transit
	generateErr error
	unwrapErr   error
	rewrapErr   error
}

func (t *mutableTransit) GenerateDataKey(ctx context.Context, req encryption.GenerateDataKeyRequest) (encryption.DataKey, error) {
	if t.generateErr != nil {
		return encryption.DataKey{}, t.generateErr
	}
	return t.delegate.GenerateDataKey(ctx, req)
}

func (t *mutableTransit) UnwrapDataKey(ctx context.Context, req encryption.UnwrapDataKeyRequest) (encryption.UnwrappedDataKey, error) {
	if t.unwrapErr != nil {
		return encryption.UnwrappedDataKey{}, t.unwrapErr
	}
	return t.delegate.UnwrapDataKey(ctx, req)
}

func (t *mutableTransit) RewrapDataKey(ctx context.Context, req encryption.RewrapDataKeyRequest) (encryption.RewrappedKey, error) {
	if t.rewrapErr != nil {
		return encryption.RewrappedKey{}, t.rewrapErr
	}
	return t.delegate.RewrapDataKey(ctx, req)
}

func (t *mutableTransit) Readiness(ctx context.Context) (encryption.Readiness, error) {
	return t.delegate.Readiness(ctx)
}
