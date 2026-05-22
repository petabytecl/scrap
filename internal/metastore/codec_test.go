package metastore

import (
	"bytes"
	"errors"
	"testing"

	metastorev1 "github.com/petabytecl/scrap/internal/gen/scrap/metastore/v1"
	publishedv1 "github.com/petabytecl/scrap/internal/gen/scrap/published/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
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
