package shard

import (
	"context"
	"errors"
	"fmt"

	"github.com/petabytecl/scrap/internal/avscan"
	"github.com/petabytecl/scrap/internal/index"
)

type scannerProgressIndexSource interface {
	withScannerProjection(func(*index.Index) error) error
}

type scannerProgressStore struct {
	source scannerProgressIndexSource
}

func (s scannerProgressStore) LoadScannerProgress(ctx context.Context) (avscan.Progress, error) {
	if err := ctx.Err(); err != nil {
		return avscan.Progress{}, err
	}
	if s.source == nil {
		return avscan.Progress{}, avscan.ErrProgressNotFound
	}
	var progress avscan.Progress
	err := s.source.withScannerProjection(func(idx *index.Index) error {
		if idx == nil {
			return avscan.ErrProgressNotFound
		}
		watermark, err := idx.GetScannerWatermark()
		if err != nil {
			if errors.Is(err, index.ErrScannerWatermarkNotFound) {
				return avscan.ErrProgressNotFound
			}
			return fmt.Errorf("shard: load scanner progress: %w", err)
		}
		progress = avscan.Progress{
			LastScannedBlockID:          watermark.LastScannedBlockID,
			LastSignatureVersionScanned: watermark.LastSignatureVersionScanned,
		}
		return nil
	})
	if err != nil {
		return avscan.Progress{}, err
	}
	return progress, nil
}

func (s scannerProgressStore) SaveScannerProgress(ctx context.Context, progress avscan.Progress) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.source == nil {
		return avscan.ErrProgressNotFound
	}
	return s.source.withScannerProjection(func(idx *index.Index) error {
		if idx == nil {
			return avscan.ErrProgressNotFound
		}
		err := idx.PutScannerWatermark(index.ScannerWatermark{
			LastScannedBlockID:          progress.LastScannedBlockID,
			LastSignatureVersionScanned: progress.LastSignatureVersionScanned,
		})
		if err != nil {
			return fmt.Errorf("shard: save scanner progress: %w", err)
		}
		return nil
	})
}

func (s *Shard) withScannerProjection(use func(*index.Index) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return use(s.idx)
}

var _ avscan.ProgressStore = scannerProgressStore{}
