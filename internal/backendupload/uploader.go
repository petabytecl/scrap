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
	Backend backend.Store
	Source  BlockSource
}

func (u Uploader) UploadBlock(ctx context.Context, intent metastore.UploadIntent) (backend.Object, error) {
	if u.Backend == nil {
		return backend.Object{}, fmt.Errorf("backendupload: backend store is not configured")
	}
	if u.Source == nil {
		return backend.Object{}, fmt.Errorf("backendupload: block source is not configured")
	}
	if intent.BlockID == "" {
		return backend.Object{}, fmt.Errorf("backendupload: upload intent block id is required")
	}
	if intent.BackendObjectKey == "" {
		return backend.Object{}, fmt.Errorf("backendupload: upload intent backend object key is required")
	}
	reader, err := u.Source.OpenBlock(ctx, intent.BlockID)
	if err != nil {
		return backend.Object{}, err
	}
	defer reader.Close()
	return u.Backend.PutObject(ctx, intent.BackendObjectKey, reader)
}
