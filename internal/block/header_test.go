package block_test

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"

	"github.com/petabytecl/scrap/internal/block"
)

func TestVerifyHeaderAcceptsMatchingBlockHeader(t *testing.T) {
	blkPath := writeHeaderTestBlock(t, 7, 42)

	if err := block.VerifyHeader(blkPath, 7, 42); err != nil {
		t.Fatalf("VerifyHeader: %v", err)
	}
}

func TestVerifyHeaderRejectsCorruptHeader(t *testing.T) {
	tests := []struct {
		name   string
		mutate func([]byte)
	}{
		{
			name: "magic",
			mutate: func(data []byte) {
				data[0] = 0
				recomputeHeaderCRC(data)
			},
		},
		{
			name: "version",
			mutate: func(data []byte) {
				binary.LittleEndian.PutUint16(data[4:6], 2)
				recomputeHeaderCRC(data)
			},
		},
		{
			name: "header length",
			mutate: func(data []byte) {
				binary.LittleEndian.PutUint16(data[6:8], 12)
				recomputeHeaderCRC(data)
			},
		},
		{
			name: "shard id",
			mutate: func(data []byte) {
				binary.LittleEndian.PutUint64(data[8:16], 8)
				recomputeHeaderCRC(data)
			},
		},
		{
			name: "block id",
			mutate: func(data []byte) {
				binary.LittleEndian.PutUint64(data[16:24], 43)
				recomputeHeaderCRC(data)
			},
		},
		{
			name: "crc",
			mutate: func(data []byte) {
				data[36] ^= 0xff
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blkPath := writeHeaderTestBlock(t, 7, 42)
			data, err := os.ReadFile(blkPath) //nolint:gosec // test reads its own temp file.
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			tt.mutate(data)
			if err := os.WriteFile(blkPath, data, 0o600); err != nil { //nolint:gosec // test writes its own temp file.
				t.Fatalf("WriteFile: %v", err)
			}

			err = block.VerifyHeader(blkPath, 7, 42)
			if !errors.Is(err, block.ErrBlockHeaderCorrupt) {
				t.Fatalf("VerifyHeader error = %v, want ErrBlockHeaderCorrupt", err)
			}
		})
	}
}

func writeHeaderTestBlock(t *testing.T, shardID, blockID uint64) string {
	t.Helper()

	blkPath := filepath.Join(t.TempDir(), "test.blk")
	w, err := block.NewWriter(blkPath, shardID, blockID)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return blkPath
}

func recomputeHeaderCRC(data []byte) {
	crc := crc32.Checksum(data[0:36], crc32.MakeTable(crc32.Castagnoli))
	binary.LittleEndian.PutUint32(data[36:40], crc)
}
