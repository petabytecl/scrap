package blockstore

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/petabytecl/scrap/internal/closeutil"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/safeconv"
	"github.com/petabytecl/scrap/internal/safepath"
)

const (
	HeaderLength      = 64
	DefaultFrameSize  = 1024 * 1024
	formatMajor       = 0
	formatMinor       = 1
	defaultReadBuffer = 1024 * 1024
	blockFilePerm     = 0o600
)

var (
	ErrChecksumMismatch = errors.New("blockstore: checksum mismatch")
	ErrInvalidRange     = errors.New("blockstore: invalid range")
	ErrBlockOpen        = errors.New("blockstore: block is still open")
	ErrEmptyBlock       = errors.New("blockstore: cannot seal empty block")
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
	Replicas      []ReplicaRef
}

type FrameRecord struct {
	FrameOffset   uint64
	SegmentOffset uint64
	SegmentLength uint64
	SHA256        [32]byte
}

type ReplicaRef struct {
	MemberID     string
	BlockID      string
	StoredOffset uint64
	StoredLength uint64
	StoredSHA256 [32]byte
}

func Open(dir string) (*Store, error) {
	blocksDir := filepath.Join(dir, "blocks")
	if err := os.MkdirAll(blocksDir, 0o700); err != nil {
		return nil, err
	}

	blockID, blockPath, blockFile, err := createBlockFile(blocksDir, DefaultFrameSize)
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

func (s *Store) SealPath(blockID string) string {
	return filepath.Join(s.blocksDir, blockID+".sealed")
}

func (s *Store) IsSealed(blockID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.isSealedLocked(blockID)
}

func (s *Store) isSealedLocked(blockID string) (bool, error) {
	sealPath, err := s.validatedSealPath(blockID)
	if err != nil {
		return false, err
	}
	_, statErr := os.Stat(sealPath)
	if statErr == nil {
		return true, nil
	}
	if errors.Is(statErr, os.ErrNotExist) {
		return false, nil
	}
	return false, statErr
}

func (s *Store) CurrentBlockLength() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.blockOffset
}

func (s *Store) CurrentBlockID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.blockID
}

func (s *Store) SealCurrent(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.blockFile == nil {
		return "", errors.New("blockstore: store is closed")
	}
	if s.blockOffset == HeaderLength {
		return "", ErrEmptyBlock
	}
	sealedBlockID := s.blockID
	if err := s.blockFile.Sync(); err != nil {
		return "", err
	}
	if err := s.sealBlockFileLocked(sealedBlockID); err != nil {
		return "", err
	}
	if err := s.blockFile.Close(); err != nil {
		return "", err
	}

	blockID, blockPath, blockFile, err := createBlockFile(s.blocksDir, s.frameSize)
	if err != nil {
		s.blockFile = nil
		return "", err
	}
	if err := syncDir(s.blocksDir); err != nil {
		_ = blockFile.Close()
		s.blockFile = nil
		return "", err
	}
	s.blockID = blockID
	s.blockPath = blockPath
	s.blockFile = blockFile
	s.blockOffset = HeaderLength
	return sealedBlockID, nil
}

func (s *Store) SealBlock(ctx context.Context, blockID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if blockID == "" {
		return false, errors.New("blockstore: block id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.blockFile == nil {
		return false, errors.New("blockstore: store is closed")
	}
	if done, err := s.validateSealBlockLocked(blockID); done || err != nil {
		return false, err
	}
	if err := s.sealBlockFileLocked(blockID); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) validateSealBlockLocked(blockID string) (bool, error) {
	if blockID == s.blockID {
		return false, errors.New("blockstore: current block must be sealed with SealCurrent")
	}
	sealed, err := s.isSealedLocked(blockID)
	if err != nil || sealed {
		return sealed, err
	}
	blockPath, err := s.validatedBlockPath(blockID)
	if err != nil {
		return false, err
	}
	stat, err := os.Stat(blockPath)
	if err != nil {
		return false, err
	}
	if stat.Size() <= HeaderLength {
		return false, ErrEmptyBlock
	}
	return false, syncFile(blockPath)
}

func (s *Store) sealBlockFileLocked(blockID string) error {
	sealPath, err := s.validatedSealPath(blockID)
	if err != nil {
		return err
	}
	if err := writeSealMarker(sealPath); err != nil {
		return err
	}
	return syncDir(s.blocksDir)
}

func (s *Store) InstallSealedBlock(ctx context.Context, blockID string, expectedLength uint64, expectedSHA256 [32]byte, reader io.Reader) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateInstallReader(blockID, reader, "sealed block reader"); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	blockPath, err := s.validatedBlockPath(blockID)
	if err != nil {
		return err
	}
	sealPath, err := s.validatedSealPath(blockID)
	if err != nil {
		return err
	}
	if sealed, err := s.sealExistingBlock(ctx, blockPath, sealPath, expectedLength, expectedSHA256); sealed || err != nil {
		return err
	}
	return s.installSealedBlock(ctx, blockPath, sealPath, expectedLength, expectedSHA256, reader)
}

