package compat

import (
	"bytes"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/petabytecl/scrap/internal/blockstore"
	metastorev1 "github.com/petabytecl/scrap/internal/gen/scrap/metastore/v1"
	publishedv1 "github.com/petabytecl/scrap/internal/gen/scrap/published/v1"
	storagev1 "github.com/petabytecl/scrap/internal/gen/scrap/storage/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/published"
	"github.com/petabytecl/scrap/internal/storageformat"
)

type fixture struct {
	name string
	path string
	data func(*testing.T) []byte
	read func([]byte) error
}

func TestInitialV1FixturesAreReadable(t *testing.T) {
	for _, fixture := range initialFixtures() {
		t.Run(fixture.name, func(t *testing.T) {
			expected := fixture.data(t)
			if os.Getenv("UPDATE_COMPAT_FIXTURES") == "1" {
				writeFixture(t, fixture.path, expected)
			}
			data := readFixture(t, fixture.path)
			if !bytes.Equal(data, expected) {
				t.Fatalf("%s fixture drifted; run UPDATE_COMPAT_FIXTURES=1 go test ./internal/compat to refresh intentionally", fixture.name)
			}
			if err := fixture.read(data); err != nil {
				t.Fatalf("read %s fixture: %v", fixture.name, err)
			}
		})
	}
}

