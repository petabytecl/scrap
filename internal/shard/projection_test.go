package shard

import (
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/index"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func TestApplyCommitDocumentToleratesProjectionAheadOfBlockIndex(t *testing.T) {
	dir := t.TempDir()
	idx, err := index.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })

	iw, err := block.NewIndexWriter(block.IdxFilePath(dir, 1))
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("Close empty index: %v", err)
	}
	if err := idx.Put("tx-replay", 1, 1, false); err != nil {
		t.Fatalf("Put projection entry: %v", err)
	}

	s := &Shard{
		blocksDir: dir,
		idx:       idx,
	}

	applyProjectionCommit(t, s, newProjectionCommit("tx-replay", "doc.xml"))
	requireProjectionDocCount(t, idx, "tx-replay", 1)
	requireProjectionDocs(t, s, "tx-replay", "doc.xml")
}

func TestApplyCommitDocumentWritesHistoricalBlockIndex(t *testing.T) {
	dir := t.TempDir()
	idx, err := index.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })

	iw, err := block.NewIndexWriter(block.IdxFilePath(dir, 1))
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("Close empty index: %v", err)
	}

	s := &Shard{
		blocksDir: dir,
		idx:       idx,
	}

	err = s.applyCommitDocument(&scrapv1.CommitDocument{
		TransactionId: "tx-historical",
		DocumentName:  "doc.xml",
		ContentType:   "text/xml",
		BlockId:       1,
		FrameCount:    1,
		TotalBytes:    4,
		Sha256:        make([]byte, 32),
		CreatedAtUs:   time.Now().UnixMicro(),
	}, 0)
	if err != nil {
		t.Fatalf("applyCommitDocument: %v", err)
	}

	resolved, err := s.projectionResolver().ResolveDocument("tx-historical", "doc.xml")
	if err != nil {
		t.Fatalf("ResolveDocument: %v", err)
	}
	if resolved.BlockID != 1 {
		t.Fatalf("BlockID = %d, want 1", resolved.BlockID)
	}
}

func TestApplyCommitDocumentRequeuesHistoricalUploadAfterIndexAppend(t *testing.T) {
	dir := t.TempDir()
	idx := openProjectionTestIndex(t)
	writeEmptyBlockAndIndex(t, dir, 7, 1)

	s := &Shard{
		blocksDir: dir,
		shardID:   7,
		idx:       idx,
		upload:    UploadConfig{Enabled: true},
	}
	s.uploads = newUploadController(s, s.upload, s.shardID, nil, nil, nil)

	err := s.applyCommitDocument(&scrapv1.CommitDocument{
		TransactionId: "tx-requeue",
		DocumentName:  "doc.xml",
		ContentType:   "text/xml",
		BlockId:       1,
		FrameCount:    1,
		TotalBytes:    4,
		Sha256:        make([]byte, 32),
		CreatedAtUs:   time.Now().UnixMicro(),
	}, 0)
	if err != nil {
		t.Fatalf("applyCommitDocument: %v", err)
	}

	requirePendingUpload(t, idx, 7, 1)
}

func TestApplyCommitDocumentRequeuesHistoricalUploadWithEntryIndexGeneration(t *testing.T) {
	dir := t.TempDir()
	idx := openProjectionTestIndex(t)
	writeEmptyBlockAndIndex(t, dir, 7, 1)

	s := &Shard{
		blocksDir: dir,
		shardID:   7,
		idx:       idx,
		upload:    UploadConfig{Enabled: true},
	}
	s.uploads = newUploadController(s, s.upload, s.shardID, nil, nil, nil)

	if err := s.applyEntryCommand(&scrapv1.RaftCommand{
		Command: &scrapv1.RaftCommand_CommitDoc{
			CommitDoc: &scrapv1.CommitDocument{
				TransactionId: "tx-requeue-entry",
				DocumentName:  "doc.xml",
				ContentType:   "text/xml",
				BlockId:       1,
				FrameCount:    1,
				TotalBytes:    4,
				Sha256:        make([]byte, 32),
				CreatedAtUs:   1716700001000000,
			},
		},
	}, 88); err != nil {
		t.Fatalf("applyEntryCommand: %v", err)
	}

	pending := requirePendingUpload(t, idx, 7, 1)
	if pending.UploadGeneration != 88 || pending.SealedAtUs != 88 {
		t.Fatalf("pending generation = %d/%d, want apply entry index", pending.UploadGeneration, pending.SealedAtUs)
	}
}

