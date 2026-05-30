package block

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"time"
)

const (
	idxMagic          = "SIDX"
	idxVersion        = 1
	idxHeaderLen      = 12
	idxEntryVersion   = 0x01
	idxMinEntryLen    = 2  // version + reserved
	idxLenPrefixSize  = 2  // uint16 length prefix for strings
	idxFixedFieldSize = 60 // created_at(8) + first_frame_off(8) + frame_count(4) + total_bytes(8) + sha256(32)

	sha256Size = 32 // SHA-256 digest length in bytes
	uint64Size = 8  // encoded uint64 length in bytes
	uint32Size = 4  // encoded uint32 length in bytes
	uint16Size = 2  // encoded uint16 length in bytes

	paddingByte = 0x00 // reserved byte in index entry header
)

var (
	ErrDocNotFound = errors.New("block: document not found in index")
	ErrIdxCorrupt  = errors.New("block: index file corrupt")
)

type IndexEntry struct {
	TransactionID string
	DocName       string
	ContentType   string
	CreatedAt     time.Time
	FirstFrameOff int64
	FrameCount    uint32
	TotalBytes    int64
	SHA256        [32]byte
}

type IndexWriter struct {
	f *os.File
}

// NewIndexWriter creates a CRC-protected .idx file.
// Header: magic(4)="SIDX" + version(2) + header_len(2) + header_crc32c(4)
func NewIndexWriter(path string) (*IndexWriter, error) {
	f, err := os.Create(path) //nolint:gosec // path is constructed by caller from controlled shard/block IDs
	if err != nil {
		return nil, fmt.Errorf("block: create index %s: %w", path, err)
	}

	var hdr [idxHeaderLen]byte
	copy(hdr[0:4], idxMagic)
	binary.LittleEndian.PutUint16(hdr[4:6], idxVersion)
	binary.LittleEndian.PutUint16(hdr[6:8], idxHeaderLen)
	binary.LittleEndian.PutUint32(hdr[8:12], crc32.Checksum(hdr[0:8], crcTable))

	if _, err := f.Write(hdr[:]); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("block: write index header: %w", err)
	}

	return &IndexWriter{f: f}, nil
}

func OpenIndexWriter(path string) (*IndexWriter, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0) //nolint:gosec // path is constructed by caller from controlled shard/block IDs
	if err != nil {
		return nil, fmt.Errorf("block: open index writer %s: %w", path, err)
	}
	if err := validateIndexHeader(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("block: seek index end %s: %w", path, err)
	}
	return &IndexWriter{f: f}, nil
}

func RepairIndexTail(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0) //nolint:gosec // path is constructed by caller from controlled shard/block IDs
	if err != nil {
		return fmt.Errorf("block: open index repair %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	if err := validateIndexHeader(f); err != nil {
		return err
	}
	return repairIndexTail(f, path)
}

func repairIndexTail(f *os.File, path string) error {
	for {
		validEnd, err := f.Seek(0, io.SeekCurrent)
		if err != nil {
			return fmt.Errorf("block: repair index seek %s: %w", path, err)
		}
		_, done, err := readNextEntry(f)
		if done {
			return nil
		}
		if err != nil {
			return truncateIncompleteIndexTail(f, path, validEnd, err)
		}
	}
}

func truncateIncompleteIndexTail(f *os.File, path string, validEnd int64, readErr error) error {
	if !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return readErr
	}
	if err := f.Truncate(validEnd); err != nil {
		return fmt.Errorf("block: truncate torn index tail %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("block: fsync repaired index %s: %w", path, err)
	}
	return nil
}

// Append writes a CRC-protected entry: entry_len(4) + payload + entry_crc32c(4)
func (w *IndexWriter) Append(e IndexEntry) error {
	payload := encodeIndexEntry(e)

	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(payload))) //nolint:gosec // index entries are small, well under uint32 max
	if _, err := w.f.Write(lenBuf[:]); err != nil {
		return err
	}

	if _, err := w.f.Write(payload); err != nil {
		return err
	}

	var crcBuf [4]byte
	binary.LittleEndian.PutUint32(crcBuf[:], crc32.Checksum(payload, crcTable))
	if _, err := w.f.Write(crcBuf[:]); err != nil {
		return err
	}
	if err := w.f.Sync(); err != nil {
		return fmt.Errorf("block: fsync index entry: %w", err)
	}
	return nil
}