func TestUnknownRequiredVersionsFailClosed(t *testing.T) {
	tests := []struct {
		name string
		data func(*testing.T) []byte
		read func([]byte) error
		want error
	}{
		{
			name: "authoritative document",
			data: func(t *testing.T) []byte {
				record := metastore.DocumentRecord(sampleMetastoreDocument())
				record.SchemaVersion = metastore.CurrentSchemaVersion + 1
				return mustMarshal(t, record)
			},
			read: func(data []byte) error {
				_, err := metastore.UnmarshalDocument(data)
				return err
			},
			want: metastore.ErrUnsupportedSchemaVersion,
		},
		{
			name: "published manifest",
			data: func(t *testing.T) []byte {
				record := sampleManifest()
				record.SchemaVersion = published.CurrentSchemaVersion + 1
				return mustMarshal(t, record)
			},
			read: func(data []byte) error {
				_, err := published.UnmarshalManifest(data)
				return err
			},
			want: published.ErrUnsupportedSchemaVersion,
		},
		{
			name: "published snapshot",
			data: func(t *testing.T) []byte {
				record := sampleSnapshot()
				record.SchemaVersion = published.CurrentSchemaVersion + 1
				return mustMarshal(t, record)
			},
			read: func(data []byte) error {
				_, err := published.UnmarshalSnapshotRecord(data)
				return err
			},
			want: published.ErrUnsupportedSchemaVersion,
		},
		{
			name: "published tail",
			data: func(t *testing.T) []byte {
				record := sampleTail()
				record.SchemaVersion = published.CurrentSchemaVersion + 1
				return mustMarshal(t, record)
			},
			read: func(data []byte) error {
				_, err := published.UnmarshalTailRecord(data)
				return err
			},
			want: published.ErrUnsupportedSchemaVersion,
		},
		{
			name: "block index",
			data: func(t *testing.T) []byte {
				record := sampleBlockIndex(t)
				record.SchemaVersion = storageformat.CurrentSchemaVersion + 1
				return mustMarshal(t, record)
			},
			read: func(data []byte) error {
				_, err := storageformat.UnmarshalBlockIndex(data)
				return err
			},
			want: storageformat.ErrUnsupportedSchemaVersion,
		},
		{
			name: "backend object set",
			data: func(t *testing.T) []byte {
				record := sampleBackendObjectSet()
				record.SchemaVersion = storageformat.CurrentSchemaVersion + 1
				return mustMarshal(t, record)
			},
			read: func(data []byte) error {
				_, err := storageformat.UnmarshalBackendObjectSet(data)
				return err
			},
			want: storageformat.ErrUnsupportedSchemaVersion,
		},
		{
			name: "envelope",
			data: func(t *testing.T) []byte {
				record := sampleEnvelopeRecord(t)
				record.SchemaVersion = storageformat.CurrentSchemaVersion + 1
				return mustMarshal(t, record)
			},
			read: func(data []byte) error {
				_, err := storageformat.UnmarshalEnvelopeRecord(data)
				return err
			},
			want: storageformat.ErrUnsupportedSchemaVersion,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.read(test.data(t)); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestForwardCompatibleUnknownFieldsArePreserved(t *testing.T) {
	tests := []struct {
		name      string
		data      func(*testing.T) []byte
		roundTrip func([]byte) (proto.Message, error)
	}{
		{
			name: "published manifest",
			data: func(t *testing.T) []byte {
				return mustMarshal(t, sampleManifest())
			},
			roundTrip: func(data []byte) (proto.Message, error) {
				record, err := published.UnmarshalManifest(data)
				if err != nil {
					return nil, err
				}
				roundTrip, err := published.MarshalManifest(record)
				if err != nil {
					return nil, err
				}
				var out publishedv1.Manifest
				if err := proto.Unmarshal(roundTrip, &out); err != nil {
					return nil, err
				}
				return &out, nil
			},
		},
		{
			name: "published snapshot",
			data: func(t *testing.T) []byte {
				return mustMarshal(t, sampleSnapshot())
			},
			roundTrip: func(data []byte) (proto.Message, error) {
				record, err := published.UnmarshalSnapshotRecord(data)
				if err != nil {
					return nil, err
				}
				roundTrip, err := published.MarshalSnapshotRecord(record)
				if err != nil {
					return nil, err
				}
				var out publishedv1.SnapshotRecord
				if err := proto.Unmarshal(roundTrip, &out); err != nil {
					return nil, err
				}
				return &out, nil
			},
		},
		{
			name: "published tail",
			data: func(t *testing.T) []byte {
				return mustMarshal(t, sampleTail())
			},
			roundTrip: func(data []byte) (proto.Message, error) {
				record, err := published.UnmarshalTailRecord(data)
				if err != nil {
					return nil, err
				}
				roundTrip, err := published.MarshalTailRecord(record)
				if err != nil {
					return nil, err
				}
				var out publishedv1.TailRecord
				if err := proto.Unmarshal(roundTrip, &out); err != nil {
					return nil, err
				}
				return &out, nil
			},
		},
		{
			name: "block index",
			data: func(t *testing.T) []byte {
				return mustMarshal(t, sampleBlockIndex(t))
			},
			roundTrip: func(data []byte) (proto.Message, error) {
				record, err := storageformat.UnmarshalBlockIndex(data)
				if err != nil {
					return nil, err
				}
				roundTrip, err := storageformat.MarshalBlockIndex(record)
				if err != nil {
					return nil, err
				}
				var out storagev1.BlockIndex
				if err := proto.Unmarshal(roundTrip, &out); err != nil {
					return nil, err
				}
				return &out, nil
			},
		},
		{
			name: "backend object set",
			data: func(t *testing.T) []byte {
				return mustMarshal(t, sampleBackendObjectSet())
			},
			roundTrip: func(data []byte) (proto.Message, error) {
				record, err := storageformat.UnmarshalBackendObjectSet(data)
				if err != nil {
					return nil, err
				}
				roundTrip, err := storageformat.MarshalBackendObjectSet(record)
				if err != nil {
					return nil, err
				}
				var out storagev1.BackendObjectSet
				if err := proto.Unmarshal(roundTrip, &out); err != nil {
					return nil, err
				}
				return &out, nil
			},
		},
		{
			name: "envelope",
			data: func(t *testing.T) []byte {
				return mustMarshal(t, sampleEnvelopeRecord(t))
			},
			roundTrip: func(data []byte) (proto.Message, error) {
				record, err := storageformat.UnmarshalEnvelopeRecord(data)
				if err != nil {
					return nil, err
				}
				roundTrip, err := storageformat.MarshalEnvelopeRecord(record)
				if err != nil {
					return nil, err
				}
				var out storagev1.EnvelopeRecord
				if err := proto.Unmarshal(roundTrip, &out); err != nil {
					return nil, err
				}
				return &out, nil
			},
		},
		{
			name: "authoritative document protobuf",
			data: func(t *testing.T) []byte {
				return mustMarshal(t, metastore.DocumentRecord(sampleMetastoreDocument()))
			},
			roundTrip: func(data []byte) (proto.Message, error) {
				var record metastorev1.DocumentRecord
				if err := proto.Unmarshal(data, &record); err != nil {
					return nil, err
				}
				roundTrip, err := proto.Marshal(&record)
				if err != nil {
					return nil, err
				}
				var out metastorev1.DocumentRecord
				if err := proto.Unmarshal(roundTrip, &out); err != nil {
					return nil, err
				}
				return &out, nil
			},
		},
	}

	unknown := appendUnknownVarint(nil, 1000, 44)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := append(test.data(t), unknown...)
			roundTrip, err := test.roundTrip(data)
			if err != nil {
				t.Fatalf("round trip: %v", err)
			}
			assertUnknownPreserved(t, roundTrip, unknown)
		})
	}
}

