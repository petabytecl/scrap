package block

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const maxFuzzIndexFileSize = idxHeaderLen + 2*(4+idxMaxEntryLen+4)

func FuzzDecodeIndexEntry(f *testing.F) {
	v1 := IndexEntry{
		TransactionID: "tx-v1",
		DocName:       "invoice.xml",
		ContentType:   "application/xml",
		CreatedAt:     time.UnixMicro(1_716_700_000_000_000),
		FirstFrameOff: HeaderSize,
		FrameCount:    2,
		TotalBytes:    MaxFramePayload + 17,
		SHA256:        [32]byte{0x01, 0x02, 0x03},
	}
	v2 := IndexEntry{
		TransactionID:      "tx-v2",
		DocName:            "encrypted.pdf",
		ContentType:        "application/pdf",
		CreatedAt:          time.UnixMicro(1_716_700_000_000_001),
		FirstFrameOff:      HeaderSize,
		FrameCount:         1,
		TotalBytes:         19,
		SHA256:             [32]byte{0x04, 0x05, 0x06},
		EncryptionEnvelope: []byte(`{"ciphertext_length":19}`),
	}
	validV1 := encodeFuzzIndexFile(f, []IndexEntry{v1})
	validV2 := encodeFuzzIndexFile(f, []IndexEntry{v1, v2})

	f.Add(validV1)
	f.Add(validV2)
	f.Add([]byte{})
	f.Add(validV1[:len(validV1)-1])
	badHeaderCRC := append([]byte(nil), validV1...)
	badHeaderCRC[8] ^= 0xff
	f.Add(badHeaderCRC)

	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzIndexFile(t, data)
	})
}

func fuzzIndexFile(t *testing.T, data []byte) {
	t.Helper()
	if len(data) > maxFuzzIndexFileSize {
		return
	}

	path := filepath.Join(t.TempDir(), "fuzz.idx")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write fuzz Block index: %v", err)
	}
	reader, err := OpenIndexReader(path)
	if err != nil {
		return
	}
	entries := append([]IndexEntry(nil), reader.Entries()...)
	if err := reader.Close(); err != nil {
		t.Fatalf("close fuzz Block index: %v", err)
	}

	for _, entry := range entries {
		if err := validateEntryFieldLens(entry); err != nil {
			t.Fatalf("decoded entry violates field bounds: %v", err)
		}
		round, err := decodeIndexEntry(encodeIndexEntry(entry))
		if err != nil {
			t.Fatalf("decode encoded entry: %v", err)
		}
		assertIndexEntrySemantics(t, round, entry)
	}
}

func encodeFuzzIndexFile(f *testing.F, entries []IndexEntry) []byte {
	f.Helper()
	path := filepath.Join(f.TempDir(), "seed.idx")
	writer, err := NewIndexWriter(path)
	if err != nil {
		f.Fatalf("create Block index seed: %v", err)
	}
	for _, entry := range entries {
		if err := writer.Append(entry); err != nil {
			_ = writer.Close()
			f.Fatalf("append Block index seed: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		f.Fatalf("close Block index seed: %v", err)
	}
	data, err := os.ReadFile(path) //nolint:gosec // path is created under f.TempDir
	if err != nil {
		f.Fatalf("read Block index seed: %v", err)
	}
	return data
}

func assertIndexEntrySemantics(t *testing.T, got, want IndexEntry) {
	t.Helper()
	if got.TransactionID != want.TransactionID ||
		got.DocName != want.DocName ||
		got.ContentType != want.ContentType ||
		got.CreatedAt.UnixMicro() != want.CreatedAt.UnixMicro() ||
		got.FirstFrameOff != want.FirstFrameOff ||
		got.FrameCount != want.FrameCount ||
		got.TotalBytes != want.TotalBytes ||
		got.SHA256 != want.SHA256 ||
		!bytes.Equal(got.EncryptionEnvelope, want.EncryptionEnvelope) {
		t.Fatalf("round-trip entry = %+v, want %+v", got, want)
	}
}
