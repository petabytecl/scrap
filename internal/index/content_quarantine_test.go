package index

import (
	"errors"
	"strings"
	"testing"

	"github.com/cockroachdb/pebble"
)

func TestContentQuarantineMissing(t *testing.T) {
	idx, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = idx.Close() }()

	_, err = idx.GetContentQuarantine("tx-quarantine", "doc.xml")
	if !errors.Is(err, ErrContentQuarantineNotFound) {
		t.Fatalf("GetContentQuarantine error = %v, want ErrContentQuarantineNotFound", err)
	}
}

func TestContentQuarantineRoundTrip(t *testing.T) {
	idx, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = idx.Close() }()

	want := ContentQuarantine{
		TransactionID: "tx-quarantine",
		DocumentName:  "doc.xml",
		BlockID:       7,
		DetectedAtUs:  1716700001000000,
		ScanType:      ContentQuarantineScanTypeInitial,
		Reason:        ContentQuarantineReasonScannerDetection,
	}
	if err := idx.PutContentQuarantine(want); err != nil {
		t.Fatalf("PutContentQuarantine: %v", err)
	}

	got, err := idx.GetContentQuarantine("tx-quarantine", "doc.xml")
	if err != nil {
		t.Fatalf("GetContentQuarantine: %v", err)
	}
	if got != want {
		t.Fatalf("ContentQuarantine = %+v, want %+v", got, want)
	}
}

func TestContentQuarantineDuplicatePutIsIdempotent(t *testing.T) {
	idx, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = idx.Close() }()

	want := ContentQuarantine{
		TransactionID: "tx-quarantine",
		DocumentName:  "doc.xml",
		BlockID:       7,
		DetectedAtUs:  1716700001000000,
		ScanType:      ContentQuarantineScanTypeInitial,
		Reason:        ContentQuarantineReasonScannerDetection,
	}
	if err := idx.PutContentQuarantine(want); err != nil {
		t.Fatalf("first PutContentQuarantine: %v", err)
	}
	if err := idx.PutContentQuarantine(want); err != nil {
		t.Fatalf("second PutContentQuarantine: %v", err)
	}

	got, err := idx.GetContentQuarantine(want.TransactionID, want.DocumentName)
	if err != nil {
		t.Fatalf("GetContentQuarantine: %v", err)
	}
	if got != want {
		t.Fatalf("ContentQuarantine = %+v, want %+v", got, want)
	}
}

func TestContentQuarantineRejectsInvalidState(t *testing.T) {
	idx, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = idx.Close() }()

	valid := ContentQuarantine{
		TransactionID: "tx-quarantine",
		DocumentName:  "doc.xml",
		BlockID:       7,
		DetectedAtUs:  1716700001000000,
		ScanType:      ContentQuarantineScanTypeInitial,
		Reason:        ContentQuarantineReasonScannerDetection,
	}
	tests := []struct {
		name       string
		mutate     func(ContentQuarantine) ContentQuarantine
		wantErrMsg string
	}{
		{
			name: "missing transaction",
			mutate: func(q ContentQuarantine) ContentQuarantine {
				q.TransactionID = ""
				return q
			},
			wantErrMsg: "transaction_id is required",
		},
		{
			name: "control byte in document name",
			mutate: func(q ContentQuarantine) ContentQuarantine {
				q.DocumentName = "doc\x00.xml"
				return q
			},
			wantErrMsg: "document_name contains control character",
		},
		{
			name: "missing detected time",
			mutate: func(q ContentQuarantine) ContentQuarantine {
				q.DetectedAtUs = 0
				return q
			},
			wantErrMsg: "detected_at_us is required",
		},
		{
			name: "oversized transaction",
			mutate: func(q ContentQuarantine) ContentQuarantine {
				q.TransactionID = strings.Repeat("t", contentQuarantineTextMaxBytes("transaction_id")+1)
				return q
			},
			wantErrMsg: "transaction_id exceeds",
		},
		{
			name: "missing scan type",
			mutate: func(q ContentQuarantine) ContentQuarantine {
				q.ScanType = 0
				return q
			},
			wantErrMsg: "scan_type is required",
		},
		{
			name: "missing reason",
			mutate: func(q ContentQuarantine) ContentQuarantine {
				q.Reason = 0
				return q
			},
			wantErrMsg: "reason is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := idx.PutContentQuarantine(tt.mutate(valid))
			if err == nil {
				t.Fatal("PutContentQuarantine succeeded")
			}
			if !errors.Is(err, ErrInvalidContentQuarantine) {
				t.Fatalf("PutContentQuarantine error = %v, want ErrInvalidContentQuarantine", err)
			}
			if got := err.Error(); !contains(got, tt.wantErrMsg) {
				t.Fatalf("error = %q, want to contain %q", got, tt.wantErrMsg)
			}
		})
	}
}

