package backendupload

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/cryptoenv"
	storagev1 "github.com/petabytecl/scrap/internal/gen/scrap/storage/v1"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/storageformat"
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

type TransitBlockEnvelopeSource struct {
	Transit cryptoenv.Transit
	CellID  string
	KeyID   string
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

func (s TransitBlockEnvelopeSource) OpenBlockEnvelope(ctx context.Context, intent metastore.UploadIntent, blockObject backend.Object) (io.ReadCloser, error) {
	material, err := cryptoenv.CreateEnvelopeRecord(ctx, s.Transit, cryptoenv.EnvelopeRequest{
		BlockID:     intent.BlockID,
		CellID:      s.CellID,
		BlockObject: blockObject,
		KeyID:       s.KeyID,
		CreatedAt:   intent.UpdatedAt,
	})
	if err != nil {
		return nil, err
	}
	data, err := storageformat.MarshalEnvelopeRecord(material.Record)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func buildLocalEnvelopeRecord(intent metastore.UploadIntent, blockObject backend.Object, cellID, keyID string) (*storagev1.EnvelopeRecord, error) {
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
	digest, err := storageformat.EnvelopeRecordSHA256(record)
	if err != nil {
		return nil, err
	}
	record.EnvelopeSha256 = digest
	return record, nil
}

func localEnvelopeAAD(cellID, blockID string, blockObject backend.Object) []byte {
	return cryptoenv.EnvelopeAAD(cellID, blockID, blockObject)
}
