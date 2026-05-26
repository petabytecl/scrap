package index

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/cockroachdb/pebble"
)

var ErrNotFound = errors.New("index: transaction not found")

const valueVersion byte = 0x01

type Entry struct {
	BlockIDs  []uint64
	DocCount  uint16
	Completed bool
}

type Index struct {
	db *pebble.DB
}

func Open(dir string) (*Index, error) {
	db, err := pebble.Open(dir, &pebble.Options{})
	if err != nil {
		return nil, fmt.Errorf("index: open pebble: %w", err)
	}
	return &Index{db: db}, nil
}

func (idx *Index) Put(txID string, blockID uint64, docCount uint16, completed bool) error {
	entry := Entry{
		BlockIDs:  []uint64{blockID},
		DocCount:  docCount,
		Completed: completed,
	}
	return idx.put(txID, entry)
}

func (idx *Index) Get(txID string) (Entry, error) {
	val, closer, err := idx.db.Get([]byte(txID))
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return Entry{}, ErrNotFound
		}
		return Entry{}, fmt.Errorf("index: get: %w", err)
	}
	defer closer.Close()

	return decodeEntry(val)
}

func (idx *Index) Exists(txID string) bool {
	_, closer, err := idx.db.Get([]byte(txID))
	if err != nil {
		return false
	}
	closer.Close()
	return true
}

func (idx *Index) AddBlockID(txID string, blockID uint64) error {
	entry, err := idx.Get(txID)
	if err != nil {
		return err
	}
	entry.BlockIDs = append(entry.BlockIDs, blockID)
	return idx.put(txID, entry)
}

func (idx *Index) IncrementDocCount(txID string) error {
	entry, err := idx.Get(txID)
	if err != nil {
		return err
	}
	entry.DocCount++
	return idx.put(txID, entry)
}

func (idx *Index) Close() error {
	return idx.db.Close()
}

func (idx *Index) put(txID string, entry Entry) error {
	val := encodeEntry(entry)
	return idx.db.Set([]byte(txID), val, pebble.Sync)
}

func encodeEntry(e Entry) []byte {
	// version(1) + block_count(2) + block_ids(8*N) + doc_count(2) + completed(1)
	n := 1 + 2 + 8*len(e.BlockIDs) + 2 + 1
	buf := make([]byte, n)
	buf[0] = valueVersion
	binary.LittleEndian.PutUint16(buf[1:3], uint16(len(e.BlockIDs)))
	off := 3
	for _, id := range e.BlockIDs {
		binary.LittleEndian.PutUint64(buf[off:off+8], id)
		off += 8
	}
	binary.LittleEndian.PutUint16(buf[off:off+2], e.DocCount)
	off += 2
	if e.Completed {
		buf[off] = 1
	}
	return buf
}

func decodeEntry(val []byte) (Entry, error) {
	if len(val) < 6 {
		return Entry{}, fmt.Errorf("index: value too short: %d bytes", len(val))
	}
	if val[0] != valueVersion {
		return Entry{}, fmt.Errorf("index: unknown value version: %d", val[0])
	}

	blockCount := binary.LittleEndian.Uint16(val[1:3])
	expected := 3 + 8*int(blockCount) + 3
	if len(val) < expected {
		return Entry{}, fmt.Errorf("index: value truncated: need %d, got %d", expected, len(val))
	}

	blockIDs := make([]uint64, blockCount)
	off := 3
	for i := range blockCount {
		blockIDs[i] = binary.LittleEndian.Uint64(val[off : off+8])
		off += 8
	}

	docCount := binary.LittleEndian.Uint16(val[off : off+2])
	off += 2
	completed := val[off] == 1

	return Entry{
		BlockIDs:  blockIDs,
		DocCount:  docCount,
		Completed: completed,
	}, nil
}
