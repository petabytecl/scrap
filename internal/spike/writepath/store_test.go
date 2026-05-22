package writepath

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestStoreWriteHeadReadRoundTrip(t *testing.T) {
	store := openTestStore(t)

	chunks := [][]byte{
		[]byte("invoice-header\n"),
		bytes.Repeat([]byte("line-item\n"), 128),
		[]byte("invoice-footer\n"),
	}
	data := bytes.Join(chunks, nil)

	result, err := store.WriteDocument(&WriteDocumentMetadata{
		TenantID:       "tenant-a",
		TransactionID:  "tx-001",
		DocumentName:   "bill.xml",
		ContentType:    "application/xml",
		ExpectedLength: int64(len(data)),
	}, chunkSource(chunks))
	if err != nil {
		t.Fatalf("write document: %v", err)
	}

	wantSHA := sha256Hex(data)
	if result.ByteLength != int64(len(data)) {
		t.Fatalf("byte length = %d, want %d", result.ByteLength, len(data))
	}
	if result.LogicalSHA256 != wantSHA {
		t.Fatalf("logical sha = %s, want %s", result.LogicalSHA256, wantSHA)
	}

	head, err := store.HeadDocument(&HeadDocumentRequest{
		TenantID:      "tenant-a",
		TransactionID: "tx-001",
		DocumentName:  "bill.xml",
	})
	if err != nil {
		t.Fatalf("head document: %v", err)
	}
	if head.Document.LogicalSHA256 != wantSHA {
		t.Fatalf("head sha = %s, want %s", head.Document.LogicalSHA256, wantSHA)
	}

	var got bytes.Buffer
	var sawMetadata bool
	err = store.ReadDocument(&ReadDocumentRequest{
		TenantID:      "tenant-a",
		TransactionID: "tx-001",
		DocumentName:  "bill.xml",
	}, func(response *ReadDocumentResponse) error {
		if response.Document != nil {
			sawMetadata = true
			if response.Document.LogicalSHA256 != wantSHA {
				t.Fatalf("read metadata sha = %s, want %s", response.Document.LogicalSHA256, wantSHA)
			}
			return nil
		}
		_, err := got.Write(response.Chunk)
		return err
	})
	if err != nil {
		t.Fatalf("read document: %v", err)
	}
	if !sawMetadata {
		t.Fatal("read did not send metadata before bytes")
	}
	if !bytes.Equal(got.Bytes(), data) {
		t.Fatal("read bytes differ from written bytes")
	}
}

func TestStoreFailedStreamIsNotVisible(t *testing.T) {
	store := openTestStore(t)

	streamErr := errors.New("stream aborted")
	sentChunk := false
	_, err := store.WriteDocument(&WriteDocumentMetadata{
		TenantID:      "tenant-a",
		TransactionID: "tx-002",
		DocumentName:  "partial.xml",
	}, func() ([]byte, error) {
		if !sentChunk {
			sentChunk = true
			return []byte("partial bytes"), nil
		}
		return nil, streamErr
	})
	if !errors.Is(err, streamErr) {
		t.Fatalf("write error = %v, want %v", err, streamErr)
	}

	assertNotFound(t, store, "tenant-a", "tx-002", "partial.xml")
}

func TestStoreHashMismatchIsRejectedAndNotVisible(t *testing.T) {
	store := openTestStore(t)

	_, err := store.WriteDocument(&WriteDocumentMetadata{
		TenantID:          "tenant-a",
		TransactionID:     "tx-003",
		DocumentName:      "bad-hash.xml",
		ExpectedSHA256Hex: "deadbeef",
	}, chunkSource([][]byte{[]byte("valid bytes with wrong expected hash")}))
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("write status = %v, want %v; err=%v", status.Code(err), codes.FailedPrecondition, err)
	}

	assertNotFound(t, store, "tenant-a", "tx-003", "bad-hash.xml")
}

func TestStoreAckedDocumentSurvivesReopenAndNewWritesUseNewBlock(t *testing.T) {
	dir := t.TempDir()
	store := openStoreAt(t, dir)

	firstData := []byte("first committed document")
	first := writeTestDocument(t, store, "tx-004", "first.xml", firstData)
	closeTestStore(t, store)

	reopened := openStoreAt(t, dir)
	assertReadDocument(t, reopened, "tenant-a", "tx-004", "first.xml", firstData)

	secondData := []byte("second committed document after reopen")
	second := writeTestDocument(t, reopened, "tx-004", "second.xml", secondData)
	if second.BlockID == first.BlockID {
		t.Fatalf("reopened store wrote into old block %s", second.BlockID)
	}
	assertReadDocument(t, reopened, "tenant-a", "tx-004", "second.xml", secondData)
}

