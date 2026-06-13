package shard_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/petabytecl/scrap/internal/block"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func TestReadDocumentReturnsCommittedMetadataAndBytes(t *testing.T) {
	s := openTestShard(t)
	ctx := context.Background()
	payload := bytes.Repeat([]byte("committed read metadata "), 16)

	writeResult, err := s.WriteDocument(ctx, "tx-read-meta", "doc.xml", "text/xml", "", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}

	head, err := s.HeadDocument(ctx, "tx-read-meta", "doc.xml")
	if err != nil {
		t.Fatalf("HeadDocument: %v", err)
	}
	assertStoreMetaMatchesWrite(t, head, "doc.xml", "text/xml", writeResult, payload)

	rc, readMeta, err := s.ReadDocument(ctx, "tx-read-meta", "doc.xml")
	if err != nil {
		t.Fatalf("ReadDocument: %v", err)
	}
	defer func() { _ = rc.Close() }()
	assertStoreMetaMatchesWrite(t, readMeta, "doc.xml", "text/xml", writeResult, payload)

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("ReadDocument payload = %q, want %q", got, payload)
	}
}

func TestReadDocumentCorruptBlockPayloadFailsClosedWithoutReader(t *testing.T) {
	s := openTestShard(t)
	ctx := context.Background()
	payload := bytes.Repeat([]byte("payload crc "), 16)

	if _, err := s.WriteDocument(ctx, "tx-corrupt-block", "doc.xml", "text/xml", "", bytes.NewReader(payload)); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	corruptShardBlockByte(t, s.DataDirForTest(), 1, block.HeaderSize+block.FrameHeaderSize)

	rc, meta, err := s.ReadDocument(ctx, "tx-corrupt-block", "doc.xml")
	if !errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("ReadDocument error = %v, want ErrDataLoss", err)
	}
	if rc != nil {
		_ = rc.Close()
		t.Fatal("ReadDocument returned reader with corrupt Block payload")
	}
	if meta != (storeapi.DocumentMeta{}) {
		t.Fatalf("ReadDocument metadata = %+v, want zero value on corrupt Block payload", meta)
	}
}

func TestReadDocumentCorruptBlockHeaderFailsClosedWithoutReader(t *testing.T) {
	s := openTestShard(t)
	ctx := context.Background()
	payload := bytes.Repeat([]byte("block header "), 16)

	if _, err := s.WriteDocument(ctx, "tx-corrupt-header", "doc.xml", "text/xml", "", bytes.NewReader(payload)); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	corruptShardBlockByte(t, s.DataDirForTest(), 1, 0)

	rc, meta, err := s.ReadDocument(ctx, "tx-corrupt-header", "doc.xml")
	if !errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("ReadDocument error = %v, want ErrDataLoss", err)
	}
	if rc != nil {
		_ = rc.Close()
		t.Fatal("ReadDocument returned reader with corrupt Block header")
	}
	if meta != (storeapi.DocumentMeta{}) {
		t.Fatalf("ReadDocument metadata = %+v, want zero value on corrupt Block header", meta)
	}
}

func TestReadDocumentTruncatedFrameFailsClosedWithoutReader(t *testing.T) {
	s := openTestShard(t)
	ctx := context.Background()
	payload := bytes.Repeat([]byte("truncated frame "), 16)

	if _, err := s.WriteDocument(ctx, "tx-truncated-block", "doc.xml", "text/xml", "", bytes.NewReader(payload)); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	truncateShardBlock(t, s.DataDirForTest(), 1)

	rc, meta, err := s.ReadDocument(ctx, "tx-truncated-block", "doc.xml")
	if !errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("ReadDocument error = %v, want ErrDataLoss", err)
	}
	if rc != nil {
		_ = rc.Close()
		t.Fatal("ReadDocument returned reader with truncated Frame data")
	}
	if meta != (storeapi.DocumentMeta{}) {
		t.Fatalf("ReadDocument metadata = %+v, want zero value on truncated Frame data", meta)
	}
}

func TestReadDocumentFrameSequenceMismatchFailsClosedWithoutReader(t *testing.T) {
	s := openTestShard(t)
	ctx := context.Background()
	payload := bytes.Repeat([]byte("F"), block.MaxFramePayload*2+1)

	if _, err := s.WriteDocument(ctx, "tx-frame-seq", "doc.xml", "text/xml", "", bytes.NewReader(payload)); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	corruptShardFrameSequence(t, s.DataDirForTest(), 1)

	rc, meta, err := s.ReadDocument(ctx, "tx-frame-seq", "doc.xml")
	if !errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("ReadDocument error = %v, want ErrDataLoss", err)
	}
	if rc != nil {
		_ = rc.Close()
		t.Fatal("ReadDocument returned reader with corrupt Frame sequence")
	}
	if meta != (storeapi.DocumentMeta{}) {
		t.Fatalf("ReadDocument metadata = %+v, want zero value on corrupt Frame sequence", meta)
	}
}

