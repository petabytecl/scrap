package storageformat

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"

	storagev1 "github.com/petabytecl/scrap/internal/gen/scrap/storage/v1"
)

const CurrentSchemaVersion uint32 = 1

var (
	ErrUnsupportedSchemaVersion = errors.New("storage format: unsupported schema version")
	ErrInvalidRecord            = errors.New("storage format: invalid record")
)

var marshalOptions = proto.MarshalOptions{Deterministic: true}

func MarshalBlockHeader(header *storagev1.BlockHeader) ([]byte, error) {
	if err := validateBlockHeader(header); err != nil {
		return nil, err
	}
	return marshalOptions.Marshal(header)
}

func UnmarshalBlockHeader(data []byte) (*storagev1.BlockHeader, error) {
	var header storagev1.BlockHeader
	if err := proto.Unmarshal(data, &header); err != nil {
		return nil, err
	}
	if err := validateBlockHeader(&header); err != nil {
		return nil, err
	}
	return &header, nil
}

func MarshalBlockIndex(index *storagev1.BlockIndex) ([]byte, error) {
	if err := validateBlockIndex(index, true); err != nil {
		return nil, err
	}
	return marshalOptions.Marshal(index)
}

func UnmarshalBlockIndex(data []byte) (*storagev1.BlockIndex, error) {
	var index storagev1.BlockIndex
	if err := proto.Unmarshal(data, &index); err != nil {
		return nil, err
	}
	if err := validateBlockIndex(&index, true); err != nil {
		return nil, err
	}
	return &index, nil
}

func BlockIndexSHA256(index *storagev1.BlockIndex) ([]byte, error) {
	if err := validateBlockIndex(index, false); err != nil {
		return nil, err
	}
	digest, err := blockIndexDigest(index)
	if err != nil {
		return nil, err
	}
	return digest, nil
}

func MarshalEnvelopeRecord(record *storagev1.EnvelopeRecord) ([]byte, error) {
	if err := validateEnvelopeRecord(record, true); err != nil {
		return nil, err
	}
	return marshalOptions.Marshal(record)
}

func UnmarshalEnvelopeRecord(data []byte) (*storagev1.EnvelopeRecord, error) {
	var record storagev1.EnvelopeRecord
	if err := proto.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	if err := validateEnvelopeRecord(&record, true); err != nil {
		return nil, err
	}
	return &record, nil
}

func EnvelopeRecordSHA256(record *storagev1.EnvelopeRecord) ([]byte, error) {
	if err := validateEnvelopeRecord(record, false); err != nil {
		return nil, err
	}
	digest, err := envelopeRecordDigest(record)
	if err != nil {
		return nil, err
	}
	return digest, nil
}

func MarshalBackendObjectSet(set *storagev1.BackendObjectSet) ([]byte, error) {
	if err := validateBackendObjectSet(set); err != nil {
		return nil, err
	}
	return marshalOptions.Marshal(set)
}

func UnmarshalBackendObjectSet(data []byte) (*storagev1.BackendObjectSet, error) {
	var set storagev1.BackendObjectSet
	if err := proto.Unmarshal(data, &set); err != nil {
		return nil, err
	}
	if err := validateBackendObjectSet(&set); err != nil {
		return nil, err
	}
	return &set, nil
}

func validateSchemaVersion(recordKind string, version uint32) error {
	if version != CurrentSchemaVersion {
		return fmt.Errorf("%w: %s version %d", ErrUnsupportedSchemaVersion, recordKind, version)
	}
	return nil
}

func validateBlockHeader(header *storagev1.BlockHeader) error {
	if header == nil {
		return invalidRecord("block header", "record is required")
	}
	if err := validateSchemaVersion("block header", header.GetSchemaVersion()); err != nil {
		return err
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "block_id", value: header.GetBlockId()},
		{name: "shard_id", value: header.GetShardId()},
	} {
		if field.value == "" {
			return invalidRecord("block header", "%s is required", field.name)
		}
	}
	if header.GetHeaderLength() == 0 {
		return invalidRecord("block header", "header_length is required")
	}
	if header.GetFrameSize() == 0 {
		return invalidRecord("block header", "frame_size is required")
	}
	return validateTimestamp("block header", "created_at", header.GetCreatedAt())
}

