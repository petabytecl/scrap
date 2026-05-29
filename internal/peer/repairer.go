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

	blkPath := block.FilePath(r.blocksDir, blockID)
	idxPath := block.IdxFilePath(r.blocksDir, blockID)

	if err := atomicWrite(blkPath, blkData); err != nil {
		return fmt.Errorf("peer: write block %d: %w", blockID, err)
	}
	if err := atomicWrite(idxPath, idxData); err != nil {
		_ = os.Remove(blkPath)
		return fmt.Errorf("peer: write index %d: %w", blockID, err)
	}

	result, verifyErr := block.VerifyBlock(blkPath, idxPath)
	if verifyErr != nil {
		_ = os.Remove(blkPath)
		_ = os.Remove(idxPath)
		return fmt.Errorf("peer: verify repaired block %d: %w", blockID, verifyErr)
	}
	if len(result.CorruptFrames) > 0 {
		_ = os.Remove(blkPath)
		_ = os.Remove(idxPath)
		return fmt.Errorf("peer: repaired block %d has %d corrupt frames", blockID, len(result.CorruptFrames))
	}

	if err := os.Remove(blkPath + block.QuarantineSuffix); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("peer: remove quarantine blk %d: %w", blockID, err)
	}
	if err := os.Remove(idxPath + block.QuarantineSuffix); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("peer: remove quarantine idx %d: %w", blockID, err)
	}

	return nil
}

func atomicWrite(destPath string, data []byte) error {
	tmp := destPath + ".tmp"

	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // path constructed from controlled block IDs
	if err != nil {
		return err
	}

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}

	if err := os.Rename(tmp, destPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
