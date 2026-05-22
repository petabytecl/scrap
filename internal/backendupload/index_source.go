package backendupload

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/petabytecl/scrap/internal/backend"
	storagev1 "github.com/petabytecl/scrap/internal/gen/scrap/storage/v1"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/storageformat"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var deterministicMarshal = proto.MarshalOptions{Deterministic: true}

type BlockIndexSource interface {
	OpenBlockIndex(context.Context, metastore.UploadIntent, backend.Object) (io.ReadCloser, error)
}

type BlockDocumentLister interface {
	ListBlockDocuments(blockID string) ([]metastore.Document, error)
}

type LocalBlockIndexSource struct {
	Documents BlockDocumentLister
	ShardID   string
}

func (s LocalBlockIndexSource) OpenBlockIndex(ctx context.Context, intent metastore.UploadIntent, blockObject backend.Object) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.Documents == nil {
		return nil, fmt.Errorf("backendupload: block document lister is not configured")
	}
	documents, err := s.Documents.ListBlockDocuments(intent.BlockID)
	if err != nil {
		return nil, err
	}
	if len(documents) == 0 {
		return nil, fmt.Errorf("backendupload: no documents found for block %q", intent.BlockID)
	}
	index, err := buildBlockIndex(intent.BlockID, s.ShardID, blockObject, intent.EnvelopeObjectKey, documents)
	if err != nil {
		return nil, err
	}
	data, err := storageformat.MarshalBlockIndex(index)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func buildBlockIndex(blockID string, shardID string, blockObject backend.Object, envelopeObjectKey string, documents []metastore.Document) (*storagev1.BlockIndex, error) {
	sort.Slice(documents, func(i int, j int) bool {
		left := documents[i]
		right := documents[j]
		if left.Location.StoredOffset != right.Location.StoredOffset {
			return left.Location.StoredOffset < right.Location.StoredOffset
		}
		if left.Identity.TenantID != right.Identity.TenantID {
			return left.Identity.TenantID < right.Identity.TenantID
		}
		if left.Identity.TransactionID != right.Identity.TransactionID {
			return left.Identity.TransactionID < right.Identity.TransactionID
		}
		return left.Identity.DocumentName < right.Identity.DocumentName
	})

	createdAt := documents[0].CreatedAt
	index := &storagev1.BlockIndex{
		SchemaVersion:     storageformat.CurrentSchemaVersion,
		BlockId:           blockID,
		ShardId:           shardID,
		FormatVersion:     1,
		BlockLength:       blockObject.Length,
		BlockSha256:       append([]byte(nil), blockObject.SHA256[:]...),
		EnvelopeObjectKey: optionalString(envelopeObjectKey, envelopeObjectKey != ""),
		CreatedAt:         timestamppb.New(createdAt),
	}
	for _, document := range documents {
		if !document.CreatedAt.IsZero() && document.CreatedAt.Before(createdAt) {
			createdAt = document.CreatedAt
			index.CreatedAt = timestamppb.New(createdAt)
		}
		record, err := buildIndexDocumentRecord(document, len(index.Frames))
		if err != nil {
			return nil, err
		}
		index.Documents = append(index.Documents, record)
		for _, frame := range document.Location.Frames {
			index.Frames = append(index.Frames, &storagev1.FrameChecksumRecord{
				FrameIndex:      uint32(len(index.Frames)),
				PlaintextOffset: frame.SegmentOffset,
				PlaintextLength: frame.SegmentLength,
				StoredOffset:    frame.SegmentOffset,
				StoredLength:    frame.SegmentLength,
				PlaintextSha256: append([]byte(nil), frame.SHA256[:]...),
				StoredSha256:    append([]byte(nil), frame.SHA256[:]...),
				EncryptionMode:  storagev1.EncryptionMode_ENCRYPTION_MODE_NONE,
			})
		}
	}
	data, err := storageformat.MarshalBlockIndex(index)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	index.IndexSha256 = append([]byte(nil), sum[:]...)
	return index, nil
}

func buildIndexDocumentRecord(document metastore.Document, firstFrame int) (*storagev1.IndexDocumentRecord, error) {
	metadata, err := deterministicMarshal.Marshal(&storagev1.DocumentMetadataBlob{
		TenantId:             document.Identity.TenantID,
		TransactionId:        document.Identity.TransactionID,
		DocumentName:         document.Identity.DocumentName,
		ContentType:          optionalString(document.ContentType, document.HasContentType),
		WorkflowStage:        optionalString(document.WorkflowStage, document.HasWorkflowStage),
		Tags:                 cloneStringMap(document.Tags),
		ClientIdempotencyKey: optionalString(document.ClientIdempotencyKey, document.HasClientIdempotencyKey),
		CreatedByService:     document.CreatedByService,
	})
	if err != nil {
		return nil, err
	}
	lastFrame := firstFrame
	if len(document.Location.Frames) > 0 {
		lastFrame = firstFrame + len(document.Location.Frames) - 1
	}
	documentFingerprint := document.DocumentIdentityFingerprint
	if documentFingerprint == [16]byte{} {
		documentFingerprint = fingerprint128(document.Identity.TenantID, document.Identity.TransactionID, document.Identity.DocumentName)
	}
	transactionFingerprint := fingerprint128(document.Identity.TenantID, document.Identity.TransactionID)
	return &storagev1.IndexDocumentRecord{
		DocumentKeyId:               fingerprint64(document.Identity.TenantID, document.Identity.TransactionID, document.Identity.DocumentName),
		TransactionKeyId:            fingerprint64(document.Identity.TenantID, document.Identity.TransactionID),
		DocumentNameFingerprint:     fingerprintBytes(document.Identity.DocumentName),
		DocumentIdentityFingerprint: append([]byte(nil), documentFingerprint[:]...),
		StoredOffset:                document.Location.StoredOffset,
		StoredLength:                document.Location.StoredLength,
		LogicalLength:               document.Length,
		LogicalSha256:               append([]byte(nil), document.LogicalSHA256[:]...),
		StoredSha256:                append([]byte(nil), document.StoredSHA256[:]...),
		DocumentClass:               uint32(document.DocumentClass),
		PriorityClass:               uint32(document.PriorityClass),
		CreatedAtMs:                 unixMillis(document.CreatedAt),
		MetadataBlob:                metadata,
		TransactionFingerprint:      append([]byte(nil), transactionFingerprint[:]...),
		FirstFrameIndex:             uint32(firstFrame),
		LastFrameIndex:              uint32(lastFrame),
	}, nil
}

func fingerprint64(parts ...string) uint64 {
	sum := fingerprint(parts...)
	return binary.BigEndian.Uint64(sum[:8])
}

func fingerprint128(parts ...string) [16]byte {
	sum := fingerprint(parts...)
	var out [16]byte
	copy(out[:], sum[:16])
	return out
}

func fingerprintBytes(parts ...string) []byte {
	sum := fingerprint(parts...)
	return append([]byte(nil), sum[:16]...)
}

func fingerprint(parts ...string) [32]byte {
	hasher := sha256.New()
	for i, part := range parts {
		if i > 0 {
			_, _ = hasher.Write([]byte{0})
		}
		_, _ = hasher.Write([]byte(part))
	}
	var out [32]byte
	copy(out[:], hasher.Sum(nil))
	return out
}

func optionalString(value string, present bool) *string {
	if !present {
		return nil
	}
	return &value
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func unixMillis(value time.Time) uint64 {
	if value.IsZero() || value.Before(time.Unix(0, 0).UTC()) {
		return 0
	}
	return uint64(value.UnixMilli())
}