func TestApplyCommitDocumentRequeuesHistoricalUploadWhenIndexEntryAlreadyPresent(t *testing.T) {
	dir := t.TempDir()
	idx := openProjectionTestIndex(t)
	writeEmptyBlockAndIndex(t, dir, 7, 1)
	writeProjectionIndexEntries(t, dir, 1,
		block.IndexEntry{TransactionID: "tx-present", DocName: "doc.xml", ContentType: "text/xml", TotalBytes: 4},
	)
	if err := idx.Put("tx-present", 1, 0, false); err != nil {
		t.Fatalf("Put projection entry: %v", err)
	}

	s := &Shard{
		blocksDir: dir,
		shardID:   7,
		idx:       idx,
		upload:    UploadConfig{Enabled: true},
	}
	s.uploads = newUploadController(s, s.upload, s.shardID, nil, nil, nil)

	applyProjectionCommit(t, s, newProjectionCommit("tx-present", "doc.xml"))
	requirePendingUpload(t, idx, 7, 1)
	requireProjectionDocCount(t, idx, "tx-present", 1)
}

func TestApplyCommitDocumentRepairsProjectionAfterIndexAppendCrash(t *testing.T) {
	dir := t.TempDir()
	idx := openProjectionTestIndex(t)
	writeProjectionIndexEntries(t, dir, 1,
		block.IndexEntry{TransactionID: "tx-crash", DocName: "a.xml"},
		block.IndexEntry{TransactionID: "tx-crash", DocName: "b.xml", ContentType: "text/xml", TotalBytes: 4},
	)
	if err := idx.Put("tx-crash", 1, 1, false); err != nil {
		t.Fatalf("Put projection entry: %v", err)
	}

	s := &Shard{
		blocksDir: dir,
		idx:       idx,
	}

	applyProjectionCommit(t, s, newProjectionCommit("tx-crash", "b.xml"))
	requireProjectionDocCount(t, idx, "tx-crash", 2)
	requireProjectionDocs(t, s, "tx-crash", "a.xml", "b.xml")
}

func TestApplyCommitDocumentRepairsTornHistoricalIndexTail(t *testing.T) {
	dir := t.TempDir()
	idx := openProjectionTestIndex(t)
	writeProjectionIndexEntries(t, dir, 1,
		block.IndexEntry{TransactionID: "tx-torn", DocName: "a.xml"},
	)
	appendRawProjectionIndexTail(t, block.IdxFilePath(dir, 1), []byte{0x03, 0x00})
	if err := idx.Put("tx-torn", 1, 1, false); err != nil {
		t.Fatalf("Put projection entry: %v", err)
	}

	s := &Shard{
		blocksDir: dir,
		idx:       idx,
	}

	applyProjectionCommit(t, s, newProjectionCommit("tx-torn", "b.xml"))
	requireProjectionDocCount(t, idx, "tx-torn", 2)
	requireProjectionDocs(t, s, "tx-torn", "a.xml", "b.xml")
}

func openProjectionTestIndex(t *testing.T) *index.Index {
	t.Helper()
	idx, err := index.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	return idx
}

func requirePendingUpload(t *testing.T, idx *index.Index, shardID, blockID uint64) index.PendingUpload {
	t.Helper()
	uploads, err := collectPendingUploads(idx)
	if err != nil {
		t.Fatalf("collectPendingUploads: %v", err)
	}
	if len(uploads) != 1 {
		t.Fatalf("pending uploads = %d, want 1", len(uploads))
	}
	if uploads[0].BlockID != blockID || uploads[0].ShardID != shardID {
		t.Fatalf("pending upload = %+v, want block %d shard %d", uploads[0], blockID, shardID)
	}
	if uploads[0].SealedSizeBytes <= 0 {
		t.Fatalf("SealedSizeBytes = %d, want > 0", uploads[0].SealedSizeBytes)
	}
	return uploads[0]
}

