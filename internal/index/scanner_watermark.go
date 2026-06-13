package index

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/cockroachdb/pebble"
)

var ErrScannerWatermarkNotFound = errors.New("index: scanner watermark not found")

const (
	scannerWatermarkPrefix        = "\x00scanner-watermark\x00"
	scannerWatermarkUpperBound    = "\x00scanner-watermark\x01"
	scannerWatermarkCurrentKey    = scannerWatermarkPrefix + "current"
	scannerWatermarkValueVersion  = 0x01
	maxScannerSignatureVersionLen = 128

	scannerWatermarkBlockIDOffset       = 1
	scannerWatermarkSignatureLenOffset  = scannerWatermarkBlockIDOffset + sizeBlockID
	scannerWatermarkSignatureDataOffset = scannerWatermarkSignatureLenOffset + 2
)

type ScannerWatermark struct {
	LastScannedBlockID          uint64
	LastSignatureVersionScanned string
}

func (idx *Index) PutScannerWatermark(watermark ScannerWatermark) error {
	if err := validateScannerSignatureVersion(watermark.LastSignatureVersionScanned); err != nil {
		return err
	}
	return idx.db.Set(scannerWatermarkKey(), encodeScannerWatermark(watermark), pebble.Sync)
}

func (idx *Index) GetScannerWatermark() (ScannerWatermark, error) {
	val, closer, err := idx.db.Get(scannerWatermarkKey())
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return ScannerWatermark{}, ErrScannerWatermarkNotFound
		}
		return ScannerWatermark{}, fmt.Errorf("index: get scanner watermark: %w", err)
	}
	defer func() { _ = closer.Close() }()

	return decodeScannerWatermark(val)
}

func scannerWatermarkKey() []byte {
	return []byte(scannerWatermarkCurrentKey)
}

func encodeScannerWatermark(watermark ScannerWatermark) []byte {
	signatureVersion := []byte(watermark.LastSignatureVersionScanned)
	buf := make([]byte, scannerWatermarkSignatureDataOffset+len(signatureVersion))
	buf[0] = scannerWatermarkValueVersion
	binary.LittleEndian.PutUint64(
		buf[scannerWatermarkBlockIDOffset:scannerWatermarkSignatureLenOffset],
		watermark.LastScannedBlockID,
	)
	binary.LittleEndian.PutUint16(
		buf[scannerWatermarkSignatureLenOffset:scannerWatermarkSignatureDataOffset],
		uint16(len(signatureVersion)), //nolint:gosec // signature version is bounded before encoding.
	)
	copy(buf[scannerWatermarkSignatureDataOffset:], signatureVersion)
	return buf
}

func decodeScannerWatermark(val []byte) (ScannerWatermark, error) {
	if len(val) < scannerWatermarkSignatureDataOffset {
		return ScannerWatermark{}, fmt.Errorf("index: scanner watermark value length %d", len(val))
	}
	if val[0] != scannerWatermarkValueVersion {
		return ScannerWatermark{}, fmt.Errorf("index: scanner watermark value version %d", val[0])
	}

	signatureLen := int(binary.LittleEndian.Uint16(
		val[scannerWatermarkSignatureLenOffset:scannerWatermarkSignatureDataOffset],
	))
	expectedLen := scannerWatermarkSignatureDataOffset + signatureLen
	if len(val) != expectedLen {
		return ScannerWatermark{}, fmt.Errorf("index: scanner watermark value length %d", len(val))
	}

	watermark := ScannerWatermark{
		LastScannedBlockID: binary.LittleEndian.Uint64(
			val[scannerWatermarkBlockIDOffset:scannerWatermarkSignatureLenOffset],
		),
		LastSignatureVersionScanned: string(val[scannerWatermarkSignatureDataOffset:]),
	}
	if err := validateScannerSignatureVersion(watermark.LastSignatureVersionScanned); err != nil {
		return ScannerWatermark{}, err
	}
	return watermark, nil
}

func validateScannerSignatureVersion(version string) error {
	if len(version) > maxScannerSignatureVersionLen {
		return fmt.Errorf("index: scanner signature version length %d", len(version))
	}
	for i := range version {
		if !isScannerSignatureVersionByte(version[i]) {
			return fmt.Errorf("index: scanner signature version contains invalid byte at %d", i)
		}
	}
	return nil
}

func isScannerSignatureVersionByte(b byte) bool {
	if b >= 'a' && b <= 'z' {
		return true
	}
	if b >= 'A' && b <= 'Z' {
		return true
	}
	if b >= '0' && b <= '9' {
		return true
	}
	switch b {
	case '.', '-', '_', ':':
		return true
	default:
		return false
	}
}