func TestSchemaEvolutionExampleIsExecutable(t *testing.T) {
	location := samplePublishedDocument().GetLocations()[0]
	locationUnknown := appendUnknownVarint(nil, 1001, 7)
	locationData := append(mustMarshal(t, location), locationUnknown...)
	var decodedLocation publishedv1.PublishedLocation
	if err := proto.Unmarshal(locationData, &decodedLocation); err != nil {
		t.Fatalf("unmarshal published location with additive field: %v", err)
	}
	locationRoundTrip := mustMarshal(t, &decodedLocation)
	var reparsedLocation publishedv1.PublishedLocation
	if err := proto.Unmarshal(locationRoundTrip, &reparsedLocation); err != nil {
		t.Fatalf("reparse published location with additive field: %v", err)
	}
	assertUnknownPreserved(t, &reparsedLocation, locationUnknown)

	additiveBlockIndex := append(mustMarshal(t, sampleBlockIndex(t)), appendUnknownVarint(nil, 1002, 9)...)
	if _, err := storageformat.UnmarshalBlockIndex(additiveBlockIndex); err != nil {
		t.Fatalf("additive block index field should remain reader-compatible: %v", err)
	}

	requiredVersion := sampleBlockIndex(t)
	requiredVersion.SchemaVersion = storageformat.CurrentSchemaVersion + 1
	if _, err := storageformat.UnmarshalBlockIndex(mustMarshal(t, requiredVersion)); !errors.Is(err, storageformat.ErrUnsupportedSchemaVersion) {
		t.Fatalf("required version change error = %v, want %v", err, storageformat.ErrUnsupportedSchemaVersion)
	}
}

