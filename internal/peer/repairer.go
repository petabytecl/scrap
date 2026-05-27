package peer

import (
	"context"
	"fmt"
	"os"

	"github.com/petabytecl/scrap/internal/block"
)

type ClientBlockRepairer struct {
	client    *Client
	blocksDir string
}

func NewClientBlockRepairer(client *Client, blocksDir string) *ClientBlockRepairer {
	return &ClientBlockRepairer{client: client, blocksDir: blocksDir}
}

func (r *ClientBlockRepairer) RepairFromPeer(ctx context.Context, blockID uint64, peerAddr string) error {
	blkData, idxData, err := r.client.TransferBlock(ctx, peerAddr, blockID)
	if err != nil {
		return fmt.Errorf("peer: fetch block %d: %w", blockID, err)
	}

	blkPath := block.BlockFilePath(r.blocksDir, blockID)
	idxPath := block.IdxFilePath(r.blocksDir, blockID)

	if err := atomicWrite(blkPath, blkData); err != nil {
		return fmt.Errorf("peer: write block %d: %w", blockID, err)
	}
	if err := atomicWrite(idxPath, idxData); err != nil {
		return fmt.Errorf("peer: write index %d: %w", blockID, err)
	}

	_ = os.Remove(blkPath + block.QuarantineSuffix)
	_ = os.Remove(idxPath + block.QuarantineSuffix)

	return nil
}

func atomicWrite(destPath string, data []byte) error {
	tmp := destPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, destPath)
}
