package peer

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/block"
)

// sha256DigestLen is the byte length of a SHA-256 digest.
const sha256DigestLen = sha256.Size

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

func (s *Server) getOrCreateBlock(blockID, shardID uint64) (*blockState, error) {
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
		_ = bw.Close()
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
		defer func() { _ = pr.Close() }()

		bs, bsErr := s.getOrCreateBlock(init.BlockId, 0)
		if bsErr != nil {
			appendErr = bsErr
			return
		}

		tee := io.TeeReader(pr, hasher)
		appendResult, appendErr = bs.writer.AppendDocument(
			init.TransactionId, init.DocumentName, init.ContentType, tee,
		)
	}()

	if recvErr := s.recvChunks(stream, pw, done); recvErr != nil {
		return recvErr
	}

	_ = pw.Close()
	<-done

	if appendErr != nil {
		return status.Errorf(codes.Internal, "append: %v", appendErr)
	}

	computedSHA := hasher.Sum(nil)
	if len(init.Sha256) == sha256DigestLen {
		if string(computedSHA) != string(init.Sha256) {
			return status.Errorf(codes.DataLoss, "SHA-256 mismatch: expected %x, got %x", init.Sha256, computedSHA)
		}
	}

	_ = appendResult

	return stream.SendAndClose(&scrapv1.ReplicateDocumentResponse{
		Sha256: computedSHA,
	})
}

// recvChunks reads chunk messages from the stream and writes them into pw.
// It closes pw (with error if needed) and waits on done before returning on failure.
func (s *Server) recvChunks(stream grpc.ClientStreamingServer[scrapv1.ReplicateDocumentRequest, scrapv1.ReplicateDocumentResponse], pw *io.PipeWriter, done <-chan struct{}) error {
	for {
		msg, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			pw.CloseWithError(err)
			<-done
			return status.Errorf(codes.Internal, "receive chunk: %v", err)
		}
		chunk := msg.GetChunkData()
		if len(chunk) > 0 {
			if _, err := pw.Write(chunk); err != nil {
				_ = pw.Close()
				<-done
				return status.Errorf(codes.Internal, "write chunk: %v", err)
			}
		}
	}
}

func (s *Server) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, bs := range s.writers {
		_ = bs.idxWriter.Close()
		_ = bs.writer.Close()
	}
}