// ReplaceSealedBlock atomically installs a verified sealed block, preserving the
// current block until the replacement is fully written and checksum-verified.
func (s *Store) ReplaceSealedBlock(ctx context.Context, blockID string, expectedLength uint64, expectedSHA256 [32]byte, reader io.Reader) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateInstallReader(blockID, reader, "replacement block reader"); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	blockPath, err := s.validatedBlockPath(blockID)
	if err != nil {
		return err
	}
	sealPath, err := s.validatedSealPath(blockID)
	if err != nil {
		return err
	}
	return s.installSealedBlock(ctx, blockPath, sealPath, expectedLength, expectedSHA256, reader)
}

func validateInstallReader(blockID string, reader io.Reader, readerName string) error {
	if blockID == "" {
		return errors.New("blockstore: block id is required")
	}
	if reader == nil {
		return fmt.Errorf("blockstore: %s is required", readerName)
	}
	return nil
}

func (s *Store) sealExistingBlock(ctx context.Context, blockPath, sealPath string, expectedLength uint64, expectedSHA256 [32]byte) (bool, error) {
	err := s.verifyWholeFile(ctx, blockPath, expectedLength, expectedSHA256)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, s.writeSealMarkerAndSync(sealPath)
}

func (s *Store) installSealedBlock(ctx context.Context, blockPath, sealPath string, expectedLength uint64, expectedSHA256 [32]byte, reader io.Reader) error {
	temp, tempPath, err := s.createTempBlockFile("restore-*.blk.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = s.removeBlockStorePath(tempPath) }()
	written, sum, err := copyAndHash(ctx, temp, reader)
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if written != expectedLength || sum != expectedSHA256 {
		return ErrChecksumMismatch
	}
	if err := syncFile(tempPath); err != nil {
		return err
	}
	// #nosec G703 -- source and destination are validated under blocksDir.
	if err := os.Rename(tempPath, blockPath); err != nil {
		return err
	}
	return s.writeSealMarkerAndSync(sealPath)
}

func (s *Store) writeSealMarkerAndSync(sealPath string) error {
	if err := writeSealMarker(sealPath); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return syncDir(s.blocksDir)
}

func (s *Store) InstallVerifiedRange(ctx context.Context, record Record, expectedSHA256 [32]byte, reader io.Reader) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateInstallReader(record.BlockID, reader, "range reader"); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tempPath, err := s.writeVerifiedTempRange(ctx, record, expectedSHA256, reader)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = s.removeBlockStorePath(tempPath)
		}
	}()

	if err := s.installVerifiedTempRange(tempPath, record); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *Store) writeVerifiedTempRange(ctx context.Context, record Record, expectedSHA256 [32]byte, reader io.Reader) (string, error) {
	temp, tempPath, err := s.createTempBlockFile("repair-range-*.tmp")
	if err != nil {
		return "", err
	}
	written, sum, err := copyAndHash(ctx, temp, reader)
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = s.removeBlockStorePath(tempPath)
		return "", err
	}
	if written != record.StoredLength || sum != expectedSHA256 {
		_ = s.removeBlockStorePath(tempPath)
		return "", ErrChecksumMismatch
	}
	if err := verifyTempRange(tempPath, record); err != nil {
		_ = s.removeBlockStorePath(tempPath)
		return "", err
	}
	return tempPath, nil
}

func (s *Store) createTempBlockFile(pattern string) (*os.File, string, error) {
	temp, err := os.CreateTemp(s.blocksDir, pattern)
	if err != nil {
		return nil, "", err
	}
	tempPath, err := s.validatedBlockStorePath(temp.Name())
	if err != nil {
		return nil, "", errors.Join(err, temp.Close(), s.removeBlockStorePath(temp.Name()))
	}
	return temp, tempPath, nil
}