func writeEmptyBlockAndIndex(t *testing.T, dir string, shardID, blockID uint64) {
	t.Helper()
	blockWriter, err := block.NewWriter(block.FilePath(dir, blockID), shardID, blockID)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := blockWriter.Close(); err != nil {
		t.Fatalf("Close block writer: %v", err)
	}
	iw, err := block.NewIndexWriter(block.IdxFilePath(dir, blockID))
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("Close empty index: %v", err)
	}
}

func writeProjectionIndexEntries(t *testing.T, dir string, blockID uint64, entries ...block.IndexEntry) {
	t.Helper()
	iw, err := block.NewIndexWriter(block.IdxFilePath(dir, blockID))
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}
	for _, entry := range entries {
		if err := iw.Append(entry); err != nil {
			t.Fatalf("Append index entry: %v", err)
		}
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("Close index: %v", err)
	}
}

func appendRawProjectionIndexTail(t *testing.T, path string, tail []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0) //nolint:gosec // test file path from temp dir
	if err != nil {
		t.Fatalf("OpenFile append: %v", err)
	}
	if _, err := f.Write(tail); err != nil {
		_ = f.Close()
		t.Fatalf("Write torn tail: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close torn tail: %v", err)
	}
}

func newProjectionCommit(txID, docName string) *scrapv1.CommitDocument {
	return &scrapv1.CommitDocument{
		TransactionId: txID,
		DocumentName:  docName,
		ContentType:   "text/xml",
		BlockId:       1,
		FrameCount:    1,
		TotalBytes:    4,
		Sha256:        make([]byte, 32),
		CreatedAtUs:   time.Now().UnixMicro(),
	}
}

func applyProjectionCommit(t *testing.T, s *Shard, doc *scrapv1.CommitDocument) {
	t.Helper()
	if err := s.applyCommitDocument(doc, 0); err != nil {
		t.Fatalf("applyCommitDocument: %v", err)
	}
}

func requireProjectionDocCount(t *testing.T, idx *index.Index, txID string, want uint16) {
	t.Helper()
	entry, err := idx.Get(txID)
	if err != nil {
		t.Fatalf("Get projection entry: %v", err)
	}
	if entry.DocCount != want {
		t.Fatalf("DocCount = %d, want %d", entry.DocCount, want)
	}
}

func requireProjectionDocs(t *testing.T, s *Shard, txID string, want ...string) {
	t.Helper()
	docs, err := s.projectionResolver().ListDocuments(txID)
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(docs) != len(want) {
		t.Fatalf("doc count = %d, want %d: %+v", len(docs), len(want), docs)
		return
	}
	got := make([]string, 0, len(docs))
	for _, doc := range docs {
		got = append(got, doc.DocName)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("docs = %v, want %v", got, want)
	}
}

func TestApplyCommitDocumentWritesCurrentBlockIndex(t *testing.T) {
	dir := t.TempDir()
	idx, err := index.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })

	blockWriter, err := block.NewWriter(block.FilePath(dir, 1), 1, 1)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	t.Cleanup(func() { _ = blockWriter.Close() })
	idxWriter, err := block.NewIndexWriter(block.IdxFilePath(dir, 1))
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}
	t.Cleanup(func() { _ = idxWriter.Close() })

	s := &Shard{
		blocksDir:   dir,
		idx:         idx,
		blockWriter: blockWriter,
		idxWriter:   idxWriter,
	}

	err = s.applyCommitDocument(&scrapv1.CommitDocument{
		TransactionId: "tx-current",
		DocumentName:  "doc.xml",
		ContentType:   "text/xml",
		BlockId:       1,
		FrameCount:    1,
		TotalBytes:    4,
		Sha256:        make([]byte, 32),
		CreatedAtUs:   time.Now().UnixMicro(),
	}, 0)
	if err != nil {
		t.Fatalf("applyCommitDocument: %v", err)
	}
	if err := idxWriter.Close(); err != nil {
		t.Fatalf("Close idx writer: %v", err)
	}
	s.idxWriter = nil

	resolved, err := s.projectionResolver().ResolveDocument("tx-current", "doc.xml")
	if err != nil {
		t.Fatalf("ResolveDocument: %v", err)
	}
	if resolved.BlockID != 1 {
		t.Fatalf("BlockID = %d, want 1", resolved.BlockID)
	}
}

