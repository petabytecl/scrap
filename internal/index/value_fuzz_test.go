package index

import (
	"slices"
	"testing"
)

const maxFuzzProjectionValueSize = 1 << 20

func FuzzDecodeEntry(f *testing.F) {
	f.Add(encodeEntry(Entry{
		BlockIDs:  []uint64{1, 2, 7},
		DocCount:  7,
		Completed: true,
	}))
	f.Add(encodeEntry(Entry{}))
	f.Add([]byte{})
	f.Add([]byte{valueVersion, 0xff, 0xff})
	f.Add([]byte{valueVersion + 1, 0, 0, 0, 0, 0})

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxFuzzProjectionValueSize {
			return
		}

		entry, err := decodeEntry(data)
		if err != nil {
			return
		}
		round, err := decodeEntry(encodeEntry(entry))
		if err != nil {
			t.Fatalf("decode encoded projection entry: %v", err)
		}
		if !slices.Equal(round.BlockIDs, entry.BlockIDs) ||
			round.DocCount != entry.DocCount ||
			round.Completed != entry.Completed {
			t.Fatalf("round-trip entry = %+v, want %+v", round, entry)
		}
	})
}
