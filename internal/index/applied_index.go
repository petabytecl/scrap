package index

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/cockroachdb/pebble"
)

// appliedIndexKey stores the raft applied index that the Pebble Projection's
// content durably covers. It is per-Member restore bookkeeping (excluded from
// StreamingHash), letting raft snapshot restore reject a projection rolled back
// below a snapshot's index (a partial DataDir restore).
const appliedIndexKey = "\x00applied-index\x00"

const appliedIndexValueLen = 8

// PersistAppliedIndex durably records, with an fsync, the raft applied index the
// projection content covers, then updates the in-memory value. Callers persist
// this at raft snapshot creation, after the covered entries' content is already
// synced, so the watermark never overstates durable content.
func (idx *Index) PersistAppliedIndex(appliedIndex uint64) error {
	var buf [appliedIndexValueLen]byte
	binary.BigEndian.PutUint64(buf[:], appliedIndex)
	if err := idx.db.Set([]byte(appliedIndexKey), buf[:], pebble.Sync); err != nil {
		return fmt.Errorf("index: persist applied index: %w", err)
	}
	idx.durableAppliedIndex.Store(appliedIndex)
	return nil
}

// AppliedIndex returns the last durably-persisted applied index, or 0 if the
// projection has never persisted one.
func (idx *Index) AppliedIndex() uint64 {
	return idx.durableAppliedIndex.Load()
}

func (idx *Index) loadAppliedIndex() error {
	val, closer, err := idx.db.Get([]byte(appliedIndexKey))
	if errors.Is(err, pebble.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("index: load applied index: %w", err)
	}
	defer func() { _ = closer.Close() }()
	if len(val) != appliedIndexValueLen {
		return fmt.Errorf("index: applied index value is %d bytes, want %d", len(val), appliedIndexValueLen)
	}
	idx.durableAppliedIndex.Store(binary.BigEndian.Uint64(val))
	return nil
}
