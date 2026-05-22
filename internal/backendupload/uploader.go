package backendupload

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/blockstore"
	"github.com/petabytecl/scrap/internal/metastore"
)

type BlockSource interface {
	OpenBlock(context.Context, string) (io.ReadCloser, error)
}

type LocalBlockSource struct {
	Blocks *blockstore.Store
}

func (s LocalBlockSource) OpenBlock(ctx context.Context, blockID string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.Blocks == nil {
		return nil, fmt.Errorf("backendupload: block source is not configured")
	}
	sealed, err := s.Blocks.IsSealed(blockID)
	if err != nil {
		return nil, err
	}
	if !sealed {
		return nil, blockstore.ErrBlockOpen
	}
	file, err := os.Open(s.Blocks.BlockPath(blockID))
	if err != nil {
		return nil, err
	}
	return file, nil
}

type Uploader struct {
	Backend  backend.Store
	Source   BlockSource
	Index    BlockIndexSource
	Envelope BlockEnvelopeSource
}

type UploadResult struct {
	Block    backend.Object
	Index    *backend.Object
	Envelope *backend.Object
}

func (u Uploader) UploadBlock(ctx context.Context, intent metastore.UploadIntent) (UploadResult, error) {
	if u.Backend == nil {
		return UploadResult{}, fmt.Errorf("backendupload: backend store is not configured")
	}
	if u.Source == nil {
		return UploadResult{}, fmt.Errorf("backendupload: block source is not configured")
	}
	if intent.BlockID == "" {
		return UploadResult{}, fmt.Errorf("backendupload: upload intent block id is required")
	}
	if intent.BackendObjectKey == "" {
		return UploadResult{}, fmt.Errorf("backendupload: upload intent backend object key is required")
	}
	if intent.EnvelopeObjectKey != "" && u.Envelope == nil {
		return UploadResult{}, fmt.Errorf("backendupload: block envelope source is not configured")
	}
	reader, err := u.Source.OpenBlock(ctx, intent.BlockID)
	if err != nil {
		return UploadResult{}, err
	}
	defer reader.Close()
	blockObject, err := u.Backend.PutObject(ctx, intent.BackendObjectKey, reader)
	if err != nil {
		return UploadResult{}, err
	}
	result := UploadResult{Block: blockObject}
	if intent.EnvelopeObjectKey != "" {
		envelopeReader, err := u.Envelope.OpenBlockEnvelope(ctx, intent, blockObject)
		if err != nil {
			return result, err
		}
		defer envelopeReader.Close()
		envelopeObject, err := u.Backend.PutObject(ctx, intent.EnvelopeObjectKey, envelopeReader)
		if err != nil {
			return result, err
		}
		result.Envelope = &envelopeObject
	}
	if intent.IndexObjectKey == "" {
		return result, nil
	}
	if u.Index == nil {
		return result, fmt.Errorf("backendupload: block index source is not configured")
	}
	indexReader, err := u.Index.OpenBlockIndex(ctx, intent, blockObject)
	if err != nil {
		return result, err
	}
	defer indexReader.Close()
	indexObject, err := u.Backend.PutObject(ctx, intent.IndexObjectKey, indexReader)
	if err != nil {
		return result, err
	}
	result.Index = &indexObject
	return result, nil
}
