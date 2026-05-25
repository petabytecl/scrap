package cryptoenv

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/petabytecl/scrap/internal/backend"
	storagev1 "github.com/petabytecl/scrap/internal/gen/scrap/storage/v1"
	"github.com/petabytecl/scrap/internal/storageformat"
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
	req, err := validateEnvelopeRequest(transit, req)
	if err != nil {
		return EnvelopeMaterial{}, err
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

func validateEnvelopeRequest(transit Transit, req EnvelopeRequest) (EnvelopeRequest, error) {
	if transit == nil {
		return EnvelopeRequest{}, fmt.Errorf("%w: transit client is required", ErrUnavailable)
	}
	if req.BlockID == "" {
		return EnvelopeRequest{}, fmt.Errorf("%w: block_id is required", ErrInvalidEnvelope)
	}
	if req.BlockObject.Key == "" {
		return EnvelopeRequest{}, fmt.Errorf("%w: block object is required", ErrInvalidEnvelope)
	}
	if req.KeyID == "" {
		return EnvelopeRequest{}, fmt.Errorf("%w: key_id is required", ErrInvalidEnvelope)
	}
	return defaultEnvelopeRequest(req), nil
}

func defaultEnvelopeRequest(req EnvelopeRequest) EnvelopeRequest {
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
	return req
}

func ValidateEnvelopeRecordForRestore(ctx context.Context, transit Transit, record *storagev1.EnvelopeRecord) error {
	_, err := UnwrapEnvelopeDataKey(ctx, transit, record)
	return err
}

func UnwrapEnvelopeDataKey(ctx context.Context, transit Transit, record *storagev1.EnvelopeRecord) ([]byte, error) {
	if record == nil {
		return nil, fmt.Errorf("%w: envelope record is required", ErrInvalidEnvelope)
	}
	if record.GetAeadAlgorithm() == "none" {
		return nil, nil
	}
	if transit == nil {
		return nil, fmt.Errorf("%w: transit client is required", ErrUnavailable)
	}
	key, err := transit.UnwrapDataKey(ctx, UnwrapDataKeyRequest{
		KeyID:      record.GetKeyId(),
		KeyVersion: record.GetKeyVersion(),
		WrappedDEK: append([]byte(nil), record.GetWrappedDek()...),
		AAD:        append([]byte(nil), record.GetAadContext()...),
		Algorithm:  record.GetDekAlgorithm(),
	})
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), key.PlaintextDEK...), nil
}

func SealPayloadFrame(plaintextDEK []byte, record *storagev1.EnvelopeRecord, frameIndex uint32, plaintextOffset uint64, plaintext []byte) ([]byte, []byte, error) {
	aead, err := payloadAEAD(plaintextDEK, record)
	if err != nil {
		return nil, nil, err
	}
	nonce := payloadFrameNonce(record, frameIndex, plaintextOffset)
	aad := payloadFrameAAD(record, frameIndex, plaintextOffset, uint64(len(plaintext)))
	ciphertext := aead.Seal(nil, nonce, plaintext, aad)
	authTagLength := aead.Overhead()
	authTag := append([]byte(nil), ciphertext[len(ciphertext)-authTagLength:]...)
	return ciphertext, authTag, nil
}

func OpenPayloadFrame(plaintextDEK []byte, record *storagev1.EnvelopeRecord, frameIndex uint32, plaintextOffset, plaintextLength uint64, ciphertext []byte) ([]byte, error) {
	aead, err := payloadAEAD(plaintextDEK, record)
	if err != nil {
		return nil, err
	}
	nonce := payloadFrameNonce(record, frameIndex, plaintextOffset)
	aad := payloadFrameAAD(record, frameIndex, plaintextOffset, plaintextLength)
	plaintext, err := aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("%w: frame %d authentication failed", ErrInvalidEnvelope, frameIndex)
	}
	if uint64(len(plaintext)) != plaintextLength {
		return nil, fmt.Errorf("%w: frame %d plaintext length mismatch", ErrInvalidEnvelope, frameIndex)
	}
	return plaintext, nil
}

