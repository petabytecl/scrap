package block_test

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/block"
)

func BenchmarkReadDocumentFromBlock(b *testing.B) {
	benchmarks := []struct {
		name string
		size int
	}{
		{name: "128KiB", size: 128 << 10},
		{name: "4MiB", size: 4 << 20},
	}

	for _, benchmark := range benchmarks {
		b.Run(benchmark.name, func(b *testing.B) {
			benchmarkReadDocumentFromBlock(b, benchmark.size)
		})
	}
}

func benchmarkReadDocumentFromBlock(b *testing.B, size int) {
	b.Helper()
	blkPath, entry := createReadBenchmarkBlock(b, size)
	b.SetBytes(int64(size))
	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		reader, err := block.ReadDocumentFromBlock(blkPath, 1, 1, entry)
		if err != nil {
			b.Fatalf("ReadDocumentFromBlock: %v", err)
		}
		n, copyErr := io.Copy(io.Discard, reader)
		closeErr := reader.Close()
		if copyErr != nil {
			b.Fatalf("read Document: %v", copyErr)
		}
		if closeErr != nil {
			b.Fatalf("close Document reader: %v", closeErr)
		}
		if n != int64(size) {
			b.Fatalf("read bytes = %d, want %d", n, size)
		}
	}
}

func BenchmarkVerifyBlock(b *testing.B) {
	const blockBytes = 64 << 20
	const wantFrames = blockBytes / block.MaxFramePayload

	blkPath, idxPath := createVerifyBenchmarkBlock(b, blockBytes)
	b.SetBytes(blockBytes)
	b.ReportAllocs()
	b.ResetTimer()
	b.ReportMetric(wantFrames, "frames/op")

	for range b.N {
		result, err := block.VerifyBlock(blkPath, idxPath)
		if err != nil {
			b.Fatalf("VerifyBlock: %v", err)
		}
		if len(result.CorruptFrames) != 0 {
			b.Fatalf("corrupt Frames = %d, want 0", len(result.CorruptFrames))
		}
		if result.FramesVerified != wantFrames {
			b.Fatalf("verified Frames = %d, want %d", result.FramesVerified, wantFrames)
		}
		if result.TransientReadRetries != 0 {
			b.Fatalf("transient read retries = %d, want 0", result.TransientReadRetries)
		}
	}
}

func createReadBenchmarkBlock(b *testing.B, size int) (string, block.IndexEntry) {
	b.Helper()
	blkPath := filepath.Join(b.TempDir(), "0000000000000001.blk")
	writer, err := block.NewWriter(blkPath, 1, 1)
	if err != nil {
		b.Fatalf("NewWriter: %v", err)
	}
	b.Cleanup(func() { _ = writer.Close() })
	payload := bytes.Repeat([]byte{0x5a}, size)
	result, err := writer.AppendDocument("tx-benchmark", "document.bin", "application/octet-stream", bytes.NewReader(payload))
	if err != nil {
		b.Fatalf("AppendDocument: %v", err)
	}
	if err := writer.Close(); err != nil {
		b.Fatalf("close Block writer: %v", err)
	}
	return blkPath, block.IndexEntry{
		TransactionID: "tx-benchmark",
		DocName:       "document.bin",
		ContentType:   "application/octet-stream",
		CreatedAt:     time.UnixMicro(1_716_700_000_000_000),
		FirstFrameOff: result.FirstFrameOffset,
		FrameCount:    result.FrameCount,
		TotalBytes:    result.Size,
		SHA256:        result.SHA256,
	}
}

func createVerifyBenchmarkBlock(b *testing.B, size int) (string, string) {
	b.Helper()
	dir := b.TempDir()
	blkPath := filepath.Join(dir, "0000000000000001.blk")
	idxPath := filepath.Join(dir, "0000000000000001.idx")
	writer, err := block.NewWriter(blkPath, 1, 1)
	if err != nil {
		b.Fatalf("NewWriter: %v", err)
	}
	b.Cleanup(func() { _ = writer.Close() })
	indexWriter, err := block.NewIndexWriter(idxPath)
	if err != nil {
		b.Fatalf("NewIndexWriter: %v", err)
	}
	b.Cleanup(func() { _ = indexWriter.Close() })

	const documentSize = 1 << 20
	payload := bytes.Repeat([]byte{0x6b}, documentSize)
	for i := range size / documentSize {
		name := fmt.Sprintf("document-%02d.bin", i)
		result, appendErr := writer.AppendDocument("tx-benchmark", name, "application/octet-stream", bytes.NewReader(payload))
		if appendErr != nil {
			b.Fatalf("AppendDocument %d: %v", i, appendErr)
		}
		if appendErr := indexWriter.Append(block.IndexEntry{
			TransactionID: "tx-benchmark",
			DocName:       name,
			ContentType:   "application/octet-stream",
			CreatedAt:     time.UnixMicro(1_716_700_000_000_000 + int64(i)),
			FirstFrameOff: result.FirstFrameOffset,
			FrameCount:    result.FrameCount,
			TotalBytes:    result.Size,
			SHA256:        result.SHA256,
		}); appendErr != nil {
			b.Fatalf("append index entry %d: %v", i, appendErr)
		}
	}
	if err := writer.Close(); err != nil {
		b.Fatalf("close Block writer: %v", err)
	}
	if err := indexWriter.Close(); err != nil {
		b.Fatalf("close Block index writer: %v", err)
	}
	return blkPath, idxPath
}
