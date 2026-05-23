package published

import (
	"fmt"
	"io"
	"sort"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	publishedv1 "github.com/petabytecl/scrap/internal/gen/scrap/published/v1"
	"github.com/petabytecl/scrap/internal/metastore"
)

type LocationObjects struct {
	BackendObjectKey  string
	IndexObjectKey    string
	EnvelopeObjectKey string
}

type SnapshotOptions struct {
	SourceNamespace string
	ShardID         string
	HighWatermark   uint64
	LocationObjects map[string]LocationObjects
}

func WriteDocumentSnapshotRecords(writer io.Writer, options SnapshotOptions, documents []metastore.Document) error {
	return WriteMetadataSnapshotRecords(writer, options, documents, nil)
}

func WriteMetadataSnapshotRecords(writer io.Writer, options SnapshotOptions, documents []metastore.Document, transactions []metastore.Transaction) error {
	if writer == nil {
		return fmt.Errorf("published metadata: snapshot writer is required")
	}
	if options.SourceNamespace == "" {
		return fmt.Errorf("published metadata: source namespace is required")
	}
	if options.ShardID == "" {
		return fmt.Errorf("published metadata: shard id is required")
	}
	ordered := append([]metastore.Document(nil), documents...)
	sort.Slice(ordered, func(i, j int) bool {
		left := ordered[i].Identity
		right := ordered[j].Identity
		if left.TenantID != right.TenantID {
			return left.TenantID < right.TenantID
		}
		if left.TransactionID != right.TransactionID {
			return left.TransactionID < right.TransactionID
		}
		return left.DocumentName < right.DocumentName
	})
	for _, document := range ordered {
		record := snapshotRecord(options, document)
		if err := WriteSnapshotRecord(writer, record); err != nil {
			return err
		}
	}
	orderedTransactions := append([]metastore.Transaction(nil), transactions...)
	sort.Slice(orderedTransactions, func(i, j int) bool {
		left := orderedTransactions[i].Identity
		right := orderedTransactions[j].Identity
		if left.TenantID != right.TenantID {
			return left.TenantID < right.TenantID
		}
		return left.TransactionID < right.TransactionID
	})
	for _, transaction := range orderedTransactions {
		record := transactionSnapshotRecord(options, transaction)
		if err := WriteSnapshotRecord(writer, record); err != nil {
			return err
		}
	}
	return nil
}

func snapshotRecord(options SnapshotOptions, document metastore.Document) *publishedv1.SnapshotRecord {
	record := &publishedv1.SnapshotRecord{
		SchemaVersion:   CurrentSchemaVersion,
		SourceNamespace: options.SourceNamespace,
		ShardId:         options.ShardID,
		HighWatermark:   options.HighWatermark,
	}
	if document.LifecycleState == metastore.LifecycleStateTombstoned {
		record.Record = &publishedv1.SnapshotRecord_Tombstone{
			Tombstone: publishedTombstone(document, options.HighWatermark),
		}
		return record
	}
	record.Record = &publishedv1.SnapshotRecord_Document{
		Document: publishedDocument(document, options.LocationObjects[document.Location.BlockID]),
	}
	return record
}

func publishedDocument(document metastore.Document, objects LocationObjects) *publishedv1.PublishedDocument {
	out := &publishedv1.PublishedDocument{
		TenantId:                    document.Identity.TenantID,
		TransactionId:               document.Identity.TransactionID,
		DocumentName:                document.Identity.DocumentName,
		DocumentClass:               uint32(document.DocumentClass),
		PriorityClass:               uint32(document.PriorityClass),
		ContentType:                 optionalPublishedString(document.ContentType, document.HasContentType),
		Length:                      document.Length,
		LogicalSha256:               append([]byte(nil), document.LogicalSHA256[:]...),
		DocumentIdentityFingerprint: append([]byte(nil), document.DocumentIdentityFingerprint[:]...),
		CreatedByService:            document.CreatedByService,
		WorkflowStage:               optionalPublishedString(document.WorkflowStage, document.HasWorkflowStage),
		CreatedAt:                   timestamppb.New(document.CreatedAt),
		FinalizedAt:                 timestamppb.New(document.FinalizedAt),
		Availability:                uint32(document.Availability),
		LifecycleState:              uint32(document.LifecycleState),
		Tags:                        clonePublishedTags(document.Tags),
		Locations: []*publishedv1.PublishedLocation{
			publishedLocation(document, objects),
		},
	}
	return out
}

