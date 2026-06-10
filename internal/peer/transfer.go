package peer

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/audit"
	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/localblock"
)

const transferChunkSize = 64 * 1024

const (
	transferReasonEvicted        = "block_evicted"
	transferReasonMetadataLoss   = "block_metadata_loss"
	transferReasonUnexpectedLoss = "block_unexpected_loss"
	transferReasonQuarantined    = "block_quarantined"
)

func (s *Server) TransferBlock(req *scrapv1.TransferBlockRequest, stream grpc.ServerStreamingServer[scrapv1.TransferBlockResponse]) error {
	if err := s.authorizePeerForShard(stream.Context(), audit.OperationTransferBlock, audit.TargetBlock, req.GetShardId()); err != nil {
		return err
	}

	blockID := req.GetBlockId()
	blkPath := filepath.Join(s.blocksDir, fmt.Sprintf("%016x.blk", blockID))
	idxPath := filepath.Join(s.blocksDir, fmt.Sprintf("%016x.idx", blockID))

	blkInfo, err := os.Stat(blkPath)
	if err != nil {
		return s.transferBlockStatError(blockID, blkPath, idxPath, err)
	}
	idxInfo, err := os.Stat(idxPath)
	if err != nil {
		return s.transferIndexStatError(blockID, blkPath, idxPath, err)
	}
	if err := block.VerifyHeader(blkPath, req.GetShardId(), blockID); err != nil {
		return status.Errorf(codes.DataLoss, "block header verification failed: %v", err)
	}

	if err := stream.Send(&scrapv1.TransferBlockResponse{
		Part: &scrapv1.TransferBlockResponse_Meta{
			Meta: &scrapv1.TransferBlockMeta{
				BlockId:   blockID,
				BlockSize: blkInfo.Size(),
				IdxSize:   idxInfo.Size(),
			},
		},
	}); err != nil {
		return err
	}

	if err := streamFile(blkPath, stream); err != nil {
		return status.Errorf(codes.Internal, "stream block: %v", err)
	}

	if err := streamFile(idxPath, stream); err != nil {
		return status.Errorf(codes.Internal, "stream index: %v", err)
	}

	return nil
}

func (s *Server) transferBlockStatError(blockID uint64, blkPath, idxPath string, statErr error) error {
	if !errors.Is(statErr, os.ErrNotExist) {
		return status.Errorf(codes.Internal, "stat block %d: %v", blockID, statErr)
	}
	idxExists, err := pathExists(idxPath)
	if err != nil {
		return status.Errorf(codes.Internal, "stat index for block %d: %v", blockID, err)
	}
	if hasQuarantine(blkPath, idxPath) {
		return status.Errorf(codes.DataLoss, "%s: block %d is quarantined", transferReasonQuarantined, blockID)
	}
	marker, hasMarker, err := readTransferEvictionMarker(s.blocksDir, blockID)
	if err != nil {
		return status.Errorf(codes.DataLoss, "%s: block %d eviction marker invalid: %v", transferReasonUnexpectedLoss, blockID, err)
	}
	switch {
	case hasMarker && idxExists:
		return status.Errorf(codes.FailedPrecondition, "%s: block %d locally evicted at %d", transferReasonEvicted, blockID, marker.EvictedAtUs)
	case hasMarker:
		return status.Errorf(codes.DataLoss, "%s: block %d evicted but index missing", transferReasonMetadataLoss, blockID)
	case idxExists:
		return status.Errorf(codes.DataLoss, "%s: block %d data missing", transferReasonUnexpectedLoss, blockID)
	default:
		return status.Errorf(codes.NotFound, "block %d not found", blockID)
	}
}

func (s *Server) transferIndexStatError(blockID uint64, blkPath, idxPath string, statErr error) error {
	if !errors.Is(statErr, os.ErrNotExist) {
		return status.Errorf(codes.Internal, "stat index for block %d: %v", blockID, statErr)
	}
	if hasQuarantine(blkPath, idxPath) {
		return status.Errorf(codes.DataLoss, "%s: block %d is quarantined", transferReasonQuarantined, blockID)
	}
	if _, hasMarker, err := readTransferEvictionMarker(s.blocksDir, blockID); err != nil {
		return status.Errorf(codes.DataLoss, "%s: block %d eviction marker invalid: %v", transferReasonUnexpectedLoss, blockID, err)
	} else if hasMarker {
		return status.Errorf(codes.DataLoss, "%s: block %d evicted but index missing", transferReasonMetadataLoss, blockID)
	}
	return status.Errorf(codes.DataLoss, "%s: index for block %d missing", transferReasonMetadataLoss, blockID)
}

func readTransferEvictionMarker(blocksDir string, blockID uint64) (localblock.EvictionMarker, bool, error) {
	marker, err := localblock.ReadEvictionMarker(blocksDir, blockID)
	if err == nil {
		return marker, true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return localblock.EvictionMarker{}, false, nil
	}
	return localblock.EvictionMarker{}, false, err
}

func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func hasQuarantine(blkPath, idxPath string) bool {
	blkQuarantine, _ := pathExists(blkPath + block.QuarantineSuffix)
	idxQuarantine, _ := pathExists(idxPath + block.QuarantineSuffix)
	return blkQuarantine || idxQuarantine
}

func transferStatusHasReason(err error, reason string) bool {
	st, ok := status.FromError(err)
	return ok && strings.Contains(st.Message(), reason)
}

func streamFile(path string, stream grpc.ServerStreamingServer[scrapv1.TransferBlockResponse]) error {
	cleanPath := filepath.Clean(path)
	f, err := os.Open(cleanPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	buf := make([]byte, transferChunkSize)
	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			if err := stream.Send(&scrapv1.TransferBlockResponse{
				Part: &scrapv1.TransferBlockResponse_ChunkData{
					ChunkData: chunk,
				},
			}); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	return nil
}
