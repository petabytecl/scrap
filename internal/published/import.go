package published

import (
	"errors"
	"fmt"
	"io"

	"github.com/petabytecl/scrap/internal/blockstore"
	publishedv1 "github.com/petabytecl/scrap/internal/gen/scrap/published/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/metastore"
)

type SnapshotContents struct {
	Documents     []metastore.Document
	Transactions  []metastore.Transaction
	UploadIntents []metastore.UploadIntent
	Tombstones    int
}

func ReadSnapshotContents(reader io.Reader) (SnapshotContents, error) {
	var contents SnapshotContents
	for {
		record, err := ReadSnapshotRecord(reader)
		if errors.Is(err, io.EOF) {
			return contents, nil
		}
		if err != nil {
			return contents, err
		}
		switch typed := record.GetRecord().(type) {
		case *publishedv1.SnapshotRecord_Document:
			document, intent, err := importDocument(typed.Document)
			if err != nil {
				return contents, err
			}
			contents.Documents = append(contents.Documents, document)
			contents.UploadIntents = append(contents.UploadIntents, intent)
		case *publishedv1.SnapshotRecord_Transaction:
			transaction, err := importTransaction(typed.Transaction)
			if err != nil {
				return contents, err
			}
			contents.Transactions = append(contents.Transactions, transaction)
		case *publishedv1.SnapshotRecord_Tombstone:
			contents.Tombstones++
		default:
			return contents, fmt.Errorf("published metadata: unsupported snapshot record %T", record.GetRecord())
		}
	}
}

func importDocument(document *publishedv1.PublishedDocument) (metastore.Document, metastore.UploadIntent, error) {
	if document == nil {
		return metastore.Document{}, metastore.UploadIntent{}, fmt.Errorf("published metadata: document record is required")
	}
	locations := document.GetLocations()
	if len(locations) == 0 {
		return metastore.Document{}, metastore.UploadIntent{}, fmt.Errorf("published metadata: document %q has no locations", document.GetDocumentName())
	}
	location := locations[0]
	if location.GetBackendObjectKey() == "" {
		return metastore.Document{}, metastore.UploadIntent{}, fmt.Errorf("published metadata: document %q has no backend object key", document.GetDocumentName())
	}
	logicalSHA256, err := fixed32(document.GetLogicalSha256(), "logical sha256")
	if err != nil {
		return metastore.Document{}, metastore.UploadIntent{}, err
	}
	fingerprint, err := optionalFixed16(document.GetDocumentIdentityFingerprint(), "document identity fingerprint")
	if err != nil {
		return metastore.Document{}, metastore.UploadIntent{}, err
	}
	frames, err := importFrames(location.GetFrames())
	if err != nil {
		return metastore.Document{}, metastore.UploadIntent{}, err
	}

	imported := metastore.Document{
		Identity: identity.Document{
			TenantID:      document.GetTenantId(),
			TransactionID: document.GetTransactionId(),
			DocumentName:  document.GetDocumentName(),
		},
		DocumentClass:               metastore.DocumentClass(document.GetDocumentClass()),
		PriorityClass:               metastore.PriorityClass(document.GetPriorityClass()),
		ContentType:                 document.GetContentType(),
		HasContentType:              document.ContentType != nil,
		Length:                      document.GetLength(),
		LogicalSHA256:               logicalSHA256,
		StoredSHA256:                logicalSHA256,
		DocumentIdentityFingerprint: fingerprint,
		CreatedByService:            document.GetCreatedByService(),
		WorkflowStage:               document.GetWorkflowStage(),
		HasWorkflowStage:            document.WorkflowStage != nil,
		Availability:                metastore.Availability(document.GetAvailability()),
		LifecycleState:              metastore.LifecycleState(document.GetLifecycleState()),
		UploadState:                 metastore.UploadStateUploaded,
		Tags:                        clonePublishedTags(document.GetTags()),
		Location: blockstore.Record{
			BlockID:       location.GetBlockId(),
			StoredOffset:  location.GetStoredOffset(),
			StoredLength:  location.GetStoredLength(),
			LogicalSHA256: logicalSHA256,
			Frames:        frames,
		},
	}
	if document.GetCreatedAt() != nil {
		imported.CreatedAt = document.GetCreatedAt().AsTime()
	}
	if document.GetFinalizedAt() != nil {
		imported.FinalizedAt = document.GetFinalizedAt().AsTime()
	}
	intent := metastore.UploadIntent{
		BlockID:           location.GetBlockId(),
		BackendObjectKey:  location.GetBackendObjectKey(),
		IndexObjectKey:    location.GetIndexObjectKey(),
		EnvelopeObjectKey: location.GetEnvelopeObjectKey(),
		State:             metastore.UploadStateUploaded,
	}
	return imported, intent, nil
}

func importTransaction(transaction *publishedv1.PublishedTransaction) (metastore.Transaction, error) {
	if transaction == nil {
		return metastore.Transaction{}, fmt.Errorf("published metadata: transaction record is required")
	}
	imported := metastore.Transaction{
		Identity: identity.Transaction{
			TenantID:      transaction.GetTenantId(),
			TransactionID: transaction.GetTransactionId(),
		},
		State:                  metastore.TransactionStateKind(transaction.GetState()),
		DocumentCount:          transaction.GetDocumentCount(),
		PermanentDocumentCount: transaction.GetPermanentDocumentCount(),
		EphemeralDocumentCount: transaction.GetEphemeralDocumentCount(),
		Tags:                   clonePublishedTags(transaction.GetTags()),
	}
	if transaction.GetCreatedAt() != nil {
		imported.CreatedAt = transaction.GetCreatedAt().AsTime()
	}
	if transaction.GetCompletedAt() != nil {
		completedAt := transaction.GetCompletedAt().AsTime()
		imported.CompletedAt = &completedAt
	}
	if transaction.GetTimeoutAt() != nil {
		timeoutAt := transaction.GetTimeoutAt().AsTime()
		imported.TimeoutAt = &timeoutAt
	}
	return imported, nil
}

func importFrames(frames []*publishedv1.PublishedFrame) ([]blockstore.FrameRecord, error) {
	out := make([]blockstore.FrameRecord, 0, len(frames))
	for _, frame := range frames {
		sum, err := fixed32(frame.GetSha256(), "frame sha256")
		if err != nil {
			return nil, err
		}
		out = append(out, blockstore.FrameRecord{
			FrameOffset:   frame.GetFrameOffset(),
			SegmentOffset: frame.GetSegmentOffset(),
			SegmentLength: frame.GetSegmentLength(),
			SHA256:        sum,
		})
	}
	return out, nil
}

func fixed32(data []byte, field string) ([32]byte, error) {
	var out [32]byte
	if len(data) != len(out) {
		return out, fmt.Errorf("published metadata: %s is %d bytes", field, len(data))
	}
	copy(out[:], data)
	return out, nil
}

func optionalFixed16(data []byte, field string) ([16]byte, error) {
	var out [16]byte
	if len(data) == 0 {
		return out, nil
	}
	if len(data) != len(out) {
		return out, fmt.Errorf("published metadata: %s is %d bytes", field, len(data))
	}
	copy(out[:], data)
	return out, nil
}
