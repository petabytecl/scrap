package metastore

import (
	"fmt"
	"time"

	"github.com/petabytecl/scrap/internal/blockstore"
	metastorev1 "github.com/petabytecl/scrap/internal/gen/scrap/metastore/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var protoMarshal = proto.MarshalOptions{Deterministic: true}

const CurrentSchemaVersion uint32 = 1

func marshalDocument(document Document) ([]byte, error) {
	return marshalDocumentRecord(documentToProto(document))
}

func MarshalDocument(document Document) ([]byte, error) {
	return marshalDocument(document)
}

func unmarshalDocument(data []byte) (Document, error) {
	record, err := unmarshalDocumentRecord(data)
	if err != nil {
		return Document{}, err
	}
	return documentFromProto(record), nil
}

func UnmarshalDocument(data []byte) (Document, error) {
	return unmarshalDocument(data)
}

func marshalDocumentRecord(record *metastorev1.DocumentRecord) ([]byte, error) {
	if err := validateSchemaVersion("document", record.GetSchemaVersion()); err != nil {
		return nil, err
	}
	return protoMarshal.Marshal(record)
}

func unmarshalDocumentRecord(data []byte) (*metastorev1.DocumentRecord, error) {
	var record metastorev1.DocumentRecord
	if err := proto.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	if err := validateSchemaVersion("document", record.GetSchemaVersion()); err != nil {
		return nil, err
	}
	return &record, nil
}

func marshalTransaction(transaction Transaction) ([]byte, error) {
	return marshalTransactionRecord(transactionToProto(transaction))
}

func unmarshalTransaction(data []byte) (Transaction, error) {
	record, err := unmarshalTransactionRecord(data)
	if err != nil {
		return Transaction{}, err
	}
	return transactionFromProto(record), nil
}

func marshalTransactionRecord(record *metastorev1.TransactionRecord) ([]byte, error) {
	if err := validateSchemaVersion("transaction", record.GetSchemaVersion()); err != nil {
		return nil, err
	}
	return protoMarshal.Marshal(record)
}

func unmarshalTransactionRecord(data []byte) (*metastorev1.TransactionRecord, error) {
	var record metastorev1.TransactionRecord
	if err := proto.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	if err := validateSchemaVersion("transaction", record.GetSchemaVersion()); err != nil {
		return nil, err
	}
	return &record, nil
}

func validateSchemaVersion(recordKind string, version uint32) error {
	if version != CurrentSchemaVersion {
		return fmt.Errorf("%w: %s record version %d", ErrUnsupportedSchemaVersion, recordKind, version)
	}
	return nil
}

func documentToProto(document Document) *metastorev1.DocumentRecord {
	return &metastorev1.DocumentRecord{
		SchemaVersion:               CurrentSchemaVersion,
		TenantId:                    document.Identity.TenantID,
		TransactionId:               document.Identity.TransactionID,
		DocumentName:                document.Identity.DocumentName,
		DocumentClass:               uint32(document.DocumentClass),
		PriorityClass:               uint32(document.PriorityClass),
		ContentType:                 optionalStringPointer(document.ContentType, document.HasContentType),
		Length:                      document.Length,
		LogicalSha256:               document.LogicalSHA256[:],
		StoredSha256:                document.StoredSHA256[:],
		DocumentIdentityFingerprint: document.DocumentIdentityFingerprint[:],
		CreatedByService:            document.CreatedByService,
		WorkflowStage:               optionalStringPointer(document.WorkflowStage, document.HasWorkflowStage),
		CreatedAt:                   timestamppb.New(document.CreatedAt),
		FinalizedAt:                 timestamppb.New(document.FinalizedAt),
		Availability:                uint32(document.Availability),
		LifecycleState:              uint32(document.LifecycleState),
		Tags:                        cloneTags(document.Tags),
		Location:                    locationToProto(document.Location),
		ClientIdempotencyKey:        optionalStringPointer(document.ClientIdempotencyKey, document.HasClientIdempotencyKey),
	}
}

func documentFromProto(record *metastorev1.DocumentRecord) Document {
	document := Document{
		Identity: identity.Document{
			TenantID:      record.GetTenantId(),
			TransactionID: record.GetTransactionId(),
			DocumentName:  record.GetDocumentName(),
		},
		DocumentClass:           DocumentClass(record.GetDocumentClass()),
		PriorityClass:           PriorityClass(record.GetPriorityClass()),
		ContentType:             record.GetContentType(),
		HasContentType:          record.ContentType != nil,
		Length:                  record.GetLength(),
		CreatedByService:        record.GetCreatedByService(),
		WorkflowStage:           record.GetWorkflowStage(),
		HasWorkflowStage:        record.WorkflowStage != nil,
		Availability:            Availability(record.GetAvailability()),
		LifecycleState:          LifecycleState(record.GetLifecycleState()),
		Tags:                    cloneTags(record.GetTags()),
		Location:                locationFromProto(record.GetLocation()),
		ClientIdempotencyKey:    record.GetClientIdempotencyKey(),
		HasClientIdempotencyKey: record.ClientIdempotencyKey != nil,
	}
	copy(document.LogicalSHA256[:], record.GetLogicalSha256())
	copy(document.StoredSHA256[:], record.GetStoredSha256())
	copy(document.DocumentIdentityFingerprint[:], record.GetDocumentIdentityFingerprint())
	document.Location.LogicalSHA256 = document.LogicalSHA256
	if record.GetCreatedAt() != nil {
		document.CreatedAt = record.GetCreatedAt().AsTime()
	}
	if record.GetFinalizedAt() != nil {
		document.FinalizedAt = record.GetFinalizedAt().AsTime()
	}
	return document
}

func transactionToProto(transaction Transaction) *metastorev1.TransactionRecord {
	return &metastorev1.TransactionRecord{
		SchemaVersion:          CurrentSchemaVersion,
		TenantId:               transaction.Identity.TenantID,
		TransactionId:          transaction.Identity.TransactionID,
		State:                  uint32(transaction.State),
		DocumentCount:          transaction.DocumentCount,
		PermanentDocumentCount: transaction.PermanentDocumentCount,
		EphemeralDocumentCount: transaction.EphemeralDocumentCount,
		CreatedAt:              timestamppb.New(transaction.CreatedAt),
		CompletedAt:            optionalTime(transaction.CompletedAt),
		TimeoutAt:              optionalTime(transaction.TimeoutAt),
		Tags:                   cloneTags(transaction.Tags),
	}
}

func transactionFromProto(record *metastorev1.TransactionRecord) Transaction {
	transaction := Transaction{
		Identity: identity.Transaction{
			TenantID:      record.GetTenantId(),
			TransactionID: record.GetTransactionId(),
		},
		State:                  TransactionStateKind(record.GetState()),
		DocumentCount:          record.GetDocumentCount(),
		PermanentDocumentCount: record.GetPermanentDocumentCount(),
		EphemeralDocumentCount: record.GetEphemeralDocumentCount(),
		Tags:                   cloneTags(record.GetTags()),
	}
	if record.GetCreatedAt() != nil {
		transaction.CreatedAt = record.GetCreatedAt().AsTime()
	}
	if record.GetCompletedAt() != nil {
		completedAt := record.GetCompletedAt().AsTime()
		transaction.CompletedAt = &completedAt
	}
	if record.GetTimeoutAt() != nil {
		timeoutAt := record.GetTimeoutAt().AsTime()
		transaction.TimeoutAt = &timeoutAt
	}
	return transaction
}

func locationToProto(location blockstore.Record) *metastorev1.Location {
	out := &metastorev1.Location{
		BlockId:      location.BlockID,
		StoredOffset: location.StoredOffset,
		StoredLength: location.StoredLength,
		Frames:       make([]*metastorev1.FrameRecord, 0, len(location.Frames)),
	}
	for _, frame := range location.Frames {
		out.Frames = append(out.Frames, &metastorev1.FrameRecord{
			FrameOffset:   frame.FrameOffset,
			SegmentOffset: frame.SegmentOffset,
			SegmentLength: frame.SegmentLength,
			Sha256:        append([]byte(nil), frame.SHA256[:]...),
		})
	}
	return out
}

func locationFromProto(location *metastorev1.Location) blockstore.Record {
	if location == nil {
		return blockstore.Record{}
	}
	out := blockstore.Record{
		BlockID:       location.GetBlockId(),
		StoredOffset:  location.GetStoredOffset(),
		StoredLength:  location.GetStoredLength(),
		Frames:        make([]blockstore.FrameRecord, 0, len(location.GetFrames())),
		LogicalSHA256: [32]byte{},
	}
	for _, frame := range location.GetFrames() {
		outFrame := blockstore.FrameRecord{
			FrameOffset:   frame.GetFrameOffset(),
			SegmentOffset: frame.GetSegmentOffset(),
			SegmentLength: frame.GetSegmentLength(),
		}
		copy(outFrame.SHA256[:], frame.GetSha256())
		out.Frames = append(out.Frames, outFrame)
	}
	return out
}

func optionalStringPointer(value string, present bool) *string {
	if !present {
		return nil
	}
	return &value
}

func optionalTime(value *time.Time) *timestamppb.Timestamp {
	if value == nil {
		return nil
	}
	return timestamppb.New(*value)
}

func cloneTags(tags map[string]string) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	out := make(map[string]string, len(tags))
	for key, value := range tags {
		out[key] = value
	}
	return out
}
