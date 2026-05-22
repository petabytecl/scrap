package published

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
	"time"

	publishedv1 "github.com/petabytecl/scrap/internal/gen/scrap/published/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestManifestRejectsUnsupportedSchemaVersion(t *testing.T) {
	manifest := sampleManifest()
	manifest.SchemaVersion = CurrentSchemaVersion + 1
	data, err := proto.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	_, err = UnmarshalManifest(data)
	if !errors.Is(err, ErrUnsupportedSchemaVersion) {
		t.Fatalf("error = %v, want %v", err, ErrUnsupportedSchemaVersion)
	}
}

func TestManifestPreservesUnknownForwardFields(t *testing.T) {
	data, err := MarshalManifest(sampleManifest())
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	unknown := appendUnknownVarint(nil, 1000, 44)
	data = append(data, unknown...)

	decoded, err := UnmarshalManifest(data)
	if err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if got := decoded.ProtoReflect().GetUnknown(); !bytes.Equal(got, unknown) {
		t.Fatalf("unknown fields = %x, want %x", got, unknown)
	}

	roundTrip, err := MarshalManifest(decoded)
	if err != nil {
		t.Fatalf("marshal round trip: %v", err)
	}
	var reparsed publishedv1.Manifest
	if err := proto.Unmarshal(roundTrip, &reparsed); err != nil {
		t.Fatalf("reparse manifest: %v", err)
	}
	if got := reparsed.ProtoReflect().GetUnknown(); !bytes.Equal(got, unknown) {
		t.Fatalf("round-trip unknown fields = %x, want %x", got, unknown)
	}
}

func TestSnapshotAndTailRecordsValidateSchemaVersion(t *testing.T) {
	snapshotData, err := MarshalSnapshotRecord(&publishedv1.SnapshotRecord{
		SchemaVersion:   CurrentSchemaVersion,
		SourceNamespace: "billing-prod",
		ShardId:         "tenant-txn",
		HighWatermark:   10,
		Record: &publishedv1.SnapshotRecord_Document{
			Document: sampleDocument(),
		},
	})
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if _, err := UnmarshalSnapshotRecord(snapshotData); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}

	tailData, err := MarshalTailRecord(&publishedv1.TailRecord{
		SchemaVersion:   CurrentSchemaVersion,
		SourceNamespace: "billing-prod",
		ShardId:         "tenant-txn",
		LogIndex:        11,
		Mutation: &publishedv1.TailRecord_FinalizedDocument{
			FinalizedDocument: sampleDocument(),
		},
	})
	if err != nil {
		t.Fatalf("marshal tail: %v", err)
	}
	if _, err := UnmarshalTailRecord(tailData); err != nil {
		t.Fatalf("unmarshal tail: %v", err)
	}
}

func TestSnapshotArtifactRecordsRoundTripAndEOF(t *testing.T) {
	first := &publishedv1.SnapshotRecord{
		SchemaVersion:   CurrentSchemaVersion,
		SourceNamespace: "billing-prod",
		ShardId:         "tenant-txn",
		HighWatermark:   10,
		Record: &publishedv1.SnapshotRecord_Document{
			Document: sampleDocument(),
		},
	}
	second := proto.Clone(first).(*publishedv1.SnapshotRecord)
	second.HighWatermark = 11

	var buf bytes.Buffer
	if err := WriteSnapshotRecord(&buf, first); err != nil {
		t.Fatalf("write first snapshot: %v", err)
	}
	if err := WriteSnapshotRecord(&buf, second); err != nil {
		t.Fatalf("write second snapshot: %v", err)
	}

	gotFirst, err := ReadSnapshotRecord(&buf)
	if err != nil {
		t.Fatalf("read first snapshot: %v", err)
	}
	if !proto.Equal(gotFirst, first) {
		t.Fatalf("first snapshot = %#v, want %#v", gotFirst, first)
	}
	gotSecond, err := ReadSnapshotRecord(&buf)
	if err != nil {
		t.Fatalf("read second snapshot: %v", err)
	}
	if !proto.Equal(gotSecond, second) {
		t.Fatalf("second snapshot = %#v, want %#v", gotSecond, second)
	}
	if _, err := ReadSnapshotRecord(&buf); !errors.Is(err, io.EOF) {
		t.Fatalf("final read error = %v, want EOF", err)
	}
}

