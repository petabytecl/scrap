package storageformat

import (
	"errors"
	"fmt"

	storagev1 "github.com/petabytecl/scrap/internal/gen/scrap/storage/v1"
	"google.golang.org/protobuf/proto"
)

const CurrentSchemaVersion uint32 = 1

var ErrUnsupportedSchemaVersion = errors.New("storage format: unsupported schema version")

var marshalOptions = proto.MarshalOptions{Deterministic: true}

func MarshalBlockHeader(header *storagev1.BlockHeader) ([]byte, error) {
	if err := validateSchemaVersion("block header", header.GetSchemaVersion()); err != nil {
		return nil, err
	}
	return marshalOptions.Marshal(header)
}

func UnmarshalBlockHeader(data []byte) (*storagev1.BlockHeader, error) {
	var header storagev1.BlockHeader
	if err := proto.Unmarshal(data, &header); err != nil {
		return nil, err
	}
	if err := validateSchemaVersion("block header", header.GetSchemaVersion()); err != nil {
		return nil, err
	}
	return &header, nil
}

func MarshalBlockIndex(index *storagev1.BlockIndex) ([]byte, error) {
	if err := validateSchemaVersion("block index", index.GetSchemaVersion()); err != nil {
		return nil, err
	}
	return marshalOptions.Marshal(index)
}

func UnmarshalBlockIndex(data []byte) (*storagev1.BlockIndex, error) {
	var index storagev1.BlockIndex
	if err := proto.Unmarshal(data, &index); err != nil {
		return nil, err
	}
	if err := validateSchemaVersion("block index", index.GetSchemaVersion()); err != nil {
		return nil, err
	}
	return &index, nil
}

func MarshalEnvelopeRecord(record *storagev1.EnvelopeRecord) ([]byte, error) {
	if err := validateSchemaVersion("envelope", record.GetSchemaVersion()); err != nil {
		return nil, err
	}
	return marshalOptions.Marshal(record)
}

func UnmarshalEnvelopeRecord(data []byte) (*storagev1.EnvelopeRecord, error) {
	var record storagev1.EnvelopeRecord
	if err := proto.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	if err := validateSchemaVersion("envelope", record.GetSchemaVersion()); err != nil {
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
