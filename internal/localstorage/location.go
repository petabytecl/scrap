package localstorage

import (
	"github.com/petabytecl/scrap/internal/blockstore"
	"github.com/petabytecl/scrap/internal/metastore"
)

func metastoreLocation(record blockstore.Record) metastore.Location {
	return metastore.Location{
		BlockID:       record.BlockID,
		StoredOffset:  record.StoredOffset,
		StoredLength:  record.StoredLength,
		LogicalSHA256: record.LogicalSHA256,
		StoredSHA256:  record.StoredSHA256,
		Frames:        metastoreFrameRecords(record.Frames),
		Replicas:      metastoreReplicaRefs(record.Replicas),
	}
}

func blockstoreRecord(location metastore.Location) blockstore.Record {
	return blockstore.Record{
		BlockID:       location.BlockID,
		StoredOffset:  location.StoredOffset,
		StoredLength:  location.StoredLength,
		LogicalSHA256: location.LogicalSHA256,
		StoredSHA256:  location.StoredSHA256,
		Frames:        blockstoreFrameRecords(location.Frames),
		Replicas:      blockstoreReplicaRefs(location.Replicas),
	}
}

func metastoreFrameRecords(frames []blockstore.FrameRecord) []metastore.FrameRecord {
	if len(frames) == 0 {
		return nil
	}
	out := make([]metastore.FrameRecord, 0, len(frames))
	for _, frame := range frames {
		out = append(out, metastore.FrameRecord{
			FrameOffset:   frame.FrameOffset,
			SegmentOffset: frame.SegmentOffset,
			SegmentLength: frame.SegmentLength,
			SHA256:        frame.SHA256,
		})
	}
	return out
}

func blockstoreFrameRecords(frames []metastore.FrameRecord) []blockstore.FrameRecord {
	if len(frames) == 0 {
		return nil
	}
	out := make([]blockstore.FrameRecord, 0, len(frames))
	for _, frame := range frames {
		out = append(out, blockstore.FrameRecord{
			FrameOffset:   frame.FrameOffset,
			SegmentOffset: frame.SegmentOffset,
			SegmentLength: frame.SegmentLength,
			SHA256:        frame.SHA256,
		})
	}
	return out
}

func metastoreReplicaRefs(replicas []blockstore.ReplicaRef) []metastore.ReplicaRef {
	if len(replicas) == 0 {
		return nil
	}
	out := make([]metastore.ReplicaRef, 0, len(replicas))
	for _, replica := range replicas {
		out = append(out, metastore.ReplicaRef{
			MemberID:     replica.MemberID,
			BlockID:      replica.BlockID,
			StoredOffset: replica.StoredOffset,
			StoredLength: replica.StoredLength,
			StoredSHA256: replica.StoredSHA256,
		})
	}
	return out
}

func blockstoreReplicaRefs(replicas []metastore.ReplicaRef) []blockstore.ReplicaRef {
	if len(replicas) == 0 {
		return nil
	}
	out := make([]blockstore.ReplicaRef, 0, len(replicas))
	for _, replica := range replicas {
		out = append(out, blockstoreReplicaRef(replica))
	}
	return out
}

func blockstoreReplicaRef(replica metastore.ReplicaRef) blockstore.ReplicaRef {
	return blockstore.ReplicaRef{
		MemberID:     replica.MemberID,
		BlockID:      replica.BlockID,
		StoredOffset: replica.StoredOffset,
		StoredLength: replica.StoredLength,
		StoredSHA256: replica.StoredSHA256,
	}
}
