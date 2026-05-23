package storageformat

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"

	storagev1 "github.com/petabytecl/scrap/internal/gen/scrap/storage/v1"
)

func TestStorageFormatSchemaShape(t *testing.T) {
	file := storagev1.File_scrap_storage_v1_storage_proto

	header := file.Messages().ByName("BlockHeader")
	requireField(t, header, "schema_version")
	requireField(t, header, "block_id")
	requireField(t, header, "shard_id")
	requireField(t, header, "format_major")
	requireField(t, header, "format_minor")
	requireField(t, header, "frame_size")

	index := file.Messages().ByName("BlockIndex")
	requireField(t, index, "schema_version")
	requireField(t, index, "block_id")
	requireField(t, index, "documents")
	requireField(t, index, "frames")
	requireField(t, index, "envelope_object_key")
	requireField(t, index, "index_sha256")

	objects := file.Messages().ByName("BackendObjectSet")
	requireField(t, objects, "schema_version")
	requireField(t, objects, "block_id")
	requireField(t, objects, "block_object")
	requireField(t, objects, "index_object")
	requireField(t, objects, "envelope_object")
	requireField(t, objects, "envelope_ref")
	requireField(t, objects, "created_at")

	objectRef := file.Messages().ByName("BackendObjectRef")
	requireField(t, objectRef, "kind")
	requireField(t, objectRef, "backend_id")
	requireField(t, objectRef, "object_key")
	requireField(t, objectRef, "length")
	requireField(t, objectRef, "sha256")
	requireField(t, objectRef, "generation")

	envelopeRef := file.Messages().ByName("EnvelopeReference")
	requireField(t, envelopeRef, "envelope_id")
	requireField(t, envelopeRef, "key_id")
	requireField(t, envelopeRef, "key_version")
	requireField(t, envelopeRef, "envelope_sha256")
	requireField(t, envelopeRef, "envelope_object")

	document := file.Messages().ByName("IndexDocumentRecord")
	requireField(t, document, "document_key_id")
	requireField(t, document, "transaction_key_id")
	requireField(t, document, "document_name_fingerprint")
	requireField(t, document, "document_identity_fingerprint")
	requireField(t, document, "stored_offset")
	requireField(t, document, "stored_length")
	requireField(t, document, "logical_sha256")
	requireField(t, document, "stored_sha256")
	requireField(t, document, "metadata_blob")
	requireField(t, document, "first_frame_index")
	requireField(t, document, "last_frame_index")

	frame := file.Messages().ByName("FrameChecksumRecord")
	requireField(t, frame, "frame_index")
	requireField(t, frame, "plaintext_offset")
	requireField(t, frame, "plaintext_length")
	requireField(t, frame, "stored_offset")
	requireField(t, frame, "stored_length")
	requireField(t, frame, "plaintext_sha256")
	requireField(t, frame, "stored_sha256")
	requireField(t, frame, "encryption_mode")
	requireField(t, frame, "auth_tag")

	envelope := file.Messages().ByName("EnvelopeRecord")
	requireField(t, envelope, "schema_version")
	requireField(t, envelope, "envelope_id")
	requireField(t, envelope, "block_id")
	requireField(t, envelope, "key_id")
	requireField(t, envelope, "key_version")
	requireField(t, envelope, "wrapped_dek")
	requireField(t, envelope, "envelope_sha256")
}

func TestBlockIndexRoundTripPreservesContractFields(t *testing.T) {
	index := blockIndexWithDigest(t)

	data, err := MarshalBlockIndex(index)
	if err != nil {
		t.Fatalf("marshal index: %v", err)
	}
	decoded, err := UnmarshalBlockIndex(data)
	if err != nil {
		t.Fatalf("unmarshal index: %v", err)
	}
	if !proto.Equal(index, decoded) {
		t.Fatalf("decoded index differs\n got: %v\nwant: %v", decoded, index)
	}
}

func TestBlockIndexMarshalIsDeterministic(t *testing.T) {
	first := blockIndexWithDigest(t)
	second := blockIndexWithDigest(t)

	firstData, err := MarshalBlockIndex(first)
	if err != nil {
		t.Fatalf("marshal first index: %v", err)
	}
	secondData, err := MarshalBlockIndex(second)
	if err != nil {
		t.Fatalf("marshal second index: %v", err)
	}
	if !bytes.Equal(firstData, secondData) {
		t.Fatal("deterministic marshal produced different bytes for equivalent indexes")
	}
}