func validateBlockIndex(index *storagev1.BlockIndex, requireDigest bool) error {
	if index == nil {
		return invalidRecord("block index", "record is required")
	}
	if err := validateSchemaVersion("block index", index.GetSchemaVersion()); err != nil {
		return err
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "block_id", value: index.GetBlockId()},
		{name: "shard_id", value: index.GetShardId()},
	} {
		if field.value == "" {
			return invalidRecord("block index", "%s is required", field.name)
		}
	}
	if index.GetFormatVersion() == 0 {
		return invalidRecord("block index", "format_version is required")
	}
	if index.GetBlockLength() == 0 {
		return invalidRecord("block index", "block_length is required")
	}
	if len(index.GetBlockSha256()) != 32 {
		return invalidRecord("block index", "block_sha256 must be 32 bytes")
	}
	if len(index.GetDocuments()) == 0 {
		return invalidRecord("block index", "documents are required")
	}
	if len(index.GetFrames()) == 0 {
		return invalidRecord("block index", "frames are required")
	}
	for i, frame := range index.GetFrames() {
		if err := validateFrameChecksumRecord(frame, i, index.GetBlockLength()); err != nil {
			return err
		}
	}
	for i, document := range index.GetDocuments() {
		if err := validateIndexDocumentRecord(document, i, index.GetFrames(), index.GetBlockLength()); err != nil {
			return err
		}
	}
	if err := validateTimestamp("block index", "created_at", index.GetCreatedAt()); err != nil {
		return err
	}
	if requireDigest {
		if len(index.GetIndexSha256()) != 32 {
			return invalidRecord("block index", "index_sha256 must be 32 bytes")
		}
		digest, err := blockIndexDigest(index)
		if err != nil {
			return err
		}
		if !bytes.Equal(index.GetIndexSha256(), digest) {
			return invalidRecord("block index", "index_sha256 does not match record payload")
		}
	}
	return nil
}

func validateIndexDocumentRecord(document *storagev1.IndexDocumentRecord, index int, frames []*storagev1.FrameChecksumRecord, blockLength uint64) error {
	recordKind := fmt.Sprintf("block index document %d", index)
	if document == nil {
		return invalidRecord(recordKind, "record is required")
	}
	if document.GetDocumentKeyId() == 0 {
		return invalidRecord(recordKind, "document_key_id is required")
	}
	if document.GetTransactionKeyId() == 0 {
		return invalidRecord(recordKind, "transaction_key_id is required")
	}
	if len(document.GetDocumentNameFingerprint()) != 16 {
		return invalidRecord(recordKind, "document_name_fingerprint must be 16 bytes")
	}
	if len(document.GetDocumentIdentityFingerprint()) != 16 {
		return invalidRecord(recordKind, "document_identity_fingerprint must be 16 bytes")
	}
	if document.GetStoredLength() == 0 {
		return invalidRecord(recordKind, "stored_length is required")
	}
	if document.GetStoredOffset()+document.GetStoredLength() < document.GetStoredOffset() ||
		document.GetStoredOffset()+document.GetStoredLength() > blockLength {
		return invalidRecord(recordKind, "stored range exceeds block length")
	}
	if document.GetLogicalLength() == 0 {
		return invalidRecord(recordKind, "logical_length is required")
	}
	if len(document.GetLogicalSha256()) != 32 {
		return invalidRecord(recordKind, "logical_sha256 must be 32 bytes")
	}
	if len(document.GetStoredSha256()) != 32 {
		return invalidRecord(recordKind, "stored_sha256 must be 32 bytes")
	}
	if document.GetDocumentClass() == 0 {
		return invalidRecord(recordKind, "document_class is required")
	}
	if document.GetPriorityClass() == 0 {
		return invalidRecord(recordKind, "priority_class is required")
	}
	if document.GetCreatedAtMs() == 0 {
		return invalidRecord(recordKind, "created_at_ms is required")
	}
	if len(document.GetMetadataBlob()) == 0 {
		return invalidRecord(recordKind, "metadata_blob is required")
	}
	if len(document.GetTransactionFingerprint()) != 16 {
		return invalidRecord(recordKind, "transaction_fingerprint must be 16 bytes")
	}
	if int(document.GetFirstFrameIndex()) >= len(frames) || int(document.GetLastFrameIndex()) >= len(frames) {
		return invalidRecord(recordKind, "frame index range exceeds frames")
	}
	if document.GetFirstFrameIndex() > document.GetLastFrameIndex() {
		return invalidRecord(recordKind, "first_frame_index must be before last_frame_index")
	}
	return validateDocumentFrameCoverage(recordKind, document, frames)
}

