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
	idxMagic     = "SIDX"
	idxVersion   = 1
	idxHeaderLen = 12
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
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("block: create index %s: %w", path, err)
	}

	var hdr [idxHeaderLen]byte
	copy(hdr[0:4], idxMagic)
	binary.LittleEndian.PutUint16(hdr[4:6], idxVersion)
	binary.LittleEndian.PutUint16(hdr[6:8], idxHeaderLen)
	binary.LittleEndian.PutUint32(hdr[8:12], crc32.Checksum(hdr[0:8], crcTable))

	if _, err := f.Write(hdr[:]); err != nil {
		f.Close()
		return nil, fmt.Errorf("block: write index header: %w", err)
	}

	return &IndexWriter{f: f}, nil
}

// Append writes a CRC-protected entry: entry_len(4) + payload + entry_crc32c(4)
func (w *IndexWriter) Append(e IndexEntry) error {
	payload := encodeIndexEntry(e)

	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(payload)))
	if _, err := w.f.Write(lenBuf[:]); err != nil {
		return err
	}

	if _, err := w.f.Write(payload); err != nil {
		return err
	}

	var crcBuf [4]byte
	binary.LittleEndian.PutUint32(crcBuf[:], crc32.Checksum(payload, crcTable))
	_, err := w.f.Write(crcBuf[:])
	return err
}

func (w *IndexWriter) Close() error {
	if err := w.f.Sync(); err != nil {
		w.f.Close()
		return err
	}
	return w.f.Close()
}

type IndexReader struct {
	entries []IndexEntry
	f       *os.File
}

func OpenIndexReader(path string) (*IndexReader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("block: open index %s: %w", path, err)
	}

	var hdr [idxHeaderLen]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		f.Close()
		return nil, fmt.Errorf("%w: read header: %v", ErrIdxCorrupt, err)
	}
	if string(hdr[0:4]) != idxMagic {
		f.Close()
		return nil, fmt.Errorf("%w: invalid magic: %q", ErrIdxCorrupt, hdr[0:4])
	}
	headerCRC := binary.LittleEndian.Uint32(hdr[8:12])
	if crc32.Checksum(hdr[0:8], crcTable) != headerCRC {
		f.Close()
		return nil, fmt.Errorf("%w: header CRC mismatch", ErrIdxCorrupt)
	}

	var entries []IndexEntry
	for {
		var lenBuf [4]byte
		if _, err := io.ReadFull(f, lenBuf[:]); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			f.Close()
			return nil, fmt.Errorf("%w: read entry len: %v", ErrIdxCorrupt, err)
		}

		payloadLen := binary.LittleEndian.Uint32(lenBuf[:])
		payload := make([]byte, payloadLen)
		if _, err := io.ReadFull(f, payload); err != nil {
			f.Close()
			return nil, fmt.Errorf("%w: read entry payload: %v", ErrIdxCorrupt, err)
		}

		var crcBuf [4]byte
		if _, err := io.ReadFull(f, crcBuf[:]); err != nil {
			f.Close()
			return nil, fmt.Errorf("%w: read entry CRC: %v", ErrIdxCorrupt, err)
		}

		expectedCRC := binary.LittleEndian.Uint32(crcBuf[:])
		if crc32.Checksum(payload, crcTable) != expectedCRC {
			f.Close()
			return nil, fmt.Errorf("%w: entry CRC mismatch", ErrIdxCorrupt)
		}

		entry, err := decodeIndexEntry(payload)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("%w: decode entry: %v", ErrIdxCorrupt, err)
		}

		entries = append(entries, entry)
	}

	return &IndexReader{entries: entries, f: f}, nil
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

func (r *IndexReader) Close() error {
	return r.f.Close()
}

func encodeIndexEntry(e IndexEntry) []byte {
	// version(1) + reserved(1) + fields
	txIDBytes := []byte(e.TransactionID)
	docNameBytes := []byte(e.DocName)
	ctBytes := []byte(e.ContentType)

	size := 2 + (2 + len(txIDBytes)) + (2 + len(docNameBytes)) + (2 + len(ctBytes)) + 8 + 8 + 4 + 8 + 32
	buf := make([]byte, 0, size)

	buf = append(buf, 0x01) // version
	buf = append(buf, 0x00) // reserved

	buf = appendLenPrefixed(buf, txIDBytes)
	buf = appendLenPrefixed(buf, docNameBytes)
	buf = appendLenPrefixed(buf, ctBytes)

	var fixed [60]byte
	binary.LittleEndian.PutUint64(fixed[0:8], uint64(e.CreatedAt.UnixMicro()))
	binary.LittleEndian.PutUint64(fixed[8:16], uint64(e.FirstFrameOff))
	binary.LittleEndian.PutUint32(fixed[16:20], e.FrameCount)
	binary.LittleEndian.PutUint64(fixed[20:28], uint64(e.TotalBytes))
	copy(fixed[28:60], e.SHA256[:])

	buf = append(buf, fixed[:60]...)
	return buf
}

func decodeIndexEntry(data []byte) (IndexEntry, error) {
	if len(data) < 2 {
		return IndexEntry{}, fmt.Errorf("entry too short")
	}
	if data[0] != 0x01 {
		return IndexEntry{}, fmt.Errorf("unknown entry version: %d", data[0])
	}

	off := 2
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

	if len(data[off:]) < 60 {
		return IndexEntry{}, fmt.Errorf("entry fixed fields truncated")
	}

	createdAt := time.UnixMicro(int64(binary.LittleEndian.Uint64(data[off : off+8])))
	firstFrameOff := int64(binary.LittleEndian.Uint64(data[off+8 : off+16]))
	frameCount := binary.LittleEndian.Uint32(data[off+16 : off+20])
	totalBytes := int64(binary.LittleEndian.Uint64(data[off+20 : off+28]))
	var sha [32]byte
	copy(sha[:], data[off+28:off+60])

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
	var lenBuf [2]byte
	binary.LittleEndian.PutUint16(lenBuf[:], uint16(len(data)))
	buf = append(buf, lenBuf[:]...)
	buf = append(buf, data...)
	return buf
}

func readLenPrefixed(data []byte) (string, int, error) {
	if len(data) < 2 {
		return "", 0, fmt.Errorf("missing length prefix")
	}
	n := int(binary.LittleEndian.Uint16(data[0:2]))
	if len(data) < 2+n {
		return "", 0, fmt.Errorf("truncated string: need %d, have %d", n, len(data)-2)
	}
	return string(data[2 : 2+n]), 2 + n, nil
}
