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
	_, err = s.HeadDocument(context.Background(), "tx-outage", "doc.xml")
	if !isMissingDocumentOrTransaction(err) {
		t.Fatalf("HeadDocument after failed write = %v, want not found", err)
	}
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

func TestEncryptedShardReadFailsClosedWhenShardEncryptionDisabled(t *testing.T) {
	transit := encryption.NewFakeTransit(encryption.FakeConfig{KeyName: testTransitKey})
	dataDir := t.TempDir()
	encrypted := openEncryptedTestShardInDir(t, dataDir, transit)
	encryptedClosed := false
	defer func() {
		if !encryptedClosed {
			_ = encrypted.Close()
		}
	}()

	content := []byte("disabled encryption must not return plaintext")
	if _, err := encrypted.WriteDocument(context.Background(), "tx-disabled-encryption", "doc.xml", "text/xml", "", bytes.NewReader(content)); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	entry := readOnlyIndexEntry(t, dataDir, "tx-disabled-encryption", "doc.xml")
	assertEnvelopeMetadata(t, entry, len(content))

	if err := encrypted.Close(); err != nil {
		encryptedClosed = true
		t.Fatalf("Close encrypted shard: %v", err)
	}
	encryptedClosed = true

	plain := openPlainTestShardInDir(t, dataDir)
	defer func() { _ = plain.Close() }()
	rc, meta, err := plain.ReadDocument(context.Background(), "tx-disabled-encryption", "doc.xml")
	if err == nil {
		if rc != nil {
			_ = rc.Close()
		}
		t.Fatal("ReadDocument error = nil, want crypto unavailable")
	}
	if rc != nil {
		_ = rc.Close()
		t.Fatal("ReadDocument returned reader for encrypted Document without Shard encryption")
	}
	if meta != (storeapi.DocumentMeta{}) {
		t.Fatalf("ReadDocument meta = %+v, want zero value", meta)
	}
	if !errors.Is(err, storeapi.ErrUnavailable) {
		t.Fatalf("ReadDocument error = %v, want ErrUnavailable", err)
	}
	assertCryptoUnavailableReason(t, err)
	assertBlockOmitsPlaintext(t, dataDir, content)
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
	docName := "doc.xml"
	content := bytes.Repeat([]byte("rewrap plaintext:"), 64)
	if _, err := s.WriteDocument(context.Background(), "tx-rewrap", docName, "text/xml", "", bytes.NewReader(content)); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	beforeBlock := readTestBlockFile(t, dataDir)

	transit.Rotate()
	result, err := s.RewrapDocument(context.Background(), rewrap.Request{
		TransactionID: "tx-rewrap",
		DocumentName:  docName,
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
	assertIndexEnvelopeVersion(t, dataDir, "tx-rewrap", docName, 2)
	assertRewrappedDocumentReadable(t, s, "tx-rewrap", docName, content)
	assertRewrapIsIdempotent(t, s, "tx-rewrap", docName, 2)
}

func TestEncryptedShardRewrapConvergesAcrossMembersWithoutRewritingBlocks(t *testing.T) {
	ctx := context.Background()
	transit := encryption.NewFakeTransit(encryption.FakeConfig{KeyName: testTransitKey})
	replicator := newWriteAckReplicator()
	cluster := openEncryptedWriteAckCluster(t, transit, replicator)
	leader := cluster.waitForLeader(t)

	docName := "cluster-doc.xml"
	content := bytes.Repeat([]byte("cluster rewrap plaintext:"), 32)
	if _, err := leader.WriteDocument(ctx, "tx-rewrap-cluster", docName, "text/xml", "", bytes.NewReader(content)); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	beforeBlocks := readClusterBlockFiles(t, cluster)

	transit.Rotate()
	result, err := leader.RewrapDocument(ctx, rewrap.Request{
		TransactionID: "tx-rewrap-cluster",
		DocumentName:  docName,
		KeyVersion:    2,
		Reason:        "test",
	})
	if err != nil {
		t.Fatalf("RewrapDocument: %v", err)
	}
	if !result.Changed || result.OldKeyVersion != 1 || result.NewKeyVersion != 2 {
		t.Fatalf("RewrapDocument result = %+v, want changed 1->2", result)
	}

	var wantEnvelope []byte
	for i, member := range cluster.shards {
		gotEnvelope := waitForMemberEnvelope(t, member, "tx-rewrap-cluster", docName, 2)
		if wantEnvelope == nil {
			wantEnvelope = gotEnvelope
		}
		if !bytes.Equal(gotEnvelope, wantEnvelope) {
			t.Fatalf("member %d envelope metadata diverged from committed replacement", i+1)
		}
		assertBlockPayloadUnchanged(t, member.DataDirForTest(), beforeBlocks[i])
	}
	assertRewrappedDocumentReadable(t, leader, "tx-rewrap-cluster", docName, content)

	if err := leader.Close(); err != nil {
		t.Fatalf("Close original leader: %v", err)
	}
	cluster.removeMemberForTest(leader)
	replacement := cluster.waitForLeader(t)
	assertRewrappedDocumentReadable(t, replacement, "tx-rewrap-cluster", docName, content)
}

func TestEncryptedShardRewrapFailureRecordsHealthAndPreservesRead(t *testing.T) {
	baseTransit := encryption.NewFakeTransit(encryption.FakeConfig{KeyName: testTransitKey})
	transit := &mutableTransit{delegate: baseTransit}
	s, _ := openEncryptedTestShard(t, transit)
	docName := "doc.xml"
	content := []byte("readable after failed rewrap")
	if _, err := s.WriteDocument(context.Background(), "tx-rewrap-fail", docName, "text/xml", "", bytes.NewReader(content)); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}

	baseTransit.Rotate()
	transit.rewrapErr = fmt.Errorf("transit outage: %w", encryption.ErrUnavailable)
	result, err := s.RewrapDocument(context.Background(), rewrap.Request{
		TransactionID: "tx-rewrap-fail",
		DocumentName:  docName,
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
	assertRewrappedDocumentReadable(t, s, "tx-rewrap-fail", docName, content)
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
	s := openEncryptedTestShardInDir(t, dir, transit)
	t.Cleanup(func() { _ = s.Close() })
	return s, dir
}

func openEncryptedTestShardInDir(t *testing.T, dir string, transit encryption.Transit) *shard.Shard {
	t.Helper()
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
	returnAfterLeader := false
	defer func() {
		if !returnAfterLeader {
			_ = s.Close()
		}
	}()
	waitForLeader(t, s)
	returnAfterLeader = true
	return s
}

func openEncryptedWriteAckCluster(t *testing.T, transit encryption.Transit, replicator *writeAckReplicator) *shardCluster {
	t.Helper()

	transport := newShardTransport()
	peers := map[uint64]string{
		1: "localhost:9091",
		2: "localhost:9092",
		3: "localhost:9093",
	}
	cluster := &shardCluster{transport: transport}
	for i := range 3 {
		id := uint64(i + 1)
		s, err := shard.Open(shard.Config{
			DataDir:      t.TempDir(),
			ShardID:      0,
			RaftID:       id,
			Peers:        peers,
			TickInterval: 10 * time.Millisecond,
			Transport:    transport,
			Replicator:   replicator,
			Encryption: shard.EncryptionConfig{
				Transit:      transit,
				TransitMount: testTransitMount,
				TransitKey:   testTransitKey,
			},
		})
		if err != nil {
			t.Fatalf("Open shard %d: %v", id, err)
		}
		cluster.shards = append(cluster.shards, s)
		transport.Register(id, s)
		replicator.Register(peers[id], s)
	}
	t.Cleanup(func() {
		for _, s := range cluster.shards {
			_ = s.Close()
		}
	})
	return cluster
}

func openPlainTestShardInDir(t *testing.T, dir string) *shard.Shard {
	t.Helper()
	s, err := shard.Open(shard.Config{
		DataDir:      dir,
		ShardID:      0,
		RaftID:       1,
		Peers:        map[uint64]string{1: "localhost:9091"},
		TickInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	returnAfterLeader := false
	defer func() {
		if !returnAfterLeader {
			_ = s.Close()
		}
	}()
	waitForLeader(t, s)
	returnAfterLeader = true
	return s
}

func readClusterBlockFiles(t *testing.T, cluster *shardCluster) [][]byte {
	t.Helper()

	blocks := make([][]byte, 0, len(cluster.shards))
	for _, member := range cluster.shards {
		blocks = append(blocks, readTestBlockFile(t, member.DataDirForTest()))
	}
	return blocks
}

func (sc *shardCluster) removeMemberForTest(member *shard.Shard) {
	remaining := sc.shards[:0]
	for _, s := range sc.shards {
		if s != member {
			remaining = append(remaining, s)
		}
	}
	sc.shards = remaining
}

func waitForMemberEnvelope(t *testing.T, s *shard.Shard, txID, docName string, wantKeyVersion int) []byte {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		envelopeBytes, version, err := readEnvelopeMetadata(s.DataDirForTest(), txID, docName)
		if err != nil {
			lastErr = err
			time.Sleep(10 * time.Millisecond)
			continue
		}
		if version != wantKeyVersion {
			lastErr = fmt.Errorf("envelope key version = %d, want %d", version, wantKeyVersion)
			time.Sleep(10 * time.Millisecond)
			continue
		}
		return envelopeBytes
	}
	t.Fatalf("timed out waiting for rewrapped member envelope: %v", lastErr)
	return nil
}

func readEnvelopeMetadata(dataDir, txID, docName string) ([]byte, int, error) {
	entry, err := findIndexEntry(dataDir, txID, docName)
	if err != nil {
		return nil, 0, err
	}
	envelope, err := encryption.ParseEnvelope(entry.EncryptionEnvelope)
	if err != nil {
		return nil, 0, err
	}
	return append([]byte(nil), entry.EncryptionEnvelope...), envelope.KeyVersion, nil
}

func readOnlyIndexEntry(t *testing.T, dataDir, txID, docName string) block.IndexEntry {
	t.Helper()
	entry, err := findIndexEntry(dataDir, txID, docName)
	if err != nil {
		t.Fatalf("Find index entry: %v", err)
	}
	return entry
}

func findIndexEntry(dataDir, txID, docName string) (block.IndexEntry, error) {
	idxFiles, err := filepath.Glob(filepath.Join(dataDir, "blocks", "*.idx"))
	if err != nil {
		return block.IndexEntry{}, fmt.Errorf("list Block indexes: %w", err)
	}
	for _, idxPath := range idxFiles {
		ir, err := block.OpenIndexReader(idxPath)
		if err != nil {
			return block.IndexEntry{}, err
		}
		entry, findErr := ir.Find(txID, docName)
		closeErr := ir.Close()
		if findErr == nil && closeErr == nil {
			return entry, nil
		}
		if closeErr != nil {
			return block.IndexEntry{}, closeErr
		}
		if !errors.Is(findErr, block.ErrDocNotFound) {
			return block.IndexEntry{}, findErr
		}
	}
	return block.IndexEntry{}, fmt.Errorf("index entry not found for %s/%s", txID, docName)
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
