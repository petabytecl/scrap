package index_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/index"
)

const (
	resolutionBenchmarkBlocks       = 7
	resolutionBenchmarkIndexEntries = 128
	resolutionBenchmarkTxID         = "tx-resolution-benchmark"
	resolutionBenchmarkTarget       = "document-07.xml"
)

func BenchmarkResolverResolveDocument(b *testing.B) {
	resolver := createResolutionBenchmarkResolver(b)
	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		doc, err := resolver.ResolveDocument(resolutionBenchmarkTxID, resolutionBenchmarkTarget)
		if err != nil {
			b.Fatalf("ResolveDocument: %v", err)
		}
		if doc.BlockID != resolutionBenchmarkBlocks || doc.DocName != resolutionBenchmarkTarget {
			b.Fatalf("resolved Document = block %d name %q, want block %d name %q", doc.BlockID, doc.DocName, resolutionBenchmarkBlocks, resolutionBenchmarkTarget)
		}
	}
}

func BenchmarkResolverListDocuments(b *testing.B) {
	resolver := createResolutionBenchmarkResolver(b)
	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		docs, err := resolver.ListDocuments(resolutionBenchmarkTxID)
		if err != nil {
			b.Fatalf("ListDocuments: %v", err)
		}
		if len(docs) != resolutionBenchmarkBlocks {
			b.Fatalf("listed Documents = %d, want %d", len(docs), resolutionBenchmarkBlocks)
		}
		for i, doc := range docs {
			wantBlockID := uint64(i + 1)
			wantName := fmt.Sprintf("document-%02d.xml", wantBlockID)
			if doc.BlockID != wantBlockID || doc.DocName != wantName {
				b.Fatalf("Document %d = block %d name %q, want block %d name %q", i, doc.BlockID, doc.DocName, wantBlockID, wantName)
			}
		}
	}
}

func createResolutionBenchmarkResolver(b *testing.B) index.Resolver {
	b.Helper()
	dir := b.TempDir()
	projection, err := index.Open(filepath.Join(dir, "pebble"))
	if err != nil {
		b.Fatalf("open Projection: %v", err)
	}
	b.Cleanup(func() {
		if err := projection.Close(); err != nil {
			b.Errorf("close Projection: %v", err)
		}
	})

	blocksDir := filepath.Join(dir, "blocks")
	if err := os.MkdirAll(blocksDir, 0o750); err != nil {
		b.Fatalf("create Blocks directory: %v", err)
	}
	for blockID := uint64(1); blockID <= resolutionBenchmarkBlocks; blockID++ {
		writeResolutionBenchmarkIndex(b, blocksDir, blockID)
	}
	if err := projection.Put(resolutionBenchmarkTxID, 1, resolutionBenchmarkBlocks, false); err != nil {
		b.Fatalf("put Transaction: %v", err)
	}
	for blockID := uint64(2); blockID <= resolutionBenchmarkBlocks; blockID++ {
		if err := projection.AddBlockID(resolutionBenchmarkTxID, blockID); err != nil {
			b.Fatalf("add Block ID %d: %v", blockID, err)
		}
	}

	return index.NewResolver(projection, func(blockID uint64) string {
		return block.IdxFilePath(blocksDir, blockID)
	})
}

func writeResolutionBenchmarkIndex(b *testing.B, blocksDir string, blockID uint64) {
	b.Helper()
	writer, err := block.NewIndexWriter(block.IdxFilePath(blocksDir, blockID))
	if err != nil {
		b.Fatalf("new Block index writer %d: %v", blockID, err)
	}
	b.Cleanup(func() { _ = writer.Close() })
	for entryID := range resolutionBenchmarkIndexEntries - 1 {
		entry := block.IndexEntry{
			TransactionID: fmt.Sprintf("tx-unrelated-%03d", entryID),
			DocName:       fmt.Sprintf("unrelated-%03d.xml", entryID),
			ContentType:   "application/xml",
			CreatedAt:     time.UnixMicro(1_716_700_000_000_000),
			FirstFrameOff: block.HeaderSize,
			FrameCount:    1,
			TotalBytes:    128 << 10,
			SHA256:        [32]byte{0x01},
		}
		if err := writer.Append(entry); err != nil {
			b.Fatalf("append unrelated Block index entry %d: %v", entryID, err)
		}
	}
	entry := block.IndexEntry{
		TransactionID: resolutionBenchmarkTxID,
		DocName:       fmt.Sprintf("document-%02d.xml", blockID),
		ContentType:   "application/xml",
		CreatedAt:     time.UnixMicro(1_716_700_000_000_000),
		FirstFrameOff: block.HeaderSize,
		FrameCount:    1,
		TotalBytes:    128 << 10,
		SHA256:        [32]byte{0x01},
	}
	if err := writer.Append(entry); err != nil {
		b.Fatalf("append Block index entry %d: %v", blockID, err)
	}
	if err := writer.Close(); err != nil {
		b.Fatalf("close Block index writer %d: %v", blockID, err)
	}
}