func publishedLocation(document metastore.Document, objects LocationObjects) *publishedv1.PublishedLocation {
	location := &publishedv1.PublishedLocation{
		BlockId:           document.Location.BlockID,
		StoredOffset:      document.Location.StoredOffset,
		StoredLength:      document.Location.StoredLength,
		BackendObjectKey:  objects.BackendObjectKey,
		IndexObjectKey:    objects.IndexObjectKey,
		EnvelopeObjectKey: objects.EnvelopeObjectKey,
		FormatVersion:     CurrentLocationFormatVersion,
		Frames:            make([]*publishedv1.PublishedFrame, 0, len(document.Location.Frames)),
	}
	for _, frame := range document.Location.Frames {
		location.Frames = append(location.Frames, &publishedv1.PublishedFrame{
			FrameOffset:   frame.FrameOffset,
			SegmentOffset: frame.SegmentOffset,
			SegmentLength: frame.SegmentLength,
			Sha256:        append([]byte(nil), frame.SHA256[:]...),
		})
	}
	return location
}

func publishedTombstone(document metastore.Document, tombstonedAtIndex uint64) *publishedv1.PublishedTombstone {
	tombstone := &publishedv1.PublishedTombstone{
		TenantId:          document.Identity.TenantID,
		TransactionId:     document.Identity.TransactionID,
		DocumentName:      &document.Identity.DocumentName,
		TombstonedAtIndex: tombstonedAtIndex,
	}
	if document.TombstonedAt != nil {
		tombstone.TombstonedAt = timestamppb.New(*document.TombstonedAt)
	}
	return tombstone
}

func transactionSnapshotRecord(options SnapshotOptions, transaction metastore.Transaction) *publishedv1.SnapshotRecord {
	return &publishedv1.SnapshotRecord{
		SchemaVersion:   CurrentSchemaVersion,
		SourceNamespace: options.SourceNamespace,
		ShardId:         options.ShardID,
		HighWatermark:   options.HighWatermark,
		Record: &publishedv1.SnapshotRecord_Transaction{
			Transaction: publishedTransaction(transaction),
		},
	}
}

func publishedTransaction(transaction metastore.Transaction) *publishedv1.PublishedTransaction {
	return &publishedv1.PublishedTransaction{
		TenantId:               transaction.Identity.TenantID,
		TransactionId:          transaction.Identity.TransactionID,
		State:                  uint32(transaction.State),
		DocumentCount:          transaction.DocumentCount,
		PermanentDocumentCount: transaction.PermanentDocumentCount,
		EphemeralDocumentCount: transaction.EphemeralDocumentCount,
		CreatedAt:              timestamppb.New(transaction.CreatedAt),
		CompletedAt:            optionalPublishedTime(transaction.CompletedAt),
		TimeoutAt:              optionalPublishedTime(transaction.TimeoutAt),
		Tags:                   clonePublishedTags(transaction.Tags),
	}
}

func optionalPublishedString(value string, present bool) *string {
	if !present {
		return nil
	}
	return &value
}

func optionalPublishedTime(value *time.Time) *timestamppb.Timestamp {
	if value == nil {
		return nil
	}
	return timestamppb.New(*value)
}

func clonePublishedTags(tags map[string]string) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	out := make(map[string]string, len(tags))
	for key, value := range tags {
		out[key] = value
	}
	return out
}