func TestStoreFailedPreparedWriteStaysInvisibleAfterReopen(t *testing.T) {
	dir := t.TempDir()
	store := openStoreAt(t, dir)

	streamErr := errors.New("stream aborted")
	sentChunk := false
	_, err := store.WriteDocument(&WriteDocumentMetadata{
		TenantID:      "tenant-a",
		TransactionID: "tx-005",
		DocumentName:  "partial-after-reopen.xml",
	}, func() ([]byte, error) {
		if !sentChunk {
			sentChunk = true
			return []byte("prepared but not committed"), nil
		}
		return nil, streamErr
	})
	if !errors.Is(err, streamErr) {
		t.Fatalf("write error = %v, want %v", err, streamErr)
	}
	closeTestStore(t, store)

	reopened := openStoreAt(t, dir)
	assertNotFound(t, reopened, "tenant-a", "tx-005", "partial-after-reopen.xml")
}

func TestStoreTruncatedBlockReturnsDataLossWithoutBytes(t *testing.T) {
	store := openTestStore(t)

	data := bytes.Repeat([]byte("durable bytes\n"), 128)
	result := writeTestDocument(t, store, "tx-006", "truncated.xml", data)
	if err := os.Truncate(filepath.Join(store.blocksDir, result.BlockID+".blk"), result.StoredOffset+result.StoredLength-1); err != nil {
		t.Fatalf("truncate block: %v", err)
	}

	assertReadDataLossWithoutBytes(t, store, "tenant-a", "tx-006", "truncated.xml")
}

func TestStoreCorruptBlockReturnsDataLossWithoutBytes(t *testing.T) {
	store := openTestStore(t)

	data := bytes.Repeat([]byte("verified bytes\n"), 128)
	result := writeTestDocument(t, store, "tx-007", "corrupt.xml", data)
	file, err := os.OpenFile(filepath.Join(store.blocksDir, result.BlockID+".blk"), os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open block for corruption: %v", err)
	}
	if _, err := file.WriteAt([]byte{^data[0]}, result.StoredOffset); err != nil {
		_ = file.Close()
		t.Fatalf("corrupt block: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close corrupted block: %v", err)
	}

	assertReadDataLossWithoutBytes(t, store, "tenant-a", "tx-007", "corrupt.xml")
}

func TestStoreCorruptRangeReadReturnsDataLossWithoutBytes(t *testing.T) {
	store := openTestStore(t)

	data := bytes.Repeat([]byte("range-verified bytes\n"), 256)
	result := writeTestDocument(t, store, "tx-008", "corrupt-range.xml", data)
	rangeOffset := int64(len(data) / 2)
	file, err := os.OpenFile(filepath.Join(store.blocksDir, result.BlockID+".blk"), os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open block for corruption: %v", err)
	}
	if _, err := file.WriteAt([]byte{^data[rangeOffset]}, result.StoredOffset+rangeOffset); err != nil {
		_ = file.Close()
		t.Fatalf("corrupt block: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close corrupted block: %v", err)
	}

	assertReadDataLossWithoutBytesForRange(t, store, "tenant-a", "tx-008", "corrupt-range.xml", rangeOffset, 128)
}

func TestStoreFrameChecksAllowCleanRangeBeforeLaterCorruption(t *testing.T) {
	store := openTestStore(t)

	data := bytes.Repeat([]byte{0x42}, defaultFrameSize+4096)
	result := writeTestDocument(t, store, "tx-009", "multi-frame.xml", data)
	corruptOffset := int64(defaultFrameSize + 128)
	file, err := os.OpenFile(filepath.Join(store.blocksDir, result.BlockID+".blk"), os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open block for corruption: %v", err)
	}
	if _, err := file.WriteAt([]byte{0x99}, result.StoredOffset+corruptOffset); err != nil {
		_ = file.Close()
		t.Fatalf("corrupt block: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close corrupted block: %v", err)
	}

	assertReadDocumentRange(t, store, "tenant-a", "tx-009", "multi-frame.xml", 0, 512, data[:512])
	assertReadDataLossWithoutBytes(t, store, "tenant-a", "tx-009", "multi-frame.xml")
}