func validateDocumentFrameCoverage(recordKind string, document *storagev1.IndexDocumentRecord, frames []*storagev1.FrameChecksumRecord) error {
	start := document.GetStoredOffset()
	end := start + document.GetStoredLength()
	coveredUntil := start
	for i := int(document.GetFirstFrameIndex()); i <= int(document.GetLastFrameIndex()); i++ {
		frame := frames[i]
		frameStart := frame.GetStoredOffset()
		frameEnd := frameStart + frame.GetStoredLength()
		if frameEnd <= start {
			continue
		}
		if frameStart >= end {
			break
		}
		if frameStart > coveredUntil {
			return invalidRecord(recordKind, "frame checksums leave a gap before offset %d", frameStart)
		}
		if frameEnd > coveredUntil {
			coveredUntil = frameEnd
		}
		if coveredUntil >= end {
			return nil
		}
	}
	return invalidRecord(recordKind, "frame checksums do not cover stored range")
}

func validateFrameChecksumRecord(frame *storagev1.FrameChecksumRecord, index int, blockLength uint64) error {
	recordKind := fmt.Sprintf("frame checksum %d", index)
	if frame == nil {
		return invalidRecord(recordKind, "record is required")
	}
	if int(frame.GetFrameIndex()) != index {
		return invalidRecord(recordKind, "frame_index must match record order")
	}
	if frame.GetPlaintextLength() == 0 {
		return invalidRecord(recordKind, "plaintext_length is required")
	}
	if frame.GetStoredLength() == 0 {
		return invalidRecord(recordKind, "stored_length is required")
	}
	if frame.GetStoredOffset()+frame.GetStoredLength() < frame.GetStoredOffset() ||
		frame.GetStoredOffset()+frame.GetStoredLength() > blockLength {
		return invalidRecord(recordKind, "stored range exceeds block length")
	}
	if len(frame.GetPlaintextSha256()) != 32 {
		return invalidRecord(recordKind, "plaintext_sha256 must be 32 bytes")
	}
	if len(frame.GetStoredSha256()) != 32 {
		return invalidRecord(recordKind, "stored_sha256 must be 32 bytes")
	}
	if frame.GetEncryptionMode() == storagev1.EncryptionMode_ENCRYPTION_MODE_UNSPECIFIED {
		return invalidRecord(recordKind, "encryption_mode is required")
	}
	if frame.GetEncryptionMode() == storagev1.EncryptionMode_ENCRYPTION_MODE_NONE && len(frame.GetAuthTag()) != 0 {
		return invalidRecord(recordKind, "auth_tag must be empty without encryption")
	}
	if frame.GetEncryptionMode() != storagev1.EncryptionMode_ENCRYPTION_MODE_NONE && len(frame.GetAuthTag()) == 0 {
		return invalidRecord(recordKind, "auth_tag is required for encrypted frames")
	}
	return nil
}

func validateEnvelopeRecord(record *storagev1.EnvelopeRecord, requireDigest bool) error {
	if record == nil {
		return invalidRecord("envelope", "record is required")
	}
	if err := validateSchemaVersion("envelope", record.GetSchemaVersion()); err != nil {
		return err
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "envelope_id", value: record.GetEnvelopeId()},
		{name: "block_id", value: record.GetBlockId()},
		{name: "key_id", value: record.GetKeyId()},
		{name: "dek_algorithm", value: record.GetDekAlgorithm()},
		{name: "aead_algorithm", value: record.GetAeadAlgorithm()},
	} {
		if field.value == "" {
			return invalidRecord("envelope", "%s is required", field.name)
		}
	}
	if record.GetKeyVersion() == 0 {
		return invalidRecord("envelope", "key_version is required")
	}
	if len(record.GetWrappedDek()) == 0 {
		return invalidRecord("envelope", "wrapped_dek is required")
	}
	if len(record.GetAadContext()) == 0 {
		return invalidRecord("envelope", "aad_context is required")
	}
	if err := validateTimestamp("envelope", "created_at", record.GetCreatedAt()); err != nil {
		return err
	}
	if requireDigest {
		if len(record.GetEnvelopeSha256()) != 32 {
			return invalidRecord("envelope", "envelope_sha256 must be 32 bytes")
		}
		digest, err := envelopeRecordDigest(record)
		if err != nil {
			return err
		}
		if !bytes.Equal(record.GetEnvelopeSha256(), digest) {
			return invalidRecord("envelope", "envelope_sha256 does not match record payload")
		}
	}
	return nil
}

