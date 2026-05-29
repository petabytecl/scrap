package peer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	raftpb "go.etcd.io/raft/v3/raftpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/scrub"
)

// sha256DigestLen is the byte length of a SHA-256 digest.
const sha256DigestLen = sha256.Size

type RebuildHandler interface {
	TriggerRebuild(ctx context.Context) (alreadyInProgress bool, err error)
}

type ReplicationSink interface {
	AppendReplicatedDocument(ctx context.Context, init *scrapv1.ReplicateDocumentInit, body io.Reader) ([]byte, error)
}

type ServerOption func(*Server)

func WithScrubCache(cache scrub.ResultCache) ServerOption {
	return func(s *Server) {
		s.scrubCache = cache
	}
}

func WithRebuildHandler(handler RebuildHandler) ServerOption {
	return func(s *Server) {
		s.rebuildHandler = handler
	}
}

func WithReplicationSink(sink ReplicationSink) ServerOption {
	return func(s *Server) {
		s.replicationSink = sink
	}
}

type Server struct {
	scrapv1.UnimplementedPeerServiceServer
	blocksDir       string
	scrubCache      scrub.ResultCache
	rebuildHandler  RebuildHandler
	replicationSink ReplicationSink
	raftRouter      atomic.Pointer[RaftRouter]
	mu              sync.Mutex
	writers         map[uint64]*blockState
}

func (s *Server) SetRaftRouter(router RaftRouter) {
	s.raftRouter.Store(&router)
}

type blockState struct {
	writer    *block.Writer
	idxWriter *block.IndexWriter
}

func NewServer(blocksDir string, opts ...ServerOption) *Server {
	s := &Server{
		blocksDir: blocksDir,
		writers:   make(map[uint64]*blockState),
	}
	for _, o := range opts {
		o(s)
	}
	return s
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

	bw, err := block.NewWriter(blkPath, shardID, blockID)
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
	if s.replicationSink != nil {
		return s.replicateToSink(stream, init)
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

func (s *Server) replicateToSink(stream grpc.ClientStreamingServer[scrapv1.ReplicateDocumentRequest, scrapv1.ReplicateDocumentResponse], init *scrapv1.ReplicateDocumentInit) error {
	var body bytes.Buffer
	for {
		msg, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return status.Errorf(codes.Internal, "receive chunk: %v", err)
		}
		chunk := msg.GetChunkData()
		if len(chunk) > 0 {
			if _, err := body.Write(chunk); err != nil {
				return status.Errorf(codes.Internal, "buffer chunk: %v", err)
			}
		}
	}

	sha, err := s.replicationSink.AppendReplicatedDocument(stream.Context(), init, bytes.NewReader(body.Bytes()))
	if err != nil {
		return status.Errorf(codes.Internal, "append replicated document: %v", err)
	}
	return stream.SendAndClose(&scrapv1.ReplicateDocumentResponse{
		Sha256: sha,
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

func (s *Server) ForwardRaft(ctx context.Context, req *scrapv1.ForwardRaftRequest) (*scrapv1.ForwardRaftResponse, error) {
	router := s.raftRouter.Load()
	if router == nil {
		return nil, status.Error(codes.FailedPrecondition, "raft router not configured")
	}
	var msg raftpb.Message
	if err := msg.Unmarshal(req.Message); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "unmarshal raft message: %v", err)
	}
	if err := (*router).RouteRaftMessage(ctx, req.ShardId, msg); err != nil {
		return nil, status.Errorf(codes.Internal, "route raft message: %v", err)
	}
	return &scrapv1.ForwardRaftResponse{}, nil
}

func (s *Server) ForwardRaftStream(stream grpc.BidiStreamingServer[scrapv1.ForwardRaftStreamRequest, scrapv1.ForwardRaftStreamResponse]) error {
	router := s.raftRouter.Load()
	if router == nil {
		return status.Error(codes.FailedPrecondition, "raft router not configured")
	}
	for {
		req, err := stream.Recv()
		if err != nil {
			return err
		}
		var msg raftpb.Message
		if err := msg.Unmarshal(req.Message); err != nil {
			continue
		}
		_ = (*router).RouteRaftMessage(stream.Context(), req.ShardId, msg)
	}
}

func (s *Server) RequestIndexRebuild(ctx context.Context, _ *scrapv1.RequestIndexRebuildRequest) (*scrapv1.RequestIndexRebuildResponse, error) {
	if s.rebuildHandler == nil {
		return nil, status.Error(codes.FailedPrecondition, "rebuild handler not configured")
	}
	alreadyInProgress, err := s.rebuildHandler.TriggerRebuild(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rebuild: %v", err)
	}
	return &scrapv1.RequestIndexRebuildResponse{
		AlreadyInProgress: alreadyInProgress,
	}, nil
}

func (s *Server) ConsistencyCheck(_ context.Context, req *scrapv1.ConsistencyCheckRequest) (*scrapv1.ConsistencyCheckResponse, error) {
	if s.scrubCache == nil {
		return nil, status.Error(codes.NotFound, "scrub cache not configured")
	}
	result, ok := s.scrubCache.GetScrubResult(req.ScrubId)
	if !ok {
		return nil, status.Errorf(codes.NotFound, "no result for scrub_id %q", req.ScrubId)
	}
	return &scrapv1.ConsistencyCheckResponse{
		ScrubId:      result.ScrubID,
		AppliedIndex: result.AppliedIndex,
		Sha256:       result.SHA256[:],
	}, nil
}

func (s *Server) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, bs := range s.writers {
		_ = bs.idxWriter.Close()
		_ = bs.writer.Close()
	}
}
