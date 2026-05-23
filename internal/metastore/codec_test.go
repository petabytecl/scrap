package metastore

import (
	"bytes"
	"encoding/hex"
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

func TestAuthoritativeDocumentRecordRoundTripPreservesContractFields(t *testing.T) {
	record := authoritativeDocumentRecordWithRefs()

	data, err := marshalDocumentRecord(record)
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	decoded, err := unmarshalDocumentRecord(data)
	if err != nil {
		t.Fatalf("unmarshal record: %v", err)
	}
	if !proto.Equal(record, decoded) {
		t.Fatalf("decoded record differs\n got: %v\nwant: %v", decoded, record)
	}
}

func TestAuthoritativeDocumentRecordMarshalIsDeterministic(t *testing.T) {
	first := authoritativeDocumentRecordWithRefs()
	first.Tags = map[string]string{"workflow": "billing", "stage": "seal"}
	second := authoritativeDocumentRecordWithRefs()
	second.Tags = map[string]string{"stage": "seal", "workflow": "billing"}

	firstData, err := marshalDocumentRecord(first)
	if err != nil {
		t.Fatalf("marshal first record: %v", err)
	}
	secondData, err := marshalDocumentRecord(second)
	if err != nil {
		t.Fatalf("marshal second record: %v", err)
	}
	if !bytes.Equal(firstData, secondData) {
		t.Fatal("deterministic marshal produced different bytes for equivalent records")
	}
}

func TestAuthoritativeDocumentRecordRejectsMissingRequiredFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*metastorev1.DocumentRecord)
	}{
		{
			name: "tenant",
			mutate: func(record *metastorev1.DocumentRecord) {
				record.TenantId = ""
			},
		},
		{
			name: "logical checksum",
			mutate: func(record *metastorev1.DocumentRecord) {
				record.LogicalSha256 = []byte{1, 2, 3}
			},
		},
		{
			name: "restore state",
			mutate: func(record *metastorev1.DocumentRecord) {
				record.RestoreState = metastorev1.RestoreState_RESTORE_STATE_UNSPECIFIED
			},
		},
		{
			name: "location",
			mutate: func(record *metastorev1.DocumentRecord) {
				record.Location = nil
			},
		},
		{
			name: "zero created timestamp",
			mutate: func(record *metastorev1.DocumentRecord) {
				record.CreatedAt = timestamppb.New(time.Time{})
			},
		},
		{
			name: "frame checksum",
			mutate: func(record *metastorev1.DocumentRecord) {
				record.Location.Frames[0].Sha256 = nil
			},
		},
		{
			name: "envelope checksum",
			mutate: func(record *metastorev1.DocumentRecord) {
				record.EnvelopeRef.EnvelopeSha256 = nil
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := authoritativeDocumentRecordWithRefs()
			test.mutate(record)

			if _, err := marshalDocumentRecord(record); !errors.Is(err, ErrInvalidRecord) {
				t.Fatalf("marshal error = %v, want %v", err, ErrInvalidRecord)
			}

			data, err := proto.Marshal(record)
			if err != nil {
				t.Fatalf("raw proto marshal: %v", err)
			}
			if _, err := unmarshalDocumentRecord(data); !errors.Is(err, ErrInvalidRecord) {
				t.Fatalf("unmarshal error = %v, want %v", err, ErrInvalidRecord)
			}
		})
	}
}

