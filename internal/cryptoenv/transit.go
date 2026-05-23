package cryptoenv

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/petabytecl/scrap/internal/backend"
	storagev1 "github.com/petabytecl/scrap/internal/gen/scrap/storage/v1"
	"github.com/petabytecl/scrap/internal/storageformat"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	DefaultDEKAlgorithm  = "aes-256"
	DefaultAEADAlgorithm = "aes-256-gcm"
)

var (
	ErrUnavailable            = errors.New("cryptoenv: transit unavailable")
	ErrKeyMaterialUnavailable = errors.New("cryptoenv: key material unavailable")
	ErrInvalidEnvelope        = errors.New("cryptoenv: invalid envelope")
)

type Transit interface {
	GenerateDataKey(context.Context, GenerateDataKeyRequest) (DataKey, error)
	UnwrapDataKey(context.Context, UnwrapDataKeyRequest) (DataKey, error)
	RewrapDataKey(context.Context, RewrapDataKeyRequest) (WrappedKey, error)
}

type GenerateDataKeyRequest struct {
	KeyID     string
	AAD       []byte
	Algorithm string
}

type UnwrapDataKeyRequest struct {
	KeyID      string
	KeyVersion uint32
	WrappedDEK []byte
	AAD        []byte
	Algorithm  string
}

type RewrapDataKeyRequest struct {
	SourceKeyID      string
	SourceKeyVersion uint32
	DestinationKeyID string
	WrappedDEK       []byte
	AAD              []byte
	Algorithm        string
}

type DataKey struct {
	KeyID        string
	KeyVersion   uint32
	PlaintextDEK []byte
	WrappedDEK   []byte
}

type WrappedKey struct {
	KeyID      string
	KeyVersion uint32
	WrappedDEK []byte
}

type EnvelopeRequest struct {
	BlockID       string
	CellID        string
	BlockObject   backend.Object
	KeyID         string
	CreatedAt     time.Time
	DEKAlgorithm  string
	AEADAlgorithm string
}

type EnvelopeMaterial struct {
	Record       *storagev1.EnvelopeRecord
	PlaintextDEK []byte
}

func CreateEnvelopeRecord(ctx context.Context, transit Transit, req EnvelopeRequest) (EnvelopeMaterial, error) {
	if transit == nil {
		return EnvelopeMaterial{}, fmt.Errorf("%w: transit client is required", ErrUnavailable)
	}
	if req.BlockID == "" {
		return EnvelopeMaterial{}, fmt.Errorf("%w: block_id is required", ErrInvalidEnvelope)
	}
	if req.BlockObject.Key == "" {
		return EnvelopeMaterial{}, fmt.Errorf("%w: block object is required", ErrInvalidEnvelope)
	}
	if req.KeyID == "" {
		return EnvelopeMaterial{}, fmt.Errorf("%w: key_id is required", ErrInvalidEnvelope)
	}
	if req.CellID == "" {
		req.CellID = "local"
	}
	if req.CreatedAt.IsZero() {
		req.CreatedAt = time.Unix(0, 0).UTC()
	}
	if req.DEKAlgorithm == "" {
		req.DEKAlgorithm = DefaultDEKAlgorithm
	}
	if req.AEADAlgorithm == "" {
		req.AEADAlgorithm = DefaultAEADAlgorithm
	}
	aad := EnvelopeAAD(req.CellID, req.BlockID, req.BlockObject)
	dataKey, err := transit.GenerateDataKey(ctx, GenerateDataKeyRequest{
		KeyID:     req.KeyID,
		AAD:       aad,
		Algorithm: req.DEKAlgorithm,
	})
	if err != nil {
		return EnvelopeMaterial{}, err
	}
	if dataKey.KeyID == "" {
		dataKey.KeyID = req.KeyID
	}
	record := &storagev1.EnvelopeRecord{
		SchemaVersion: storageformat.CurrentSchemaVersion,
		EnvelopeId:    "openbao-transit:" + req.BlockID,
		BlockId:       req.BlockID,
		KeyId:         dataKey.KeyID,
		KeyVersion:    dataKey.KeyVersion,
		WrappedDek:    append([]byte(nil), dataKey.WrappedDEK...),
		DekAlgorithm:  req.DEKAlgorithm,
		AeadAlgorithm: req.AEADAlgorithm,
		AadContext:    aad,
		CreatedAt:     timestamppb.New(req.CreatedAt),
	}
	digest, err := storageformat.EnvelopeRecordSHA256(record)
	if err != nil {
		return EnvelopeMaterial{}, err
	}
	record.EnvelopeSha256 = digest
	return EnvelopeMaterial{
		Record:       record,
		PlaintextDEK: append([]byte(nil), dataKey.PlaintextDEK...),
	}, nil
}

