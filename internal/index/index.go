package index

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sync/atomic"

	"github.com/cockroachdb/pebble"
)

var ErrNotFound = errors.New("index: transaction not found")

const (
	valueVersion byte = 0x01

	sizeVersion    = 1
	sizeBlockCount = 2
	sizeBlockID    = 8
	sizeDocCount   = 2
	sizeCompleted  = 1

	headerLen     = sizeVersion + sizeBlockCount // version(1) + block_count(2)
	trailerLen    = sizeDocCount + sizeCompleted // doc_count(2) + completed(1)
	minEncodedLen = headerLen + trailerLen       // 6 bytes with zero blocks

	txKeyPrefix = "\x00tx\x00"
)

type Entry struct {
	BlockIDs  []uint64
	DocCount  uint16
	Completed bool
}

type Index struct {
	db           *pebble.DB
	appliedIndex atomic.Uint64
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
	val, closer, err := idx.db.Get(txKey(txID))
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return Entry{}, ErrNotFound
		}
		return Entry{}, fmt.Errorf("index: get: %w", err)
	}
	defer func() { _ = closer.Close() }()

	return decodeEntry(val)
}

func (idx *Index) Exists(txID string) bool {
	_, closer, err := idx.db.Get(txKey(txID))
	if err != nil {
		return false
	}
	_ = closer.Close()
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

func (idx *Index) SetAppliedIndex(ai uint64) {
	idx.appliedIndex.Store(ai)
}

// DiskUsageBytes reports the total on-disk size of the Pebble projection, clamped
// to int64 for OTel observation. Feeds the USE dashboard's disk panel.
func (idx *Index) DiskUsageBytes() int64 {
	if idx.db == nil {
		return 0
	}
	usage := idx.db.Metrics().DiskSpaceUsage()
	if usage > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(usage)
}

func (idx *Index) StreamingHash() (appliedIndex uint64, hash [32]byte, err error) {
	appliedIndex = idx.appliedIndex.Load()

	snap := idx.db.NewSnapshot()
	defer func() { _ = snap.Close() }()

	iter, iterErr := snap.NewIter(nil)
	if iterErr != nil {
		return 0, [32]byte{}, fmt.Errorf("index: new iter: %w", iterErr)
	}
	defer func() { _ = iter.Close() }()

	h := sha256.New()
	for iter.First(); iter.Valid(); iter.Next() {
		val, err := iter.ValueAndErr()
		if err != nil {
			return 0, [32]byte{}, fmt.Errorf("index: iter value: %w", err)
		}
		h.Write(iter.Key())
		h.Write(val)
	}
	if err := iter.Error(); err != nil {
		return 0, [32]byte{}, fmt.Errorf("index: iter: %w", err)
	}

	copy(hash[:], h.Sum(nil))
	return appliedIndex, hash, nil
}

func (idx *Index) put(txID string, entry Entry) error {
	val := encodeEntry(entry)
	return idx.db.Set(txKey(txID), val, pebble.Sync)
}

func txKey(txID string) []byte {
	key := make([]byte, len(txKeyPrefix)+len(txID))
	copy(key, txKeyPrefix)
	copy(key[len(txKeyPrefix):], txID)
	return key
}

func encodeEntry(e Entry) []byte {
	n := headerLen + sizeBlockID*len(e.BlockIDs) + trailerLen
	buf := make([]byte, n)
	buf[0] = valueVersion
	binary.LittleEndian.PutUint16(buf[sizeVersion:headerLen], uint16(len(e.BlockIDs))) //nolint:gosec // block count bounded by shard design
	off := headerLen
	for _, id := range e.BlockIDs {
		binary.LittleEndian.PutUint64(buf[off:off+sizeBlockID], id)
		off += sizeBlockID
	}
	binary.LittleEndian.PutUint16(buf[off:off+sizeDocCount], e.DocCount)
	off += sizeDocCount
	if e.Completed {
		buf[off] = 1
	}
	return buf
}

func decodeEntry(val []byte) (Entry, error) {
	if len(val) < minEncodedLen {
		return Entry{}, fmt.Errorf("index: value too short: %d bytes", len(val))
	}
	if val[0] != valueVersion {
		return Entry{}, fmt.Errorf("index: unknown value version: %d", val[0])
	}

	blockCount := binary.LittleEndian.Uint16(val[sizeVersion:headerLen])
	expected := headerLen + sizeBlockID*int(blockCount) + trailerLen
	if len(val) < expected {
		return Entry{}, fmt.Errorf("index: value truncated: need %d, got %d", expected, len(val))
	}

	blockIDs := make([]uint64, blockCount)
	off := headerLen
	for i := range blockCount {
		blockIDs[i] = binary.LittleEndian.Uint64(val[off : off+sizeBlockID])
		off += sizeBlockID
	}

	docCount := binary.LittleEndian.Uint16(val[off : off+sizeDocCount])
	off += sizeDocCount
	completed := val[off] == 1

	return Entry{
		BlockIDs:  blockIDs,
		DocCount:  docCount,
		Completed: completed,
	}, nil
}
