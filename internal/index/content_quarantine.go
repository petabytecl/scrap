package index

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"unicode"

	"github.com/cockroachdb/pebble"

	storeapi "github.com/petabytecl/scrap/internal/store"
)

var (
	ErrContentQuarantineNotFound = errors.New("index: content quarantine not found")
	ErrInvalidContentQuarantine  = errors.New("index: invalid content quarantine")
)

const (
	contentQuarantinePrefix         = "q\x01"
	contentQuarantineMaxKeyByte     = 0xff
	contentQuarantineValueVersionV1 = 0x01
	contentQuarantineValueVersion   = 0x02
	contentQuarantineValueLenV1     = 1 + sizeBlockID + 8 + 1 + 1
	contentQuarantineValueLen       = contentQuarantineValueLenV1 + 8
	// time.Time.MarshalJSON accepts years through 9999. Projection values above
	// this bound are corrupt for the admin evidence surface and must fail closed.
	maxContentQuarantineUnixMicro = 253402300799999999
	// contentQuarantineListPrealloc caps the initial allocation so a large
	// caller-supplied limit cannot drive an oversized make.
	contentQuarantineListPrealloc = 64
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
	ConfirmedAtUs int64
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

func (idx *Index) ListContentQuarantines(txID string, limit int) ([]ContentQuarantine, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("%w: limit must be positive", ErrInvalidContentQuarantine)
	}
	lower, upper, err := contentQuarantineScanBounds(txID)
	if err != nil {
		return nil, err
	}
	iter, err := idx.db.NewIter(&pebble.IterOptions{
		LowerBound: lower,
		UpperBound: upper,
	})
	if err != nil {
		return nil, fmt.Errorf("index: content quarantine iter: %w", err)
	}
	defer func() { _ = iter.Close() }()

	records := make([]ContentQuarantine, 0, min(limit, contentQuarantineListPrealloc))
	for iter.First(); iter.Valid() && len(records) < limit; iter.Next() {
		val, err := iter.ValueAndErr()
		if err != nil {
			return nil, fmt.Errorf("index: content quarantine iter value: %w", err)
		}
		keyCopy := append([]byte(nil), iter.Key()...)
		valCopy := append([]byte(nil), val...)
		record, err := decodeContentQuarantine(keyCopy, valCopy)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := iter.Error(); err != nil {
		return nil, fmt.Errorf("index: content quarantine iter: %w", err)
	}
	return records, nil
}

func (idx *Index) ConfirmContentQuarantine(txID, docName string, confirmedAtUs int64) error {
	if confirmedAtUs <= 0 {
		return fmt.Errorf("%w: confirmed_at_us is required", ErrInvalidContentQuarantine)
	}
	quarantine, err := idx.GetContentQuarantine(txID, docName)
	if err != nil {
		return err
	}
	if quarantine.ConfirmedAtUs > 0 {
		return nil
	}
	quarantine.ConfirmedAtUs = confirmedAtUs
	return idx.PutContentQuarantine(quarantine)
}

func (idx *Index) ReleaseContentQuarantine(txID, docName string) error {
	key, err := contentQuarantineKey(txID, docName)
	if err != nil {
		return err
	}
	if _, err := idx.GetContentQuarantine(txID, docName); err != nil {
		return err
	}
	if err := idx.db.Delete(key, pebble.Sync); err != nil {
		return fmt.Errorf("index: release content quarantine: %w", err)
	}
	return nil
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

func contentQuarantineScanBounds(txID string) ([]byte, []byte, error) {
	if txID == "" {
		lower := []byte(contentQuarantinePrefix)
		return lower, contentQuarantinePrefixSuccessor(lower), nil
	}
	if err := validateContentQuarantineText("transaction_id", txID); err != nil {
		return nil, nil, err
	}
	lower := make([]byte, len(contentQuarantinePrefix)+len(txID)+1)
	copy(lower, contentQuarantinePrefix)
	copy(lower[len(contentQuarantinePrefix):], txID)
	lower[len(lower)-1] = 0
	return lower, contentQuarantinePrefixSuccessor(lower), nil
}

func contentQuarantinePrefixSuccessor(prefix []byte) []byte {
	upper := append([]byte(nil), prefix...)
	for i := len(upper) - 1; i >= 0; i-- {
		if upper[i] != contentQuarantineMaxKeyByte {
			upper[i]++
			return upper[:i+1]
		}
	}
	return append(upper, 0)
}

func encodeContentQuarantine(quarantine ContentQuarantine) []byte {
	buf := make([]byte, contentQuarantineValueLen)
	buf[0] = contentQuarantineValueVersion
	binary.LittleEndian.PutUint64(buf[1:9], quarantine.BlockID)
	putNonNegativeInt64(buf[9:17], quarantine.DetectedAtUs)
	buf[17] = byte(quarantine.ScanType)
	buf[18] = byte(quarantine.Reason)
	putNonNegativeInt64(buf[19:27], quarantine.ConfirmedAtUs)
	return buf
}

func decodeContentQuarantine(key, val []byte) (ContentQuarantine, error) {
	txID, docName, err := decodeContentQuarantineKey(key)
	if err != nil {
		return ContentQuarantine{}, err
	}
	version, err := contentQuarantineValueVersionForDecode(val)
	if err != nil {
		return ContentQuarantine{}, err
	}
	detectedAtUs, err := readContentQuarantineInt64(val[9:17], "detected_at_us")
	if err != nil {
		return ContentQuarantine{}, err
	}
	confirmedAtUs := int64(0)
	if version == contentQuarantineValueVersion {
		confirmedAtUs, err = readContentQuarantineInt64(val[19:27], "confirmed_at_us")
		if err != nil {
			return ContentQuarantine{}, err
		}
	}
	quarantine := ContentQuarantine{
		TransactionID: txID,
		DocumentName:  docName,
		BlockID:       binary.LittleEndian.Uint64(val[1:9]),
		DetectedAtUs:  detectedAtUs,
		ConfirmedAtUs: confirmedAtUs,
		ScanType:      ContentQuarantineScanType(val[17]),
		Reason:        ContentQuarantineReason(val[18]),
	}
	if err := validateContentQuarantine(quarantine); err != nil {
		return ContentQuarantine{}, err
	}
	return quarantine, nil
}

func contentQuarantineValueVersionForDecode(val []byte) (byte, error) {
	if len(val) == 0 {
		return 0, fmt.Errorf("%w: value length %d", ErrInvalidContentQuarantine, len(val))
	}
	switch version := val[0]; version {
	case contentQuarantineValueVersionV1:
		return version, requireContentQuarantineValueLen(val, contentQuarantineValueLenV1)
	case contentQuarantineValueVersion:
		return version, requireContentQuarantineValueLen(val, contentQuarantineValueLen)
	default:
		return 0, fmt.Errorf("%w: value version %d", ErrInvalidContentQuarantine, version)
	}
}

func requireContentQuarantineValueLen(val []byte, want int) error {
	if len(val) != want {
		return fmt.Errorf("%w: value length %d", ErrInvalidContentQuarantine, len(val))
	}
	return nil
}

func decodeContentQuarantineKey(key []byte) (string, string, error) {
	if !bytes.HasPrefix(key, []byte(contentQuarantinePrefix)) {
		return "", "", fmt.Errorf("%w: key prefix", ErrInvalidContentQuarantine)
	}
	rest := key[len(contentQuarantinePrefix):]
	sep := bytes.IndexByte(rest, 0)
	if sep < 0 {
		return "", "", fmt.Errorf("%w: key missing separator", ErrInvalidContentQuarantine)
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
	if quarantine.DetectedAtUs <= 0 {
		return fmt.Errorf("%w: detected_at_us is required", ErrInvalidContentQuarantine)
	}
	if quarantine.DetectedAtUs > maxContentQuarantineUnixMicro {
		return fmt.Errorf("%w: detected_at_us exceeds json time range", ErrInvalidContentQuarantine)
	}
	if quarantine.ConfirmedAtUs < 0 {
		return fmt.Errorf("%w: confirmed_at_us must be non-negative", ErrInvalidContentQuarantine)
	}
	if quarantine.ConfirmedAtUs > maxContentQuarantineUnixMicro {
		return fmt.Errorf("%w: confirmed_at_us exceeds json time range", ErrInvalidContentQuarantine)
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
	maxBytes := contentQuarantineTextMaxBytes(name)
	if len(value) > maxBytes {
		return fmt.Errorf("%w: %s exceeds %d bytes", ErrInvalidContentQuarantine, name, maxBytes)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: %s contains control character", ErrInvalidContentQuarantine, name)
		}
	}
	return nil
}

func contentQuarantineTextMaxBytes(name string) int {
	switch name {
	case "transaction_id":
		return storeapi.MaxTransactionIDBytes
	case "document_name":
		return storeapi.MaxDocumentNameBytes
	default:
		return 0
	}
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
		return 0, fmt.Errorf("%w: %s overflows int64", ErrInvalidContentQuarantine, field)
	}
	return int64(raw), nil
}