func initialFixtures() []fixture {
	return []fixture{
		{
			name: "authoritative document",
			path: "testdata/v1/authoritative_document.hex",
			data: func(t *testing.T) []byte {
				return mustMarshal(t, metastore.DocumentRecord(sampleMetastoreDocument()))
			},
			read: func(data []byte) error {
				_, err := metastore.UnmarshalDocument(data)
				return err
			},
		},
		{
			name: "published manifest",
			path: "testdata/v1/published_manifest.hex",
			data: func(t *testing.T) []byte {
				return mustMarshal(t, sampleManifest())
			},
			read: func(data []byte) error {
				_, err := published.UnmarshalManifest(data)
				return err
			},
		},
		{
			name: "published snapshot",
			path: "testdata/v1/published_snapshot.hex",
			data: func(t *testing.T) []byte {
				return mustMarshal(t, sampleSnapshot())
			},
			read: func(data []byte) error {
				_, err := published.UnmarshalSnapshotRecord(data)
				return err
			},
		},
		{
			name: "published tail",
			path: "testdata/v1/published_tail.hex",
			data: func(t *testing.T) []byte {
				return mustMarshal(t, sampleTail())
			},
			read: func(data []byte) error {
				_, err := published.UnmarshalTailRecord(data)
				return err
			},
		},
		{
			name: "block index with frame checksum",
			path: "testdata/v1/block_index.hex",
			data: func(t *testing.T) []byte {
				return mustMarshal(t, sampleBlockIndex(t))
			},
			read: func(data []byte) error {
				_, err := storageformat.UnmarshalBlockIndex(data)
				return err
			},
		},
		{
			name: "frame checksum",
			path: "testdata/v1/frame_checksum.hex",
			data: func(t *testing.T) []byte {
				return mustMarshal(t, sampleFrameChecksum())
			},
			read: func(data []byte) error {
				var record storagev1.FrameChecksumRecord
				return proto.Unmarshal(data, &record)
			},
		},
		{
			name: "backend object set",
			path: "testdata/v1/backend_object_set.hex",
			data: func(t *testing.T) []byte {
				return mustMarshal(t, sampleBackendObjectSet())
			},
			read: func(data []byte) error {
				_, err := storageformat.UnmarshalBackendObjectSet(data)
				return err
			},
		},
		{
			name: "envelope",
			path: "testdata/v1/envelope_record.hex",
			data: func(t *testing.T) []byte {
				return mustMarshal(t, sampleEnvelopeRecord(t))
			},
			read: func(data []byte) error {
				_, err := storageformat.UnmarshalEnvelopeRecord(data)
				return err
			},
		},
	}
}

func sampleMetastoreDocument() metastore.Document {
	now := time.Unix(100, 0).UTC()
	return metastore.Document{
		Identity: identity.Document{
			TenantID:      "tenant",
			TransactionID: "txn",
			DocumentName:  "invoice.xml",
		},
		DocumentClass:               metastore.DocumentClassPermanent,
		PriorityClass:               metastore.PriorityClassNormal,
		ContentType:                 "application/xml",
		HasContentType:              true,
		Length:                      42,
		LogicalSHA256:               array32(1),
		StoredSHA256:                array32(2),
		DocumentIdentityFingerprint: array16(3),
		CreatedByService:            "billing-etl",
		WorkflowStage:               "seal",
		HasWorkflowStage:            true,
		CreatedAt:                   now,
		FinalizedAt:                 now,
		Availability:                metastore.AvailabilityHot,
		LifecycleState:              metastore.LifecycleStateActive,
		RestoreState:                metastore.RestoreStateHot,
		UploadState:                 metastore.UploadStatePending,
		Tags:                        map[string]string{"stage": "seal", "workflow": "billing"},
		Location: blockstore.Record{
			BlockID:       "018f6d86-7a22-7abc-8def-123456789abc",
			StoredOffset:  64,
			StoredLength:  42,
			LogicalSHA256: array32(1),
			Frames: []blockstore.FrameRecord{
				{
					FrameOffset:   0,
					SegmentOffset: 64,
					SegmentLength: 42,
					SHA256:        array32(4),
				},
			},
		},
	}
}