func TestHeadAndReadDocumentCorruptIndexFailClosed(t *testing.T) {
	s := openTestShard(t)
	ctx := context.Background()
	payload := bytes.Repeat([]byte("corrupt index "), 16)

	if _, err := s.WriteDocument(ctx, "tx-corrupt-index", "doc.xml", "text/xml", "", bytes.NewReader(payload)); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	truncateShardIndex(t, s.DataDirForTest(), 1)

	if _, err := s.HeadDocument(ctx, "tx-corrupt-index", "doc.xml"); !errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("HeadDocument error = %v, want ErrDataLoss", err)
	}
	rc, meta, err := s.ReadDocument(ctx, "tx-corrupt-index", "doc.xml")
	if !errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("ReadDocument error = %v, want ErrDataLoss", err)
	}
	if rc != nil {
		_ = rc.Close()
		t.Fatal("ReadDocument returned reader with corrupt .idx metadata")
	}
	if meta != (storeapi.DocumentMeta{}) {
		t.Fatalf("ReadDocument metadata = %+v, want zero value on corrupt .idx metadata", meta)
	}
}

func assertStoreMetaMatchesWrite(t *testing.T, meta storeapi.DocumentMeta, name, contentType string, writeResult storeapi.WriteResult, payload []byte) {
	t.Helper()

	if meta.Name != name {
		t.Fatalf("Name = %q, want %q", meta.Name, name)
	}
	if meta.ContentType != contentType {
		t.Fatalf("ContentType = %q, want %q", meta.ContentType, contentType)
	}
	if meta.Size != int64(len(payload)) {
		t.Fatalf("Size = %d, want %d", meta.Size, len(payload))
	}
	if meta.SHA256 != writeResult.SHA256 {
		t.Fatalf("SHA256 = %x, want %x", meta.SHA256, writeResult.SHA256)
	}
	if !meta.CreatedAt.Equal(writeResult.CreatedAt) {
		t.Fatalf("CreatedAt = %v, want %v", meta.CreatedAt, writeResult.CreatedAt)
	}
}

func corruptShardBlockByte(t *testing.T, dataDir string, blockID uint64, offset int) {
	t.Helper()

	blkPath := block.FilePath(filepath.Join(dataDir, "blocks"), blockID)
	data, err := os.ReadFile(blkPath) //nolint:gosec // path is from test Shard temp dir.
	if err != nil {
		t.Fatalf("ReadFile block: %v", err)
	}
	if len(data) <= offset {
		t.Fatalf("Block length = %d, want byte offset %d", len(data), offset)
	}
	data[offset] ^= 0xff
	if err := os.WriteFile(blkPath, data, 0o600); err != nil { //nolint:gosec // path is from test Shard temp dir.
		t.Fatalf("WriteFile corrupt block: %v", err)
	}
}

func corruptShardFrameSequence(t *testing.T, dataDir string, blockID uint64) {
	t.Helper()

	blkPath := block.FilePath(filepath.Join(dataDir, "blocks"), blockID)
	data, err := os.ReadFile(blkPath) //nolint:gosec // path is from test Shard temp dir.
	if err != nil {
		t.Fatalf("ReadFile block: %v", err)
	}
	secondFrameStart := block.HeaderSize + block.FrameHeaderSize + block.MaxFramePayload
	if len(data) < secondFrameStart+block.FrameHeaderSize {
		t.Fatalf("Block length = %d, want second Frame header at %d", len(data), secondFrameStart)
	}
	binary.LittleEndian.PutUint32(data[secondFrameStart+12:secondFrameStart+16], 7)
	block.RecomputeFramePayloadCRC(data, secondFrameStart)
	if err := os.WriteFile(blkPath, data, 0o600); err != nil { //nolint:gosec // path is from test Shard temp dir.
		t.Fatalf("WriteFile corrupt frame sequence: %v", err)
	}
}

func truncateShardBlock(t *testing.T, dataDir string, blockID uint64) {
	t.Helper()

	blkPath := block.FilePath(filepath.Join(dataDir, "blocks"), blockID)
	info, err := os.Stat(blkPath)
	if err != nil {
		t.Fatalf("Stat block: %v", err)
	}
	if info.Size() <= int64(block.HeaderSize+block.FrameHeaderSize) {
		t.Fatalf("Block size = %d, want payload bytes to truncate", info.Size())
	}
	if err := os.Truncate(blkPath, info.Size()-1); err != nil {
		t.Fatalf("Truncate block: %v", err)
	}
}

func truncateShardIndex(t *testing.T, dataDir string, blockID uint64) {
	t.Helper()

	idxPath := block.IdxFilePath(filepath.Join(dataDir, "blocks"), blockID)
	info, err := os.Stat(idxPath)
	if err != nil {
		t.Fatalf("Stat index: %v", err)
	}
	if info.Size() <= 1 {
		t.Fatalf("Index size = %d, want bytes to truncate", info.Size())
	}
	if err := os.Truncate(idxPath, info.Size()-1); err != nil {
		t.Fatalf("Truncate index: %v", err)
	}
}
