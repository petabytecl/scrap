package block_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/block"
)

func writeVerifyTestBlock(t *testing.T, dir string) (blkPath, idxPath string) {
	t.Helper()

	blkPath = filepath.Join(dir, "0000000000000064.blk")
	idxPath = filepath.Join(dir, "0000000000000064.idx")

	bw, err := block.NewWriter(blkPath, 1, 100)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	iw, err := block.NewIndexWriter(idxPath)
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}

	docs := []struct {
		txID, name, ct string
		data           []byte
	}{
		{"tx-v1", "a.txt", "text/plain", bytes.Repeat([]byte("A"), 512)},
		{"tx-v1", "b.bin", "application/octet-stream", bytes.Repeat([]byte("B"), block.MaxFramePayload+100)},
	}

	for _, d := range docs {
		result, err := bw.AppendDocument(d.txID, d.name, d.ct, bytes.NewReader(d.data))
		if err != nil {
			t.Fatalf("AppendDocument %s: %v", d.name, err)
		}
		if err := iw.Append(block.IndexEntry{
			TransactionID: d.txID,
			DocName:       d.name,
			ContentType:   d.ct,
			CreatedAt:     time.Now(),
			FirstFrameOff: result.FirstFrameOffset,
			FrameCount:    result.FrameCount,
			TotalBytes:    result.Size,
			SHA256:        result.SHA256,
		}); err != nil {
			t.Fatalf("Append index %s: %v", d.name, err)
		}
	}

	if err := bw.Close(); err != nil {
		t.Fatalf("Close block: %v", err)
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("Close index: %v", err)
	}

	return blkPath, idxPath
}

func TestVerifyBlock_Clean(t *testing.T) {
	dir := t.TempDir()
	blkPath, idxPath := writeVerifyTestBlock(t, dir)

	result, err := block.VerifyBlock(blkPath, idxPath)
	if err != nil {
		t.Fatalf("VerifyBlock: %v", err)
	}
	if len(result.CorruptFrames) != 0 {
		t.Fatalf("expected 0 corrupt frames, got %d", len(result.CorruptFrames))
	}
	if result.FramesVerified == 0 {
		t.Fatal("expected frames to be verified")
	}
}

func TestVerifyBlock_EncryptedEntryVerifiesStoredCiphertext(t *testing.T) {
	dir := t.TempDir()
	blkPath := filepath.Join(dir, "0000000000000064.blk")
	idxPath := filepath.Join(dir, "0000000000000064.idx")

	bw, err := block.NewWriter(blkPath, 1, 100)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	iw, err := block.NewIndexWriter(idxPath)
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}

	storedCiphertext := []byte("ciphertext-frame")
	plaintextSHA := sha256.Sum256([]byte("plaintext-frame"))
	result, err := bw.AppendDocumentFrames("tx-encrypted", "doc.xml", "text/xml", block.DocumentFrames{
		Payloads: [][]byte{storedCiphertext},
		SHA256:   plaintextSHA,
		Size:     int64(len("plaintext-frame")),
	})
	if err != nil {
		t.Fatalf("AppendDocumentFrames: %v", err)
	}
	if err := iw.Append(block.IndexEntry{
		TransactionID:      "tx-encrypted",
		DocName:            "doc.xml",
		ContentType:        "text/xml",
		CreatedAt:          time.Now(),
		FirstFrameOff:      result.FirstFrameOffset,
		FrameCount:         result.FrameCount,
		TotalBytes:         result.Size,
		SHA256:             result.SHA256,
		EncryptionEnvelope: []byte(`{"ciphertext_length":16}`),
	}); err != nil {
		t.Fatalf("Append index: %v", err)
	}
	if err := bw.Close(); err != nil {
		t.Fatalf("Close block: %v", err)
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("Close index: %v", err)
	}

	resultVerify, err := block.VerifyBlock(blkPath, idxPath)
	if err != nil {
		t.Fatalf("VerifyBlock: %v", err)
	}
	if len(resultVerify.CorruptFrames) != 0 {
		t.Fatalf("expected encrypted block to verify cleanly, got %v", resultVerify.CorruptFrames)
	}
}

func TestVerifyBlock_FrameCRCCorruption(t *testing.T) {
	dir := t.TempDir()
	blkPath, idxPath := writeVerifyTestBlock(t, dir)

	data, err := os.ReadFile(blkPath) //nolint:gosec // test file path from temp dir
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	data[block.HeaderSize+block.FrameHeaderSize+5] ^= 0xFF
	if err := os.WriteFile(blkPath, data, 0o600); err != nil { //nolint:gosec // test file path from temp dir
		t.Fatalf("WriteFile: %v", err)
	}

	result, err := block.VerifyBlock(blkPath, idxPath)
	if err != nil {
		t.Fatalf("VerifyBlock: %v", err)
	}
	if len(result.CorruptFrames) == 0 {
		t.Fatal("expected at least 1 corrupt frame")
	}
	if result.CorruptFrames[0].Type != block.CorruptionFrameCRC {
		t.Fatalf("expected frame_crc corruption, got %s", result.CorruptFrames[0].Type)
	}
}

