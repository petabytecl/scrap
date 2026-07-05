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

type DocumentFrames struct {
	Payloads [][]byte
	SHA256   [32]byte
	Size     int64
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
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // path is constructed by caller from controlled shard/block IDs
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

// OpenWriter opens an existing Block for appending after validating its header
// and recovering the next document sequence from existing Frames.
func OpenWriter(path string, shardID, blockID uint64) (*Writer, error) {
	if err := VerifyHeader(path, shardID, blockID); err != nil {
		return nil, err
	}

	f, err := os.OpenFile(path, os.O_RDWR, 0) //nolint:gosec // path is constructed by caller from controlled shard/block IDs
	if err != nil {
		return nil, fmt.Errorf("block: open writer %s: %w", path, err)
	}

	offset, docSeq, docCount, err := scanWriterState(f, path)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("block: seek append offset %s: %w", path, err)
	}

	return &Writer{
		f:        f,
		path:     path,
		shardID:  shardID,
		blockID:  blockID,
		offset:   offset,
		docSeq:   docSeq,
		docCount: docCount,
	}, nil
}

func scanWriterState(f *os.File, path string) (int64, uint32, uint32, error) {
	if _, err := f.Seek(HeaderSize, io.SeekStart); err != nil {
		return 0, 0, 0, fmt.Errorf("block: scan writer seek %s: %w", path, err)
	}

	offset := int64(HeaderSize)
	var docSeq uint32
	var frameSeq uint32
	var docCount uint32
	for {
		hdr, payload, err := ReadFrame(f)
		if errors.Is(err, io.EOF) {
			if frameSeq != 0 {
				return 0, 0, 0, fmt.Errorf("block: open writer %s: incomplete document at EOF", path)
			}
			return offset, docSeq, docCount, nil
		}
		if err != nil {
			return 0, 0, 0, fmt.Errorf("block: scan writer %s: %w", path, err)
		}
		if err := validateWriterScanFrame(hdr, docSeq, frameSeq); err != nil {
			return 0, 0, 0, fmt.Errorf("block: scan writer %s: %w", path, err)
		}

		offset += int64(FrameHeaderSize) + int64(len(payload))
		if isLastFrame(hdr.Flags) {
			docSeq++
			docCount++
			frameSeq = 0
			continue
		}
		frameSeq++
	}
}

func validateWriterScanFrame(hdr FrameHeader, docSeq, frameSeq uint32) error {
	if hdr.DocSeq != docSeq {
		return fmt.Errorf("doc_seq %d, want %d", hdr.DocSeq, docSeq)
	}
	if hdr.FrameSeq != frameSeq {
		return fmt.Errorf("frame_seq %d, want %d", hdr.FrameSeq, frameSeq)
	}
	return validateWriterScanFrameFlags(hdr.Flags, frameSeq)
}

