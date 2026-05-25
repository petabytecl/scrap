package backendupload

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/petabytecl/scrap/internal/backend"
	storagev1 "github.com/petabytecl/scrap/internal/gen/scrap/storage/v1"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/safeconv"
	"github.com/petabytecl/scrap/internal/storageformat"
)

var deterministicMarshal = proto.MarshalOptions{Deterministic: true}

type BlockIndexSource interface {
	OpenBlockIndex(context.Context, metastore.UploadIntent, backend.Object) (io.ReadCloser, error)
}

type BlockIndexSourceWithOptions interface {
	OpenBlockIndexWithOptions(context.Context, metastore.UploadIntent, backend.Object, BlockIndexOptions) (io.ReadCloser, error)
}

type BlockIndexOptions struct {
	EncryptedFrames []EncryptedFrame
}

type BlockDocumentLister interface {
	ListBlockDocuments(blockID string) ([]metastore.Document, error)
}

type LocalBlockIndexSource struct {
	Documents BlockDocumentLister
	ShardID   string
}

func (s LocalBlockIndexSource) OpenBlockIndex(ctx context.Context, intent metastore.UploadIntent, blockObject backend.Object) (io.ReadCloser, error) {
	return s.OpenBlockIndexWithOptions(ctx, intent, blockObject, BlockIndexOptions{})
}

func (s LocalBlockIndexSource) OpenBlockIndexWithOptions(ctx context.Context, intent metastore.UploadIntent, blockObject backend.Object, options BlockIndexOptions) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.Documents == nil {
		return nil, errors.New("backendupload: block document lister is not configured")
	}
	documents, err := s.Documents.ListBlockDocuments(intent.BlockID)
	if err != nil {
		return nil, err
	}
	if len(documents) == 0 {
		return nil, fmt.Errorf("backendupload: no documents found for block %q", intent.BlockID)
	}
	index, err := buildBlockIndex(intent.BlockID, s.ShardID, blockObject, intent.EnvelopeObjectKey, documents, options)
	if err != nil {
		return nil, err
	}
	data, err := storageformat.MarshalBlockIndex(index)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func buildBlockIndex(blockID, shardID string, blockObject backend.Object, envelopeObjectKey string, documents []metastore.Document, options ...BlockIndexOptions) (*storagev1.BlockIndex, error) {
	sortBlockIndexDocuments(documents)
	blockIndexOptions := BlockIndexOptions{}
	if len(options) > 0 {
		blockIndexOptions = options[0]
	}

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
	if len(blockIndexOptions.EncryptedFrames) > 0 {
		if err := appendEncryptedBlockIndexDocuments(index, documents, blockIndexOptions); err != nil {
			return nil, err
		}
		if err := appendEncryptedFrameRecords(index, blockIndexOptions.EncryptedFrames); err != nil {
			return nil, err
		}
	} else if err := appendPlainBlockIndexDocuments(index, documents); err != nil {
		return nil, err
	}
	digest, err := storageformat.BlockIndexSHA256(index)
	if err != nil {
		return nil, err
	}
	index.IndexSha256 = digest
	return index, nil
}

func appendPlainBlockIndexDocuments(index *storagev1.BlockIndex, documents []metastore.Document) error {
	for _, document := range documents {
		if !document.CreatedAt.IsZero() && document.CreatedAt.Before(index.GetCreatedAt().AsTime()) {
			index.CreatedAt = timestamppb.New(document.CreatedAt)
		}
		firstFrame, lastFrame, err := indexDocumentFrameRange(document, index.Frames, BlockIndexOptions{})
		if err != nil {
			return err
		}
		record, err := buildIndexDocumentRecord(document, firstFrame, lastFrame)
		if err != nil {
			return err
		}
		index.Documents = append(index.Documents, record)
		if err := appendIndexFrameRecords(index, document.Location.Frames); err != nil {
			return err
		}
	}
	return nil
}

func appendEncryptedBlockIndexDocuments(index *storagev1.BlockIndex, documents []metastore.Document, options BlockIndexOptions) error {
	for _, document := range documents {
		if !document.CreatedAt.IsZero() && document.CreatedAt.Before(index.GetCreatedAt().AsTime()) {
			index.CreatedAt = timestamppb.New(document.CreatedAt)
		}
		firstFrame, lastFrame, err := indexDocumentFrameRange(document, nil, options)
		if err != nil {
			return err
		}
		record, err := buildIndexDocumentRecord(document, firstFrame, lastFrame)
		if err != nil {
			return err
		}
		index.Documents = append(index.Documents, record)
	}
	return nil
}