func ValidateEnvelopeRecordForRestore(ctx context.Context, transit Transit, record *storagev1.EnvelopeRecord) error {
	if transit == nil {
		return fmt.Errorf("%w: transit client is required", ErrUnavailable)
	}
	if record.GetAeadAlgorithm() == "none" {
		return nil
	}
	if _, err := transit.UnwrapDataKey(ctx, UnwrapDataKeyRequest{
		KeyID:      record.GetKeyId(),
		KeyVersion: record.GetKeyVersion(),
		WrappedDEK: append([]byte(nil), record.GetWrappedDek()...),
		AAD:        append([]byte(nil), record.GetAadContext()...),
		Algorithm:  record.GetDekAlgorithm(),
	}); err != nil {
		return err
	}
	return nil
}

func RewrapEnvelopeRecord(ctx context.Context, transit Transit, record *storagev1.EnvelopeRecord, destinationKeyID string, createdAt time.Time) (*storagev1.EnvelopeRecord, error) {
	if transit == nil {
		return nil, fmt.Errorf("%w: transit client is required", ErrUnavailable)
	}
	if record == nil {
		return nil, fmt.Errorf("%w: envelope record is required", ErrInvalidEnvelope)
	}
	if destinationKeyID == "" {
		return nil, fmt.Errorf("%w: destination key_id is required", ErrInvalidEnvelope)
	}
	if createdAt.IsZero() {
		createdAt = time.Unix(0, 0).UTC()
	}
	wrapped, err := transit.RewrapDataKey(ctx, RewrapDataKeyRequest{
		SourceKeyID:      record.GetKeyId(),
		SourceKeyVersion: record.GetKeyVersion(),
		DestinationKeyID: destinationKeyID,
		WrappedDEK:       append([]byte(nil), record.GetWrappedDek()...),
		AAD:              append([]byte(nil), record.GetAadContext()...),
		Algorithm:        record.GetDekAlgorithm(),
	})
	if err != nil {
		return nil, err
	}
	if wrapped.KeyID == "" {
		wrapped.KeyID = destinationKeyID
	}
	rewrapped := proto.Clone(record).(*storagev1.EnvelopeRecord)
	rewrapped.KeyId = wrapped.KeyID
	rewrapped.KeyVersion = wrapped.KeyVersion
	rewrapped.WrappedDek = append([]byte(nil), wrapped.WrappedDEK...)
	rewrapped.CreatedAt = timestamppb.New(createdAt)
	rewrapped.EnvelopeSha256 = nil
	digest, err := storageformat.EnvelopeRecordSHA256(rewrapped)
	if err != nil {
		return nil, err
	}
	rewrapped.EnvelopeSha256 = digest
	return rewrapped, nil
}

func EnvelopeAAD(cellID string, blockID string, blockObject backend.Object) []byte {
	return []byte(fmt.Sprintf("%s\x00%s\x00%s\x00%x", cellID, blockID, blockObject.Key, blockObject.SHA256))
}

func IsUnavailable(err error) bool {
	return errors.Is(err, ErrUnavailable) || errors.Is(err, ErrKeyMaterialUnavailable)
}