func validateWriterScanFrameFlags(flags byte, frameSeq uint32) error {
	if flags != frameFlags(frameSeq, isLastFrame(flags)) {
		return fmt.Errorf("invalid frame flags 0x%02x for frame_seq %d", flags, frameSeq)
	}
	return nil
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
	if frameSeq == 0 {
		return AppendResult{}, errors.New("block: document body is empty")
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

//nolint:revive // txID, docName, contentType are part of the public API contract; callers pass document metadata
func (w *Writer) AppendDocumentFrames(txID, docName, contentType string, frames DocumentFrames) (AppendResult, error) {
	if w.closed {
		return AppendResult{}, errors.New("block: writer is closed")
	}
	if frames.Size < 0 {
		return AppendResult{}, errors.New("block: document size is negative")
	}
	if len(frames.Payloads) == 0 {
		return AppendResult{}, errors.New("block: document has no frames")
	}

	firstOffset := w.offset
	for frameSeq, payload := range frames.Payloads {
		isLast := frameSeq == len(frames.Payloads)-1
		flags := frameFlags(uint32(frameSeq), isLast)
		if err := WriteFrame(w.f, FrameHeader{
			DocSeq:   w.docSeq,
			FrameSeq: uint32(frameSeq),
			Flags:    flags,
		}, payload); err != nil {
			return AppendResult{}, fmt.Errorf("block: write prepared frame %d: %w", frameSeq, err)
		}
		w.offset += int64(FrameHeaderSize + len(payload))
	}

	if err := w.f.Sync(); err != nil {
		return AppendResult{}, fmt.Errorf("block: fsync after document: %w", err)
	}

	w.docSeq++
	w.docCount++

	return AppendResult{
		SHA256:           frames.SHA256,
		Size:             frames.Size,
		FrameCount:       uint32(len(frames.Payloads)), //nolint:gosec // frame count is bounded by memory and uint32 protocol fields.
		FirstFrameOffset: firstOffset,
	}, nil
}

// AppendDocumentFrameSource streams prepared ciphertext Frames into the
// Block without buffering the whole Document. next returns each payload and
// whether it is the final Frame; it returns io.EOF only when the Document
// has no Frames at all, which is an error. The Document SHA-256 and size are
// only known to the caller after the source is exhausted, so this returns
// the first Frame offset and Frame count instead of a full AppendResult; on
// error the caller must Truncate back to the starting offset.
func (w *Writer) AppendDocumentFrameSource(next func() (payload []byte, last bool, err error)) (int64, uint32, error) {
	if w.closed {
		return 0, 0, errors.New("block: writer is closed")
	}

	firstOffset := w.offset
	var frameSeq uint32
	for {
		payload, last, err := next()
		if err != nil {
			if errors.Is(err, io.EOF) && frameSeq == 0 {
				return 0, 0, errors.New("block: document has no frames")
			}
			return 0, 0, err
		}
		if err := WriteFrame(w.f, FrameHeader{
			DocSeq:   w.docSeq,
			FrameSeq: frameSeq,
			Flags:    frameFlags(frameSeq, last),
		}, payload); err != nil {
			return 0, 0, fmt.Errorf("block: write streamed frame %d: %w", frameSeq, err)
		}
		w.offset += int64(FrameHeaderSize + len(payload))
		frameSeq++
		if last {
			break
		}
	}

	if err := w.f.Sync(); err != nil {
		return 0, 0, fmt.Errorf("block: fsync after document: %w", err)
	}

	w.docSeq++
	w.docCount++
	return firstOffset, frameSeq, nil
}

// writeDocFrames reads one frame ahead of what it writes so the final frame
// is known before its header is emitted: io.ReadFull returns nil (not io.EOF)
// when a read exactly fills the buffer, so bodies sized an exact multiple of
// MaxFramePayload only reveal EOF on the read after the last payload.
func (w *Writer) writeDocFrames(body io.Reader) (uint32, int64, hash.Hash, error) {
	hasher := sha256.New()
	cur := make([]byte, MaxFramePayload)
	next := make([]byte, MaxFramePayload)
	var frameSeq uint32
	var totalSize int64

	curN, curErr := io.ReadFull(body, cur)
	for {
		if curErr != nil && !isBodyEOF(curErr) {
			return 0, 0, nil, fmt.Errorf("block: read body: %w", curErr)
		}
		if curN == 0 {
			return frameSeq, totalSize, hasher, nil
		}

		nextN := 0
		var nextErr error
		if curErr == nil {
			nextN, nextErr = io.ReadFull(body, next)
		}
		isLast := isBodyEOF(curErr) || nextN == 0

		payload := cur[:curN]
		hasher.Write(payload)
		totalSize += int64(curN)

		err := WriteFrame(w.f, FrameHeader{
			DocSeq:   w.docSeq,
			FrameSeq: frameSeq,
			Flags:    frameFlags(frameSeq, isLast),
		}, payload)
		if err != nil {
			return 0, 0, nil, fmt.Errorf("block: write frame %d: %w", frameSeq, err)
		}

		w.offset += int64(FrameHeaderSize + curN)
		frameSeq++

		cur, next = next, cur
		curN, curErr = nextN, nextErr
	}
}

func isBodyEOF(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
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

func (w *Writer) Truncate(offset int64) error {
	if offset < HeaderSize {
		return fmt.Errorf("block: truncate offset %d before header", offset)
	}
	if offset > w.offset {
		return fmt.Errorf("block: truncate offset %d beyond end %d", offset, w.offset)
	}
	if err := w.f.Truncate(offset); err != nil {
		return fmt.Errorf("block: truncate: %w", err)
	}
	if err := w.f.Sync(); err != nil {
		return fmt.Errorf("block: fsync after truncate: %w", err)
	}
	// Re-derive the document counters from the Frames that survive the
	// truncation. Abort paths truncate to a document boundary after a partial
	// or complete append, and docSeq/docCount must match the Frames actually on
	// disk: a leaked doc_seq desynchronizes the next committed Frame's DocSeq
	// from its .idx position, which deep scrub reads as corruption and
	// quarantines an intact Block.
	newOffset, docSeq, docCount, err := scanWriterState(w.f, w.path)
	if err != nil {
		return err
	}
	if _, err := w.f.Seek(newOffset, io.SeekStart); err != nil {
		return fmt.Errorf("block: seek after truncate: %w", err)
	}
	w.offset = newOffset
	w.docSeq = docSeq
	w.docCount = docCount
	return nil
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
