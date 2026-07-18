package block

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
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
	// Raw entry payloads so the V1/V2 decoder is fuzzed directly rather than
	// only through the CRC-gated reader, which rejects mutated payloads before
	// decodeIndexEntry ever runs.
	f.Add(encodeIndexEntry(v1))
	f.Add(encodeIndexEntry(v2))

	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzIndexFile(t, data)
		fuzzIndexEntryPayload(t, data)
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

// fuzzIndexEntryPayload treats the fuzzer bytes as a single raw entry payload
// so decodeIndexEntry sees malformed length prefixes, fixed fields, envelopes,
// and trailing data directly. Re-framing the same payload with a valid CRC and
// reading it back proves the reader and the decoder stay in agreement.
func fuzzIndexEntryPayload(t *testing.T, payload []byte) {
	t.Helper()
	if len(payload) > idxMaxEntryLen {
		return
	}

	entry, decErr := decodeIndexEntry(payload)

	reader, readErr := openFuzzIndexReader(t, frameFuzzIndexEntry(payload))
	if decErr != nil {
		if readErr == nil {
			_ = reader.Close()
			t.Fatalf("payload fails to decode (%v) but the reader accepted it", decErr)
		}
		return
	}
	if readErr != nil {
		t.Fatalf("payload decodes cleanly but the reader rejected it: %v", readErr)
	}

	got := append([]IndexEntry(nil), reader.Entries()...)
	if err := reader.Close(); err != nil {
		t.Fatalf("close framed fuzz Block index: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("framed single entry decoded to %d entries", len(got))
	}
	assertIndexEntrySemantics(t, got[0], entry)

	if err := validateEntryFieldLens(entry); err != nil {
		t.Fatalf("decoded entry violates field bounds: %v", err)
	}
	round, err := decodeIndexEntry(encodeIndexEntry(entry))
	if err != nil {
		t.Fatalf("re-decode encoded entry: %v", err)
	}
	assertIndexEntrySemantics(t, round, entry)
}

// frameFuzzIndexEntry wraps a raw entry payload in a valid index file: the
// standard header plus one length-prefixed, CRC-protected entry record.
func frameFuzzIndexEntry(payload []byte) []byte {
	var hdr [idxHeaderLen]byte
	copy(hdr[0:4], idxMagic)
	binary.LittleEndian.PutUint16(hdr[4:6], idxVersion)
	binary.LittleEndian.PutUint16(hdr[6:8], idxHeaderLen)
	binary.LittleEndian.PutUint32(hdr[8:12], crc32.Checksum(hdr[0:8], crcTable))

	buf := append([]byte(nil), hdr[:]...)
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(payload))) //nolint:gosec // bounded by idxMaxEntryLen above
	buf = append(buf, lenBuf[:]...)
	buf = append(buf, payload...)
	var crcBuf [4]byte
	binary.LittleEndian.PutUint32(crcBuf[:], crc32.Checksum(payload, crcTable))
	buf = append(buf, crcBuf[:]...)
	return buf
}

func openFuzzIndexReader(t *testing.T, framed []byte) (*IndexReader, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "framed.idx")
	if err := os.WriteFile(path, framed, 0o600); err != nil {
		t.Fatalf("write framed fuzz Block index: %v", err)
	}
	return OpenIndexReader(path)
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
