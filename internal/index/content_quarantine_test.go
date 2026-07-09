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
		{
			name: "detected time outside json range",
			mutate: func(q ContentQuarantine) ContentQuarantine {
				q.DetectedAtUs = maxContentQuarantineUnixMicro + 1
				return q
			},
			wantErrMsg: "detected_at_us exceeds json time range",
		},
		{
			name: "confirmed time outside json range",
			mutate: func(q ContentQuarantine) ContentQuarantine {
				q.ConfirmedAtUs = maxContentQuarantineUnixMicro + 1
				return q
			},
			wantErrMsg: "confirmed_at_us exceeds json time range",
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
			if got := err.Error(); !strings.Contains(got, tt.wantErrMsg) {
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
	if got := err.Error(); !strings.Contains(got, "detected_at_us is required") {
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

func TestContentQuarantineListFiltersAndLimits(t *testing.T) {
	idx, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = idx.Close() }()

	first := contentQuarantineFixture("tx-a", "doc-a.xml", 7)
	second := contentQuarantineFixture("tx-b", "doc-b.xml", 8)
	putContentQuarantineForTest(t, idx, first)
	putContentQuarantineForTest(t, idx, second)

	list, err := idx.ListContentQuarantines("", 10)
	if err != nil {
		t.Fatalf("ListContentQuarantines: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("list length = %d, want 2", len(list))
	}
	filtered, err := idx.ListContentQuarantines(first.TransactionID, 1)
	if err != nil {
		t.Fatalf("ListContentQuarantines filtered: %v", err)
	}
	if len(filtered) != 1 || filtered[0].DocumentName != first.DocumentName {
		t.Fatalf("filtered list = %+v, want only %s", filtered, first.DocumentName)
	}
}

func TestContentQuarantineConfirmIsIdempotent(t *testing.T) {
	idx, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = idx.Close() }()

	first := contentQuarantineFixture("tx-a", "doc-a.xml", 7)
	putContentQuarantineForTest(t, idx, first)
	if err := idx.ConfirmContentQuarantine(first.TransactionID, first.DocumentName, 1716700003000000); err != nil {
		t.Fatalf("ConfirmContentQuarantine: %v", err)
	}
	confirmed, err := idx.GetContentQuarantine(first.TransactionID, first.DocumentName)
	if err != nil {
		t.Fatalf("GetContentQuarantine confirmed: %v", err)
	}
	if confirmed.ConfirmedAtUs != 1716700003000000 {
		t.Fatalf("ConfirmedAtUs = %d, want 1716700003000000", confirmed.ConfirmedAtUs)
	}
	if err := idx.ConfirmContentQuarantine(first.TransactionID, first.DocumentName, 1716700004000000); err != nil {
		t.Fatalf("ConfirmContentQuarantine idempotent: %v", err)
	}
	confirmed, err = idx.GetContentQuarantine(first.TransactionID, first.DocumentName)
	if err != nil {
		t.Fatalf("GetContentQuarantine after second confirm: %v", err)
	}
	if confirmed.ConfirmedAtUs != 1716700003000000 {
		t.Fatalf("ConfirmedAtUs after second confirm = %d, want original", confirmed.ConfirmedAtUs)
	}
}

func TestContentQuarantineReleaseRemovesOnlyRequestedRecord(t *testing.T) {
	idx, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = idx.Close() }()

	first := contentQuarantineFixture("tx-a", "doc-a.xml", 7)
	second := contentQuarantineFixture("tx-b", "doc-b.xml", 8)
	putContentQuarantineForTest(t, idx, first)
	putContentQuarantineForTest(t, idx, second)
	if err := idx.ReleaseContentQuarantine(first.TransactionID, first.DocumentName); err != nil {
		t.Fatalf("ReleaseContentQuarantine: %v", err)
	}
	if _, err := idx.GetContentQuarantine(first.TransactionID, first.DocumentName); !errors.Is(err, ErrContentQuarantineNotFound) {
		t.Fatalf("GetContentQuarantine after release = %v, want ErrContentQuarantineNotFound", err)
	}
	remaining, err := idx.ListContentQuarantines("", 10)
	if err != nil {
		t.Fatalf("ListContentQuarantines remaining: %v", err)
	}
	if len(remaining) != 1 || remaining[0].DocumentName != second.DocumentName {
		t.Fatalf("remaining list = %+v, want only %s", remaining, second.DocumentName)
	}
}

// H-06 / ADR 0025: committed release/confirm must be replay-safe when the
// Content Quarantine record is already gone. Not-found is only for pre-proposal
// validation, never an apply failure.
func TestContentQuarantineReleaseMissingIsIdempotent(t *testing.T) {
	idx, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = idx.Close() }()

	if err := idx.ReleaseContentQuarantine("tx-missing", "doc.xml"); err != nil {
		t.Fatalf("ReleaseContentQuarantine missing = %v, want nil", err)
	}
}

