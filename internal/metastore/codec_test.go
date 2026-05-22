package metastore

import (
	"bytes"
	"errors"
	"testing"
	"time"

	metastorev1 "github.com/petabytecl/scrap/internal/gen/scrap/metastore/v1"
	publishedv1 "github.com/petabytecl/scrap/internal/gen/scrap/published/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestAuthoritativeDocumentRecordSchemaShape(t *testing.T) {
	document := metastorev1.File_scrap_metastore_v1_metastore_proto.Messages().ByName("DocumentRecord")
	requireField(t, document, "schema_version")
	requireField(t, document, "tenant_id")
	requireField(t, document, "transaction_id")
	requireField(t, document, "document_name")
	requireField(t, document, "logical_sha256")
	requireField(t, document, "stored_sha256")
	requireField(t, document, "document_identity_fingerprint")
	requireField(t, document, "location")
	requireField(t, document, "envelope_ref")
	requireField(t, document, "restore_state")
	requireField(t, document, "upload_state")
	requireField(t, document, "tombstoned_at")
	requireField(t, document, "tombstone_operation_id")

	location := metastorev1.File_scrap_metastore_v1_metastore_proto.Messages().ByName("Location")
	requireField(t, location, "block_id")
	requireField(t, location, "stored_offset")
	requireField(t, location, "stored_length")
	requireField(t, location, "frames")
	requireField(t, location, "replicas")
	requireField(t, location, "backend_object_key")
	requireField(t, location, "index_object_key")
	requireField(t, location, "envelope_object_key")
}

func TestUploadIntentRecordSchemaShape(t *testing.T) {
	intent := metastorev1.File_scrap_metastore_v1_metastore_proto.Messages().ByName("UploadIntentRecord")
	requireField(t, intent, "schema_version")
	requireField(t, intent, "block_id")
	requireField(t, intent, "backend_object_key")
	requireField(t, intent, "index_object_key")
	requireField(t, intent, "envelope_object_key")
	requireField(t, intent, "state")
	requireField(t, intent, "updated_at")
}

func TestRepairStateRecordSchemaShape(t *testing.T) {
	repair := metastorev1.File_scrap_metastore_v1_metastore_proto.Messages().ByName("RepairStateRecord")
	requireField(t, repair, "schema_version")
	requireField(t, repair, "tenant_id")
	requireField(t, repair, "transaction_id")
	requireField(t, repair, "document_name")
	requireField(t, repair, "physical_ref")
	requireField(t, repair, "incident_id")
	requireField(t, repair, "quarantined")
	requireField(t, repair, "updated_at")
}

func TestPublishedMetadataBoundaryExcludesInternalState(t *testing.T) {
	document := publishedv1.File_scrap_published_v1_metadata_proto.Messages().ByName("PublishedDocument")
	requireField(t, document, "tenant_id")
	requireField(t, document, "transaction_id")
	requireField(t, document, "document_name")
	requireField(t, document, "logical_sha256")
	requireField(t, document, "document_identity_fingerprint")
	requireField(t, document, "locations")
	requireNoField(t, document, "committed_index")
	requireNoField(t, document, "client_idempotency_key")
	requireNoField(t, document, "upload_state")
	requireNoField(t, document, "repair_state")

	manifest := publishedv1.File_scrap_published_v1_metadata_proto.Messages().ByName("Manifest")
	requireField(t, manifest, "schema_version")
	requireField(t, manifest, "cell_id")
	requireField(t, manifest, "source_namespace")
	requireField(t, manifest, "generation")
	requireField(t, manifest, "shard_watermarks")
	requireField(t, manifest, "snapshots")
	requireField(t, manifest, "tails")
	requireField(t, manifest, "required_objects")
}

func TestAuthoritativeDocumentRecordRejectsUnsupportedSchemaVersion(t *testing.T) {
	record := documentToProto(sampleDocument("invoice.xml", DocumentClassPermanent))
	record.SchemaVersion = CurrentSchemaVersion + 1
	data, err := proto.Marshal(record)
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}

	_, err = unmarshalDocumentRecord(data)
	if !errors.Is(err, ErrUnsupportedSchemaVersion) {
		t.Fatalf("error = %v, want %v", err, ErrUnsupportedSchemaVersion)
	}
}

func TestAuthoritativeDocumentRecordPreservesUnknownForwardFields(t *testing.T) {
	record := documentToProto(sampleDocument("invoice.xml", DocumentClassPermanent))
	data, err := marshalDocumentRecord(record)
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	unknown := appendUnknownVarint(nil, 1000, 99)
	data = append(data, unknown...)

	decoded, err := unmarshalDocumentRecord(data)
	if err != nil {
		t.Fatalf("unmarshal record: %v", err)
	}
	if got := decoded.ProtoReflect().GetUnknown(); !bytes.Equal(got, unknown) {
		t.Fatalf("unknown fields = %x, want %x", got, unknown)
	}

	roundTrip, err := marshalDocumentRecord(decoded)
	if err != nil {
		t.Fatalf("marshal round trip: %v", err)
	}
	var reparsed metastorev1.DocumentRecord
	if err := proto.Unmarshal(roundTrip, &reparsed); err != nil {
		t.Fatalf("reparse record: %v", err)
	}
	if got := reparsed.ProtoReflect().GetUnknown(); !bytes.Equal(got, unknown) {
		t.Fatalf("round-trip unknown fields = %x, want %x", got, unknown)
	}
}

