package peer

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
)

const transferChunkSize = 64 * 1024

func (s *Server) TransferBlock(req *scrapv1.TransferBlockRequest, stream grpc.ServerStreamingServer[scrapv1.TransferBlockResponse]) error {
	blockID := req.GetBlockId()
	blkPath := filepath.Join(s.blocksDir, fmt.Sprintf("%016x.blk", blockID))
	idxPath := filepath.Join(s.blocksDir, fmt.Sprintf("%016x.idx", blockID))

	blkInfo, err := os.Stat(blkPath)
	if err != nil {
		return status.Errorf(codes.NotFound, "block %d not found", blockID)
	}
	idxInfo, err := os.Stat(idxPath)
	if err != nil {
		return status.Errorf(codes.NotFound, "index for block %d not found", blockID)
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