func validateBackendObjectSet(set *storagev1.BackendObjectSet) error {
	if set == nil {
		return invalidRecord("backend object set", "record is required")
	}
	if err := validateSchemaVersion("backend object set", set.GetSchemaVersion()); err != nil {
		return err
	}
	if set.GetBlockId() == "" {
		return invalidRecord("backend object set", "block_id is required")
	}
	if err := validateBackendObjectRef("backend object set block_object", set.GetBlockObject(), storagev1.BackendObjectKind_BACKEND_OBJECT_KIND_BLOCK); err != nil {
		return err
	}
	if err := validateBackendObjectRef("backend object set index_object", set.GetIndexObject(), storagev1.BackendObjectKind_BACKEND_OBJECT_KIND_INDEX); err != nil {
		return err
	}
	if set.EnvelopeObject != nil {
		if err := validateBackendObjectRef("backend object set envelope_object", set.GetEnvelopeObject(), storagev1.BackendObjectKind_BACKEND_OBJECT_KIND_ENVELOPE); err != nil {
			return err
		}
		if set.EnvelopeRef == nil {
			return invalidRecord("backend object set", "envelope_ref is required when envelope_object is present")
		}
	}
	if set.EnvelopeRef != nil {
		if err := validateEnvelopeReference("backend object set envelope_ref", set.GetEnvelopeRef()); err != nil {
			return err
		}
		if set.EnvelopeObject == nil {
			return invalidRecord("backend object set", "envelope_object is required when envelope_ref is present")
		}
	}
	return validateTimestamp("backend object set", "created_at", set.GetCreatedAt())
}

func validateEnvelopeReference(recordKind string, ref *storagev1.EnvelopeReference) error {
	if ref == nil {
		return invalidRecord(recordKind, "record is required")
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "envelope_id", value: ref.GetEnvelopeId()},
		{name: "key_id", value: ref.GetKeyId()},
	} {
		if field.value == "" {
			return invalidRecord(recordKind, "%s is required", field.name)
		}
	}
	if ref.GetKeyVersion() == 0 {
		return invalidRecord(recordKind, "key_version is required")
	}
	if len(ref.GetEnvelopeSha256()) != 32 {
		return invalidRecord(recordKind, "envelope_sha256 must be 32 bytes")
	}
	return validateBackendObjectRef(recordKind+" envelope_object", ref.GetEnvelopeObject(), storagev1.BackendObjectKind_BACKEND_OBJECT_KIND_ENVELOPE)
}

func validateBackendObjectRef(recordKind string, ref *storagev1.BackendObjectRef, kind storagev1.BackendObjectKind) error {
	if ref == nil {
		return invalidRecord(recordKind, "record is required")
	}
	if ref.GetKind() != kind {
		return invalidRecord(recordKind, "kind must be %s", kind.String())
	}
	if ref.GetBackendId() == "" {
		return invalidRecord(recordKind, "backend_id is required")
	}
	if ref.GetObjectKey() == "" {
		return invalidRecord(recordKind, "object_key is required")
	}
	if ref.GetLength() == 0 {
		return invalidRecord(recordKind, "length is required")
	}
	if len(ref.GetSha256()) != 32 {
		return invalidRecord(recordKind, "sha256 must be 32 bytes")
	}
	return nil
}

func validateTimestamp(recordKind, field string, value *timestamppb.Timestamp) error {
	if value == nil {
		return invalidRecord(recordKind, "%s is required", field)
	}
	if err := value.CheckValid(); err != nil {
		return fmt.Errorf("%w: %s %s is invalid: %w", ErrInvalidRecord, recordKind, field, err)
	}
	if value.AsTime().IsZero() {
		return invalidRecord(recordKind, "%s must be non-zero", field)
	}
	return nil
}

func blockIndexDigest(index *storagev1.BlockIndex) ([]byte, error) {
	record := proto.Clone(index).(*storagev1.BlockIndex)
	record.IndexSha256 = nil
	clearUnknownFields(record)
	data, err := marshalOptions.Marshal(record)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	return append([]byte(nil), sum[:]...), nil
}

func envelopeRecordDigest(record *storagev1.EnvelopeRecord) ([]byte, error) {
	payload := proto.Clone(record).(*storagev1.EnvelopeRecord)
	payload.EnvelopeSha256 = nil
	clearUnknownFields(payload)
	data, err := marshalOptions.Marshal(payload)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	return append([]byte(nil), sum[:]...), nil
}

func clearUnknownFields(message proto.Message) {
	fields := message.ProtoReflect()
	fields.SetUnknown(nil)
	fields.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		if field.IsList() && field.Kind() == protoreflect.MessageKind {
			list := value.List()
			for i := range list.Len() {
				clearUnknownFields(list.Get(i).Message().Interface())
			}
			return true
		}
		if field.Kind() == protoreflect.MessageKind {
			clearUnknownFields(value.Message().Interface())
		}
		return true
	})
}

func invalidRecord(recordKind, format string, args ...any) error {
	return fmt.Errorf("%w: %s %s", ErrInvalidRecord, recordKind, fmt.Sprintf(format, args...))
}