func TestContentQuarantineConfirmMissingIsIdempotent(t *testing.T) {
	idx, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = idx.Close() }()

	if err := idx.ConfirmContentQuarantine("tx-missing", "doc.xml", 1716700003000000); err != nil {
		t.Fatalf("ConfirmContentQuarantine missing = %v, want nil", err)
	}
}

func contentQuarantineFixture(txID, docName string, blockID uint64) ContentQuarantine {
	return ContentQuarantine{
		TransactionID: txID,
		DocumentName:  docName,
		BlockID:       blockID,
		DetectedAtUs:  1716700001000000,
		ScanType:      ContentQuarantineScanTypeInitial,
		Reason:        ContentQuarantineReasonScannerDetection,
	}
}

func putContentQuarantineForTest(t *testing.T, idx *Index, quarantine ContentQuarantine) {
	t.Helper()
	if err := idx.PutContentQuarantine(quarantine); err != nil {
		t.Fatalf("PutContentQuarantine: %v", err)
	}
}

func TestContentQuarantineDecodesV1RecordsAsUnconfirmed(t *testing.T) {
	idx, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = idx.Close() }()

	key, err := contentQuarantineKey("tx-v1", "doc.xml")
	if err != nil {
		t.Fatalf("contentQuarantineKey: %v", err)
	}
	if err := idx.db.Set(key, contentQuarantineValueV1ForTest(
		ContentQuarantineScanTypeInitial,
		ContentQuarantineReasonScannerDetection,
	), pebble.Sync); err != nil {
		t.Fatalf("Set v1 value: %v", err)
	}
	got, err := idx.GetContentQuarantine("tx-v1", "doc.xml")
	if err != nil {
		t.Fatalf("GetContentQuarantine v1: %v", err)
	}
	if got.ConfirmedAtUs != 0 {
		t.Fatalf("ConfirmedAtUs = %d, want 0 for v1", got.ConfirmedAtUs)
	}
}

func TestContentQuarantineListFailsClosedOnCorruptValue(t *testing.T) {
	idx, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = idx.Close() }()

	key, err := contentQuarantineKey("tx-corrupt", "doc.xml")
	if err != nil {
		t.Fatalf("contentQuarantineKey: %v", err)
	}
	if err := idx.db.Set(key, []byte{contentQuarantineValueVersion, 1, 2}, pebble.Sync); err != nil {
		t.Fatalf("Set corrupt value: %v", err)
	}
	if _, err := idx.ListContentQuarantines("", 10); !errors.Is(err, ErrInvalidContentQuarantine) {
		t.Fatalf("ListContentQuarantines error = %v, want ErrInvalidContentQuarantine", err)
	}
}

func TestContentQuarantineConfirmAndReleaseFailClosedOnCorruptValue(t *testing.T) {
	tests := []struct {
		name string
		run  func(*Index) error
	}{
		{
			name: "confirm",
			run: func(idx *Index) error {
				return idx.ConfirmContentQuarantine("tx-corrupt", "doc.xml", 1716700003000000)
			},
		},
		{
			name: "release",
			run: func(idx *Index) error {
				return idx.ReleaseContentQuarantine("tx-corrupt", "doc.xml")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx, err := Open(t.TempDir())
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer func() { _ = idx.Close() }()

			key, err := contentQuarantineKey("tx-corrupt", "doc.xml")
			if err != nil {
				t.Fatalf("contentQuarantineKey: %v", err)
			}
			if err := idx.db.Set(key, []byte{contentQuarantineValueVersion, 1, 2}, pebble.Sync); err != nil {
				t.Fatalf("Set corrupt value: %v", err)
			}
			if err := tt.run(idx); !errors.Is(err, ErrInvalidContentQuarantine) {
				t.Fatalf("%s error = %v, want ErrInvalidContentQuarantine", tt.name, err)
			}
		})
	}
}

func contentQuarantineValueHeaderForTest(scanType ContentQuarantineScanType, reason ContentQuarantineReason) []byte {
	value := make([]byte, contentQuarantineValueLen)
	value[0] = contentQuarantineValueVersion
	value[1] = 7
	value[17] = byte(scanType)
	value[18] = byte(reason)
	putNonNegativeInt64(value[9:17], 1716700001000000)
	return value
}

func contentQuarantineValueV1ForTest(scanType ContentQuarantineScanType, reason ContentQuarantineReason) []byte {
	value := contentQuarantineValueHeaderForTest(scanType, reason)
	value[0] = contentQuarantineValueVersionV1
	return value[:contentQuarantineValueLenV1]
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
