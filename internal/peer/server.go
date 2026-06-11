package peer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"go.etcd.io/raft/v3/raftpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/audit"
	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/scrub"
	"github.com/petabytecl/scrap/internal/security"
)

// sha256DigestLen is the byte length of an SHA-256 digest.
const sha256DigestLen = sha256.Size

type RebuildHandler interface {
	TriggerRebuild(ctx context.Context) (alreadyInProgress bool, err error)
}

type ReplicationSink interface {
	AppendReplicatedDocument(ctx context.Context, init *scrapv1.ReplicateDocumentInit, body io.Reader) ([]byte, error)
}

type BlockDirResolver interface {
	BlockDirForShard(shardID uint64) (string, bool)
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

func WithBlockDirResolver(resolver BlockDirResolver) ServerOption {
	return func(s *Server) {
		s.blockDirResolver = resolver
	}
}

func WithAuthorizer(authorizer *security.Authorizer, expected security.PeerIdentityConfig) ServerOption {
	return func(s *Server) {
		s.authorizer = authorizer
		s.expectedPeerIdentity = expected
	}
}

func WithAuthorizedShards(shardIDs ...uint64) ServerOption {
	authorizedShardIDs := make(map[uint64]struct{}, len(shardIDs))
	for _, shardID := range shardIDs {
		authorizedShardIDs[shardID] = struct{}{}
	}
	return func(s *Server) {
		s.authorizedShardIDs = cloneShardIDSet(authorizedShardIDs)
	}
}

func WithAuditSink(sink audit.Sink) ServerOption {
	return func(s *Server) {
		s.auditSink = sink
	}
}

func WithAuthorizationObserver(observer security.AuthorizationObserver) ServerOption {
	return func(s *Server) {
		s.authorizationObserver = observer
	}
}

func WithRateLimiter(limiter *security.RateLimiter) ServerOption {
	return func(s *Server) {
		s.rateLimiter = limiter
	}
}

func WithLogger(logger *slog.Logger) ServerOption {
	return func(s *Server) {
		s.logger = logger
	}
}

type Server struct {
	scrapv1.UnimplementedPeerServiceServer
	blocksDir             string
	blockDirResolver      BlockDirResolver
	scrubCache            scrub.ResultCache
	rebuildHandler        RebuildHandler
	replicationSink       ReplicationSink
	authorizer            *security.Authorizer
	expectedPeerIdentity  security.PeerIdentityConfig
	authorizedShardIDs    map[uint64]struct{}
	authorizationObserver security.AuthorizationObserver
	auditSink             audit.Sink
	rateLimiter           *security.RateLimiter
	raftRouter            atomic.Pointer[RaftRouter]
	logger                *slog.Logger
	malformedRaftMsgs     atomic.Uint64
	mu                    sync.Mutex
	writers               map[uint64]*blockState
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
		logger:    slog.Default(),
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

func cloneShardIDSet(shardIDs map[uint64]struct{}) map[uint64]struct{} {
	cloned := make(map[uint64]struct{}, len(shardIDs))
	for shardID := range shardIDs {
		cloned[shardID] = struct{}{}
	}
	return cloned
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
	if err := s.checkPeerAuthorization(stream.Context(), audit.OperationReplicateDocument, audit.TargetDocument); err != nil {
		return err
	}

	init, err := s.receiveReplicateDocumentInit(stream)
	if err != nil {
		return err
	}
	if err := s.authorizePeerShardAfterPrecheck(stream.Context(), audit.OperationReplicateDocument, audit.TargetDocument, init.GetShardId()); err != nil {
		return err
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

		bs, bsErr := s.getOrCreateBlock(init.BlockId, init.GetShardId())
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

func (s *Server) receiveReplicateDocumentInit(stream grpc.ClientStreamingServer[scrapv1.ReplicateDocumentRequest, scrapv1.ReplicateDocumentResponse]) (*scrapv1.ReplicateDocumentInit, error) {
	first, err := stream.Recv()
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "receive init: %v", err)
	}
	init := first.GetInit()
	if init == nil {
		return nil, status.Error(codes.InvalidArgument, "first message must be init")
	}
	return init, nil
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
// It closes pw (with an error if needed) and waits on done before returning on failure.
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
	if err := s.authorizePeerForShard(ctx, audit.OperationForwardRaft, audit.TargetPeer, req.GetShardId()); err != nil {
		return nil, err
	}
	router := s.raftRouter.Load()
	if router == nil {
		return nil, status.Error(codes.FailedPrecondition, "raft router not configured")
	}
	var msg raftpb.Message
	if err := msg.Unmarshal(req.Message); err != nil {
		s.recordMalformedRaftMessage(ctx, audit.OperationForwardRaft, req.ShardId, err)
		return nil, status.Errorf(codes.InvalidArgument, "unmarshal raft message: %v", err)
	}
	if err := (*router).RouteRaftMessage(ctx, req.ShardId, msg); err != nil {
		return nil, status.Errorf(codes.Internal, "route raft message: %v", err)
	}
	return &scrapv1.ForwardRaftResponse{}, nil
}

func (s *Server) ForwardRaftStream(stream grpc.BidiStreamingServer[scrapv1.ForwardRaftStreamRequest, scrapv1.ForwardRaftStreamResponse]) error {
	if err := s.checkPeerAuthorization(stream.Context(), audit.OperationForwardRaftStream, audit.TargetPeer); err != nil {
		return err
	}
	router := s.raftRouter.Load()
	if router == nil {
		return status.Error(codes.FailedPrecondition, "raft router not configured")
	}
	allowedAuditRecorded := false
	for {
		req, err := stream.Recv()
		if err != nil {
			return err
		}
		recordedAllowed, err := s.handleForwardRaftStreamRequest(stream.Context(), router, req, !allowedAuditRecorded)
		if err != nil {
			return err
		}
		allowedAuditRecorded = allowedAuditRecorded || recordedAllowed
	}
}

func (s *Server) handleForwardRaftStreamRequest(ctx context.Context, router *RaftRouter, req *scrapv1.ForwardRaftStreamRequest, recordAllowed bool) (bool, error) {
	if err := s.authorizePeerShardScope(ctx, audit.OperationForwardRaftStream, audit.TargetPeer, req.GetShardId()); err != nil {
		return false, err
	}
	if recordAllowed {
		if err := s.recordAllowedAudit(ctx, audit.OperationForwardRaftStream, audit.TargetPeer); err != nil {
			return false, err
		}
	}
	var msg raftpb.Message
	if err := msg.Unmarshal(req.Message); err != nil {
		s.recordMalformedRaftMessage(ctx, audit.OperationForwardRaftStream, req.ShardId, err)
		return recordAllowed, nil
	}
	if err := (*router).RouteRaftMessage(ctx, req.ShardId, msg); err != nil {
		s.recordRaftRouteError(ctx, audit.OperationForwardRaftStream, req.ShardId, err)
	}
	return recordAllowed, nil
}

func (s *Server) recordMalformedRaftMessage(ctx context.Context, operation string, shardID uint64, err error) {
	count := s.malformedRaftMsgs.Add(1)
	if s.logger == nil || !shouldLogMalformedRaftCount(count) {
		return
	}
	s.logger.WarnContext(ctx, "peer received malformed raft message",
		"audit.surface", audit.SurfacePeer,
		"audit.operation", operation,
		"scrap.shard_id", shardID,
		"malformed_raft_messages", count,
		"err", err,
	)
}

func (s *Server) recordRaftRouteError(ctx context.Context, operation string, shardID uint64, err error) {
	if s.logger == nil {
		return
	}
	s.logger.WarnContext(ctx, "peer failed to route raft message",
		"audit.surface", audit.SurfacePeer,
		"audit.operation", operation,
		"scrap.shard_id", shardID,
		"err", err,
	)
}

func shouldLogMalformedRaftCount(count uint64) bool {
	return count == 1 || count&(count-1) == 0
}

func (s *Server) RequestIndexRebuild(ctx context.Context, _ *scrapv1.RequestIndexRebuildRequest) (*scrapv1.RequestIndexRebuildResponse, error) {
	if err := s.authorizePeer(ctx, audit.OperationRequestIndexRebuild, audit.TargetPeer); err != nil {
		return nil, err
	}
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

func (s *Server) ConsistencyCheck(ctx context.Context, req *scrapv1.ConsistencyCheckRequest) (*scrapv1.ConsistencyCheckResponse, error) {
	if err := s.authorizePeer(ctx, audit.OperationConsistencyCheck, audit.TargetPeer); err != nil {
		return nil, err
	}
	if s.scrubCache == nil {
		return nil, status.Error(codes.FailedPrecondition, "scrub cache not configured")
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

func (s *Server) authorizePeer(ctx context.Context, operation, target string) error {
	return s.authorizePeerWithChecks(ctx, operation, target)
}

func (s *Server) authorizePeerForShard(ctx context.Context, operation, target string, shardID uint64) error {
	return s.authorizePeerWithChecks(ctx, operation, target, func() error {
		return s.authorizeShard(shardID)
	})
}

func (s *Server) authorizePeerWithChecks(ctx context.Context, operation, target string, checks ...func() error) error {
	if err := s.checkPeerAuthorization(ctx, operation, target); err != nil {
		return err
	}
	for _, check := range checks {
		if err := check(); err != nil {
			reason := s.auditReasonForError(err)
			if auditErr := s.recordAudit(ctx, operation, target, audit.ResultDenied, reason); auditErr != nil {
				return status.Error(codes.Internal, "audit event failed")
			}
			s.recordAuthorizationDenied(ctx, operation, reason, err)
			return err
		}
	}
	return s.recordAllowedAudit(ctx, operation, target)
}

func (s *Server) checkPeerAuthorization(ctx context.Context, operation, target string) error {
	if decision := s.checkRateLimit(ctx, operation); decision.Limited {
		if err := s.recordAudit(ctx, operation, target, audit.ResultRateLimited, audit.ReasonRateLimited); err != nil {
			return status.Error(codes.Internal, "audit event failed")
		}
		return security.RateLimitedError()
	}
	if err := s.authorizePeerIdentity(ctx); err != nil {
		reason := s.auditReasonForError(err)
		if auditErr := s.recordAudit(ctx, operation, target, audit.ResultDenied, reason); auditErr != nil {
			return status.Error(codes.Internal, "audit event failed")
		}
		s.recordAuthorizationDenied(ctx, operation, reason, err)
		return err
	}
	return nil
}

func (s *Server) authorizePeerIdentity(ctx context.Context) error {
	if s.authorizer == nil {
		return nil
	}
	if err := s.authorizer.Authorize(ctx, security.RolePeerMember); err != nil {
		return err
	}
	identity, ok := security.PeerIdentityFromContext(ctx)
	if !ok {
		s.authorizer.RecordAuthorizationStatus(security.AuthorizationStatusDenied)
		return security.UnauthenticatedError("verified peer identity is required")
	}
	if identity.CellID != s.expectedPeerIdentity.CellID {
		s.authorizer.RecordAuthorizationStatus(security.AuthorizationStatusMismatch)
		return security.PermissionDeniedErrorWithStatus("peer identity mismatch", security.AuthorizationStatusMismatch)
	}
	principal, ok := security.PrincipalFromContext(ctx)
	if !ok {
		s.authorizer.RecordAuthorizationStatus(security.AuthorizationStatusDenied)
		return security.UnauthenticatedError("verified peer identity is required")
	}
	if principal.ID != security.PeerIdentityPrincipalID(identity) {
		s.authorizer.RecordAuthorizationStatus(security.AuthorizationStatusMismatch)
		return security.PermissionDeniedErrorWithStatus("peer identity mismatch", security.AuthorizationStatusMismatch)
	}
	return nil
}

func (s *Server) authorizePeerShardScope(ctx context.Context, operation, target string, shardID uint64) error {
	if err := s.authorizeShard(shardID); err != nil {
		reason := s.auditReasonForError(err)
		if auditErr := s.recordAudit(ctx, operation, target, audit.ResultDenied, reason); auditErr != nil {
			return status.Error(codes.Internal, "audit event failed")
		}
		s.recordAuthorizationDenied(ctx, operation, reason, err)
		return err
	}
	return nil
}

func (s *Server) authorizePeerShardAfterPrecheck(ctx context.Context, operation, target string, shardID uint64) error {
	if err := s.authorizePeerShardScope(ctx, operation, target, shardID); err != nil {
		return err
	}
	return s.recordAllowedAudit(ctx, operation, target)
}

func (s *Server) authorizeShard(shardID uint64) error {
	if s.authorizedShardIDs == nil {
		return nil
	}
	if _, ok := s.authorizedShardIDs[shardID]; ok {
		return nil
	}
	if s.authorizer != nil {
		s.authorizer.RecordAuthorizationStatus(security.AuthorizationStatusDenied)
	}
	return security.PermissionDeniedErrorWithStatus("peer Shard not authorized", security.AuthorizationStatusDenied)
}

func (s *Server) recordAllowedAudit(ctx context.Context, operation, target string) error {
	if err := s.recordAudit(ctx, operation, target, audit.ResultAllowed, audit.ReasonAllowed); err != nil {
		return status.Error(codes.Internal, "audit event failed")
	}
	return nil
}

func (s *Server) checkRateLimit(ctx context.Context, operation string) security.RateLimitDecision {
	if s.rateLimiter == nil {
		return security.RateLimitDecision{}
	}
	return s.rateLimiter.Allow(ctx, security.RateLimitSurfacePeer, peerPrincipalID(ctx), operation)
}

func (s *Server) recordAuthorizationDenied(ctx context.Context, operation, reason string, err error) {
	if s.authorizationObserver == nil {
		return
	}
	s.authorizationObserver.AuthorizationDenied(ctx, security.AuthorizationDecision{
		Surface:   security.RateLimitSurfacePeer,
		Operation: operation,
		Reason:    reason,
		Status:    security.AuthorizationStatusForError(err),
	})
}

func (s *Server) recordAudit(ctx context.Context, operation, target, result, reason string) error {
	if s.auditSink == nil {
		return nil
	}
	event, err := audit.NewEvent(audit.EventInput{
		PrincipalID: peerPrincipalID(ctx),
		Role:        string(security.RolePeerMember),
		Surface:     audit.SurfacePeer,
		Operation:   operation,
		Target:      target,
		Result:      result,
		Reason:      reason,
	})
	if err != nil {
		return err
	}
	return s.auditSink.Record(ctx, event)
}

func (s *Server) auditReasonForError(err error) string {
	switch {
	case errors.Is(err, security.ErrUnauthenticated):
		return audit.ReasonUnauthenticated
	case errors.Is(err, security.ErrPermissionDenied):
		switch security.AuthorizationStatusForError(err) {
		case security.AuthorizationStatusMissingRole:
			return audit.ReasonMissingRole
		case security.AuthorizationStatusMismatch:
			return audit.ReasonMismatch
		}
		return audit.ReasonPermissionDenied
	default:
		return audit.ReasonInternalError
	}
}

func peerPrincipalID(ctx context.Context) string {
	principal, ok := security.PrincipalFromContext(ctx)
	if !ok {
		return ""
	}
	return principal.ID
}

// Close flushes and closes every block and index writer the server owns. It is
// safe to call more than once; the following calls are no-ops. When closing
// multiple writers, it attempts to close all of them and returns the first error
// encountered. Callers must ensure the peer gRPC server has stopped accepting
// RPCs (e.g. after GracefulStop) before calling Close, so no new writer is
// created concurrently with the close.
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var firstErr error
	for id, bs := range s.writers {
		if err := bs.idxWriter.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		if err := bs.writer.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(s.writers, id)
	}
	return firstErr
}
