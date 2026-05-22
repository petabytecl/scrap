package blockstore

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	HeaderLength      = 64
	DefaultFrameSize  = 1024 * 1024
	formatMajor       = 0
	formatMinor       = 1
	defaultReadBuffer = 1024 * 1024
)

var (
	ErrChecksumMismatch = errors.New("blockstore: checksum mismatch")
	ErrInvalidRange     = errors.New("blockstore: invalid range")
)

type Store struct {
	mu          sync.Mutex
	dir         string
	blocksDir   string
	blockID     string
	blockPath   string
	blockFile   *os.File
	blockOffset uint64
	frameSize   uint64
}

type Record struct {
	BlockID       string
	StoredOffset  uint64
	StoredLength  uint64
	LogicalSHA256 [32]byte
	Frames        []FrameRecord
}

type FrameRecord struct {
	FrameOffset   uint64
	SegmentOffset uint64
	SegmentLength uint64
	SHA256        [32]byte
}

func Open(dir string) (*Store, error) {
	blocksDir := filepath.Join(dir, "blocks")
	if err := os.MkdirAll(blocksDir, 0o700); err != nil {
		return nil, err
	}

	blockID, err := newUUIDv7()
	if err != nil {
		return nil, err
	}
	blockPath := filepath.Join(blocksDir, blockID+".blk")
	blockFile, err := os.OpenFile(blockPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	store := &Store{
		dir:         dir,
		blocksDir:   blocksDir,
		blockID:     blockID,
		blockPath:   blockPath,
		blockFile:   blockFile,
		blockOffset: HeaderLength,
		frameSize:   DefaultFrameSize,
	}
	if err := store.writeHeader(); err != nil {
		_ = blockFile.Close()
		return nil, err
	}
	if err := blockFile.Sync(); err != nil {
		_ = blockFile.Close()
		return nil, err
	}
	if err := syncDir(blocksDir); err != nil {
		_ = blockFile.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.blockFile == nil {
		return nil
	}
	err := s.blockFile.Close()
	s.blockFile = nil
	return err
}

func (s *Store) BlockPath(blockID string) string {
	return filepath.Join(s.blocksDir, blockID+".blk")
}

func (s *Store) Append(ctx context.Context, reader io.Reader) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.blockFile == nil {
		return Record{}, errors.New("blockstore: store is closed")
	}
	startOffset := s.blockOffset
	if _, err := s.blockFile.Seek(int64(startOffset), io.SeekStart); err != nil {
		return Record{}, err
	}

	committed := false
	defer func() {
		if !committed {
			_ = s.blockFile.Truncate(int64(startOffset))
			_, _ = s.blockFile.Seek(int64(startOffset), io.SeekStart)
		}
	}()

	hasher := sha256.New()
	frames := newFrameAccumulator(s.frameSize)
	var storedLength uint64
	buf := make([]byte, DefaultFrameSize)
	for {
		if err := ctx.Err(); err != nil {
			return Record{}, err
		}
		n, readErr := reader.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			written, err := s.blockFile.Write(chunk)
			if err != nil {
				return Record{}, err
			}
			if written != n {
				return Record{}, io.ErrShortWrite
			}
			if _, err := hasher.Write(chunk); err != nil {
				return Record{}, err
			}
			frames.Write(startOffset+storedLength, chunk)
			storedLength += uint64(n)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return Record{}, readErr
		}
	}
	if err := s.blockFile.Sync(); err != nil {
		return Record{}, err
	}

	record := Record{
		BlockID:      s.blockID,
		StoredOffset: startOffset,
		StoredLength: storedLength,
		Frames:       frames.Records(),
	}
	copy(record.LogicalSHA256[:], hasher.Sum(nil))
	s.blockOffset = startOffset + storedLength
	committed = true
	return record, nil
}