func (s *Store) installVerifiedTempRange(tempPath string, record Record) error {
	source, err := openValidatedFile(tempPath)
	if err != nil {
		return err
	}
	defer closeutil.Ignore(source)
	blockPath, err := s.validatedBlockPath(record.BlockID)
	if err != nil {
		return err
	}
	target, err := s.openRangeInstallTarget(blockPath, record.BlockID)
	if err != nil {
		return err
	}
	if err := writeRangeToTarget(target, source, record.StoredOffset); err != nil {
		return err
	}
	if err := s.verifyReadableRange(record, 0, record.StoredLength); err != nil {
		return err
	}
	return syncDir(s.blocksDir)
}

func (s *Store) openRangeInstallTarget(blockPath, blockID string) (*os.File, error) {
	target, err := openValidatedFileWithFlags(blockPath, os.O_CREATE|os.O_EXCL|os.O_RDWR)
	if errors.Is(err, os.ErrExist) {
		return openValidatedFileWithFlags(blockPath, os.O_RDWR)
	}
	if err != nil {
		return nil, err
	}
	if err := writeHeader(target, blockID, s.frameSize); err != nil {
		return nil, errors.Join(err, target.Close())
	}
	return target, nil
}

func writeRangeToTarget(target *os.File, source io.Reader, storedOffset uint64) error {
	storedOffsetInt, err := safeconv.Uint64ToInt64("stored offset", storedOffset)
	if err != nil {
		return errors.Join(err, target.Close())
	}
	if _, err := target.Seek(storedOffsetInt, io.SeekStart); err != nil {
		return errors.Join(err, target.Close())
	}
	if _, err := io.Copy(target, source); err != nil {
		return errors.Join(err, target.Close())
	}
	if err := target.Sync(); err != nil {
		return errors.Join(err, target.Close())
	}
	if err := target.Close(); err != nil {
		return err
	}
	return nil
}

func (s *Store) EnsureSealedBlock(ctx context.Context, blockID string, expectedLength uint64, expectedSHA256 [32]byte) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	blockPath, err := s.validatedBlockPath(blockID)
	if err != nil {
		return false, err
	}
	sealPath, err := s.validatedSealPath(blockID)
	if err != nil {
		return false, err
	}
	if err := s.verifyWholeFile(ctx, blockPath, expectedLength, expectedSHA256); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return s.ensureSealMarker(sealPath)
}

func (s *Store) ensureSealMarker(sealPath string) (bool, error) {
	_, statErr := os.Stat(sealPath)
	if statErr == nil {
		return true, nil
	}
	if !errors.Is(statErr, os.ErrNotExist) {
		return false, statErr
	}
	if err := writeSealMarker(sealPath); err != nil && !errors.Is(err, os.ErrExist) {
		return false, err
	}
	return true, syncDir(s.blocksDir)
}

func (s *Store) Append(ctx context.Context, reader io.Reader) (Record, error) {
	return s.AppendValidated(ctx, reader, nil)
}

func (s *Store) AppendValidated(ctx context.Context, reader io.Reader, validate func(Record) error) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.blockFile == nil {
		return Record{}, errors.New("blockstore: store is closed")
	}
	startOffset := s.blockOffset
	startOffsetInt, err := safeconv.Uint64ToInt64("start offset", startOffset)
	if err != nil {
		return Record{}, err
	}
	if _, err := s.blockFile.Seek(startOffsetInt, io.SeekStart); err != nil {
		return Record{}, err
	}

	committed := false
	defer func() {
		if !committed {
			_ = s.blockFile.Truncate(startOffsetInt)
			_, _ = s.blockFile.Seek(startOffsetInt, io.SeekStart)
		}
	}()

	appended, err := s.appendReader(ctx, reader, startOffset)
	if err != nil {
		return Record{}, err
	}
	if err := s.blockFile.Sync(); err != nil {
		return Record{}, err
	}

	record := Record{
		BlockID:      s.blockID,
		StoredOffset: startOffset,
		StoredLength: appended.storedLength,
		Frames:       appended.frames,
	}
	record.LogicalSHA256 = appended.logicalSHA256
	if validate != nil {
		if err := validate(record); err != nil {
			return Record{}, err
		}
	}
	nextOffset, err := addUint64("block offset", startOffset, appended.storedLength)
	if err != nil {
		return Record{}, err
	}
	s.blockOffset = nextOffset
	committed = true
	return record, nil
}

