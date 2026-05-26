package peer

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/block"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Server struct {
	scrapv1.UnimplementedPeerServiceServer
	blocksDir string
	mu        sync.Mutex
	writers   map[uint64]*blockState
}

type blockState struct {
	writer    *block.BlockWriter
	idxWriter *block.IndexWriter
}

func NewServer(blocksDir string) *Server {
	return &Server{
		blocksDir: blocksDir,
		writers:   make(map[uint64]*blockState),
	}
}

func RegisterServer(gs *grpc.Server, s *Server) {
	scrapv1.RegisterPeerServiceServer(gs, s)
}

func (s *Server) getOrCreateBlock(blockID uint64, shardID uint64) (*blockState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if bs, ok := s.writers[blockID]; ok {
		return bs, nil
	}

	blkPath := filepath.Join(s.blocksDir, fmt.Sprintf("%016x.blk", blockID))
	idxPath := filepath.Join(s.blocksDir, fmt.Sprintf("%016x.idx", blockID))

	if _, err := os.Stat(blkPath); err == nil {
		return nil, fmt.Errorf("block %d already exists", blockID)
	}

	bw, err := block.NewBlockWriter(blkPath, shardID, blockID)
	if err != nil {
		return nil, err
	}

	iw, err := block.NewIndexWriter(idxPath)
	if err != nil {
		bw.Close()
		return nil, err
	}

	bs := &blockState{writer: bw, idxWriter: iw}
	s.writers[blockID] = bs
	return bs, nil
}

func (s *Server) ReplicateDocument(stream grpc.ClientStreamingServer[scrapv1.ReplicateDocumentRequest, scrapv1.ReplicateDocumentResponse]) error {
	first, err := stream.Recv()
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "receive init: %v", err)
	}

	init := first.GetInit()
	if init == nil {
		return status.Error(codes.InvalidArgument, "first message must be init")
	}

	pr, pw := io.Pipe()
	hasher := sha256.New()

	var appendResult block.AppendResult
	var appendErr error
	done := make(chan struct{})

	go func() {
		defer close(done)
		defer pr.Close()

		bs, err := s.getOrCreateBlock(init.BlockId, 0)
		if err != nil {
			appendErr = err
			return
		}

		tee := io.TeeReader(pr, hasher)
		appendResult, appendErr = bs.writer.AppendDocument(
			init.TransactionId, init.DocumentName, init.ContentType, tee,
		)
	}()

	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			pw.CloseWithError(err)
			<-done
			return status.Errorf(codes.Internal, "receive chunk: %v", err)
		}
		chunk := msg.GetChunkData()
		if len(chunk) > 0 {
			if _, err := pw.Write(chunk); err != nil {
				pw.Close()
				<-done
				return status.Errorf(codes.Internal, "write chunk: %v", err)
			}
		}
	}

	pw.Close()
	<-done

	if appendErr != nil {
		return status.Errorf(codes.Internal, "append: %v", appendErr)
	}

	computedSHA := hasher.Sum(nil)
	if init.Sha256 != nil && len(init.Sha256) == 32 {
		if string(computedSHA) != string(init.Sha256) {
			return status.Errorf(codes.DataLoss, "SHA-256 mismatch: expected %x, got %x", init.Sha256, computedSHA)
		}
	}

	_ = appendResult

	return stream.SendAndClose(&scrapv1.ReplicateDocumentResponse{
		Sha256: computedSHA,
	})
}

func (s *Server) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, bs := range s.writers {
		bs.idxWriter.Close()
		bs.writer.Close()
	}
}