func TestAppendDocumentIndexEntryReportsCurrentWriterError(t *testing.T) {
	dir := t.TempDir()
	blockWriter, err := block.NewWriter(block.FilePath(dir, 1), 1, 1)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	t.Cleanup(func() { _ = blockWriter.Close() })
	idxWriter, err := block.NewIndexWriter(block.IdxFilePath(dir, 1))
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}
	if err := idxWriter.Close(); err != nil {
		t.Fatalf("Close idx writer: %v", err)
	}

	s := &Shard{
		blockWriter: blockWriter,
		idxWriter:   idxWriter,
	}

	err = s.appendDocumentIndexEntry(&scrapv1.CommitDocument{BlockId: 1}, block.IndexEntry{
		TransactionID: "tx-current-error",
		DocName:       "doc.xml",
	}, 0)
	if err == nil {
		t.Fatal("expected current writer error")
	}
}

func TestApplyCommitDocumentSkipsExistingHistoricalIndexEntry(t *testing.T) {
	dir := t.TempDir()
	idx, err := index.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })

	path := block.IdxFilePath(dir, 1)
	iw, err := block.NewIndexWriter(path)
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}
	if err := iw.Append(block.IndexEntry{TransactionID: "tx-existing", DocName: "doc.xml"}); err != nil {
		t.Fatalf("Append existing entry: %v", err)
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("Close existing index: %v", err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat before: %v", err)
	}

	s := &Shard{
		blocksDir: dir,
		idx:       idx,
	}

	err = s.applyCommitDocument(&scrapv1.CommitDocument{
		TransactionId: "tx-existing",
		DocumentName:  "doc.xml",
		ContentType:   "text/xml",
		BlockId:       1,
		FrameCount:    1,
		TotalBytes:    4,
		Sha256:        make([]byte, 32),
		CreatedAtUs:   time.Now().UnixMicro(),
	}, 0)
	if err != nil {
		t.Fatalf("applyCommitDocument: %v", err)
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	if after.Size() != before.Size() {
		t.Fatalf("index size changed: got %d, want %d", after.Size(), before.Size())
	}
}

func TestApplyCommitDocumentNoopsExistingVisibleDocumentReplay(t *testing.T) {
	dir := t.TempDir()
	idx, err := index.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })

	iw, err := block.NewIndexWriter(block.IdxFilePath(dir, 1))
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}
	if err := iw.Append(block.IndexEntry{TransactionID: "tx-visible", DocName: "doc.xml", ContentType: "text/xml", TotalBytes: 4}); err != nil {
		t.Fatalf("Append visible entry: %v", err)
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("Close visible index: %v", err)
	}
	if err := idx.Put("tx-visible", 1, 1, false); err != nil {
		t.Fatalf("Put projection entry: %v", err)
	}

	s := &Shard{
		blocksDir: dir,
		idx:       idx,
	}

	err = s.applyCommitDocument(&scrapv1.CommitDocument{
		TransactionId: "tx-visible",
		DocumentName:  "doc.xml",
		ContentType:   "text/xml",
		BlockId:       1,
		TotalBytes:    4,
		Sha256:        make([]byte, 32),
		CreatedAtUs:   time.Now().UnixMicro(),
	}, 0)
	if err != nil {
		t.Fatalf("applyCommitDocument: %v", err)
	}
	requireProjectionDocCount(t, idx, "tx-visible", 1)
	requireProjectionDocs(t, s, "tx-visible", "doc.xml")
}