func TestBackendObjectSetRoundTripPreservesProviderNeutralRefs(t *testing.T) {
	set := sampleBackendObjectSet()

	data, err := MarshalBackendObjectSet(set)
	if err != nil {
		t.Fatalf("marshal object set: %v", err)
	}
	decoded, err := UnmarshalBackendObjectSet(data)
	if err != nil {
		t.Fatalf("unmarshal object set: %v", err)
	}
	if !proto.Equal(set, decoded) {
		t.Fatalf("decoded object set differs\n got: %v\nwant: %v", decoded, set)
	}
	if decoded.GetBlockObject().GetBackendId() != "primary" ||
		decoded.GetIndexObject().GetKind() != storagev1.BackendObjectKind_BACKEND_OBJECT_KIND_INDEX ||
		decoded.GetEnvelopeRef().GetEnvelopeId() != "env-1" {
		t.Fatalf("decoded object set = %#v, want provider-neutral block/index/envelope refs", decoded)
	}
}

func TestBlockIndexRejectsUnsupportedSchemaVersion(t *testing.T) {
	index := sampleBlockIndex()
	index.SchemaVersion = CurrentSchemaVersion + 1
	data, err := proto.Marshal(index)
	if err != nil {
		t.Fatalf("marshal index: %v", err)
	}

	_, err = UnmarshalBlockIndex(data)
	if !errors.Is(err, ErrUnsupportedSchemaVersion) {
		t.Fatalf("error = %v, want %v", err, ErrUnsupportedSchemaVersion)
	}
}

func TestBlockIndexPreservesUnknownForwardFields(t *testing.T) {
	data, err := MarshalBlockIndex(blockIndexWithDigest(t))
	if err != nil {
		t.Fatalf("marshal index: %v", err)
	}
	unknown := appendUnknownVarint(nil, 1000, 77)
	data = append(data, unknown...)

	decoded, err := UnmarshalBlockIndex(data)
	if err != nil {
		t.Fatalf("unmarshal index: %v", err)
	}
	if got := decoded.ProtoReflect().GetUnknown(); !bytes.Equal(got, unknown) {
		t.Fatalf("unknown fields = %x, want %x", got, unknown)
	}

	roundTrip, err := MarshalBlockIndex(decoded)
	if err != nil {
		t.Fatalf("marshal round trip: %v", err)
	}
	var reparsed storagev1.BlockIndex
	if err := proto.Unmarshal(roundTrip, &reparsed); err != nil {
		t.Fatalf("reparse index: %v", err)
	}
	if got := reparsed.ProtoReflect().GetUnknown(); !bytes.Equal(got, unknown) {
		t.Fatalf("round-trip unknown fields = %x, want %x", got, unknown)
	}
}

func TestBlockIndexRejectsInvalidRequiredFieldsAndChecksums(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*storagev1.BlockIndex)
	}{
		{
			name: "missing block checksum",
			mutate: func(index *storagev1.BlockIndex) {
				index.BlockSha256 = nil
			},
		},
		{
			name: "corrupt index checksum",
			mutate: func(index *storagev1.BlockIndex) {
				index.IndexSha256[0] ^= 0xff
			},
		},
		{
			name: "missing frame checksum",
			mutate: func(index *storagev1.BlockIndex) {
				index.Frames[0].StoredSha256 = nil
			},
		},
		{
			name: "missing frame coverage",
			mutate: func(index *storagev1.BlockIndex) {
				index.Frames[0].StoredLength--
			},
		},
		{
			name: "missing document checksum",
			mutate: func(index *storagev1.BlockIndex) {
				index.Documents[0].LogicalSha256 = nil
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			index := blockIndexWithDigest(t)
			test.mutate(index)

			if _, err := MarshalBlockIndex(index); !errors.Is(err, ErrInvalidRecord) {
				t.Fatalf("marshal error = %v, want %v", err, ErrInvalidRecord)
			}

			data, err := proto.Marshal(index)
			if err != nil {
				t.Fatalf("raw proto marshal: %v", err)
			}
			if _, err := UnmarshalBlockIndex(data); !errors.Is(err, ErrInvalidRecord) {
				t.Fatalf("unmarshal error = %v, want %v", err, ErrInvalidRecord)
			}
		})
	}
}