func TestAuthoritativeDocumentRecordReadsInitialV1Fixture(t *testing.T) {
	data, err := hex.DecodeString("0a0674656e616e74120374786e1a0b696e766f6963652e786d6c20012802320f6170706c69636174696f6e2f786d6c382a422001020300000000000000000000000000000000000000000000000000000000004a2004050600000000000000000000000000000000000000000000000000000000005210000000000000000000000000000000005a0b62696c6c696e672d65746c62047365616c6a02080a7202080a78018001018a01130a08776f726b666c6f77120762696c6c696e679201540a2430313866366438362d376132322d376162632d386465662d3132333435363738396162631040182a222808401040182a22200708090000000000000000000000000000000000000000000000000000000000a00101c00101c80102")
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	document, err := UnmarshalDocument(data)
	if err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	if document.Identity.TenantID != "tenant" ||
		document.Identity.TransactionID != "txn" ||
		document.Identity.DocumentName != "invoice.xml" ||
		document.Location.BlockID != "018f6d86-7a22-7abc-8def-123456789abc" {
		t.Fatalf("fixture document = %#v, want initial v1 invoice metadata", document)
	}
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

func TestCommandReceiptRecordSchemaShape(t *testing.T) {
	receipt := metastorev1.File_scrap_metastore_v1_metastore_proto.Messages().ByName("CommandReceiptRecord")
	requireField(t, receipt, "schema_version")
	requireField(t, receipt, "command_id")
	requireField(t, receipt, "command_sha256")
}

func TestShardCommandRoundTripsAllCommandTypes(t *testing.T) {
	for _, test := range sampleShardCommands() {
		t.Run(test.name, func(t *testing.T) {
			data, err := MarshalShardCommand(test.command)
			if err != nil {
				t.Fatalf("marshal command: %v", err)
			}
			decoded, err := UnmarshalShardCommand(data)
			if err != nil {
				t.Fatalf("unmarshal command: %v", err)
			}
			if !proto.Equal(test.command, decoded) {
				t.Fatalf("decoded command differs\n got: %v\nwant: %v", decoded, test.command)
			}
		})
	}
}

func TestShardCommandMarshalIsDeterministic(t *testing.T) {
	first := sampleCompleteTransactionCommand()
	first.GetCompleteTransaction().Tags = map[string]string{"workflow": "billing", "closed_by": "test"}
	second := sampleCompleteTransactionCommand()
	second.GetCompleteTransaction().Tags = map[string]string{"closed_by": "test", "workflow": "billing"}

	firstData, err := MarshalShardCommand(first)
	if err != nil {
		t.Fatalf("marshal first command: %v", err)
	}
	secondData, err := MarshalShardCommand(second)
	if err != nil {
		t.Fatalf("marshal second command: %v", err)
	}
	if !bytes.Equal(firstData, secondData) {
		t.Fatal("deterministic command marshal produced different bytes for equivalent maps")
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

func TestShardCommandRejectsMixedVersionDocumentBody(t *testing.T) {
	command := sampleShardCommand()
	command.GetCommitDocument().Document.SchemaVersion = CurrentSchemaVersion + 1
	if _, err := MarshalShardCommand(command); !errors.Is(err, ErrUnsupportedSchemaVersion) {
		t.Fatalf("marshal error = %v, want %v", err, ErrUnsupportedSchemaVersion)
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

func TestShardCommandRejectsZeroProposedAt(t *testing.T) {
	command := sampleShardCommand()
	command.ProposedAt = timestamppb.New(time.Time{})

	if _, err := MarshalShardCommand(command); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("marshal error = %v, want %v", err, ErrInvalidRecord)
	}

	data, err := proto.Marshal(command)
	if err != nil {
		t.Fatalf("raw proto marshal: %v", err)
	}
	if _, err := UnmarshalShardCommand(data); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("unmarshal error = %v, want %v", err, ErrInvalidRecord)
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

func sampleShardCommands() []struct {
	name    string
	command *metastorev1.ShardCommand
} {
	documentName := "invoice.xml"
	lastError := "backend throttled"
	return []struct {
		name    string
		command *metastorev1.ShardCommand
	}{
		{name: "commit document", command: sampleShardCommand()},
		{name: "complete transaction", command: sampleCompleteTransactionCommand()},
		{
			name: "record upload intent",
			command: withShardCommandBody(baseShardCommand("upload-1"), &metastorev1.ShardCommand_RecordUploadIntent{
				RecordUploadIntent: &metastorev1.RecordUploadIntentCommand{
					BlockId:           "block-1",
					BackendObjectKey:  "objects/block-1.blk",
					IndexObjectKey:    "objects/block-1.idx",
					EnvelopeObjectKey: "objects/block-1.env",
				},
			}),
		},
		{
			name: "update upload intent state",
			command: withShardCommandBody(baseShardCommand("upload-state-1"), &metastorev1.ShardCommand_UpdateUploadIntentState{
				UpdateUploadIntentState: &metastorev1.UpdateUploadIntentStateCommand{
					BlockId:   "block-1",
					State:     metastorev1.UploadState_UPLOAD_STATE_FAILED,
					LastError: &lastError,
				},
			}),
		},
		{
			name: "update restore state",
			command: withShardCommandBody(baseShardCommand("restore-1"), &metastorev1.ShardCommand_UpdateRestoreState{
				UpdateRestoreState: &metastorev1.UpdateRestoreStateCommand{
					TenantId:      "tenant",
					TransactionId: "txn",
					DocumentName:  &documentName,
					RestoreState:  metastorev1.RestoreState_RESTORE_STATE_RESTORE_PENDING,
					Reason:        "restore requested",
				},
			}),
		},
		{
			name: "record repair state",
			command: withShardCommandBody(baseShardCommand("repair-1"), &metastorev1.ShardCommand_RecordRepairState{
				RecordRepairState: &metastorev1.RecordRepairStateCommand{
					TenantId:      "tenant",
					TransactionId: "txn",
					DocumentName:  "invoice.xml",
					PhysicalRef:   "member-a/block-1/64",
					IncidentId:    "incident-1",
					Quarantined:   true,
				},
			}),
		},
		{
			name: "tombstone document",
			command: withShardCommandBody(baseShardCommand("tombstone-1"), &metastorev1.ShardCommand_TombstoneDocument{
				TombstoneDocument: &metastorev1.TombstoneDocumentCommand{
					TenantId:      "tenant",
					TransactionId: "txn",
					DocumentName:  "invoice.xml",
					TombstonedAt:  timestamppb.New(time.Unix(200, 0).UTC()),
					OperationId:   "operation-1",
				},
			}),
		},
	}
}

func sampleCompleteTransactionCommand() *metastorev1.ShardCommand {
	return withShardCommandBody(baseShardCommand("complete-1"), &metastorev1.ShardCommand_CompleteTransaction{
		CompleteTransaction: &metastorev1.CompleteTransactionCommand{
			TenantId:      "tenant",
			TransactionId: "txn",
			CompletedAt:   timestamppb.New(time.Unix(200, 0).UTC()),
			Tags:          map[string]string{"closed_by": "test"},
		},
	})
}

func baseShardCommand(commandID string) *metastorev1.ShardCommand {
	return &metastorev1.ShardCommand{
		SchemaVersion: CurrentSchemaVersion,
		ShardId:       "tenant-txn",
		CommandId:     commandID,
		ProposedAt:    timestamppb.New(time.Unix(100, 0).UTC()),
	}
}

func withShardCommandBody(command *metastorev1.ShardCommand, body any) *metastorev1.ShardCommand {
	switch typed := body.(type) {
	case *metastorev1.ShardCommand_CompleteTransaction:
		command.Command = typed
	case *metastorev1.ShardCommand_RecordUploadIntent:
		command.Command = typed
	case *metastorev1.ShardCommand_UpdateUploadIntentState:
		command.Command = typed
	case *metastorev1.ShardCommand_UpdateRestoreState:
		command.Command = typed
	case *metastorev1.ShardCommand_RecordRepairState:
		command.Command = typed
	case *metastorev1.ShardCommand_TombstoneDocument:
		command.Command = typed
	default:
		panic("unsupported shard command body")
	}
	return command
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

func authoritativeDocumentRecordWithRefs() *metastorev1.DocumentRecord {
	record := documentToProto(sampleDocument("invoice.xml", DocumentClassPermanent))
	record.EnvelopeRef = &metastorev1.EnvelopeRef{
		EnvelopeId:     "env-1",
		KeyId:          "openbao/transit/scrap",
		KeyVersion:     7,
		EnvelopeSha256: bytes.Repeat([]byte{9}, 32),
	}
	record.Location.Replicas = []*metastorev1.ReplicaRef{
		{
			MemberId:     "member-a",
			BlockId:      record.GetLocation().GetBlockId(),
			StoredOffset: record.GetLocation().GetStoredOffset(),
			StoredLength: record.GetLocation().GetStoredLength(),
			StoredSha256: bytes.Repeat([]byte{4}, 32),
		},
	}
	record.Location.BackendObjectKey = proto.String("objects/block-1.blk")
	record.Location.IndexObjectKey = proto.String("objects/block-1.idx")
	record.Location.EnvelopeObjectKey = proto.String("objects/block-1.env")
	record.Location.FormatVersion = 1
	return record
}
