package block

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
)

var ErrBlockHeaderCorrupt = errors.New("block: header corrupt")

func VerifyHeader(blkPath string, shardID, blockID uint64) error {
	f, err := os.Open(blkPath) //nolint:gosec // path is constructed by caller from controlled block IDs.
	if err != nil {
		return fmt.Errorf("block: open header: %w", err)
	}
	defer func() { _ = f.Close() }()

	var hdr [HeaderSize]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return fmt.Errorf("%w: read: %w", ErrBlockHeaderCorrupt, err)
	}
	if string(hdr[0:4]) != "SCRP" {
		return fmt.Errorf("%w: invalid magic %q", ErrBlockHeaderCorrupt, hdr[0:4])
	}
	if version := binary.LittleEndian.Uint16(hdr[4:6]); version != 1 {
		return fmt.Errorf("%w: version %d", ErrBlockHeaderCorrupt, version)
	}
	if headerLen := binary.LittleEndian.Uint16(hdr[6:8]); headerLen != HeaderSize {
		return fmt.Errorf("%w: header_len %d", ErrBlockHeaderCorrupt, headerLen)
	}
	if gotShardID := binary.LittleEndian.Uint64(hdr[8:16]); gotShardID != shardID {
		return fmt.Errorf("%w: shard_id %d want %d", ErrBlockHeaderCorrupt, gotShardID, shardID)
	}
	if gotBlockID := binary.LittleEndian.Uint64(hdr[16:24]); gotBlockID != blockID {
		return fmt.Errorf("%w: block_id %d want %d", ErrBlockHeaderCorrupt, gotBlockID, blockID)
	}
	headerCRC := binary.LittleEndian.Uint32(hdr[36:40])
	if want := crc32.Checksum(hdr[0:36], crcTable); headerCRC != want {
		return fmt.Errorf("%w: header CRC mismatch", ErrBlockHeaderCorrupt)
	}
	return nil
}
