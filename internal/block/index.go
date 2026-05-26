package block

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

const (
	idxMagic   = "SIDX"
	idxVersion = 1
)

var ErrDocNotFound = errors.New("block: document not found in index")

type IndexEntry struct {
	TransactionID  string
	DocName        string
	ContentType    string
	CreatedAt      time.Time
	FirstFrameOff  int64
	FrameCount     uint16
	TotalBytes     int64
	SHA256Checksum string
}

type IndexWriter struct {
	f *os.File
}

func NewIndexWriter(path string) (*IndexWriter, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("block: create index %s: %w", path, err)
	}

	var hdr [8]byte
	copy(hdr[0:4], idxMagic)
	binary.LittleEndian.PutUint16(hdr[4:6], idxVersion)
	binary.LittleEndian.PutUint16(hdr[6:8], 0) // reserved

	if _, err := f.Write(hdr[:]); err != nil {
		f.Close()
		return nil, fmt.Errorf("block: write index header: %w", err)
	}

	return &IndexWriter{f: f}, nil
}

func (w *IndexWriter) Append(e IndexEntry) error {
	if err := writeString(w.f, e.TransactionID); err != nil {
		return err
	}
	if err := writeString(w.f, e.DocName); err != nil {
		return err
	}
	if err := writeString(w.f, e.ContentType); err != nil {
		return err
	}
	if err := writeString(w.f, e.SHA256Checksum); err != nil {
		return err
	}

	var buf [26]byte
	binary.LittleEndian.PutUint64(buf[0:8], uint64(e.CreatedAt.UnixMicro()))
	binary.LittleEndian.PutUint64(buf[8:16], uint64(e.FirstFrameOff))
	binary.LittleEndian.PutUint16(buf[16:18], e.FrameCount)
	binary.LittleEndian.PutUint64(buf[18:26], uint64(e.TotalBytes))

	_, err := w.f.Write(buf[:])
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

	var hdr [8]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		f.Close()
		return nil, fmt.Errorf("block: read index header: %w", err)
	}
	if string(hdr[0:4]) != idxMagic {
		f.Close()
		return nil, fmt.Errorf("block: invalid index magic: %q", hdr[0:4])
	}

	var entries []IndexEntry
	for {
		txID, err := readString(f)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			f.Close()
			return nil, fmt.Errorf("block: read index entry: %w", err)
		}

		docName, err := readString(f)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("block: read doc name: %w", err)
		}

		contentType, err := readString(f)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("block: read content type: %w", err)
		}

		checksum, err := readString(f)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("block: read checksum: %w", err)
		}

		var buf [26]byte
		if _, err := io.ReadFull(f, buf[:]); err != nil {
			f.Close()
			return nil, fmt.Errorf("block: read index fixed fields: %w", err)
		}

		entries = append(entries, IndexEntry{
			TransactionID:  txID,
			DocName:        docName,
			ContentType:    contentType,
			SHA256Checksum: checksum,
			CreatedAt:      time.UnixMicro(int64(binary.LittleEndian.Uint64(buf[0:8]))),
			FirstFrameOff:  int64(binary.LittleEndian.Uint64(buf[8:16])),
			FrameCount:     binary.LittleEndian.Uint16(buf[16:18]),
			TotalBytes:     int64(binary.LittleEndian.Uint64(buf[18:26])),
		})
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

func writeString(w io.Writer, s string) error {
	var lenBuf [2]byte
	binary.LittleEndian.PutUint16(lenBuf[:], uint16(len(s)))
	if _, err := w.Write(lenBuf[:]); err != nil {
		return err
	}
	if len(s) > 0 {
		_, err := w.Write([]byte(s))
		return err
	}
	return nil
}

func readString(r io.Reader) (string, error) {
	var lenBuf [2]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return "", err
	}
	n := binary.LittleEndian.Uint16(lenBuf[:])
	if n == 0 {
		return "", nil
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}