func TestStoreCrashAfterBlockSyncLeavesDocumentInvisibleAfterReopen(t *testing.T) {
	dir := t.TempDir()
	crashErr := errors.New("crash after block sync")
	store := openStoreAtWithOptions(t, dir, StoreOptions{
		Faults: StoreFaults{
			AfterBlockSync: func(DocumentRecord) error {
				return crashErr
			},
		},
	})

	_, err := store.WriteDocument(&WriteDocumentMetadata{
		TenantID:       "tenant-a",
		TransactionID:  "tx-010",
		DocumentName:   "after-block-sync.xml",
		ExpectedLength: 3,
	}, chunkSource([][]byte{[]byte("ack")}))
	if !errors.Is(err, crashErr) {
		t.Fatalf("write error = %v, want %v", err, crashErr)
	}
	closeTestStore(t, store)

	reopened := openStoreAt(t, dir)
	assertNotFound(t, reopened, "tenant-a", "tx-010", "after-block-sync.xml")
}

func TestStoreCrashAfterOpenLogSyncLeavesDocumentInvisibleAfterReopen(t *testing.T) {
	dir := t.TempDir()
	crashErr := errors.New("crash after openlog sync")
	store := openStoreAtWithOptions(t, dir, StoreOptions{
		Faults: StoreFaults{
			AfterOpenLogSync: func(DocumentRecord) error {
				return crashErr
			},
		},
	})

	_, err := store.WriteDocument(&WriteDocumentMetadata{
		TenantID:       "tenant-a",
		TransactionID:  "tx-011",
		DocumentName:   "after-openlog-sync.xml",
		ExpectedLength: 3,
	}, chunkSource([][]byte{[]byte("ack")}))
	if !errors.Is(err, crashErr) {
		t.Fatalf("write error = %v, want %v", err, crashErr)
	}
	closeTestStore(t, store)

	reopened := openStoreAt(t, dir)
	assertNotFound(t, reopened, "tenant-a", "tx-011", "after-openlog-sync.xml")
}

func TestStorePostCommitFailureStillAcksAndSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	store := openStoreAtWithOptions(t, dir, StoreOptions{
		Faults: StoreFaults{
			AfterMetadataCommit: func(DocumentRecord) error {
				return errors.New("post-commit bookkeeping failed")
			},
		},
	})

	data := []byte("acked after commit")
	result, err := store.WriteDocument(&WriteDocumentMetadata{
		TenantID:       "tenant-a",
		TransactionID:  "tx-012",
		DocumentName:   "after-commit.xml",
		ExpectedLength: int64(len(data)),
	}, chunkSource([][]byte{data}))
	if err != nil {
		t.Fatalf("post-commit failure should not fail ACK: %v", err)
	}
	if result.LogicalSHA256 != sha256Hex(data) {
		t.Fatalf("logical sha = %s, want %s", result.LogicalSHA256, sha256Hex(data))
	}
	closeTestStore(t, store)

	reopened := openStoreAt(t, dir)
	assertReadDocument(t, reopened, "tenant-a", "tx-012", "after-commit.xml", data)
}

func TestStorePeerPrepareSeesCommittedBytesBeforeAck(t *testing.T) {
	dir := t.TempDir()
	peer := &recordingPeerPreparer{}
	store := openStoreAtWithOptions(t, dir, StoreOptions{
		RequiredPeerPrepares: 1,
		PeerPreparers:        []PeerPreparer{peer},
	})

	data := []byte("replicated before ack")
	result := writeTestDocument(t, store, "tx-013", "peer-prepared.xml", data)
	if peer.calls != 1 {
		t.Fatalf("peer prepare calls = %d, want 1", peer.calls)
	}
	if peer.logicalSHA256 != sha256Hex(data) {
		t.Fatalf("peer saw sha = %s, want %s", peer.logicalSHA256, sha256Hex(data))
	}
	if peer.record.BlockID != result.BlockID || peer.record.StoredOffset != result.StoredOffset {
		t.Fatalf("peer saw record %+v, want block %s offset %d", peer.record, result.BlockID, result.StoredOffset)
	}
}

func TestStorePeerPrepareFailureLeavesDocumentInvisibleAfterReopen(t *testing.T) {
	dir := t.TempDir()
	prepareErr := errors.New("peer prepare failed")
	store := openStoreAtWithOptions(t, dir, StoreOptions{
		RequiredPeerPrepares: 1,
		PeerPreparers: []PeerPreparer{
			&recordingPeerPreparer{err: prepareErr},
		},
	})

	_, err := store.WriteDocument(&WriteDocumentMetadata{
		TenantID:       "tenant-a",
		TransactionID:  "tx-014",
		DocumentName:   "peer-prepare-failed.xml",
		ExpectedLength: 3,
	}, chunkSource([][]byte{[]byte("ack")}))
	if !errors.Is(err, prepareErr) {
		t.Fatalf("write error = %v, want %v", err, prepareErr)
	}
	closeTestStore(t, store)

	reopened := openStoreAt(t, dir)
	assertNotFound(t, reopened, "tenant-a", "tx-014", "peer-prepare-failed.xml")
}

