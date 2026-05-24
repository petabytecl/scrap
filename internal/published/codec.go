package published

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"

	"google.golang.org/protobuf/proto"

	publishedv1 "github.com/petabytecl/scrap/internal/gen/scrap/published/v1"
	"github.com/petabytecl/scrap/internal/safeconv"
)

const (
	// CurrentSchemaVersion is the published metadata protobuf schema version.
	// Additive fields keep the current value; required semantic changes must
	// bump it and update the schema evolution changelog.
	CurrentSchemaVersion uint32 = 1
	// CurrentLocationFormatVersion is the per-location physical reference
	// format version inside published metadata.
	CurrentLocationFormatVersion uint32 = 1
	maxArtifactRecordPayload            = 64 * 1024 * 1024
)

var (
	ErrUnsupportedSchemaVersion  = errors.New("published metadata: unsupported schema version")
	ErrArtifactChecksumMismatch  = errors.New("published metadata: artifact record checksum mismatch")
	ErrArtifactRecordTooLarge    = errors.New("published metadata: artifact record too large")
	errArtifactRecordPayloadZero = errors.New("published metadata: artifact record payload is empty")
)

var (
	marshalOptions = proto.MarshalOptions{Deterministic: true}
	crcTable       = crc32.MakeTable(crc32.Castagnoli)
)

func MarshalCurrentPointer(pointer *publishedv1.CurrentPointer) ([]byte, error) {
	if err := validateSchemaVersion("current pointer", pointer.GetSchemaVersion()); err != nil {
		return nil, err
	}
	return marshalOptions.Marshal(pointer)
}

func UnmarshalCurrentPointer(data []byte) (*publishedv1.CurrentPointer, error) {
	var pointer publishedv1.CurrentPointer
	if err := proto.Unmarshal(data, &pointer); err != nil {
		return nil, err
	}
	if err := validateSchemaVersion("current pointer", pointer.GetSchemaVersion()); err != nil {
		return nil, err
	}
	return &pointer, nil
}

func MarshalManifest(manifest *publishedv1.Manifest) ([]byte, error) {
	if err := validateSchemaVersion("manifest", manifest.GetSchemaVersion()); err != nil {
		return nil, err
	}
	return marshalOptions.Marshal(manifest)
}

func UnmarshalManifest(data []byte) (*publishedv1.Manifest, error) {
	var manifest publishedv1.Manifest
	if err := proto.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	if err := validateSchemaVersion("manifest", manifest.GetSchemaVersion()); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func MarshalSnapshotRecord(record *publishedv1.SnapshotRecord) ([]byte, error) {
	if err := validateSchemaVersion("snapshot", record.GetSchemaVersion()); err != nil {
		return nil, err
	}
	return marshalOptions.Marshal(record)
}

func UnmarshalSnapshotRecord(data []byte) (*publishedv1.SnapshotRecord, error) {
	var record publishedv1.SnapshotRecord
	if err := proto.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	if err := validateSchemaVersion("snapshot", record.GetSchemaVersion()); err != nil {
		return nil, err
	}
	return &record, nil
}

func MarshalTailRecord(record *publishedv1.TailRecord) ([]byte, error) {
	if err := validateSchemaVersion("tail", record.GetSchemaVersion()); err != nil {
		return nil, err
	}
	return marshalOptions.Marshal(record)
}

func UnmarshalTailRecord(data []byte) (*publishedv1.TailRecord, error) {
	var record publishedv1.TailRecord
	if err := proto.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	if err := validateSchemaVersion("tail", record.GetSchemaVersion()); err != nil {
		return nil, err
	}
	return &record, nil
}

func WriteSnapshotRecord(writer io.Writer, record *publishedv1.SnapshotRecord) error {
	data, err := MarshalSnapshotRecord(record)
	if err != nil {
		return err
	}
	return writeArtifactRecord(writer, data)
}

func ReadSnapshotRecord(reader io.Reader) (*publishedv1.SnapshotRecord, error) {
	data, err := readArtifactRecord(reader)
	if err != nil {
		return nil, err
	}
	return UnmarshalSnapshotRecord(data)
}

func WriteTailRecord(writer io.Writer, record *publishedv1.TailRecord) error {
	data, err := MarshalTailRecord(record)
	if err != nil {
		return err
	}
	return writeArtifactRecord(writer, data)
}

func ReadTailRecord(reader io.Reader) (*publishedv1.TailRecord, error) {
	data, err := readArtifactRecord(reader)
	if err != nil {
		return nil, err
	}
	return UnmarshalTailRecord(data)
}

func writeArtifactRecord(writer io.Writer, payload []byte) error {
	if len(payload) == 0 {
		return errArtifactRecordPayloadZero
	}
	if len(payload) > maxArtifactRecordPayload {
		return fmt.Errorf("%w: %d bytes", ErrArtifactRecordTooLarge, len(payload))
	}
	payloadLen, err := safeconv.IntToUint32("published artifact payload length", len(payload))
	if err != nil {
		return err
	}
	var header [8]byte
	binary.BigEndian.PutUint32(header[:4], payloadLen)
	binary.BigEndian.PutUint32(header[4:], crc32.Checksum(payload, crcTable))
	if err := writeFull(writer, header[:4]); err != nil {
		return err
	}
	if err := writeFull(writer, payload); err != nil {
		return err
	}
	return writeFull(writer, header[4:])
}

func readArtifactRecord(reader io.Reader) ([]byte, error) {
	var lengthBytes [4]byte
	if _, err := io.ReadFull(reader, lengthBytes[:]); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, io.EOF
		}
		return nil, err
	}
	length := binary.BigEndian.Uint32(lengthBytes[:])
	if length == 0 {
		return nil, errArtifactRecordPayloadZero
	}
	if length > maxArtifactRecordPayload {
		return nil, fmt.Errorf("%w: %d bytes", ErrArtifactRecordTooLarge, length)
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}
	var crcBytes [4]byte
	if _, err := io.ReadFull(reader, crcBytes[:]); err != nil {
		return nil, err
	}
	want := binary.BigEndian.Uint32(crcBytes[:])
	if got := crc32.Checksum(payload, crcTable); got != want {
		return nil, fmt.Errorf("%w: got %08x want %08x", ErrArtifactChecksumMismatch, got, want)
	}
	return payload, nil
}

func writeFull(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := writer.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func validateSchemaVersion(recordKind string, version uint32) error {
	if version != CurrentSchemaVersion {
		return fmt.Errorf("%w: %s version %d", ErrUnsupportedSchemaVersion, recordKind, version)
	}
	return nil
}
