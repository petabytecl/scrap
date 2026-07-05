package peer

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
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

	blocksDir, ok := s.blocksDirForShard(req.GetShardId())
	if !ok {
		return status.Error(codes.FailedPrecondition, "local Shard not configured")
	}
	blockID := req.GetBlockId()
	blkPath := filepath.Join(blocksDir, fmt.Sprintf("%016x.blk", blockID))
	idxPath := filepath.Join(blocksDir, fmt.Sprintf("%016x.idx", blockID))

	blkInfo, err := os.Stat(blkPath)
	if err != nil {
		return s.transferBlockStatError(blocksDir, blockID, blkPath, idxPath, err)
	}
	idxInfo, err := os.Stat(idxPath)
	if err != nil {
		return s.transferIndexStatError(blocksDir, blockID, blkPath, idxPath, err)
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

	if err := streamFile(blkPath, blkInfo.Size(), stream); err != nil {
		return status.Errorf(codes.Internal, "stream block: %v", err)
	}

	if err := streamFile(idxPath, idxInfo.Size(), stream); err != nil {
		return status.Errorf(codes.Internal, "stream index: %v", err)
	}

	return nil
}

func (s *Server) blocksDirForShard(shardID uint64) (string, bool) {
	if s.blockDirResolver != nil {
		return s.blockDirResolver.BlockDirForShard(shardID)
	}
	return s.blocksDir, true
}

func (s *Server) transferBlockStatError(blocksDir string, blockID uint64, blkPath, idxPath string, statErr error) error {
	if !errors.Is(statErr, os.ErrNotExist) {
		return status.Errorf(codes.Internal, "stat block %d: %v", blockID, statErr)
	}
	idxExists, err := pathExists(idxPath)
	if err != nil {
		return status.Errorf(codes.Internal, "stat index for block %d: %v", blockID, err)
	}
	if hasQuarantine(blkPath, idxPath) {
		return transferReasonStatus(codes.DataLoss, transferReasonQuarantined, "block %d is quarantined", blockID)
	}
	marker, hasMarker, err := readTransferEvictionMarker(blocksDir, blockID)
	if err != nil {
		return transferReasonStatus(codes.DataLoss, transferReasonUnexpectedLoss, "block %d eviction marker invalid: %v", blockID, err)
	}
	switch {
	case hasMarker && idxExists:
		return transferReasonStatus(codes.FailedPrecondition, transferReasonEvicted, "block %d locally evicted at %d", blockID, marker.EvictedAtUs)
	case hasMarker:
		return transferReasonStatus(codes.DataLoss, transferReasonMetadataLoss, "block %d evicted but index missing", blockID)
	case idxExists:
		return transferReasonStatus(codes.DataLoss, transferReasonUnexpectedLoss, "block %d data missing", blockID)
	default:
		return status.Errorf(codes.NotFound, "block %d not found", blockID)
	}
}

func (s *Server) transferIndexStatError(blocksDir string, blockID uint64, blkPath, idxPath string, statErr error) error {
	if !errors.Is(statErr, os.ErrNotExist) {
		return status.Errorf(codes.Internal, "stat index for block %d: %v", blockID, statErr)
	}
	if hasQuarantine(blkPath, idxPath) {
		return transferReasonStatus(codes.DataLoss, transferReasonQuarantined, "block %d is quarantined", blockID)
	}
	if _, hasMarker, err := readTransferEvictionMarker(blocksDir, blockID); err != nil {
		return transferReasonStatus(codes.DataLoss, transferReasonUnexpectedLoss, "block %d eviction marker invalid: %v", blockID, err)
	} else if hasMarker {
		return transferReasonStatus(codes.DataLoss, transferReasonMetadataLoss, "block %d evicted but index missing", blockID)
	}
	return transferReasonStatus(codes.DataLoss, transferReasonMetadataLoss, "index for block %d missing", blockID)
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

// transferReasonStatus builds a transfer failure status carrying the machine-
// readable reason as an ErrorInfo detail. Classification must never depend on
// message text: substring-based error mapping is forbidden (CONTEXT.md).
func transferReasonStatus(code codes.Code, reason, format string, args ...any) error {
	st := status.Newf(code, reason+": "+format, args...)
	detailed, err := st.WithDetails(&errdetails.ErrorInfo{Reason: reason})
	if err != nil {
		return st.Err()
	}
	return detailed.Err()
}

func transferStatusHasReason(err error, reason string) bool {
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	for _, detail := range st.Details() {
		if info, ok := detail.(*errdetails.ErrorInfo); ok {
			return info.GetReason() == reason
		}
	}
	return false
}

// streamFile sends exactly size bytes of the file. The client splits the
// stream at the declared sizes, so bytes appended concurrently (e.g. the
// currently-open Block still being written) must fail the transfer instead of
// shifting the blk/idx split point.
func streamFile(path string, size int64, stream grpc.ServerStreamingServer[scrapv1.TransferBlockResponse]) error {
	cleanPath := filepath.Clean(path)
	f, err := os.Open(cleanPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	buf := make([]byte, transferChunkSize)
	var sent int64
	for sent < size {
		toRead := min(int64(transferChunkSize), size-sent)
		n, readErr := f.Read(buf[:toRead])
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
			sent += int64(n)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if sent != size {
		return fmt.Errorf("file shrank during transfer: sent %d of %d bytes", sent, size)
	}
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() != size {
		return fmt.Errorf("file size changed during transfer: now %d, declared %d", info.Size(), size)
	}
	return nil
}