func (s *Store) ReadRange(ctx context.Context, record Record, offset uint64, length *uint64, writer io.Writer) error {
	if offset > record.StoredLength {
		return ErrInvalidRange
	}
	readLength := record.StoredLength - offset
	if length != nil {
		if *length > record.StoredLength-offset {
			return ErrInvalidRange
		}
		readLength = *length
	}
	if readLength == 0 {
		return nil
	}
	if err := s.verifyReadableRange(record, offset, readLength); err != nil {
		return err
	}
	file, err := os.Open(s.BlockPath(record.BlockID))
	if err != nil {
		return err
	}
	defer file.Close()

	buf := make([]byte, defaultReadBuffer)
	section := io.NewSectionReader(file, int64(record.StoredOffset+offset), int64(readLength))
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := section.Read(buf)
		if n > 0 {
			if _, err := writer.Write(buf[:n]); err != nil {
				return err
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func (s *Store) verifyReadableRange(record Record, offset uint64, length uint64) error {
	if offset+length < offset || offset+length > record.StoredLength {
		return ErrInvalidRange
	}
	file, err := os.Open(s.BlockPath(record.BlockID))
	if err != nil {
		return err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return err
	}
	if uint64(info.Size()) < record.StoredOffset+offset+length {
		return io.ErrUnexpectedEOF
	}
	if len(record.Frames) == 0 {
		return verifyWholeRecord(file, record)
	}
	return verifyFrameRange(file, record, offset, length)
}

func verifyWholeRecord(file *os.File, record Record) error {
	hasher := sha256.New()
	reader := io.NewSectionReader(file, int64(record.StoredOffset), int64(record.StoredLength))
	if _, err := io.Copy(hasher, reader); err != nil {
		return err
	}
	var got [32]byte
	copy(got[:], hasher.Sum(nil))
	if got != record.LogicalSHA256 {
		return ErrChecksumMismatch
	}
	return nil
}

func verifyFrameRange(file *os.File, record Record, offset uint64, length uint64) error {
	rangeStart := record.StoredOffset + offset
	rangeEnd := rangeStart + length
	coveredUntil := rangeStart
	for _, frame := range record.Frames {
		segmentStart := frame.SegmentOffset
		segmentEnd := frame.SegmentOffset + frame.SegmentLength
		if segmentEnd <= rangeStart {
			continue
		}
		if segmentStart >= rangeEnd {
			break
		}
		if segmentStart > coveredUntil {
			return io.ErrUnexpectedEOF
		}
		if err := verifyFrame(file, frame); err != nil {
			return err
		}
		if segmentEnd > coveredUntil {
			coveredUntil = segmentEnd
		}
		if coveredUntil >= rangeEnd {
			return nil
		}
	}
	return io.ErrUnexpectedEOF
}

func verifyFrame(file *os.File, frame FrameRecord) error {
	hasher := sha256.New()
	reader := io.NewSectionReader(file, int64(frame.SegmentOffset), int64(frame.SegmentLength))
	if _, err := io.Copy(hasher, reader); err != nil {
		return err
	}
	var got [32]byte
	copy(got[:], hasher.Sum(nil))
	if got != frame.SHA256 {
		return ErrChecksumMismatch
	}
	return nil
}

func (s *Store) writeHeader() error {
	header := make([]byte, HeaderLength)
	copy(header[0:8], "SCRAPBLK")
	binary.BigEndian.PutUint16(header[8:10], formatMajor)
	binary.BigEndian.PutUint16(header[10:12], formatMinor)
	binary.BigEndian.PutUint32(header[12:16], HeaderLength)
	blockBytes, err := parseUUIDBytes(s.blockID)
	if err != nil {
		return err
	}
	copy(header[16:32], blockBytes[:])
	binary.BigEndian.PutUint32(header[40:44], uint32(s.frameSize))
	if _, err := s.blockFile.WriteAt(header, 0); err != nil {
		return err
	}
	_, err = s.blockFile.Seek(HeaderLength, io.SeekStart)
	return err
}

func newUUIDv7() (string, error) {
	var b [16]byte
	now := uint64(time.Now().UnixMilli())
	b[0] = byte(now >> 40)
	b[1] = byte(now >> 32)
	b[2] = byte(now >> 24)
	b[3] = byte(now >> 16)
	b[4] = byte(now >> 8)
	b[5] = byte(now)
	if _, err := rand.Read(b[6:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x70
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func parseUUIDBytes(value string) ([16]byte, error) {
	var out [16]byte
	var compact [32]byte
	var n int
	for i := 0; i < len(value); i++ {
		if value[i] == '-' {
			continue
		}
		if n >= len(compact) {
			return out, fmt.Errorf("uuid has too many hex bytes: %q", value)
		}
		compact[n] = value[i]
		n++
	}
	if n != len(compact) {
		return out, fmt.Errorf("uuid has %d hex bytes, want 32", n)
	}
	for i := 0; i < len(out); i++ {
		hi, ok := hexValue(compact[i*2])
		if !ok {
			return out, fmt.Errorf("uuid contains non-hex byte: %q", value)
		}
		lo, ok := hexValue(compact[i*2+1])
		if !ok {
			return out, fmt.Errorf("uuid contains non-hex byte: %q", value)
		}
		out[i] = hi<<4 | lo
	}
	return out, nil
}

func hexValue(b byte) (byte, bool) {
	switch {
	case '0' <= b && b <= '9':
		return b - '0', true
	case 'a' <= b && b <= 'f':
		return b - 'a' + 10, true
	case 'A' <= b && b <= 'F':
		return b - 'A' + 10, true
	default:
		return 0, false
	}
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

type frameAccumulator struct {
	frameSize uint64
	frames    []pendingFrame
}

type pendingFrame struct {
	frameOffset   uint64
	segmentOffset uint64
	segmentLength uint64
	hasher        hash.Hash
}

func newFrameAccumulator(frameSize uint64) *frameAccumulator {
	return &frameAccumulator{frameSize: frameSize}
}

func (a *frameAccumulator) Write(offset uint64, data []byte) {
	for len(data) > 0 {
		frameOffset := frameStart(offset, a.frameSize)
		frameEnd := frameOffset + a.frameSize
		n := int(frameEnd - offset)
		if n > len(data) {
			n = len(data)
		}
		frame := a.current(frameOffset, offset)
		_, _ = frame.hasher.Write(data[:n])
		frame.segmentLength += uint64(n)
		offset += uint64(n)
		data = data[n:]
	}
}

func (a *frameAccumulator) current(frameOffset uint64, segmentOffset uint64) *pendingFrame {
	if len(a.frames) > 0 && a.frames[len(a.frames)-1].frameOffset == frameOffset {
		return &a.frames[len(a.frames)-1]
	}
	a.frames = append(a.frames, pendingFrame{
		frameOffset:   frameOffset,
		segmentOffset: segmentOffset,
		hasher:        sha256.New(),
	})
	return &a.frames[len(a.frames)-1]
}

func (a *frameAccumulator) Records() []FrameRecord {
	records := make([]FrameRecord, 0, len(a.frames))
	for _, frame := range a.frames {
		record := FrameRecord{
			FrameOffset:   frame.frameOffset,
			SegmentOffset: frame.segmentOffset,
			SegmentLength: frame.segmentLength,
		}
		copy(record.SHA256[:], frame.hasher.Sum(nil))
		records = append(records, record)
	}
	return records
}

func frameStart(offset uint64, frameSize uint64) uint64 {
	return HeaderLength + ((offset - HeaderLength) / frameSize * frameSize)
}