func payloadAEAD(plaintextDEK []byte, record *storagev1.EnvelopeRecord) (cipher.AEAD, error) {
	if record == nil {
		return nil, fmt.Errorf("%w: envelope record is required", ErrInvalidEnvelope)
	}
	if record.GetDekAlgorithm() != DefaultDEKAlgorithm {
		return nil, fmt.Errorf("%w: unsupported DEK algorithm %q", ErrInvalidEnvelope, record.GetDekAlgorithm())
	}
	if record.GetAeadAlgorithm() != DefaultAEADAlgorithm {
		return nil, fmt.Errorf("%w: unsupported AEAD algorithm %q", ErrInvalidEnvelope, record.GetAeadAlgorithm())
	}
	if len(plaintextDEK) == 0 {
		return nil, fmt.Errorf("%w: plaintext DEK is required", ErrKeyMaterialUnavailable)
	}
	key := payloadAEADKey(plaintextDEK, record)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("%w: initialize AES cipher: %w", ErrInvalidEnvelope, err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%w: initialize AES-GCM: %w", ErrInvalidEnvelope, err)
	}
	return aead, nil
}

func payloadAEADKey(plaintextDEK []byte, record *storagev1.EnvelopeRecord) [32]byte {
	hasher := sha256.New()
	writePayloadFramePart(hasher, []byte("scrap/backend-block/aes-256-gcm/key/v1"))
	writePayloadFramePart(hasher, plaintextDEK)
	writePayloadFramePart(hasher, []byte(record.GetEnvelopeId()))
	writePayloadFramePart(hasher, []byte(record.GetBlockId()))
	writePayloadFramePart(hasher, record.GetAadContext())
	var out [32]byte
	copy(out[:], hasher.Sum(nil))
	return out
}

func payloadFrameNonce(record *storagev1.EnvelopeRecord, frameIndex uint32, plaintextOffset uint64) []byte {
	hasher := sha256.New()
	writePayloadFramePart(hasher, []byte("scrap/backend-block/aes-256-gcm/nonce/v1"))
	writePayloadFramePart(hasher, []byte(record.GetEnvelopeId()))
	writePayloadFramePart(hasher, []byte(record.GetBlockId()))
	writePayloadFramePart(hasher, record.GetAadContext())
	var numeric [12]byte
	binary.BigEndian.PutUint32(numeric[:4], frameIndex)
	binary.BigEndian.PutUint64(numeric[4:], plaintextOffset)
	writePayloadFramePart(hasher, numeric[:])
	sum := hasher.Sum(nil)
	return append([]byte(nil), sum[:12]...)
}

func payloadFrameAAD(record *storagev1.EnvelopeRecord, frameIndex uint32, plaintextOffset, plaintextLength uint64) []byte {
	hasher := sha256.New()
	writePayloadFramePart(hasher, []byte("scrap/backend-block/aes-256-gcm/aad/v1"))
	writePayloadFramePart(hasher, []byte(record.GetEnvelopeId()))
	writePayloadFramePart(hasher, []byte(record.GetBlockId()))
	writePayloadFramePart(hasher, record.GetAadContext())
	var numeric [20]byte
	binary.BigEndian.PutUint32(numeric[:4], frameIndex)
	binary.BigEndian.PutUint64(numeric[4:12], plaintextOffset)
	binary.BigEndian.PutUint64(numeric[12:], plaintextLength)
	writePayloadFramePart(hasher, numeric[:])
	return hasher.Sum(nil)
}

func writePayloadFramePart(hasher hash.Hash, part []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(part)))
	_, _ = hasher.Write(length[:])
	_, _ = hasher.Write(part)
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

func EnvelopeAAD(cellID, blockID string, blockObject backend.Object) []byte {
	return []byte(fmt.Sprintf("%s\x00%s\x00%s", cellID, blockID, blockObject.Key))
}

func IsUnavailable(err error) bool {
	return errors.Is(err, ErrUnavailable) || errors.Is(err, ErrKeyMaterialUnavailable)
}