func TestShardCommandSchemaShape(t *testing.T) {
	command := metastorev1.File_scrap_metastore_v1_metastore_proto.Messages().ByName("ShardCommand")
	requireField(t, command, "schema_version")
	requireField(t, command, "shard_id")
	requireField(t, command, "command_id")
	requireField(t, command, "proposed_at")
	if command.Oneofs().ByName("command") == nil {
		t.Fatal("ShardCommand.command oneof missing")
	}

	for _, name := range []protoreflect.Name{
		"CommitDocumentCommand",
		"CompleteTransactionCommand",
		"RecordUploadIntentCommand",
		"UpdateUploadIntentStateCommand",
		"UpdateRestoreStateCommand",
		"RecordRepairStateCommand",
		"TombstoneDocumentCommand",
	} {
		if metastorev1.File_scrap_metastore_v1_metastore_proto.Messages().ByName(name) == nil {
			t.Fatalf("%s descriptor missing", name)
		}
	}
}

func TestShardCommandRejectsUnsupportedSchemaVersion(t *testing.T) {
	command := sampleShardCommand()
	command.SchemaVersion = CurrentSchemaVersion + 1
	data, err := proto.Marshal(command)
	if err != nil {
		t.Fatalf("marshal command: %v", err)
	}

	_, err = UnmarshalShardCommand(data)
	if !errors.Is(err, ErrUnsupportedSchemaVersion) {
		t.Fatalf("error = %v, want %v", err, ErrUnsupportedSchemaVersion)
	}
}

func TestShardCommandRequiresCommandBody(t *testing.T) {
	_, err := MarshalShardCommand(&metastorev1.ShardCommand{
		SchemaVersion: CurrentSchemaVersion,
		ShardId:       "tenant-txn",
		CommandId:     "cmd-1",
		ProposedAt:    timestamppb.New(time.Unix(100, 0).UTC()),
	})
	if err == nil {
		t.Fatal("expected missing command body error")
	}
}

func TestShardCommandPreservesUnknownForwardFields(t *testing.T) {
	data, err := MarshalShardCommand(sampleShardCommand())
	if err != nil {
		t.Fatalf("marshal command: %v", err)
	}
	unknown := appendUnknownVarint(nil, 1000, 123)
	data = append(data, unknown...)

	decoded, err := UnmarshalShardCommand(data)
	if err != nil {
		t.Fatalf("unmarshal command: %v", err)
	}
	if got := decoded.ProtoReflect().GetUnknown(); !bytes.Equal(got, unknown) {
		t.Fatalf("unknown fields = %x, want %x", got, unknown)
	}

	roundTrip, err := MarshalShardCommand(decoded)
	if err != nil {
		t.Fatalf("marshal round trip: %v", err)
	}
	var reparsed metastorev1.ShardCommand
	if err := proto.Unmarshal(roundTrip, &reparsed); err != nil {
		t.Fatalf("reparse command: %v", err)
	}
	if got := reparsed.ProtoReflect().GetUnknown(); !bytes.Equal(got, unknown) {
		t.Fatalf("round-trip unknown fields = %x, want %x", got, unknown)
	}
}

func sampleShardCommand() *metastorev1.ShardCommand {
	return &metastorev1.ShardCommand{
		SchemaVersion: CurrentSchemaVersion,
		ShardId:       "tenant-txn",
		CommandId:     "cmd-1",
		ProposedAt:    timestamppb.New(time.Unix(100, 0).UTC()),
		Command: &metastorev1.ShardCommand_CommitDocument{
			CommitDocument: &metastorev1.CommitDocumentCommand{
				Document: documentToProto(sampleDocument("invoice.xml", DocumentClassPermanent)),
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

func requireNoField(t *testing.T, message protoreflect.MessageDescriptor, fieldName protoreflect.Name) {
	t.Helper()
	if message == nil {
		t.Fatalf("%s field owner descriptor missing", fieldName)
	}
	if message.Fields().ByName(fieldName) != nil {
		t.Fatalf("%s.%s must stay out of published metadata", message.Name(), fieldName)
	}
}

func appendUnknownVarint(data []byte, fieldNumber protowire.Number, value uint64) []byte {
	data = protowire.AppendTag(data, fieldNumber, protowire.VarintType)
	return protowire.AppendVarint(data, value)
}
