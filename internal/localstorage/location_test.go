package localstorage

import (
	"crypto/sha256"
	"testing"

	"github.com/petabytecl/scrap/internal/blockstore"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestLocationConversionsCopyAllFields(t *testing.T) {
	record := testBlockstoreRecord()
	wantRecord := testBlockstoreRecord()

	location := metastoreLocation(record)
	record.Frames[0].FrameOffset = 999
	record.Replicas[0].MemberID = "mutated"

	testutil.RequireDeepEqualf(t, location, metastore.Location{
		BlockID:       "block-1",
		StoredOffset:  64,
		StoredLength:  128,
		LogicalSHA256: wantRecord.LogicalSHA256,
		StoredSHA256:  wantRecord.StoredSHA256,
		Frames: []metastore.FrameRecord{
			{
				FrameOffset:   8,
				SegmentOffset: 64,
				SegmentLength: 128,
				SHA256:        wantRecord.Frames[0].SHA256,
			},
		},
		Replicas: []metastore.ReplicaRef{
			{
				MemberID:     "member-a",
				BlockID:      "block-1",
				StoredOffset: 64,
				StoredLength: 128,
				StoredSHA256: wantRecord.Replicas[0].StoredSHA256,
			},
		},
	}, "metastore location")

	roundTrip := blockstoreRecord(location)
	location.Frames[0].SegmentLength = 999
	location.Replicas[0].StoredLength = 999

	testutil.RequireDeepEqualf(t, roundTrip, wantRecord, "round-trip blockstore record")
}

func TestLocationConversionsPreserveNilCollections(t *testing.T) {
	testutil.RequireTruef(t, metastoreFrameRecords(nil) == nil, "nil metastore frames")
	testutil.RequireTruef(t, metastoreReplicaRefs(nil) == nil, "nil metastore replicas")
	testutil.RequireTruef(t, blockstoreFrameRecords(nil) == nil, "nil blockstore frames")
	testutil.RequireTruef(t, blockstoreReplicaRefs(nil) == nil, "nil blockstore replicas")
}

func testBlockstoreRecord() blockstore.Record {
	logicalSHA := sha256.Sum256([]byte("logical"))
	storedSHA := sha256.Sum256([]byte("stored"))
	frameSHA := sha256.Sum256([]byte("frame"))
	replicaSHA := sha256.Sum256([]byte("replica"))
	return blockstore.Record{
		BlockID:       "block-1",
		StoredOffset:  64,
		StoredLength:  128,
		LogicalSHA256: logicalSHA,
		StoredSHA256:  storedSHA,
		Frames: []blockstore.FrameRecord{
			{
				FrameOffset:   8,
				SegmentOffset: 64,
				SegmentLength: 128,
				SHA256:        frameSHA,
			},
		},
		Replicas: []blockstore.ReplicaRef{
			{
				MemberID:     "member-a",
				BlockID:      "block-1",
				StoredOffset: 64,
				StoredLength: 128,
				StoredSHA256: replicaSHA,
			},
		},
	}
}
