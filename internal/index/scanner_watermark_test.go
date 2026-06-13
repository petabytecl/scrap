package index

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"

	"github.com/cockroachdb/pebble"
)

func TestScannerWatermarkMissing(t *testing.T) {
	idx, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })

	_, err = idx.GetScannerWatermark()
	if !errors.Is(err, ErrScannerWatermarkNotFound) {
		t.Fatalf("GetScannerWatermark error = %v, want ErrScannerWatermarkNotFound", err)
	}
}

func TestScannerWatermarkRoundTrip(t *testing.T) {
	idx, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })

	want := ScannerWatermark{
		LastScannedBlockID:          42,
		LastSignatureVersionScanned: "daily-2026.06.12:1",
	}
	if err := idx.PutScannerWatermark(want); err != nil {
		t.Fatalf("PutScannerWatermark: %v", err)
	}

	got, err := idx.GetScannerWatermark()
	if err != nil {
		t.Fatalf("GetScannerWatermark: %v", err)
	}
	if got != want {
		t.Fatalf("ScannerWatermark = %+v, want %+v", got, want)
	}
}

func TestScannerWatermarkRejectsCorruptValues(t *testing.T) {
	tests := []struct {
		name string
		val  []byte
	}{
		{name: "empty", val: nil},
		{name: "unknown version", val: []byte{0xff, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}},
		{name: "truncated block id", val: []byte{scannerWatermarkValueVersion, 1, 2}},
		{name: "truncated signature length", val: []byte{scannerWatermarkValueVersion, 1, 2, 3, 4, 5, 6, 7, 8, 1}},
		{name: "truncated signature", val: append(scannerWatermarkHeaderForTest(3), 'a')},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx, err := Open(t.TempDir())
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			t.Cleanup(func() { _ = idx.Close() })

			if err := idx.db.Set(scannerWatermarkKey(), tt.val, pebble.Sync); err != nil {
				t.Fatalf("Set corrupt watermark: %v", err)
			}

			if _, err := idx.GetScannerWatermark(); err == nil {
				t.Fatal("GetScannerWatermark succeeded for corrupt value")
			}
		})
	}
}

func TestScannerWatermarkRejectsInvalidSignatureVersion(t *testing.T) {
	tests := []struct {
		name             string
		signatureVersion string
	}{
		{name: "path separator", signatureVersion: "daily/2026-06-12"},
		{name: "control character", signatureVersion: "daily\n2026-06-12"},
		{name: "space", signatureVersion: "daily 2026-06-12"},
		{name: "too long", signatureVersion: strings.Repeat("a", maxScannerSignatureVersionLen+1)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx, err := Open(t.TempDir())
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			t.Cleanup(func() { _ = idx.Close() })

			err = idx.PutScannerWatermark(ScannerWatermark{
				LastScannedBlockID:          7,
				LastSignatureVersionScanned: tt.signatureVersion,
			})
			if err == nil {
				t.Fatal("PutScannerWatermark succeeded with invalid signature version")
			}
		})
	}
}

func TestScannerWatermarkDoesNotAffectStreamingHash(t *testing.T) {
	idx1, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open idx1: %v", err)
	}
	t.Cleanup(func() { _ = idx1.Close() })

	idx2, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open idx2: %v", err)
	}
	t.Cleanup(func() { _ = idx2.Close() })

	watermark := ScannerWatermark{
		LastScannedBlockID:          42,
		LastSignatureVersionScanned: "daily-2026.06.12:1",
	}
	putScannerWatermarkFixture(t, idx1, watermark)
	putScannerWatermarkFixture(t, idx2, watermark)

	_, hash1, err := idx1.StreamingHash()
	if err != nil {
		t.Fatalf("StreamingHash idx1: %v", err)
	}
	_, hash2, err := idx2.StreamingHash()
	if err != nil {
		t.Fatalf("StreamingHash idx2: %v", err)
	}
	if hash1 != hash2 {
		t.Fatalf("same scanner watermark hashes differ: %x vs %x", hash1, hash2)
	}

	putScannerWatermarkFixture(t, idx2, ScannerWatermark{
		LastScannedBlockID:          watermark.LastScannedBlockID + 1,
		LastSignatureVersionScanned: watermark.LastSignatureVersionScanned,
	})

	_, hash3, err := idx2.StreamingHash()
	if err != nil {
		t.Fatalf("StreamingHash changed idx2: %v", err)
	}
	if !bytes.Equal(hash1[:], hash3[:]) {
		t.Fatal("changing scanner watermark should not change projection consistency hash")
	}
}

func scannerWatermarkHeaderForTest(signatureLen uint16) []byte {
	buf := make([]byte, scannerWatermarkSignatureDataOffset)
	buf[0] = scannerWatermarkValueVersion
	binary.LittleEndian.PutUint64(buf[scannerWatermarkBlockIDOffset:scannerWatermarkSignatureLenOffset], 1)
	binary.LittleEndian.PutUint16(buf[scannerWatermarkSignatureLenOffset:scannerWatermarkSignatureDataOffset], signatureLen)
	return buf
}

func putScannerWatermarkFixture(t *testing.T, idx *Index, watermark ScannerWatermark) {
	t.Helper()

	if err := idx.PutScannerWatermark(watermark); err != nil {
		t.Fatalf("PutScannerWatermark: %v", err)
	}
}
