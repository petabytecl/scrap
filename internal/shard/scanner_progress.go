package shard

import (
	"context"
	"errors"
	"fmt"

	"github.com/petabytecl/scrap/internal/avscan"
	"github.com/petabytecl/scrap/internal/index"
)

type scannerProgressStore struct {
	idx *index.Index
}

func (s scannerProgressStore) LoadScannerProgress(ctx context.Context) (avscan.Progress, error) {
	if err := ctx.Err(); err != nil {
		return avscan.Progress{}, err
	}
	if s.idx == nil {
		return avscan.Progress{}, avscan.ErrProgressNotFound
	}
	watermark, err := s.idx.GetScannerWatermark()
	if err != nil {
		if errors.Is(err, index.ErrScannerWatermarkNotFound) {
			return avscan.Progress{}, avscan.ErrProgressNotFound
		}
		return avscan.Progress{}, fmt.Errorf("shard: load scanner progress: %w", err)
	}
	return avscan.Progress{
		LastScannedBlockID:          watermark.LastScannedBlockID,
		LastSignatureVersionScanned: watermark.LastSignatureVersionScanned,
	}, nil
}

func (s scannerProgressStore) SaveScannerProgress(ctx context.Context, progress avscan.Progress) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.idx == nil {
		return avscan.ErrProgressNotFound
	}
	err := s.idx.PutScannerWatermark(index.ScannerWatermark{
		LastScannedBlockID:          progress.LastScannedBlockID,
		LastSignatureVersionScanned: progress.LastSignatureVersionScanned,
	})
	if err != nil {
		return fmt.Errorf("shard: save scanner progress: %w", err)
	}
	return nil
}

var _ avscan.ProgressStore = scannerProgressStore{}