func TestContentQuarantineRejectsCorruptValues(t *testing.T) {
	tests := []struct {
		name string
		val  []byte
	}{
		{name: "empty", val: nil},
		{name: "unknown version", val: []byte{0xff}},
		{name: "truncated", val: []byte{contentQuarantineValueVersion, 1, 2}},
		{name: "missing scan type", val: contentQuarantineValueHeaderForTest(0, ContentQuarantineReasonScannerDetection)},
		{name: "missing reason", val: contentQuarantineValueHeaderForTest(ContentQuarantineScanTypeInitial, 0)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx, err := Open(t.TempDir())
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer func() { _ = idx.Close() }()

			key, err := contentQuarantineKey("tx-quarantine", "doc.xml")
			if err != nil {
				t.Fatalf("contentQuarantineKey: %v", err)
			}
			if err := idx.db.Set(key, tt.val, pebble.Sync); err != nil {
				t.Fatalf("Set corrupt value: %v", err)
			}
			if _, err := idx.GetContentQuarantine("tx-quarantine", "doc.xml"); !errors.Is(err, ErrInvalidContentQuarantine) {
				t.Fatalf("GetContentQuarantine error = %v, want ErrInvalidContentQuarantine", err)
			}
		})
	}
}

func TestContentQuarantineRejectsOversizedLookupIdentity(t *testing.T) {
	idx, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = idx.Close() }()

	_, err = idx.GetContentQuarantine(strings.Repeat("t", contentQuarantineTextMaxBytes("transaction_id")+1), "doc.xml")
	if !errors.Is(err, ErrInvalidContentQuarantine) {
		t.Fatalf("GetContentQuarantine error = %v, want ErrInvalidContentQuarantine", err)
	}
}

func TestContentQuarantineRejectsMissingDetectedTime(t *testing.T) {
	idx, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = idx.Close() }()

	err = idx.PutContentQuarantine(ContentQuarantine{
		TransactionID: "tx-quarantine",
		DocumentName:  "doc.xml",
		BlockID:       7,
		ScanType:      ContentQuarantineScanTypeInitial,
		Reason:        ContentQuarantineReasonScannerDetection,
	})
	if !errors.Is(err, ErrInvalidContentQuarantine) {
		t.Fatalf("PutContentQuarantine error = %v, want ErrInvalidContentQuarantine", err)
	}
	if got := err.Error(); !contains(got, "detected_at_us is required") {
		t.Fatalf("error = %q, want detected_at_us is required", got)
	}
}

func TestContentQuarantineRejectsCorruptKeys(t *testing.T) {
	_, err := decodeContentQuarantine([]byte(contentQuarantinePrefix+"bad-key"), contentQuarantineValueHeaderForTest(
		ContentQuarantineScanTypeInitial,
		ContentQuarantineReasonScannerDetection,
	))
	if !errors.Is(err, ErrInvalidContentQuarantine) {
		t.Fatalf("decodeContentQuarantine error = %v, want ErrInvalidContentQuarantine", err)
	}
}

func TestContentQuarantineRejectsZeroDetectedTimeFromCorruptValue(t *testing.T) {
	idx, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = idx.Close() }()

	key, err := contentQuarantineKey("tx-quarantine", "doc.xml")
	if err != nil {
		t.Fatalf("contentQuarantineKey: %v", err)
	}
	if err := idx.db.Set(key, contentQuarantineValueWithDetectedTimeForTest(
		0,
		ContentQuarantineScanTypeInitial,
		ContentQuarantineReasonScannerDetection,
	), pebble.Sync); err != nil {
		t.Fatalf("Set corrupt value: %v", err)
	}
	if _, err := idx.GetContentQuarantine("tx-quarantine", "doc.xml"); !errors.Is(err, ErrInvalidContentQuarantine) {
		t.Fatalf("GetContentQuarantine error = %v, want ErrInvalidContentQuarantine", err)
	}
}

func TestContentQuarantineAffectsStreamingHash(t *testing.T) {
	idx1, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open idx1: %v", err)
	}
	defer func() { _ = idx1.Close() }()
	idx2, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open idx2: %v", err)
	}
	defer func() { _ = idx2.Close() }()

	for _, idx := range []*Index{idx1, idx2} {
		if err := idx.Put("tx-quarantine", 7, 1, true); err != nil {
			t.Fatalf("Put transaction: %v", err)
		}
	}

	_, hash1, err := idx1.StreamingHash()
	if err != nil {
		t.Fatalf("StreamingHash idx1: %v", err)
	}
	if err := idx2.PutContentQuarantine(ContentQuarantine{
		TransactionID: "tx-quarantine",
		DocumentName:  "doc.xml",
		BlockID:       7,
		DetectedAtUs:  1716700001000000,
		ScanType:      ContentQuarantineScanTypeInitial,
		Reason:        ContentQuarantineReasonScannerDetection,
	}); err != nil {
		t.Fatalf("PutContentQuarantine: %v", err)
	}
	_, hash2, err := idx2.StreamingHash()
	if err != nil {
		t.Fatalf("StreamingHash idx2: %v", err)
	}
	if hash1 == hash2 {
		t.Fatal("quarantine state did not affect StreamingHash")
	}
}

func contentQuarantineValueHeaderForTest(scanType ContentQuarantineScanType, reason ContentQuarantineReason) []byte {
	value := []byte{
		contentQuarantineValueVersion,
		7, 0, 0, 0, 0, 0, 0, 0,
		0, 0, 0, 0, 0, 0, 0, 0,
		byte(scanType),
		byte(reason),
	}
	putNonNegativeInt64(value[9:17], 1716700001000000)
	return value
}

func contentQuarantineValueWithDetectedTimeForTest(
	detectedAtUs int64,
	scanType ContentQuarantineScanType,
	reason ContentQuarantineReason,
) []byte {
	value := contentQuarantineValueHeaderForTest(scanType, reason)
	putNonNegativeInt64(value[9:17], detectedAtUs)
	return value
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return substr == ""
}
