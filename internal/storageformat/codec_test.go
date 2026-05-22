package storageformat

import (
	"bytes"
	"errors"
	"testing"
	"time"

	storagev1 "github.com/petabytecl/scrap/internal/gen/scrap/storage/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"
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
	data, err := MarshalBlockIndex(sampleBlockIndex())
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

	envelopeData, err := MarshalEnvelopeRecord(&storagev1.EnvelopeRecord{
		SchemaVersion:  CurrentSchemaVersion,
		EnvelopeId:     "env-1",
		BlockId:        "block-1",
		KeyId:          "transit/key",
		KeyVersion:     3,
		WrappedDek:     []byte{1, 2, 3},
		DekAlgorithm:   "AES-256",
		AeadAlgorithm:  "AES-256-GCM",
		AadContext:     []byte("cell-a:block-1"),
		EnvelopeSha256: []byte{4, 5, 6},
		CreatedAt:      timestamppb.New(time.Unix(100, 0).UTC()),
	})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	if _, err := UnmarshalEnvelopeRecord(envelopeData); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
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
		BlockSha256:       []byte{1, 2, 3},
		EnvelopeObjectKey: stringPtr("blocks/block-1.env"),
		IndexSha256:       []byte{4, 5, 6},
		CreatedAt:         now,
		Documents: []*storagev1.IndexDocumentRecord{
			{
				DocumentKeyId:               1,
				TransactionKeyId:            2,
				DocumentNameFingerprint:     []byte{3},
				DocumentIdentityFingerprint: []byte{4},
				StoredOffset:                64,
				StoredLength:                42,
				LogicalLength:               42,
				LogicalSha256:               []byte{5},
				StoredSha256:                []byte{6},
				ContentTypeId:               7,
				DocumentClass:               1,
				PriorityClass:               2,
				CreatedAtMs:                 100000,
				MetadataBlob:                []byte{8, 9},
				TransactionFingerprint:      []byte{10},
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
				PlaintextSha256: []byte{11},
				StoredSha256:    []byte{12},
				EncryptionMode:  storagev1.EncryptionMode_ENCRYPTION_MODE_NONE,
			},
		},
	}
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
