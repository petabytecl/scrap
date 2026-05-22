package published

import (
	"errors"
	"fmt"

	publishedv1 "github.com/petabytecl/scrap/internal/gen/scrap/published/v1"
	"google.golang.org/protobuf/proto"
)

const CurrentSchemaVersion uint32 = 1

var ErrUnsupportedSchemaVersion = errors.New("published metadata: unsupported schema version")

var marshalOptions = proto.MarshalOptions{Deterministic: true}

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

func validateSchemaVersion(recordKind string, version uint32) error {
	if version != CurrentSchemaVersion {
		return fmt.Errorf("%w: %s version %d", ErrUnsupportedSchemaVersion, recordKind, version)
	}
	return nil
}