type appendResult struct {
	storedLength  uint64
	logicalSHA256 [32]byte
	frames        []FrameRecord
}

func (s *Store) appendReader(ctx context.Context, reader io.Reader, startOffset uint64) (appendResult, error) {
	accumulator := blockAppendAccumulator{
		file:        s.blockFile,
		hasher:      sha256.New(),
		frames:      newFrameAccumulator(s.frameSize),
		startOffset: startOffset,
	}
	buf := make([]byte, DefaultFrameSize)
	for {
		if err := ctx.Err(); err != nil {
			return appendResult{}, err
		}
		n, readErr := reader.Read(buf)
		if n > 0 {
			if err := accumulator.write(buf[:n]); err != nil {
				return appendResult{}, err
			}
		}
		if errors.Is(readErr, io.EOF) {
			return accumulator.result(), nil
		}
		if readErr != nil {
			return appendResult{}, readErr
		}
	}
}

type blockAppendAccumulator struct {
	file         *os.File
	hasher       hash.Hash
	frames       *frameAccumulator
	startOffset  uint64
	storedLength uint64
}

func (a *blockAppendAccumulator) write(chunk []byte) error {
	written, err := a.file.Write(chunk)
	if err != nil {
		return err
	}
	if written != len(chunk) {
		return io.ErrShortWrite
	}
	if _, err := a.hasher.Write(chunk); err != nil {
		return err
	}
	chunkLength, err := safeconv.IntToUint64("append chunk length", len(chunk))
	if err != nil {
		return err
	}
	segmentOffset, err := addUint64("append segment offset", a.startOffset, a.storedLength)
	if err != nil {
		return err
	}
	if err := a.frames.Write(segmentOffset, chunk); err != nil {
		return err
	}
	a.storedLength, err = addUint64("stored length", a.storedLength, chunkLength)
	return err
}

func (a *blockAppendAccumulator) result() appendResult {
	var sum [32]byte
	copy(sum[:], a.hasher.Sum(nil))
	return appendResult{
		storedLength:  a.storedLength,
		logicalSHA256: sum,
		frames:        a.frames.Records(),
	}
}

func (s *Store) ReadRange(ctx context.Context, record Record, offset uint64, length *uint64, writer io.Writer) error {
	readLength, err := normalizeRange(record, offset, length)
	if err != nil {
		return err
	}
	if readLength == 0 {
		return nil
	}
	if err := s.verifyReadableRange(record, offset, readLength); err != nil {
		return err
	}
	section, err := s.openReadRangeSection(record, offset, readLength)
	if err != nil {
		return err
	}
	return copyReadRange(ctx, writer, section)
}

type readRangeSection struct {
	file   *os.File
	reader *io.SectionReader
}

func (s *Store) openReadRangeSection(record Record, offset, readLength uint64) (readRangeSection, error) {
	blockPath, err := s.validatedBlockPath(record.BlockID)
	if err != nil {
		return readRangeSection{}, err
	}
	file, err := openValidatedFile(blockPath)
	if err != nil {
		return readRangeSection{}, err
	}
	readOffset, err := addUint64("read range offset", record.StoredOffset, offset)
	if err != nil {
		_ = file.Close()
		return readRangeSection{}, ErrInvalidRange
	}
	readOffsetInt, err := safeconv.Uint64ToInt64("read range offset", readOffset)
	if err != nil {
		_ = file.Close()
		return readRangeSection{}, ErrInvalidRange
	}
	readLengthInt, err := safeconv.Uint64ToInt64("read range length", readLength)
	if err != nil {
		_ = file.Close()
		return readRangeSection{}, ErrInvalidRange
	}
	return readRangeSection{
		file:   file,
		reader: io.NewSectionReader(file, readOffsetInt, readLengthInt),
	}, nil
}