func sampleManifest() *publishedv1.Manifest {
	now := timestamppb.New(time.Unix(100, 0).UTC())
	return &publishedv1.Manifest{
		SchemaVersion:   published.CurrentSchemaVersion,
		CellId:          "cell-a",
		SourceNamespace: "billing-prod",
		ManifestId:      "manifest-0001",
		Generation:      1,
		PublishedAt:     now,
		ShardWatermarks: []*publishedv1.ShardWatermark{{ShardId: "tenant-txn", HighWatermark: 10}},
		Snapshots: []*publishedv1.ArtifactRef{
			{
				Kind:       publishedv1.ArtifactKind_ARTIFACT_KIND_SNAPSHOT,
				ShardId:    "tenant-txn",
				ObjectKey:  "cells/cell-a/metadata/v1/snapshots/shard=tenant-txn/0001.snap",
				Length:     128,
				Sha256:     bytes32(5),
				FirstIndex: 1,
				LastIndex:  10,
			},
		},
		Tails: []*publishedv1.ArtifactRef{
			{
				Kind:       publishedv1.ArtifactKind_ARTIFACT_KIND_TAIL,
				ShardId:    "tenant-txn",
				ObjectKey:  "cells/cell-a/metadata/v1/tails/shard=tenant-txn/00000000000000000011-00000000000000000012.tail",
				Length:     64,
				Sha256:     bytes32(6),
				FirstIndex: 11,
				LastIndex:  12,
			},
		},
		RequiredObjects: []*publishedv1.ObjectRef{
			{Kind: publishedv1.ObjectKind_OBJECT_KIND_BLOCK, ObjectKey: "blocks/block-1.blk", Length: 4096, Sha256: bytes32(7), EnvelopeId: "env-1"},
			{Kind: publishedv1.ObjectKind_OBJECT_KIND_INDEX, ObjectKey: "blocks/block-1.idx", Length: 1024, Sha256: bytes32(8)},
			{Kind: publishedv1.ObjectKind_OBJECT_KIND_ENVELOPE, ObjectKey: "blocks/block-1.env", Length: 512, Sha256: bytes32(9), EnvelopeId: "env-1"},
		},
		ProducerBuild:  "test-build",
		ProducerSchema: "published.v1",
	}
}

func sampleSnapshot() *publishedv1.SnapshotRecord {
	return &publishedv1.SnapshotRecord{
		SchemaVersion:   published.CurrentSchemaVersion,
		SourceNamespace: "billing-prod",
		ShardId:         "tenant-txn",
		HighWatermark:   10,
		Record: &publishedv1.SnapshotRecord_Document{
			Document: samplePublishedDocument(),
		},
	}
}

func sampleTail() *publishedv1.TailRecord {
	return &publishedv1.TailRecord{
		SchemaVersion:   published.CurrentSchemaVersion,
		SourceNamespace: "billing-prod",
		ShardId:         "tenant-txn",
		LogIndex:        11,
		Mutation: &publishedv1.TailRecord_FinalizedDocument{
			FinalizedDocument: samplePublishedDocument(),
		},
	}
}

func samplePublishedDocument() *publishedv1.PublishedDocument {
	now := timestamppb.New(time.Unix(100, 0).UTC())
	return &publishedv1.PublishedDocument{
		TenantId:                    "tenant",
		TransactionId:               "txn",
		DocumentName:                "invoice.xml",
		DocumentClass:               1,
		PriorityClass:               2,
		ContentType:                 stringPtr("application/xml"),
		Length:                      42,
		LogicalSha256:               bytes32(10),
		DocumentIdentityFingerprint: bytes16(11),
		CreatedByService:            "billing-etl",
		WorkflowStage:               stringPtr("seal"),
		CreatedAt:                   now,
		FinalizedAt:                 now,
		Availability:                1,
		LifecycleState:              1,
		Tags:                        map[string]string{"stage": "seal"},
		Locations: []*publishedv1.PublishedLocation{
			{
				BlockId:           "block-1",
				StoredOffset:      64,
				StoredLength:      42,
				BackendObjectKey:  "blocks/block-1.blk",
				IndexObjectKey:    "blocks/block-1.idx",
				EnvelopeObjectKey: "blocks/block-1.env",
				FormatVersion:     published.CurrentLocationFormatVersion,
				Frames: []*publishedv1.PublishedFrame{
					{
						FrameOffset:   0,
						SegmentOffset: 64,
						SegmentLength: 42,
						Sha256:        bytes32(12),
					},
				},
			},
		},
	}
}

