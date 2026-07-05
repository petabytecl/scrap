package index

import "testing"

func TestDecodeEntryRejectsTrailingBytes(t *testing.T) {
	val := encodeEntry(Entry{BlockIDs: []uint64{7}, DocCount: 3, Completed: true})
	if _, err := decodeEntry(val); err != nil {
		t.Fatalf("decodeEntry clean value: %v", err)
	}
	if _, err := decodeEntry(append(val, 0x00)); err == nil {
		t.Fatal("decodeEntry with trailing byte succeeded, want error")
	}
}