func copyReadRange(ctx context.Context, writer io.Writer, section readRangeSection) error {
	defer closeutil.Ignore(section.file)
	buf := make([]byte, defaultReadBuffer)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := section.reader.Read(buf)
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

func (s *Store) VerifyRange(record Record, offset uint64, length *uint64) error {
	readLength, err := normalizeRange(record, offset, length)
	if err != nil {
		return err
	}
	if readLength == 0 {
		return nil
	}
	return s.verifyReadableRange(record, offset, readLength)
}

func (s *Store) verifyReadableRange(record Record, offset, length uint64) error {
	if offset+length < offset || offset+length > record.StoredLength {
		return ErrInvalidRange
	}
	file, err := s.openReadableRangeFile(record, offset, length)
	if err != nil {
		return err
	}
	defer closeutil.Ignore(file)
	if len(record.Frames) == 0 {
		return verifyWholeRecord(file, record)
	}
	return verifyFrameRange(file, record, offset, length)
}

func (s *Store) openReadableRangeFile(record Record, offset, length uint64) (*os.File, error) {
	blockPath, err := s.validatedBlockPath(record.BlockID)
	if err != nil {
		return nil, err
	}
	file, err := openValidatedFile(blockPath)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		return nil, errors.Join(err, file.Close())
	}
	size, err := safeconv.Int64ToUint64("block file size", info.Size())
	if err != nil {
		return nil, errors.Join(err, file.Close())
	}
	rangeStart, err := addUint64("block readable range start", record.StoredOffset, offset)
	if err != nil {
		return nil, errors.Join(ErrInvalidRange, file.Close())
	}
	rangeEnd, err := addUint64("block readable range end", rangeStart, length)
	if err != nil {
		return nil, errors.Join(ErrInvalidRange, file.Close())
	}
	if size < rangeEnd {
		return nil, errors.Join(io.ErrUnexpectedEOF, file.Close())
	}
	return file, nil
}

func (s *Store) verifyWholeFile(ctx context.Context, path string, expectedLength uint64, expectedSHA256 [32]byte) error {
	file, err := openValidatedFile(path)
	if err != nil {
		return err
	}
	defer closeutil.Ignore(file)
	written, sum, err := copyAndHash(ctx, io.Discard, file)
	if err != nil {
		return err
	}
	if written != expectedLength || sum != expectedSHA256 {
		return ErrChecksumMismatch
	}
	return nil
}

func copyAndHash(ctx context.Context, writer io.Writer, reader io.Reader) (uint64, [32]byte, error) {
	hasher := sha256.New()
	multi := io.MultiWriter(writer, hasher)
	buf := make([]byte, defaultReadBuffer)
	var written uint64
	for {
		if err := ctx.Err(); err != nil {
			return 0, [32]byte{}, err
		}
		n, readErr := reader.Read(buf)
		if n > 0 {
			var err error
			written, err = copyHashChunk(multi, buf[:n], written)
			if err != nil {
				return 0, [32]byte{}, err
			}
		}
		if errors.Is(readErr, io.EOF) {
			var sum [32]byte
			copy(sum[:], hasher.Sum(nil))
			return written, sum, nil
		}
		if readErr != nil {
			return 0, [32]byte{}, readErr
		}
	}
}

func copyHashChunk(writer io.Writer, chunk []byte, written uint64) (uint64, error) {
	copied, err := writer.Write(chunk)
	if err != nil {
		return 0, err
	}
	if copied != len(chunk) {
		return 0, io.ErrShortWrite
	}
	chunkLength, err := safeconv.IntToUint64("copied chunk length", len(chunk))
	if err != nil {
		return 0, err
	}
	return addUint64("copied byte count", written, chunkLength)
}

func normalizeRange(record Record, offset uint64, length *uint64) (uint64, error) {
	if offset > record.StoredLength {
		return 0, ErrInvalidRange
	}
	readLength := record.StoredLength - offset
	if length != nil {
		if *length > record.StoredLength-offset {
			return 0, ErrInvalidRange
		}
		readLength = *length
	}
	return readLength, nil
}