func sampleBlockIndex(t *testing.T) *storagev1.BlockIndex {
	t.Helper()
	index := &storagev1.BlockIndex{
		SchemaVersion:     storageformat.CurrentSchemaVersion,
		BlockId:           "block-1",
		ShardId:           "tenant-txn",
		FormatVersion:     1,
		BlockLength:       4096,
		BlockSha256:       bytes32(13),
		EnvelopeObjectKey: stringPtr("blocks/block-1.env"),
		CreatedAt:         timestamppb.New(time.Unix(100, 0).UTC()),
		Documents: []*storagev1.IndexDocumentRecord{
			{
				DocumentKeyId:               1,
				TransactionKeyId:            2,
				DocumentNameFingerprint:     bytes16(14),
				DocumentIdentityFingerprint: bytes16(15),
				StoredOffset:                64,
				StoredLength:                42,
				LogicalLength:               42,
				LogicalSha256:               bytes32(16),
				StoredSha256:                bytes32(17),
				DocumentClass:               1,
				PriorityClass:               2,
				CreatedAtMs:                 100000,
				MetadataBlob:                []byte{1, 2, 3},
				TransactionFingerprint:      bytes16(18),
				FirstFrameIndex:             0,
				LastFrameIndex:              0,
			},
		},
		Frames: []*storagev1.FrameChecksumRecord{sampleFrameChecksum()},
	}
	digest, err := storageformat.BlockIndexSHA256(index)
	if err != nil {
		t.Fatalf("compute block index digest: %v", err)
	}
	index.IndexSha256 = digest
	return index
}

func sampleFrameChecksum() *storagev1.FrameChecksumRecord {
	return &storagev1.FrameChecksumRecord{
		FrameIndex:      0,
		PlaintextOffset: 64,
		PlaintextLength: 42,
		StoredOffset:    64,
		StoredLength:    42,
		PlaintextSha256: bytes32(19),
		StoredSha256:    bytes32(20),
		EncryptionMode:  storagev1.EncryptionMode_ENCRYPTION_MODE_NONE,
	}
}

func sampleBackendObjectSet() *storagev1.BackendObjectSet {
	envelopeObject := objectRef(storagev1.BackendObjectKind_BACKEND_OBJECT_KIND_ENVELOPE, "blocks/block-1.env", 512, 23)
	return &storagev1.BackendObjectSet{
		SchemaVersion:  storageformat.CurrentSchemaVersion,
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

func sampleEnvelopeRecord(t *testing.T) *storagev1.EnvelopeRecord {
	t.Helper()
	record := &storagev1.EnvelopeRecord{
		SchemaVersion: storageformat.CurrentSchemaVersion,
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
	digest, err := storageformat.EnvelopeRecordSHA256(record)
	if err != nil {
		t.Fatalf("compute envelope digest: %v", err)
	}
	record.EnvelopeSha256 = digest
	return record
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

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	out, err := hex.DecodeString(string(bytes.TrimSpace(data)))
	if err != nil {
		t.Fatalf("decode fixture %s: %v", name, err)
	}
	return out
}

func writeFixture(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatalf("create fixture dir: %v", err)
	}
	if err := os.WriteFile(filepath.Clean(name), []byte(hex.EncodeToString(data)+"\n"), 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", name, err)
	}
}

func mustMarshal(t *testing.T, message proto.Message) []byte {
	t.Helper()
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		t.Fatalf("marshal %T: %v", message, err)
	}
	return data
}

func assertUnknownPreserved(t *testing.T, message proto.Message, unknown []byte) {
	t.Helper()
	if got := message.ProtoReflect().GetUnknown(); !bytes.Equal(got, unknown) {
		t.Fatalf("round-trip unknown fields = %x, want %x", got, unknown)
	}
}

func appendUnknownVarint(data []byte, fieldNumber protowire.Number, value uint64) []byte {
	data = protowire.AppendTag(data, fieldNumber, protowire.VarintType)
	return protowire.AppendVarint(data, value)
}

func array32(value byte) [32]byte {
	var out [32]byte
	for i := range out {
		out[i] = value
	}
	return out
}

func array16(value byte) [16]byte {
	var out [16]byte
	for i := range out {
		out[i] = value
	}
	return out
}

func bytes32(value byte) []byte {
	out := array32(value)
	return append([]byte(nil), out[:]...)
}

func bytes16(value byte) []byte {
	out := array16(value)
	return append([]byte(nil), out[:]...)
}

func stringPtr(value string) *string {
	return &value
}
