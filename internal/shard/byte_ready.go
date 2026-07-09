package shard

import (
	"errors"
	"fmt"
	"io"
	"os"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/block"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

type byteReadyKey struct {
	blockID uint64
	offset  uint64
}

func (s *Shard) recordByteReadyLocked(blockID, offset uint64) {
	if s.byteReady == nil {
		s.byteReady = make(map[byteReadyKey]struct{})
	}
	s.byteReady[byteReadyKey{blockID: blockID, offset: offset}] = struct{}{}
}

// ensureCommitDocumentBytesReadyLocked verifies local Frames exist for a
// committed Document before read serving or leadership write admission
// (ADR 0033 / H-04). It must not be called from Raft apply: apply must stay
// deterministic when bytes lag metadata (ADR 0001).
func (s *Shard) ensureCommitDocumentBytesReadyLocked(doc *scrapv1.CommitDocument) error {
	key := byteReadyKey{blockID: doc.GetBlockId(), offset: doc.GetFirstFrameOff()}
	if _, ok := s.byteReady[key]; ok {
		return nil
	}

	f, err := os.Open(s.blockPath(doc.GetBlockId()))
	if err != nil {
		return fmt.Errorf("%w: committed Document bytes unavailable: %w", storeapi.ErrDataLoss, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(safeUint64ToInt64(doc.GetFirstFrameOff()), io.SeekStart); err != nil {
		return fmt.Errorf("%w: seek committed Document bytes: %w", storeapi.ErrDataLoss, err)
	}

	for range doc.GetFrameCount() {
		_, _, err := block.ReadFrame(f)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return fmt.Errorf("%w: committed Document bytes missing at Block %d offset %d", storeapi.ErrDataLoss, doc.GetBlockId(), doc.GetFirstFrameOff())
			}
			return fmt.Errorf("%w: verify committed Document bytes: %w", storeapi.ErrDataLoss, err)
		}
	}
	s.recordByteReadyLocked(doc.GetBlockId(), doc.GetFirstFrameOff())
	return nil
}