func verifyWholeRecord(file *os.File, record Record) error {
	hasher := sha256.New()
	storedOffset, err := safeconv.Uint64ToInt64("stored offset", record.StoredOffset)
	if err != nil {
		return ErrInvalidRange
	}
	storedLength, err := safeconv.Uint64ToInt64("stored length", record.StoredLength)
	if err != nil {
		return ErrInvalidRange
	}
	reader := io.NewSectionReader(file, storedOffset, storedLength)
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

func verifyFrameRange(file *os.File, record Record, offset, length uint64) error {
	frameRange, err := newFrameRange(record, offset, length)
	if err != nil {
		return ErrInvalidRange
	}
	coveredUntil := frameRange.start
	for _, frame := range record.Frames {
		complete, nextCoveredUntil, err := verifyRangeFrame(file, frameRange, coveredUntil, frame)
		if err != nil {
			return err
		}
		coveredUntil = nextCoveredUntil
		if complete {
			return nil
		}
	}
	return io.ErrUnexpectedEOF
}

type verificationFrameRange struct {
	start uint64
	end   uint64
}

func newFrameRange(record Record, offset, length uint64) (verificationFrameRange, error) {
	rangeStart, err := addUint64("frame range start", record.StoredOffset, offset)
	if err != nil {
		return verificationFrameRange{}, err
	}
	rangeEnd, err := addUint64("frame range end", rangeStart, length)
	if err != nil {
		return verificationFrameRange{}, err
	}
	return verificationFrameRange{start: rangeStart, end: rangeEnd}, nil
}

func verifyRangeFrame(file *os.File, frameRange verificationFrameRange, coveredUntil uint64, frame FrameRecord) (bool, uint64, error) {
	segmentEnd, err := addUint64("frame segment end", frame.SegmentOffset, frame.SegmentLength)
	if err != nil {
		return false, coveredUntil, ErrInvalidRange
	}
	if segmentEnd <= frameRange.start {
		return false, coveredUntil, nil
	}
	if frame.SegmentOffset >= frameRange.end || frame.SegmentOffset > coveredUntil {
		return false, coveredUntil, io.ErrUnexpectedEOF
	}
	if err := verifyFrame(file, frame); err != nil {
		return false, coveredUntil, err
	}
	if segmentEnd > coveredUntil {
		coveredUntil = segmentEnd
	}
	return coveredUntil >= frameRange.end, coveredUntil, nil
}

func verifyFrame(file *os.File, frame FrameRecord) error {
	hasher := sha256.New()
	segmentOffset, err := safeconv.Uint64ToInt64("frame segment offset", frame.SegmentOffset)
	if err != nil {
		return ErrInvalidRange
	}
	segmentLength, err := safeconv.Uint64ToInt64("frame segment length", frame.SegmentLength)
	if err != nil {
		return ErrInvalidRange
	}
	reader := io.NewSectionReader(file, segmentOffset, segmentLength)
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

func verifyTempRange(path string, record Record) error {
	file, err := openValidatedFile(path)
	if err != nil {
		return err
	}
	defer closeutil.Ignore(file)
	for _, frame := range record.Frames {
		if frame.SegmentOffset < record.StoredOffset {
			return ErrInvalidRange
		}
		relativeOffset := frame.SegmentOffset - record.StoredOffset
		relativeEnd, err := addUint64("temp frame relative end", relativeOffset, frame.SegmentLength)
		if err != nil || relativeEnd > record.StoredLength {
			return ErrInvalidRange
		}
		tempFrame := frame
		tempFrame.FrameOffset = 0
		tempFrame.SegmentOffset = relativeOffset
		if err := verifyFrame(file, tempFrame); err != nil {
			return err
		}
	}
	return nil
}

func createBlockFile(blocksDir string, frameSize uint64) (string, string, *os.File, error) {
	blockID, err := identity.NewUUIDv7()
	if err != nil {
		return "", "", nil, err
	}
	blockPath, err := safepath.UnderDir(blocksDir, filepath.Join(blocksDir, blockID+".blk"))
	if err != nil {
		return "", "", nil, err
	}
	blockFile, err := openValidatedFileWithFlags(blockPath, os.O_CREATE|os.O_EXCL|os.O_RDWR)
	if err != nil {
		return "", "", nil, err
	}
	if err := writeHeader(blockFile, blockID, frameSize); err != nil {
		_ = blockFile.Close()
		return "", "", nil, err
	}
	if err := blockFile.Sync(); err != nil {
		_ = blockFile.Close()
		return "", "", nil, err
	}
	return blockID, blockPath, blockFile, nil
}

func writeHeader(blockFile *os.File, blockID string, frameSize uint64) error {
	header := make([]byte, HeaderLength)
	copy(header[0:8], "SCRAPBLK")
	binary.BigEndian.PutUint16(header[8:10], formatMajor)
	binary.BigEndian.PutUint16(header[10:12], formatMinor)
	binary.BigEndian.PutUint32(header[12:16], HeaderLength)
	blockBytes, err := identity.UUIDBytes(blockID)
	if err != nil {
		return err
	}
	copy(header[16:32], blockBytes[:])
	frameSize32, err := safeconv.Uint64ToUint32("frame size", frameSize)
	if err != nil {
		return err
	}
	binary.BigEndian.PutUint32(header[40:44], frameSize32)
	if _, err := blockFile.WriteAt(header, 0); err != nil {
		return err
	}
	_, err = blockFile.Seek(HeaderLength, io.SeekStart)
	return err
}

func writeSealMarker(path string) error {
	file, err := openValidatedFileWithFlags(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := file.WriteString("sealed\n"); err != nil {
		return errors.Join(err, file.Close())
	}
	return errors.Join(file.Sync(), file.Close())
}

func syncFile(path string) error {
	// #nosec G304 G703 -- callers pass paths validated under the configured block directory.
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}

func syncDir(path string) error {
	// #nosec G304 -- callers pass configured storage directories.
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

func (a *frameAccumulator) Write(offset uint64, data []byte) error {
	for len(data) > 0 {
		n, segmentLength, err := frameChunk(offset, len(data), a.frameSize)
		if err != nil {
			return err
		}
		if err := a.writeChunk(offset, data[:n], segmentLength); err != nil {
			return err
		}
		offset, err = nextFrameOffset(offset, segmentLength)
		if err != nil {
			return err
		}
		data = data[n:]
	}
	return nil
}

func frameChunk(offset uint64, dataLen int, frameSize uint64) (int, uint64, error) {
	frameOffset := frameStart(offset, frameSize)
	frameEnd, err := addUint64("frame end", frameOffset, frameSize)
	if err != nil {
		return 0, 0, err
	}
	dataLength, err := safeconv.IntToUint64("frame data length", dataLen)
	if err != nil {
		return 0, 0, err
	}
	remaining := frameEnd - offset
	if remaining >= dataLength {
		return dataLen, dataLength, nil
	}
	n, err := safeconv.Uint64ToInt("frame remaining length", remaining)
	if err != nil {
		return 0, 0, err
	}
	return n, remaining, nil
}

func (a *frameAccumulator) writeChunk(offset uint64, data []byte, segmentLength uint64) error {
	frame := a.current(frameStart(offset, a.frameSize), offset)
	_, _ = frame.hasher.Write(data)
	nextLength, err := addUint64("frame segment length", frame.segmentLength, segmentLength)
	if err != nil {
		return err
	}
	frame.segmentLength = nextLength
	return nil
}

func nextFrameOffset(offset, segmentLength uint64) (uint64, error) {
	return addUint64("frame offset", offset, segmentLength)
}

func (a *frameAccumulator) current(frameOffset, segmentOffset uint64) *pendingFrame {
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

func frameStart(offset, frameSize uint64) uint64 {
	return HeaderLength + ((offset - HeaderLength) / frameSize * frameSize)
}

func (s *Store) validatedBlockPath(blockID string) (string, error) {
	return s.validatedBlockStorePath(s.BlockPath(blockID))
}

func (s *Store) validatedSealPath(blockID string) (string, error) {
	return s.validatedBlockStorePath(s.SealPath(blockID))
}

func (s *Store) validatedBlockStorePath(path string) (string, error) {
	return safepath.UnderDir(s.blocksDir, path)
}

func openValidatedFile(path string) (*os.File, error) {
	// #nosec G304 G703 -- callers validate paths under the configured storage directory.
	return os.Open(path)
}

func openValidatedFileWithFlags(path string, flag int) (*os.File, error) {
	// #nosec G304 G703 -- callers validate paths under the configured storage directory.
	return os.OpenFile(path, flag, blockFilePerm)
}

func (s *Store) removeBlockStorePath(path string) error {
	path, err := s.validatedBlockStorePath(path)
	if err != nil {
		return err
	}
	// #nosec G703 -- path is validated under blocksDir before removal.
	return os.Remove(path)
}

func addUint64(name string, left, right uint64) (uint64, error) {
	value := left + right
	if value < left {
		return 0, fmt.Errorf("blockstore: %s overflows uint64", name)
	}
	return value, nil
}