func (w *IndexWriter) Close() error {
	if err := w.f.Sync(); err != nil {
		_ = w.f.Close()
		return err
	}
	return w.f.Close()
}

type IndexReader struct {
	entries []IndexEntry
	f       *os.File
}

func OpenIndexReader(path string) (*IndexReader, error) {
	f, err := os.Open(path) //nolint:gosec // path is constructed by caller from controlled shard/block IDs
	if err != nil {
		return nil, fmt.Errorf("block: open index %s: %w", path, err)
	}

	entries, err := readIndexEntries(f)
	if err != nil {
		_ = f.Close()
		return nil, err
	}

	return &IndexReader{entries: entries, f: f}, nil
}

func readIndexEntries(f *os.File) ([]IndexEntry, error) {
	if err := validateIndexHeader(f); err != nil {
		return nil, err
	}

	var entries []IndexEntry
	for {
		entry, done, err := readNextEntry(f)
		if err != nil {
			return nil, err
		}
		if done {
			break
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func validateIndexHeader(f *os.File) error {
	var hdr [idxHeaderLen]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return fmt.Errorf("%w: read header: %w", ErrIdxCorrupt, err)
	}
	if string(hdr[0:4]) != idxMagic {
		return fmt.Errorf("%w: invalid magic: %q", ErrIdxCorrupt, hdr[0:4])
	}
	headerCRC := binary.LittleEndian.Uint32(hdr[8:12])
	if crc32.Checksum(hdr[0:8], crcTable) != headerCRC {
		return fmt.Errorf("%w: header CRC mismatch", ErrIdxCorrupt)
	}
	return nil
}

func readNextEntry(f *os.File) (IndexEntry, bool, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(f, lenBuf[:]); err != nil {
		if errors.Is(err, io.EOF) {
			return IndexEntry{}, true, nil
		}
		return IndexEntry{}, false, fmt.Errorf("%w: read entry len: %w", ErrIdxCorrupt, err)
	}

	payloadLen := binary.LittleEndian.Uint32(lenBuf[:])
	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(f, payload); err != nil {
		return IndexEntry{}, false, fmt.Errorf("%w: read entry payload: %w", ErrIdxCorrupt, err)
	}

	var crcBuf [4]byte
	if _, err := io.ReadFull(f, crcBuf[:]); err != nil {
		return IndexEntry{}, false, fmt.Errorf("%w: read entry CRC: %w", ErrIdxCorrupt, err)
	}

	expectedCRC := binary.LittleEndian.Uint32(crcBuf[:])
	if crc32.Checksum(payload, crcTable) != expectedCRC {
		return IndexEntry{}, false, fmt.Errorf("%w: entry CRC mismatch", ErrIdxCorrupt)
	}

	entry, err := decodeIndexEntry(payload)
	if err != nil {
		return IndexEntry{}, false, fmt.Errorf("%w: decode entry: %w", ErrIdxCorrupt, err)
	}

	return entry, false, nil
}

func (r *IndexReader) Find(txID, docName string) (IndexEntry, error) {
	for _, e := range r.entries {
		if e.TransactionID == txID && e.DocName == docName {
			return e, nil
		}
	}
	return IndexEntry{}, ErrDocNotFound
}

func (r *IndexReader) FindByTransaction(txID string) []IndexEntry {
	var result []IndexEntry
	for _, e := range r.entries {
		if e.TransactionID == txID {
			result = append(result, e)
		}
	}
	return result
}

func (r *IndexReader) Entries() []IndexEntry {
	return r.entries
}

func (r *IndexReader) Close() error {
	return r.f.Close()
}

func encodeIndexEntry(e IndexEntry) []byte {
	// version(1) + reserved(1) + fields
	txIDBytes := []byte(e.TransactionID)
	docNameBytes := []byte(e.DocName)
	ctBytes := []byte(e.ContentType)

	size := idxMinEntryLen + (uint16Size + len(txIDBytes)) + (uint16Size + len(docNameBytes)) + (uint16Size + len(ctBytes)) + uint64Size + uint64Size + uint32Size + uint64Size + sha256Size
	buf := make([]byte, 0, size)

	buf = append(buf, idxEntryVersion) // version
	buf = append(buf, paddingByte)     // reserved

	buf = appendLenPrefixed(buf, txIDBytes)
	buf = appendLenPrefixed(buf, docNameBytes)
	buf = appendLenPrefixed(buf, ctBytes)

	var fixed [idxFixedFieldSize]byte
	binary.LittleEndian.PutUint64(fixed[0:8], uint64(e.CreatedAt.UnixMicro()))
	binary.LittleEndian.PutUint64(fixed[8:16], uint64(e.FirstFrameOff)) //nolint:gosec // frame offsets are non-negative in valid blocks
	binary.LittleEndian.PutUint32(fixed[16:20], e.FrameCount)
	binary.LittleEndian.PutUint64(fixed[20:28], uint64(e.TotalBytes)) //nolint:gosec // total bytes are non-negative in valid blocks
	copy(fixed[28:idxFixedFieldSize], e.SHA256[:])

	buf = append(buf, fixed[:idxFixedFieldSize]...)
	return buf
}

func decodeIndexEntry(data []byte) (IndexEntry, error) {
	if len(data) < idxMinEntryLen {
		return IndexEntry{}, errors.New("entry too short")
	}
	if data[0] != idxEntryVersion {
		return IndexEntry{}, fmt.Errorf("unknown entry version: %d", data[0])
	}

	off := idxMinEntryLen
	txID, n, err := readLenPrefixed(data[off:])
	if err != nil {
		return IndexEntry{}, err
	}
	off += n

	docName, n, err := readLenPrefixed(data[off:])
	if err != nil {
		return IndexEntry{}, err
	}
	off += n

	contentType, n, err := readLenPrefixed(data[off:])
	if err != nil {
		return IndexEntry{}, err
	}
	off += n

	if len(data[off:]) < idxFixedFieldSize {
		return IndexEntry{}, errors.New("entry fixed fields truncated")
	}

	createdAt := time.UnixMicro(int64(binary.LittleEndian.Uint64(data[off : off+8]))) //nolint:gosec // stored as uint64, safe round-trip for valid timestamps
	firstFrameOff := int64(binary.LittleEndian.Uint64(data[off+8 : off+16]))          //nolint:gosec // stored as uint64, safe round-trip for valid frame offsets
	frameCount := binary.LittleEndian.Uint32(data[off+16 : off+20])
	totalBytes := int64(binary.LittleEndian.Uint64(data[off+20 : off+28])) //nolint:gosec // stored as uint64, safe round-trip for valid byte counts
	var sha [32]byte
	copy(sha[:], data[off+28:off+idxFixedFieldSize])

	return IndexEntry{
		TransactionID: txID,
		DocName:       docName,
		ContentType:   contentType,
		CreatedAt:     createdAt,
		FirstFrameOff: firstFrameOff,
		FrameCount:    frameCount,
		TotalBytes:    totalBytes,
		SHA256:        sha,
	}, nil
}

func appendLenPrefixed(buf, data []byte) []byte {
	var lenBuf [idxLenPrefixSize]byte
	binary.LittleEndian.PutUint16(lenBuf[:], uint16(len(data))) //nolint:gosec // index string fields (txID, docName, contentType) are well under 64 KiB
	buf = append(buf, lenBuf[:]...)
	buf = append(buf, data...)
	return buf
}

func readLenPrefixed(data []byte) (string, int, error) {
	if len(data) < idxLenPrefixSize {
		return "", 0, errors.New("missing length prefix")
	}
	n := int(binary.LittleEndian.Uint16(data[0:idxLenPrefixSize]))
	if len(data) < idxLenPrefixSize+n {
		return "", 0, fmt.Errorf("truncated string: need %d, have %d", n, len(data)-idxLenPrefixSize)
	}
	return string(data[idxLenPrefixSize : idxLenPrefixSize+n]), idxLenPrefixSize + n, nil
}