func TestApplyCommitDocumentRejectsConflictingVisibleDocument(t *testing.T) {
	dir := t.TempDir()
	idx, err := index.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })

	iw, err := block.NewIndexWriter(block.IdxFilePath(dir, 1))
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}
	if err := iw.Append(block.IndexEntry{TransactionID: "tx-visible", DocName: "doc.xml", ContentType: "text/xml"}); err != nil {
		t.Fatalf("Append visible entry: %v", err)
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("Close visible index: %v", err)
	}
	if err := idx.Put("tx-visible", 1, 1, false); err != nil {
		t.Fatalf("Put projection entry: %v", err)
	}

	s := &Shard{
		blocksDir: dir,
		idx:       idx,
	}

	err = s.applyCommitDocument(&scrapv1.CommitDocument{
		TransactionId: "tx-visible",
		DocumentName:  "doc.xml",
		ContentType:   "application/xml",
		BlockId:       1,
		Sha256:        make([]byte, 32),
		CreatedAtUs:   time.Now().UnixMicro(),
	}, 0)
	if !errors.Is(err, storeapi.ErrAlreadyExists) {
		t.Fatalf("applyCommitDocument error = %v, want ErrAlreadyExists", err)
	}
}

func TestApplyCommitDocumentFailsClosedOnUncountedDuplicateInDifferentBlock(t *testing.T) {
	dir := t.TempDir()
	idx := openProjectionTestIndex(t)
	writeProjectionIndexEntries(t, dir, 1,
		block.IndexEntry{TransactionID: "tx-uncounted-duplicate", DocName: "a.xml", ContentType: "text/xml", TotalBytes: 4},
	)
	writeProjectionIndexEntries(t, dir, 2,
		block.IndexEntry{TransactionID: "tx-uncounted-duplicate", DocName: "doc.xml", ContentType: "text/xml", TotalBytes: 4},
	)
	if err := idx.Put("tx-uncounted-duplicate", 1, 1, false); err != nil {
		t.Fatalf("Put projection entry: %v", err)
	}
	if err := idx.AddBlockID("tx-uncounted-duplicate", 2); err != nil {
		t.Fatalf("AddBlockID: %v", err)
	}

	s := &Shard{
		blocksDir: dir,
		idx:       idx,
	}

	doc := newProjectionCommit("tx-uncounted-duplicate", "doc.xml")
	doc.BlockId = 3
	err := s.applyCommitDocument(doc, 0)
	if !errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("applyCommitDocument error = %v, want ErrDataLoss", err)
	}
	requireProjectionDocCount(t, idx, "tx-uncounted-duplicate", 1)
}

func TestDuplicateDocumentEntryFailsClosedOnMultipleVisibleMatches(t *testing.T) {
	dir := t.TempDir()
	idx := openProjectionTestIndex(t)
	writeProjectionIndexEntries(t, dir, 1,
		block.IndexEntry{TransactionID: "tx-visible-duplicate", DocName: "doc.xml", ContentType: "text/xml", TotalBytes: 4},
		block.IndexEntry{TransactionID: "tx-visible-duplicate", DocName: "doc.xml", ContentType: "text/xml", TotalBytes: 4},
	)
	if err := idx.Put("tx-visible-duplicate", 1, 2, false); err != nil {
		t.Fatalf("Put projection entry: %v", err)
	}

	s := &Shard{
		blocksDir: dir,
		idx:       idx,
	}

	_, _, err := s.duplicateDocumentEntry("tx-visible-duplicate", "doc.xml")
	if !errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("duplicateDocumentEntry error = %v, want ErrDataLoss", err)
	}
}

func TestApplyCommitDocumentFailsWhenHistoricalIndexMissing(t *testing.T) {
	idx, err := index.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })

	s := &Shard{
		blocksDir: t.TempDir(),
		idx:       idx,
	}

	err = s.applyCommitDocument(&scrapv1.CommitDocument{
		TransactionId: "tx-missing-index",
		DocumentName:  "doc.xml",
		BlockId:       1,
		Sha256:        make([]byte, 32),
		CreatedAtUs:   time.Now().UnixMicro(),
	}, 0)
	if err == nil {
		t.Fatal("expected missing historical index error")
	}
}
