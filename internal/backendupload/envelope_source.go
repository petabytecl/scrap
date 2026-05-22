package backendupload

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"time"

	"github.com/petabytecl/scrap/internal/backend"
	storagev1 "github.com/petabytecl/scrap/internal/gen/scrap/storage/v1"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/storageformat"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	localNoopEnvelopeKeyID      = "local-non-production/noop"
	localNoopEnvelopeKeyVersion = 1
)

type BlockEnvelopeSource interface {
	OpenBlockEnvelope(context.Context, metastore.UploadIntent, backend.Object) (io.ReadCloser, error)
}

type LocalBlockEnvelopeSource struct {
	CellID string
	KeyID  string
}

func (s LocalBlockEnvelopeSource) OpenBlockEnvelope(ctx context.Context, intent metastore.UploadIntent, blockObject backend.Object) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	record, err := buildLocalEnvelopeRecord(intent, blockObject, s.CellID, s.KeyID)
	if err != nil {
		return nil, err
	}
	data, err := storageformat.MarshalEnvelopeRecord(record)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func buildLocalEnvelopeRecord(intent metastore.UploadIntent, blockObject backend.Object, cellID string, keyID string) (*storagev1.EnvelopeRecord, error) {
	if intent.BlockID == "" {
		return nil, fmt.Errorf("backendupload: upload intent block id is required")
	}
	if blockObject.Key == "" {
		return nil, fmt.Errorf("backendupload: uploaded block object is required")
	}
	if cellID == "" {
		cellID = "local"
	}
	if keyID == "" {
		keyID = localNoopEnvelopeKeyID
	}
	createdAt := intent.UpdatedAt
	if createdAt.IsZero() {
		createdAt = time.Unix(0, 0).UTC()
	}
	record := &storagev1.EnvelopeRecord{
		SchemaVersion: storageformat.CurrentSchemaVersion,
		EnvelopeId:    "local-noop:" + intent.BlockID,
		BlockId:       intent.BlockID,
		KeyId:         keyID,
		KeyVersion:    localNoopEnvelopeKeyVersion,
		WrappedDek:    []byte("local-non-production-noop"),
		DekAlgorithm:  "none",
		AeadAlgorithm: "none",
		AadContext:    localEnvelopeAAD(cellID, intent.BlockID, blockObject),
		CreatedAt:     timestamppb.New(createdAt),
	}
	data, err := storageformat.MarshalEnvelopeRecord(record)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	record.EnvelopeSha256 = append([]byte(nil), sum[:]...)
	return record, nil
}

func localEnvelopeAAD(cellID string, blockID string, blockObject backend.Object) []byte {
	return []byte(fmt.Sprintf("%s\x00%s\x00%s\x00%x", cellID, blockID, blockObject.Key, blockObject.SHA256))
}