func openTestStore(t *testing.T) *Store {
	t.Helper()

	return openStoreAt(t, t.TempDir())
}

func openStoreAt(t *testing.T, dir string) *Store {
	t.Helper()

	return openStoreAtWithOptions(t, dir, StoreOptions{})
}

func openStoreAtWithOptions(t *testing.T, dir string, options StoreOptions) *Store {
	t.Helper()

	store, err := OpenStoreWithOptions(dir, options)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if store.blockFile != nil || store.db != nil {
			closeTestStore(t, store)
		}
	})
	return store
}

func closeTestStore(t *testing.T, store *Store) {
	t.Helper()

	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
}

func writeTestDocument(t *testing.T, store *Store, transactionID, documentName string, data []byte) *WriteDocumentResult {
	t.Helper()

	result, err := store.WriteDocument(&WriteDocumentMetadata{
		TenantID:       "tenant-a",
		TransactionID:  transactionID,
		DocumentName:   documentName,
		ExpectedLength: int64(len(data)),
	}, chunkSource([][]byte{data}))
	if err != nil {
		t.Fatalf("write document: %v", err)
	}
	return result
}

func chunkSource(chunks [][]byte) func() ([]byte, error) {
	next := 0
	return func() ([]byte, error) {
		if next >= len(chunks) {
			return nil, io.EOF
		}
		chunk := chunks[next]
		next++
		return chunk, nil
	}
}

func assertReadDocument(t *testing.T, store *Store, tenantID, transactionID, documentName string, want []byte) {
	t.Helper()

	assertReadDocumentRange(t, store, tenantID, transactionID, documentName, 0, 0, want)
}

func assertReadDocumentRange(t *testing.T, store *Store, tenantID, transactionID, documentName string, offset, length int64, want []byte) {
	t.Helper()

	var got bytes.Buffer
	err := store.ReadDocument(&ReadDocumentRequest{
		TenantID:      tenantID,
		TransactionID: transactionID,
		DocumentName:  documentName,
		Offset:        offset,
		Length:        length,
	}, func(response *ReadDocumentResponse) error {
		if response.Document != nil {
			return nil
		}
		_, err := got.Write(response.Chunk)
		return err
	})
	if err != nil {
		t.Fatalf("read document: %v", err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatal("read bytes differ from written bytes")
	}
}

func assertReadDataLossWithoutBytes(t *testing.T, store *Store, tenantID, transactionID, documentName string) {
	t.Helper()

	assertReadDataLossWithoutBytesForRange(t, store, tenantID, transactionID, documentName, 0, 0)
}

func assertReadDataLossWithoutBytesForRange(t *testing.T, store *Store, tenantID, transactionID, documentName string, offset, length int64) {
	t.Helper()

	var chunks int
	err := store.ReadDocument(&ReadDocumentRequest{
		TenantID:      tenantID,
		TransactionID: transactionID,
		DocumentName:  documentName,
		Offset:        offset,
		Length:        length,
	}, func(response *ReadDocumentResponse) error {
		if response.Document != nil {
			return nil
		}
		chunks++
		return nil
	})
	if status.Code(err) != codes.DataLoss {
		t.Fatalf("read status = %v, want %v; err=%v", status.Code(err), codes.DataLoss, err)
	}
	if chunks != 0 {
		t.Fatalf("read sent %d byte chunks before reporting data loss", chunks)
	}
}

func assertNotFound(t *testing.T, store *Store, tenantID, transactionID, documentName string) {
	t.Helper()

	_, err := store.HeadDocument(&HeadDocumentRequest{
		TenantID:      tenantID,
		TransactionID: transactionID,
		DocumentName:  documentName,
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("head status = %v, want %v; err=%v", status.Code(err), codes.NotFound, err)
	}

	err = store.ReadDocument(&ReadDocumentRequest{
		TenantID:      tenantID,
		TransactionID: transactionID,
		DocumentName:  documentName,
	}, func(*ReadDocumentResponse) error {
		t.Fatal("read sent a response for an invisible document")
		return nil
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("read status = %v, want %v; err=%v", status.Code(err), codes.NotFound, err)
	}
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type recordingPeerPreparer struct {
	err           error
	calls         int
	record        DocumentRecord
	logicalSHA256 string
}

func (p *recordingPeerPreparer) PrepareDocument(record DocumentRecord, reader io.Reader) error {
	p.calls++
	p.record = record
	hasher := sha256.New()
	if _, err := io.Copy(hasher, reader); err != nil {
		return err
	}
	p.logicalSHA256 = hex.EncodeToString(hasher.Sum(nil))
	return p.err
}
