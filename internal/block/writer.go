package block

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"os"
	"time"
)

const HeaderSize = 40

type AppendResult struct {
	SHA256           [32]byte
	Size             int64
	FrameCount       uint32
	FirstFrameOffset int64
}

type Writer struct {
	f        *os.File
	path     string
	shardID  uint64
	blockID  uint64
	offset   int64
	docSeq   uint32
	docCount uint32
	closed   bool
}

// NewWriter creates a new block file with a 40-byte CRC-protected header.
// Layout: magic(4)="SCRP" + version(2) + header_len(2) + shard_id(8) + block_id(8) +
//
//	created_at_unix_micro(8) + reserved(4) + header_crc32c(4)
func NewWriter(path string, shardID, blockID uint64) (*Writer, error) {
	f, err := os.Create(path) //nolint:gosec // path is constructed by caller from controlled shard/block IDs
	if err != nil {
		return nil, fmt.Errorf("block: create %s: %w", path, err)
	}

	var hdr [HeaderSize]byte
	copy(hdr[0:4], "SCRP")
	binary.LittleEndian.PutUint16(hdr[4:6], 1)
	binary.LittleEndian.PutUint16(hdr[6:8], HeaderSize)
	binary.LittleEndian.PutUint64(hdr[8:16], shardID)
	binary.LittleEndian.PutUint64(hdr[16:24], blockID)
	binary.LittleEndian.PutUint64(hdr[24:32], uint64(time.Now().UnixMicro()))
	// hdr[32:36] reserved
	binary.LittleEndian.PutUint32(hdr[36:40], crc32.Checksum(hdr[0:36], crcTable))

	if _, err := f.Write(hdr[:]); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("block: write header: %w", err)
	}

	return &Writer{
		f:       f,
		path:    path,
		shardID: shardID,
		blockID: blockID,
		offset:  HeaderSize,
	}, nil
}

//nolint:revive // txID, docName, contentType are part of the public API contract; callers pass document metadata
func (w *Writer) AppendDocument(txID, docName, contentType string, body io.Reader) (AppendResult, error) {
	if w.closed {
		return AppendResult{}, errors.New("block: writer is closed")
	}

	firstOffset := w.offset
	frameSeq, totalSize, docHash, err := w.writeDocFrames(body)
	if err != nil {
		return AppendResult{}, err
	}

	if err := w.f.Sync(); err != nil {
		return AppendResult{}, fmt.Errorf("block: fsync after document: %w", err)
	}

	w.docSeq++
	w.docCount++

	var digest [32]byte
	copy(digest[:], docHash.Sum(nil))

	return AppendResult{
		SHA256:           digest,
		Size:             totalSize,
		FrameCount:       frameSeq,
		FirstFrameOffset: firstOffset,
	}, nil
}

func (w *Writer) writeDocFrames(body io.Reader) (uint32, int64, hash.Hash, error) {
	hasher := sha256.New()
	buf := make([]byte, MaxFramePayload)
	var frameSeq uint32
	var totalSize int64

	for {
		n, readErr := io.ReadFull(body, buf)
		if n > 0 {
			payload := buf[:n]
			hasher.Write(payload)
			totalSize += int64(n)

			isLast := readErr == io.EOF || readErr == io.ErrUnexpectedEOF
			flags := frameFlags(frameSeq, isLast)

			err := WriteFrame(w.f, FrameHeader{
				DocSeq:   w.docSeq,
				FrameSeq: frameSeq,
				Flags:    flags,
			}, payload)
			if err != nil {
				return 0, 0, nil, fmt.Errorf("block: write frame %d: %w", frameSeq, err)
			}

			w.offset += int64(FrameHeaderSize + n)
			frameSeq++
		}
		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			break
		}
		if readErr != nil {
			return 0, 0, nil, fmt.Errorf("block: read body: %w", readErr)
		}
	}
	return frameSeq, totalSize, hasher, nil
}

func frameFlags(seq uint32, isLast bool) byte {
	switch {
	case seq == 0 && isLast:
		return FlagSingleFrame
	case seq == 0:
		return FlagFirstFrame
	case isLast:
		return FlagLastFrame
	default:
		return 0
	}
}

func (w *Writer) DocCount() uint32 {
	return w.docCount
}

func (w *Writer) Offset() int64 {
	return w.offset
}

func (w *Writer) BlockID() uint64 {
	return w.blockID
}

func (w *Writer) Path() string {
	return w.path
}

func (w *Writer) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	if err := w.f.Sync(); err != nil {
		_ = w.f.Close()
		return fmt.Errorf("block: final fsync: %w", err)
	}
	return w.f.Close()
}
