package index

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"unicode"

	"github.com/cockroachdb/pebble"
)

var (
	ErrContentQuarantineNotFound = errors.New("index: content quarantine not found")
	ErrInvalidContentQuarantine  = errors.New("index: invalid content quarantine")
)

const (
	contentQuarantinePrefix       = "q\x01"
	contentQuarantineValueVersion = 0x01
	contentQuarantineValueLen     = 1 + sizeBlockID + 8 + 1 + 1
)

type ContentQuarantineScanType byte

const (
	ContentQuarantineScanTypeInitial ContentQuarantineScanType = 1
	ContentQuarantineScanTypeRescan  ContentQuarantineScanType = 2
)

type ContentQuarantineReason byte

const (
	ContentQuarantineReasonScannerDetection ContentQuarantineReason = 1
)

type ContentQuarantine struct {
	TransactionID string
	DocumentName  string
	BlockID       uint64
	DetectedAtUs  int64
	ScanType      ContentQuarantineScanType
	Reason        ContentQuarantineReason
}

func (idx *Index) PutContentQuarantine(quarantine ContentQuarantine) error {
	key, err := contentQuarantineKey(quarantine.TransactionID, quarantine.DocumentName)
	if err != nil {
		return err
	}
	if err := validateContentQuarantine(quarantine); err != nil {
		return err
	}
	return idx.db.Set(key, encodeContentQuarantine(quarantine), pebble.Sync)
}

func (idx *Index) GetContentQuarantine(txID, docName string) (ContentQuarantine, error) {
	key, err := contentQuarantineKey(txID, docName)
	if err != nil {
		return ContentQuarantine{}, err
	}
	val, closer, err := idx.db.Get(key)
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return ContentQuarantine{}, ErrContentQuarantineNotFound
		}
		return ContentQuarantine{}, fmt.Errorf("index: get content quarantine: %w", err)
	}
	defer func() { _ = closer.Close() }()

	return decodeContentQuarantine(key, val)
}

func contentQuarantineKey(txID, docName string) ([]byte, error) {
	if err := validateContentQuarantineIdentity(txID, docName); err != nil {
		return nil, err
	}
	key := make([]byte, len(contentQuarantinePrefix)+len(txID)+1+len(docName))
	copy(key, contentQuarantinePrefix)
	off := len(contentQuarantinePrefix)
	copy(key[off:], txID)
	off += len(txID)
	key[off] = 0
	copy(key[off+1:], docName)
	return key, nil
}

func encodeContentQuarantine(quarantine ContentQuarantine) []byte {
	buf := make([]byte, contentQuarantineValueLen)
	buf[0] = contentQuarantineValueVersion
	binary.LittleEndian.PutUint64(buf[1:9], quarantine.BlockID)
	putNonNegativeInt64(buf[9:17], quarantine.DetectedAtUs)
	buf[17] = byte(quarantine.ScanType)
	buf[18] = byte(quarantine.Reason)
	return buf
}

func decodeContentQuarantine(key, val []byte) (ContentQuarantine, error) {
	txID, docName, err := decodeContentQuarantineKey(key)
	if err != nil {
		return ContentQuarantine{}, err
	}
	if len(val) != contentQuarantineValueLen {
		return ContentQuarantine{}, fmt.Errorf("index: content quarantine value length %d", len(val))
	}
	if val[0] != contentQuarantineValueVersion {
		return ContentQuarantine{}, fmt.Errorf("index: content quarantine value version %d", val[0])
	}
	detectedAtUs, err := readContentQuarantineInt64(val[9:17], "detected_at_us")
	if err != nil {
		return ContentQuarantine{}, err
	}
	quarantine := ContentQuarantine{
		TransactionID: txID,
		DocumentName:  docName,
		BlockID:       binary.LittleEndian.Uint64(val[1:9]),
		DetectedAtUs:  detectedAtUs,
		ScanType:      ContentQuarantineScanType(val[17]),
		Reason:        ContentQuarantineReason(val[18]),
	}
	if err := validateContentQuarantine(quarantine); err != nil {
		return ContentQuarantine{}, err
	}
	return quarantine, nil
}

func decodeContentQuarantineKey(key []byte) (string, string, error) {
	if !bytes.HasPrefix(key, []byte(contentQuarantinePrefix)) {
		return "", "", errors.New("index: content quarantine key prefix")
	}
	rest := key[len(contentQuarantinePrefix):]
	sep := bytes.IndexByte(rest, 0)
	if sep < 0 {
		return "", "", errors.New("index: content quarantine key missing separator")
	}
	txID := string(rest[:sep])
	docName := string(rest[sep+1:])
	if err := validateContentQuarantineIdentity(txID, docName); err != nil {
		return "", "", err
	}
	return txID, docName, nil
}

func validateContentQuarantine(quarantine ContentQuarantine) error {
	if err := validateContentQuarantineIdentity(quarantine.TransactionID, quarantine.DocumentName); err != nil {
		return err
	}
	if quarantine.DetectedAtUs < 0 {
		return fmt.Errorf("%w: detected_at_us is negative: %d", ErrInvalidContentQuarantine, quarantine.DetectedAtUs)
	}
	if !validContentQuarantineScanType(quarantine.ScanType) {
		return fmt.Errorf("%w: scan_type is required", ErrInvalidContentQuarantine)
	}
	if !validContentQuarantineReason(quarantine.Reason) {
		return fmt.Errorf("%w: reason is required", ErrInvalidContentQuarantine)
	}
	return nil
}

func validateContentQuarantineIdentity(txID, docName string) error {
	if err := validateContentQuarantineText("transaction_id", txID); err != nil {
		return err
	}
	return validateContentQuarantineText("document_name", docName)
}

func validateContentQuarantineText(name, value string) error {
	if value == "" {
		return fmt.Errorf("%w: %s is required", ErrInvalidContentQuarantine, name)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: %s contains control character", ErrInvalidContentQuarantine, name)
		}
	}
	return nil
}

func validContentQuarantineScanType(scanType ContentQuarantineScanType) bool {
	return scanType == ContentQuarantineScanTypeInitial || scanType == ContentQuarantineScanTypeRescan
}

func validContentQuarantineReason(reason ContentQuarantineReason) bool {
	return reason == ContentQuarantineReasonScannerDetection
}

func readContentQuarantineInt64(buf []byte, field string) (int64, error) {
	raw := binary.LittleEndian.Uint64(buf)
	if raw > math.MaxInt64 {
		return 0, fmt.Errorf("index: content quarantine %s overflows int64", field)
	}
	return int64(raw), nil
}