func TestVerifyBlock_FrameMagicCorruptionWithValidHeaderCRC(t *testing.T) {
	dir := t.TempDir()
	blkPath, idxPath := writeVerifyTestBlock(t, dir)

	data, err := os.ReadFile(blkPath) //nolint:gosec // test file path from temp dir
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	frameStart := block.HeaderSize
	data[frameStart] ^= 0xFF
	block.RecomputeFramePayloadCRC(data, frameStart)
	if err := os.WriteFile(blkPath, data, 0o600); err != nil { //nolint:gosec // test file path from temp dir
		t.Fatalf("WriteFile: %v", err)
	}

	result, err := block.VerifyBlock(blkPath, idxPath)
	if err != nil {
		t.Fatalf("VerifyBlock: %v", err)
	}
	if len(result.CorruptFrames) == 0 {
		t.Fatal("expected bad frame magic to be reported as corruption")
	}
	if result.CorruptFrames[0].Type != block.CorruptionFrameCRC {
		t.Fatalf("expected frame_crc corruption, got %s", result.CorruptFrames[0].Type)
	}
}

func TestVerifyBlock_DocSHA256Mismatch(t *testing.T) {
	dir := t.TempDir()
	blkPath, idxPath := writeVerifyTestBlock(t, dir)

	data, err := os.ReadFile(blkPath) //nolint:gosec // test file path from temp dir
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	payloadOff := block.HeaderSize + block.FrameHeaderSize + 5
	data[payloadOff] ^= 0xFF
	block.RecomputeFramePayloadCRC(data, block.HeaderSize)

	if err := os.WriteFile(blkPath, data, 0o600); err != nil { //nolint:gosec // test file path from temp dir
		t.Fatalf("WriteFile: %v", err)
	}

	result, err := block.VerifyBlock(blkPath, idxPath)
	if err != nil {
		t.Fatalf("VerifyBlock: %v", err)
	}
	hasSHA := false
	for _, cf := range result.CorruptFrames {
		if cf.Type == block.CorruptionDocSHA256 {
			hasSHA = true
		}
	}
	if !hasSHA {
		t.Fatalf("expected doc_sha256 corruption, got %v", result.CorruptFrames)
	}
}

func TestVerifyBlock_ReportsMissingIndexedDocumentAtEOF(t *testing.T) {
	dir := t.TempDir()
	blkPath, idxPath := writeVerifyTestBlock(t, dir)

	ir, err := block.OpenIndexReader(idxPath)
	if err != nil {
		t.Fatalf("OpenIndexReader: %v", err)
	}
	entries := ir.Entries()
	_ = ir.Close()
	if len(entries) < 2 {
		t.Fatalf("expected at least 2 index entries, got %d", len(entries))
	}

	if err := os.Truncate(blkPath, entries[1].FirstFrameOff); err != nil {
		t.Fatalf("Truncate: %v", err)
	}

	result, err := block.VerifyBlock(blkPath, idxPath)
	if err != nil {
		t.Fatalf("VerifyBlock: %v", err)
	}
	if len(result.CorruptFrames) == 0 {
		t.Fatal("expected missing indexed document to be reported as corruption")
	}
}

func TestVerifyBlock_OversizedPayloadLen(t *testing.T) {
	dir := t.TempDir()
	blkPath, idxPath := writeVerifyTestBlock(t, dir)

	data, err := os.ReadFile(blkPath) //nolint:gosec // test file path from temp dir
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// Set payloadLen to 0xFFFFFFFF in the first frame header, then fix header CRC.
	frameStart := block.HeaderSize
	binary.LittleEndian.PutUint32(data[frameStart+16:frameStart+20], 0xFFFFFFFF)
	headerCRC := crc32.Checksum(data[frameStart:frameStart+28], crc32.MakeTable(crc32.Castagnoli))
	binary.LittleEndian.PutUint32(data[frameStart+28:frameStart+32], headerCRC)

	if err := os.WriteFile(blkPath, data, 0o600); err != nil { //nolint:gosec // test file path from temp dir
		t.Fatalf("WriteFile: %v", err)
	}

	result, err := block.VerifyBlock(blkPath, idxPath)
	if err != nil {
		t.Fatalf("VerifyBlock: %v", err)
	}
	if len(result.CorruptFrames) == 0 {
		t.Fatal("expected oversized payload to be reported as corruption")
	}
}

func TestVerifyBlock_CorruptHeaderReported(t *testing.T) {
	dir := t.TempDir()
	blkPath := filepath.Join(dir, "0000000000000064.blk")
	idxPath := filepath.Join(dir, "0000000000000064.idx")

	bw, err := block.NewWriter(blkPath, 1, 100)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := bw.Close(); err != nil {
		t.Fatalf("Close block: %v", err)
	}
	iw, err := block.NewIndexWriter(idxPath)
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("Close index: %v", err)
	}

	raw, err := os.ReadFile(blkPath) //nolint:gosec // test file path from temp dir
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	raw[0] ^= 0xFF                                            // break the header magic
	if err := os.WriteFile(blkPath, raw, 0o600); err != nil { //nolint:gosec // test file path from temp dir
		t.Fatalf("WriteFile: %v", err)
	}

	result, err := block.VerifyBlock(blkPath, idxPath)
	if err != nil {
		t.Fatalf("VerifyBlock: %v", err)
	}
	if len(result.CorruptFrames) != 1 {
		t.Fatalf("CorruptFrames: got %d, want 1", len(result.CorruptFrames))
	}
	if result.CorruptFrames[0].Type != block.CorruptionHeader {
		t.Fatalf("corruption type: got %s, want %s", result.CorruptFrames[0].Type, block.CorruptionHeader)
	}
}
