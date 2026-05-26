package block

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"time"
)

const BlockHeaderSize = 32

type AppendResult struct {
	SHA256Checksum   string
	Size             int64
	FrameCount       uint16
	FirstFrameOffset int64
}

type BlockWriter struct {
	f        *os.File
	path     string
	shardID  uint64
	blockID  uint64
	offset   int64
	docSeq   uint32
	docCount uint32
	closed   bool
}

func NewBlockWriter(path string, shardID, blockID uint64) (*BlockWriter, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("block: create %s: %w", path, err)
	}

	var hdr [BlockHeaderSize]byte
	copy(hdr[0:4], "SCRP")
	binary.LittleEndian.PutUint16(hdr[4:6], 1)
	binary.LittleEndian.PutUint64(hdr[6:14], shardID)
	binary.LittleEndian.PutUint64(hdr[14:22], blockID)
	binary.LittleEndian.PutUint64(hdr[22:30], uint64(time.Now().UnixMicro()))

	if _, err := f.Write(hdr[:]); err != nil {
		f.Close()
		return nil, fmt.Errorf("block: write header: %w", err)
	}

	return &BlockWriter{
		f:       f,
		path:    path,
		shardID: shardID,
		blockID: blockID,
		offset:  BlockHeaderSize,
	}, nil
}

func (w *BlockWriter) AppendDocument(txID, docName, contentType string, body io.Reader) (AppendResult, error) {
	if w.closed {
		return AppendResult{}, fmt.Errorf("block: writer is closed")
	}

	firstOffset := w.offset
	hasher := sha256.New()
	buf := make([]byte, MaxFramePayload)
	var frameSeq uint16
	var totalSize int64

	for {
		n, readErr := io.ReadFull(body, buf)
		if n > 0 {
			payload := buf[:n]
			hasher.Write(payload)
			totalSize += int64(n)

			var flags byte
			if frameSeq == 0 && (readErr == io.EOF || readErr == io.ErrUnexpectedEOF) {
				flags = FlagSingleFrame
			} else if frameSeq == 0 {
				flags = FlagFirstFrame
			} else if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
				flags = FlagLastFrame
			}

			err := WriteFrame(w.f, FrameHeader{
				DocSeq:   w.docSeq,
				FrameSeq: frameSeq,
				Flags:    flags,
			}, payload)
			if err != nil {
				return AppendResult{}, fmt.Errorf("block: write frame %d: %w", frameSeq, err)
			}

			w.offset += int64(FrameHeaderSize + n)
			frameSeq++
		}
		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			break
		}
		if readErr != nil {
			return AppendResult{}, fmt.Errorf("block: read body: %w", readErr)
		}
	}

	if err := w.f.Sync(); err != nil {
		return AppendResult{}, fmt.Errorf("block: fsync after document: %w", err)
	}

	w.docSeq++
	w.docCount++

	return AppendResult{
		SHA256Checksum:   hex.EncodeToString(hasher.Sum(nil)),
		Size:             totalSize,
		FrameCount:       frameSeq,
		FirstFrameOffset: firstOffset,
	}, nil
}

func (w *BlockWriter) DocCount() uint32 {
	return w.docCount
}

func (w *BlockWriter) Offset() int64 {
	return w.offset
}

func (w *BlockWriter) BlockID() uint64 {
	return w.blockID
}

func (w *BlockWriter) Path() string {
	return w.path
}

func (w *BlockWriter) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	if err := w.f.Sync(); err != nil {
		w.f.Close()
		return fmt.Errorf("block: final fsync: %w", err)
	}
	return w.f.Close()
}