func TestTailArtifactRecordRejectsChecksumMismatch(t *testing.T) {
	record := &publishedv1.TailRecord{
		SchemaVersion:   CurrentSchemaVersion,
		SourceNamespace: "billing-prod",
		ShardId:         "tenant-txn",
		LogIndex:        11,
		Mutation: &publishedv1.TailRecord_FinalizedDocument{
			FinalizedDocument: sampleDocument(),
		},
	}
	var buf bytes.Buffer
	if err := WriteTailRecord(&buf, record); err != nil {
		t.Fatalf("write tail: %v", err)
	}
	data := buf.Bytes()
	if len(data) < 6 {
		t.Fatalf("framed data too short: %d", len(data))
	}
	data[5] ^= 0xff

	_, err := ReadTailRecord(bytes.NewReader(data))
	if !errors.Is(err, ErrArtifactChecksumMismatch) {
		t.Fatalf("read error = %v, want checksum mismatch", err)
	}
}

func TestArtifactRecordRejectsTruncationAndOversize(t *testing.T) {
	record := &publishedv1.TailRecord{
		SchemaVersion:   CurrentSchemaVersion,
		SourceNamespace: "billing-prod",
		ShardId:         "tenant-txn",
		LogIndex:        11,
		Mutation: &publishedv1.TailRecord_FinalizedDocument{
			FinalizedDocument: sampleDocument(),
		},
	}
	var buf bytes.Buffer
	if err := WriteTailRecord(&buf, record); err != nil {
		t.Fatalf("write tail: %v", err)
	}
	truncated := buf.Bytes()[:buf.Len()-1]
	if _, err := ReadTailRecord(bytes.NewReader(truncated)); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated read error = %v, want unexpected EOF", err)
	}

	var oversizedHeader [4]byte
	binary.BigEndian.PutUint32(oversizedHeader[:], maxArtifactRecordPayload+1)
	if _, err := ReadTailRecord(bytes.NewReader(oversizedHeader[:])); !errors.Is(err, ErrArtifactRecordTooLarge) {
		t.Fatalf("oversized read error = %v, want record too large", err)
	}
}

func sampleManifest() *publishedv1.Manifest {
	now := timestamppb.New(time.Unix(100, 0).UTC())
	return &publishedv1.Manifest{
		SchemaVersion:   CurrentSchemaVersion,
		CellId:          "cell-a",
		SourceNamespace: "billing-prod",
		ManifestId:      "manifest-0001",
		Generation:      1,
		PublishedAt:     now,
		ShardWatermarks: []*publishedv1.ShardWatermark{
			{ShardId: "tenant-txn", HighWatermark: 10},
		},
		Snapshots: []*publishedv1.ArtifactRef{
			{
				Kind:       publishedv1.ArtifactKind_ARTIFACT_KIND_SNAPSHOT,
				ShardId:    "tenant-txn",
				ObjectKey:  "cells/cell-a/metadata/v1/snapshots/shard=tenant-txn/0001.snap",
				Length:     128,
				Sha256:     []byte{1, 2, 3},
				FirstIndex: 1,
				LastIndex:  10,
			},
		},
		RequiredObjects: []*publishedv1.ObjectRef{
			{
				Kind:      publishedv1.ObjectKind_OBJECT_KIND_BLOCK,
				ObjectKey: "blocks/0001.blk",
				Length:    1024,
				Sha256:    []byte{4, 5, 6},
			},
		},
		ProducerBuild:  "test-build",
		ProducerSchema: "published.v1",
	}
}

func sampleDocument() *publishedv1.PublishedDocument {
	now := timestamppb.New(time.Unix(100, 0).UTC())
	return &publishedv1.PublishedDocument{
		TenantId:       "tenant",
		TransactionId:  "txn",
		DocumentName:   "invoice.xml",
		DocumentClass:  1,
		PriorityClass:  2,
		Length:         42,
		LogicalSha256:  []byte{1, 2, 3},
		CreatedAt:      now,
		FinalizedAt:    now,
		Availability:   1,
		LifecycleState: 1,
		Locations: []*publishedv1.PublishedLocation{
			{
				BlockId:      "block-1",
				StoredOffset: 64,
				StoredLength: 42,
				Frames: []*publishedv1.PublishedFrame{
					{
						FrameOffset:   64,
						SegmentOffset: 64,
						SegmentLength: 42,
						Sha256:        []byte{7, 8, 9},
					},
				},
				BackendObjectKey:  "blocks/block-1.blk",
				IndexObjectKey:    "blocks/block-1.idx",
				EnvelopeObjectKey: "blocks/block-1.env",
				FormatVersion:     1,
			},
		},
	}
}

func appendUnknownVarint(data []byte, fieldNumber protowire.Number, value uint64) []byte {
	data = protowire.AppendTag(data, fieldNumber, protowire.VarintType)
	return protowire.AppendVarint(data, value)
}