func TestBackendObjectSetRejectsMissingOrMismatchedEnvelopeRefs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*storagev1.BackendObjectSet)
	}{
		{
			name: "missing block object",
			mutate: func(set *storagev1.BackendObjectSet) {
				set.BlockObject = nil
			},
		},
		{
			name: "wrong index kind",
			mutate: func(set *storagev1.BackendObjectSet) {
				set.IndexObject.Kind = storagev1.BackendObjectKind_BACKEND_OBJECT_KIND_BLOCK
			},
		},
		{
			name: "missing envelope reference",
			mutate: func(set *storagev1.BackendObjectSet) {
				set.EnvelopeRef = nil
			},
		},
		{
			name: "missing referenced envelope checksum",
			mutate: func(set *storagev1.BackendObjectSet) {
				set.EnvelopeRef.EnvelopeSha256 = nil
			},
		},
		{
			name: "missing envelope object",
			mutate: func(set *storagev1.BackendObjectSet) {
				set.EnvelopeObject = nil
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			set := sampleBackendObjectSet()
			test.mutate(set)

			if _, err := MarshalBackendObjectSet(set); !errors.Is(err, ErrInvalidRecord) {
				t.Fatalf("marshal error = %v, want %v", err, ErrInvalidRecord)
			}
		})
	}
}

func TestHeaderAndEnvelopeValidateSchemaVersion(t *testing.T) {
	headerData, err := MarshalBlockHeader(&storagev1.BlockHeader{
		SchemaVersion: CurrentSchemaVersion,
		BlockId:       "block-1",
		ShardId:       "tenant-txn",
		FormatMajor:   1,
		FormatMinor:   0,
		HeaderLength:  64,
		FrameSize:     1024 * 1024,
		CreatedAt:     timestamppb.New(time.Unix(100, 0).UTC()),
	})
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	if _, err := UnmarshalBlockHeader(headerData); err != nil {
		t.Fatalf("unmarshal header: %v", err)
	}

	envelope := &storagev1.EnvelopeRecord{
		SchemaVersion: CurrentSchemaVersion,
		EnvelopeId:    "env-1",
		BlockId:       "block-1",
		KeyId:         "transit/key",
		KeyVersion:    3,
		WrappedDek:    []byte{1, 2, 3},
		DekAlgorithm:  "AES-256",
		AeadAlgorithm: "AES-256-GCM",
		AadContext:    []byte("cell-a:block-1"),
		CreatedAt:     timestamppb.New(time.Unix(100, 0).UTC()),
	}
	envelope.EnvelopeSha256 = envelopeDigest(t, envelope)
	envelopeData, err := MarshalEnvelopeRecord(envelope)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	if _, err := UnmarshalEnvelopeRecord(envelopeData); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
}

func TestEnvelopeRecordRejectsMissingOrCorruptDigest(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*storagev1.EnvelopeRecord)
	}{
		{
			name: "missing digest",
			mutate: func(record *storagev1.EnvelopeRecord) {
				record.EnvelopeSha256 = nil
			},
		},
		{
			name: "corrupt digest",
			mutate: func(record *storagev1.EnvelopeRecord) {
				record.EnvelopeSha256[0] ^= 0xff
			},
		},
		{
			name: "missing wrapped dek",
			mutate: func(record *storagev1.EnvelopeRecord) {
				record.WrappedDek = nil
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := sampleEnvelopeRecord(t)
			test.mutate(record)

			if _, err := MarshalEnvelopeRecord(record); !errors.Is(err, ErrInvalidRecord) {
				t.Fatalf("marshal error = %v, want %v", err, ErrInvalidRecord)
			}
		})
	}
}

func sampleBlockIndex() *storagev1.BlockIndex {
	now := timestamppb.New(time.Unix(100, 0).UTC())
	return &storagev1.BlockIndex{
		SchemaVersion:     CurrentSchemaVersion,
		BlockId:           "block-1",
		ShardId:           "tenant-txn",
		FormatVersion:     1,
		BlockLength:       1024,
		BlockSha256:       bytes32(1),
		EnvelopeObjectKey: stringPtr("blocks/block-1.env"),
		CreatedAt:         now,
		Documents: []*storagev1.IndexDocumentRecord{
			{
				DocumentKeyId:               1,
				TransactionKeyId:            2,
				DocumentNameFingerprint:     bytes16(3),
				DocumentIdentityFingerprint: bytes16(4),
				StoredOffset:                64,
				StoredLength:                42,
				LogicalLength:               42,
				LogicalSha256:               bytes32(5),
				StoredSha256:                bytes32(6),
				ContentTypeId:               7,
				DocumentClass:               1,
				PriorityClass:               2,
				CreatedAtMs:                 100000,
				MetadataBlob:                []byte{8, 9},
				TransactionFingerprint:      bytes16(10),
				FirstFrameIndex:             0,
				LastFrameIndex:              0,
			},
		},
		Frames: []*storagev1.FrameChecksumRecord{
			{
				FrameIndex:      0,
				PlaintextOffset: 64,
				PlaintextLength: 42,
				StoredOffset:    64,
				StoredLength:    42,
				PlaintextSha256: bytes32(11),
				StoredSha256:    bytes32(12),
				EncryptionMode:  storagev1.EncryptionMode_ENCRYPTION_MODE_NONE,
			},
		},
	}
}