func sortBlockIndexDocuments(documents []metastore.Document) {
	sort.Slice(documents, func(i, j int) bool {
		return compareBlockIndexDocuments(documents[i], documents[j])
	})
}

func compareBlockIndexDocuments(left, right metastore.Document) bool {
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
}

func appendIndexFrameRecords(index *storagev1.BlockIndex, frames []metastore.FrameRecord) error {
	for _, frame := range frames {
		frameIndex, err := safeconv.IntToUint32("block index frame index", len(index.Frames))
		if err != nil {
			return err
		}
		index.Frames = append(index.Frames, &storagev1.FrameChecksumRecord{
			FrameIndex:      frameIndex,
			PlaintextOffset: frame.SegmentOffset,
			PlaintextLength: frame.SegmentLength,
			StoredOffset:    frame.SegmentOffset,
			StoredLength:    frame.SegmentLength,
			PlaintextSha256: append([]byte(nil), frame.SHA256[:]...),
			StoredSha256:    append([]byte(nil), frame.SHA256[:]...),
			EncryptionMode:  storagev1.EncryptionMode_ENCRYPTION_MODE_NONE,
		})
	}
	return nil
}

func appendEncryptedFrameRecords(index *storagev1.BlockIndex, frames []EncryptedFrame) error {
	for _, frame := range frames {
		frameIndex, err := safeconv.IntToUint32("block index frame index", len(index.Frames))
		if err != nil {
			return err
		}
		if frame.FrameIndex != frameIndex {
			return fmt.Errorf("backendupload: encrypted frame index %d does not match record order %d", frame.FrameIndex, frameIndex)
		}
		index.Frames = append(index.Frames, &storagev1.FrameChecksumRecord{
			FrameIndex:      frameIndex,
			PlaintextOffset: frame.PlaintextOffset,
			PlaintextLength: frame.PlaintextLength,
			StoredOffset:    frame.StoredOffset,
			StoredLength:    frame.StoredLength,
			PlaintextSha256: append([]byte(nil), frame.PlaintextSHA256[:]...),
			StoredSha256:    append([]byte(nil), frame.StoredSHA256[:]...),
			EncryptionMode:  storagev1.EncryptionMode_ENCRYPTION_MODE_AES_256_GCM,
			AuthTag:         append([]byte(nil), frame.AuthTag...),
		})
	}
	return nil
}

func indexDocumentFrameRange(document metastore.Document, frames []*storagev1.FrameChecksumRecord, options BlockIndexOptions) (int, int, error) {
	if len(options.EncryptedFrames) == 0 {
		firstFrame := len(frames)
		lastFrame := firstFrame
		if len(document.Location.Frames) > 0 {
			lastFrame = firstFrame + len(document.Location.Frames) - 1
		}
		return firstFrame, lastFrame, nil
	}
	documentStart := document.Location.StoredOffset
	documentEnd := documentStart + document.Location.StoredLength
	if documentEnd < documentStart {
		return 0, 0, fmt.Errorf("backendupload: document %s stored range overflows", document.Identity.DocumentName)
	}
	firstFrame := -1
	lastFrame := -1
	for i, frame := range options.EncryptedFrames {
		frameStart := frame.PlaintextOffset
		frameEnd := frame.PlaintextOffset + frame.PlaintextLength
		if frameEnd < frameStart {
			return 0, 0, fmt.Errorf("backendupload: encrypted frame %d plaintext range overflows", i)
		}
		if frameEnd <= documentStart || frameStart >= documentEnd {
			continue
		}
		if firstFrame == -1 {
			firstFrame = i
		}
		lastFrame = i
	}
	if firstFrame == -1 {
		return 0, 0, fmt.Errorf("backendupload: encrypted frames do not cover document %s", document.Identity.DocumentName)
	}
	return firstFrame, lastFrame, nil
}

func buildIndexDocumentRecord(document metastore.Document, firstFrame, lastFrame int) (*storagev1.IndexDocumentRecord, error) {
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
	firstFrameIndex, err := safeconv.IntToUint32("document first frame index", firstFrame)
	if err != nil {
		return nil, err
	}
	lastFrameIndex, err := safeconv.IntToUint32("document last frame index", lastFrame)
	if err != nil {
		return nil, err
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
		FirstFrameIndex:             firstFrameIndex,
		LastFrameIndex:              lastFrameIndex,
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