func blockIndexWithDigest(t *testing.T) *storagev1.BlockIndex {
	t.Helper()
	index := sampleBlockIndex()
	digest, err := BlockIndexSHA256(index)
	if err != nil {
		t.Fatalf("compute block index digest: %v", err)
	}
	index.IndexSha256 = digest
	return index
}

func sampleBackendObjectSet() *storagev1.BackendObjectSet {
	envelopeObject := objectRef(storagev1.BackendObjectKind_BACKEND_OBJECT_KIND_ENVELOPE, "blocks/block-1.env", 512, 23)
	return &storagev1.BackendObjectSet{
		SchemaVersion:  CurrentSchemaVersion,
		BlockId:        "block-1",
		BlockObject:    objectRef(storagev1.BackendObjectKind_BACKEND_OBJECT_KIND_BLOCK, "blocks/block-1.blk", 4096, 21),
		IndexObject:    objectRef(storagev1.BackendObjectKind_BACKEND_OBJECT_KIND_INDEX, "blocks/block-1.idx", 1024, 22),
		EnvelopeObject: envelopeObject,
		EnvelopeRef: &storagev1.EnvelopeReference{
			EnvelopeId:     "env-1",
			KeyId:          "transit/key",
			KeyVersion:     3,
			EnvelopeSha256: bytes32(24),
			EnvelopeObject: proto.Clone(envelopeObject).(*storagev1.BackendObjectRef),
		},
		CreatedAt: timestamppb.New(time.Unix(100, 0).UTC()),
	}
}

func objectRef(kind storagev1.BackendObjectKind, key string, length uint64, checksumByte byte) *storagev1.BackendObjectRef {
	return &storagev1.BackendObjectRef{
		Kind:      kind,
		BackendId: "primary",
		ObjectKey: key,
		Length:    length,
		Sha256:    bytes32(checksumByte),
	}
}

func sampleEnvelopeRecord(t *testing.T) *storagev1.EnvelopeRecord {
	t.Helper()
	record := &storagev1.EnvelopeRecord{
		SchemaVersion: CurrentSchemaVersion,
		EnvelopeId:    "env-1",
		BlockId:       "block-1",
		KeyId:         "transit/key",
		KeyVersion:    3,
		WrappedDek:    []byte{1, 2, 3},
		DekAlgorithm:  "AES-256",
		AeadAlgorithm: "AES-256-GCM",
		AadContext:    []byte("cell-a:block-1"),
		CreatedAt:     timestamppb.New(time.Unix(100, 0).UTC()),
	}
	record.EnvelopeSha256 = envelopeDigest(t, record)
	return record
}

func envelopeDigest(t *testing.T, record *storagev1.EnvelopeRecord) []byte {
	t.Helper()
	digest, err := EnvelopeRecordSHA256(record)
	if err != nil {
		t.Fatalf("compute envelope digest: %v", err)
	}
	return digest
}

func bytes32(value byte) []byte {
	return bytes.Repeat([]byte{value}, 32)
}

func bytes16(value byte) []byte {
	return bytes.Repeat([]byte{value}, 16)
}

func requireField(t *testing.T, message protoreflect.MessageDescriptor, fieldName protoreflect.Name) {
	t.Helper()
	if message == nil {
		t.Fatalf("%s field owner descriptor missing", fieldName)
	}
	if message.Fields().ByName(fieldName) == nil {
		t.Fatalf("%s.%s descriptor missing", message.Name(), fieldName)
	}
}

func appendUnknownVarint(data []byte, fieldNumber protowire.Number, value uint64) []byte {
	data = protowire.AppendTag(data, fieldNumber, protowire.VarintType)
	return protowire.AppendVarint(data, value)
}

func stringPtr(value string) *string {
	return &value
}
