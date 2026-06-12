package server

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/audit"
	"github.com/petabytecl/scrap/internal/security"
	storeapi "github.com/petabytecl/scrap/internal/store"
	"github.com/petabytecl/scrap/internal/telemetry"
)

const readBufSize = 64 * 1024 // 64 KiB read buffer for streaming document chunks

type documentServer struct {
	scrapv1.UnimplementedDocumentServiceServer
	store          storeapi.Store
	telemetry      Telemetry
	identifierMode telemetry.IdentifierMode
	logger         *slog.Logger
	authorizer     *security.Authorizer
	auditSink      audit.Sink
	rateLimiter    *security.RateLimiter
}

func Register(gs *grpc.Server, s storeapi.Store, opts ...Option) {
	srv := &documentServer{
		store:     s,
		telemetry: noopTelemetry{},
		logger:    slog.New(slog.DiscardHandler),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(srv)
		}
	}
	scrapv1.RegisterDocumentServiceServer(gs, srv)
}

func (s *documentServer) WriteDocument(stream grpc.ClientStreamingServer[scrapv1.WriteDocumentRequest, scrapv1.WriteDocumentResponse]) error {
	ctx, rpc := s.telemetry.StartRPC(stream.Context(), "WriteDocument")
	rpcCode := codes.OK
	defer func() { rpc.Finish(rpcCode) }()

	if err := s.requireSecurity(ctx, security.RoleDocumentWriter, audit.OperationWriteDocument); err != nil {
		rpcCode = status.Code(err)
		return err
	}

	first, err := stream.Recv()
	if err != nil {
		rpcCode = codes.InvalidArgument
		return status.Errorf(codes.InvalidArgument, "receive init message: %v", err)
	}

	init := first.GetInit()
	if init == nil {
		rpcCode = codes.InvalidArgument
		return status.Error(codes.InvalidArgument, "first message must be init")
	}

	txID := init.GetTransactionId()
	docName := init.GetDocumentName()
	contentType := init.GetContentType()
	idempotencyKey := init.GetIdempotencyKey()
	tenantID := init.GetTenantId()
	rpc.AddSpanAttributes(telemetry.DocumentIdentityAttributes(
		txID,
		docName,
		s.identifierMode,
	)...)

	if err := storeapi.ValidateWriteMetadata(txID, docName, contentType, tenantID, idempotencyKey); err != nil {
		mappedErr := mapStoreError(err)
		rpcCode = status.Code(mappedErr)
		return mappedErr
	}

	pr, pw := io.Pipe()

	var writeResult storeapi.WriteResult
	var writeErr error
	done := make(chan struct{})

	go func() {
		defer close(done)
		defer func() { _ = pr.Close() }()
		writeResult, writeErr = s.store.WriteDocument(ctx, txID, docName, contentType, idempotencyKey, pr)
	}()

	if recvErr := s.recvChunks(ctx, stream, pw, done, &writeErr); recvErr != nil {
		rpcCode = status.Code(recvErr)
		return recvErr
	}

	_ = pw.Close()
	<-done

	if writeErr != nil {
		mappedErr := s.mapStoreError(ctx, "WriteDocument", writeErr)
		rpcCode = status.Code(mappedErr)
		return mappedErr
	}

	if err := stream.SendAndClose(&scrapv1.WriteDocumentResponse{
		Sha256Checksum: hex.EncodeToString(writeResult.SHA256[:]),
		Size:           writeResult.Size,
		CreatedAt:      timestamppb.New(writeResult.CreatedAt),
	}); err != nil {
		rpcCode = status.Code(err)
		return err
	}
	return nil
}

// recvChunks reads chunk messages from the stream and writes them into pw.
// It closes pw (with an error if needed) and waits on done before returning on failure.
func (s *documentServer) recvChunks(ctx context.Context, stream grpc.ClientStreamingServer[scrapv1.WriteDocumentRequest, scrapv1.WriteDocumentResponse], pw *io.PipeWriter, done <-chan struct{}, writeErr *error) error {
	var totalBytes int64
	for {
		msg, err := stream.Recv()
		if err != nil {
			return s.handleRecvError(ctx, pw, done, totalBytes, err)
		}
		chunk := msg.GetChunkData()
		if len(chunk) == 0 {
			continue
		}
		nextTotal, err := validateIncomingChunk(totalBytes, chunk)
		if err != nil {
			return closePipeAndMapStoreError(pw, done, err)
		}
		totalBytes = nextTotal
		if err := s.writeChunk(ctx, pw, done, writeErr, chunk); err != nil {
			return err
		}
	}
}

func (s *documentServer) handleRecvError(ctx context.Context, pw *io.PipeWriter, done <-chan struct{}, totalBytes int64, recvErr error) error {
	if errors.Is(recvErr, io.EOF) {
		if totalBytes > 0 {
			return nil
		}
		err := fmt.Errorf("%w: document body is empty", storeapi.ErrInvalidArgument)
		return closePipeAndMapStoreError(pw, done, err)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		pw.CloseWithError(ctxErr)
		<-done
		return status.FromContextError(ctxErr).Err()
	}
	pw.CloseWithError(recvErr)
	<-done
	return status.Errorf(codes.Internal, "receive chunk: %v", recvErr)
}

func validateIncomingChunk(totalBytes int64, chunk []byte) (int64, error) {
	if err := storeapi.ValidateClientChunk(chunk); err != nil {
		return 0, err
	}
	nextTotal := totalBytes + int64(len(chunk))
	if nextTotal > storeapi.MaxDocumentBytes {
		return 0, storeapi.NewResourceExhausted(storeapi.ResourceExhaustedReasonDocumentTooLarge, "document too large")
	}
	return nextTotal, nil
}

func closePipeAndMapStoreError(pw *io.PipeWriter, done <-chan struct{}, err error) error {
	pw.CloseWithError(err)
	<-done
	return mapStoreError(err)
}

func (s *documentServer) writeChunk(ctx context.Context, pw *io.PipeWriter, done <-chan struct{}, writeErr *error, chunk []byte) error {
	writeDone := make(chan error, 1)
	go func() {
		_, err := pw.Write(chunk)
		writeDone <- err
	}()

	select {
	case err := <-writeDone:
		if err == nil {
			return nil
		}
		_ = pw.Close()
		<-done
		if *writeErr != nil {
			return s.mapStoreError(ctx, "WriteDocument", *writeErr)
		}
		return status.Errorf(codes.Internal, "write chunk: %v", err)
	case <-done:
		_ = pw.Close()
		if *writeErr != nil {
			return s.mapStoreError(ctx, "WriteDocument", *writeErr)
		}
		return status.Error(codes.Internal, "write consumer stopped")
	case <-ctx.Done():
		_ = pw.CloseWithError(ctx.Err())
		<-done
		return status.FromContextError(ctx.Err()).Err()
	}
}

func (s *documentServer) HeadDocument(ctx context.Context, req *scrapv1.HeadDocumentRequest) (*scrapv1.HeadDocumentResponse, error) {
	ctx, rpc := s.telemetry.StartRPC(ctx, "HeadDocument")
	rpc.AddSpanAttributes(telemetry.DocumentIdentityAttributes(
		req.GetTransactionId(),
		req.GetDocumentName(),
		s.identifierMode,
	)...)
	rpcCode := codes.OK
	defer func() { rpc.Finish(rpcCode) }()

	if err := s.requireSecurity(ctx, security.RoleDocumentReader, audit.OperationHeadDocument); err != nil {
		rpcCode = status.Code(err)
		return nil, err
	}
	if err := storeapi.ValidateDocumentIdentity(req.GetTransactionId(), req.GetDocumentName(), req.GetTenantId()); err != nil {
		mappedErr := mapStoreError(err)
		rpcCode = status.Code(mappedErr)
		return nil, mappedErr
	}

	meta, err := s.store.HeadDocument(ctx, req.GetTransactionId(), req.GetDocumentName())
	if err != nil {
		mappedErr := s.mapStoreError(ctx, "HeadDocument", err)
		rpcCode = status.Code(mappedErr)
		return nil, mappedErr
	}

	return &scrapv1.HeadDocumentResponse{
		Name:           meta.Name,
		ContentType:    meta.ContentType,
		Size:           meta.Size,
		Sha256Checksum: hex.EncodeToString(meta.SHA256[:]),
		CreatedAt:      timestamppb.New(meta.CreatedAt),
	}, nil
}

func (s *documentServer) ReadDocument(req *scrapv1.ReadDocumentRequest, stream grpc.ServerStreamingServer[scrapv1.ReadDocumentResponse]) error {
	ctx, rpc := s.telemetry.StartRPC(stream.Context(), "ReadDocument")
	rpc.AddSpanAttributes(telemetry.DocumentIdentityAttributes(
		req.GetTransactionId(),
		req.GetDocumentName(),
		s.identifierMode,
	)...)
	rpcCode := codes.OK
	defer func() { rpc.Finish(rpcCode) }()

	if err := s.requireSecurity(ctx, security.RoleDocumentReader, audit.OperationReadDocument); err != nil {
		rpcCode = status.Code(err)
		return err
	}
	if err := storeapi.ValidateDocumentIdentity(req.GetTransactionId(), req.GetDocumentName(), req.GetTenantId()); err != nil {
		mappedErr := mapStoreError(err)
		rpcCode = status.Code(mappedErr)
		return mappedErr
	}
	if err := statusErrorFromContext(ctx); err != nil {
		rpcCode = status.Code(err)
		return err
	}

	rc, meta, err := s.store.ReadDocument(ctx, req.GetTransactionId(), req.GetDocumentName())
	if err != nil {
		mappedErr := s.mapStoreStatusOrContextError(ctx, "ReadDocument", err)
		rpcCode = status.Code(mappedErr)
		return mappedErr
	}
	defer func() { _ = rc.Close() }()

	if err := statusErrorFromContext(ctx); err != nil {
		rpcCode = status.Code(err)
		return err
	}

	if err := sendReadMetadata(ctx, stream, meta); err != nil {
		rpcCode = status.Code(err)
		return err
	}
	if err := streamReadChunks(ctx, stream, rc); err != nil {
		rpcCode = status.Code(err)
		return err
	}

	return nil
}

func sendReadMetadata(ctx context.Context, stream grpc.ServerStreamingServer[scrapv1.ReadDocumentResponse], meta storeapi.DocumentMeta) error {
	if err := statusErrorFromContext(ctx); err != nil {
		return err
	}
	if err := stream.Send(&scrapv1.ReadDocumentResponse{
		Part: &scrapv1.ReadDocumentResponse_Meta{
			Meta: &scrapv1.ReadDocumentMeta{
				ContentType:    meta.ContentType,
				Size:           meta.Size,
				Sha256Checksum: hex.EncodeToString(meta.SHA256[:]),
				CreatedAt:      timestamppb.New(meta.CreatedAt),
			},
		},
	}); err != nil {
		if mappedErr := statusErrorFromContextOrError(ctx, err); mappedErr != nil {
			return mappedErr
		}
		return status.Errorf(codes.Internal, "send metadata: %v", err)
	}
	return nil
}

func streamReadChunks(ctx context.Context, stream grpc.ServerStreamingServer[scrapv1.ReadDocumentResponse], rc io.Reader) error {
	buf := make([]byte, readBufSize)
	for {
		if err := statusErrorFromContext(ctx); err != nil {
			return err
		}
		n, readErr := rc.Read(buf)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			_, err := handleReadChunkError(ctx, readErr)
			return err
		}
		if n > 0 {
			if err := sendReadChunk(ctx, stream, buf[:n]); err != nil {
				return err
			}
		}
		done, err := handleReadChunkError(ctx, readErr)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
}

func handleReadChunkError(ctx context.Context, readErr error) (bool, error) {
	if readErr == nil {
		return false, nil
	}
	if errors.Is(readErr, io.EOF) {
		return true, nil
	}
	if mappedErr := statusErrorFromContextOrError(ctx, readErr); mappedErr != nil {
		return false, mappedErr
	}
	if storeErr := mapStoreErrorForRead(readErr); storeErr != nil {
		return false, storeErr
	}
	return false, status.Errorf(codes.Internal, "read document: %v", readErr)
}

func sendReadChunk(ctx context.Context, stream grpc.ServerStreamingServer[scrapv1.ReadDocumentResponse], data []byte) error {
	chunk := make([]byte, len(data))
	copy(chunk, data)
	if err := statusErrorFromContext(ctx); err != nil {
		return err
	}
	if err := stream.Send(&scrapv1.ReadDocumentResponse{
		Part: &scrapv1.ReadDocumentResponse_ChunkData{
			ChunkData: chunk,
		},
	}); err != nil {
		if mappedErr := statusErrorFromContextOrError(ctx, err); mappedErr != nil {
			return mappedErr
		}
		return status.Errorf(codes.Internal, "send chunk: %v", err)
	}
	return nil
}

func statusErrorFromContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return status.FromContextError(err).Err()
	}
	return nil
}

func (s *documentServer) mapStoreStatusOrContextError(ctx context.Context, method string, err error) error {
	if mappedErr := statusErrorFromContextOrError(ctx, err); mappedErr != nil {
		return mappedErr
	}
	return s.mapStoreError(ctx, method, err)
}

func statusErrorFromContextOrError(ctx context.Context, err error) error {
	if ctxErr := statusErrorFromContext(ctx); ctxErr != nil {
		return ctxErr
	}
	switch {
	case errors.Is(err, context.Canceled):
		return status.FromContextError(context.Canceled).Err()
	case errors.Is(err, context.DeadlineExceeded):
		return status.FromContextError(context.DeadlineExceeded).Err()
	}
	if st, ok := status.FromError(err); ok {
		return st.Err()
	}
	return nil
}

func mapStoreErrorForRead(err error) error {
	switch {
	case errors.Is(err, storeapi.ErrAlreadyExists),
		errors.Is(err, storeapi.ErrNotFound),
		errors.Is(err, storeapi.ErrTxNotFound),
		errors.Is(err, storeapi.ErrInvalidArgument),
		errors.Is(err, storeapi.ErrResourceExhausted),
		errors.Is(err, storeapi.ErrRebuilding),
		errors.Is(err, storeapi.ErrUnavailable),
		errors.Is(err, storeapi.ErrDataLoss):
		return mapStoreError(err)
	default:
		return nil
	}
}

func (s *documentServer) FindDocuments(ctx context.Context, req *scrapv1.FindDocumentsRequest) (*scrapv1.FindDocumentsResponse, error) {
	ctx, rpc := s.telemetry.StartRPC(ctx, "FindDocuments")
	rpc.AddSpanAttributes(telemetry.DocumentIdentityAttributes(
		req.GetTransactionId(),
		"",
		s.identifierMode,
	)...)
	rpcCode := codes.OK
	defer func() { rpc.Finish(rpcCode) }()

	if err := s.requireSecurity(ctx, security.RoleDocumentReader, audit.OperationFindDocuments); err != nil {
		rpcCode = status.Code(err)
		return nil, err
	}
	if err := storeapi.ValidateTransactionLookup(req.GetTransactionId(), req.GetTenantId()); err != nil {
		mappedErr := mapStoreError(err)
		rpcCode = status.Code(mappedErr)
		return nil, mappedErr
	}
	if err := statusErrorFromContext(ctx); err != nil {
		rpcCode = status.Code(err)
		return nil, err
	}

	docs, err := s.store.FindDocuments(ctx, req.GetTransactionId())
	if err != nil {
		mappedErr := s.mapStoreStatusOrContextError(ctx, "FindDocuments", err)
		rpcCode = status.Code(mappedErr)
		return nil, mappedErr
	}
	if err := statusErrorFromContext(ctx); err != nil {
		rpcCode = status.Code(err)
		return nil, err
	}

	var pbDocs []*scrapv1.DocumentMeta
	for _, d := range docs {
		pbDocs = append(pbDocs, &scrapv1.DocumentMeta{
			Name:           d.Name,
			ContentType:    d.ContentType,
			Size:           d.Size,
			Sha256Checksum: hex.EncodeToString(d.SHA256[:]),
			CreatedAt:      timestamppb.New(d.CreatedAt),
		})
	}

	return &scrapv1.FindDocumentsResponse{Documents: pbDocs}, nil
}

func (s *documentServer) requireRole(ctx context.Context, role security.Role) error {
	if s.authorizer == nil {
		return nil
	}
	return s.authorizer.Authorize(ctx, role)
}

func (s *documentServer) requireSecurity(ctx context.Context, role security.Role, operation string) error {
	if decision := s.checkRateLimit(ctx, operation); decision.Limited {
		if err := s.recordAudit(ctx, role, operation, audit.ResultRateLimited, audit.ReasonRateLimited); err != nil {
			return status.Error(codes.Internal, "audit event failed")
		}
		return security.RateLimitedError()
	}
	if err := s.requireRole(ctx, role); err != nil {
		if auditErr := s.recordAudit(ctx, role, operation, audit.ResultDenied, s.auditReasonForError(err)); auditErr != nil {
			return status.Error(codes.Internal, "audit event failed")
		}
		return err
	}
	if err := s.recordAudit(ctx, role, operation, audit.ResultAllowed, audit.ReasonAllowed); err != nil {
		return status.Error(codes.Internal, "audit event failed")
	}
	return nil
}

func (s *documentServer) checkRateLimit(ctx context.Context, operation string) security.RateLimitDecision {
	if s.rateLimiter == nil {
		return security.RateLimitDecision{}
	}
	return s.rateLimiter.Allow(ctx, security.RateLimitSurfacePublic, principalIDFromContext(ctx), operation)
}

func (s *documentServer) recordAudit(ctx context.Context, role security.Role, operation, result, reason string) error {
	if s.auditSink == nil {
		return nil
	}
	event, err := audit.NewEvent(audit.EventInput{
		PrincipalID: principalIDFromContext(ctx),
		Role:        string(role),
		Surface:     audit.SurfacePublic,
		Operation:   operation,
		Target:      audit.TargetDocument,
		Result:      result,
		Reason:      reason,
	})
	if err != nil {
		return err
	}
	return s.auditSink.Record(ctx, event)
}

func (s *documentServer) auditReasonForError(err error) string {
	switch {
	case errors.Is(err, security.ErrUnauthenticated):
		return audit.ReasonUnauthenticated
	case errors.Is(err, security.ErrPermissionDenied):
		if security.AuthorizationStatusForError(err) == security.AuthorizationStatusMissingRole {
			return audit.ReasonMissingRole
		}
		return audit.ReasonPermissionDenied
	default:
		return audit.ReasonInternalError
	}
}

func principalIDFromContext(ctx context.Context) string {
	principal, ok := security.PrincipalFromContext(ctx)
	if !ok {
		return ""
	}
	return principal.ID
}

func (s *documentServer) mapStoreError(ctx context.Context, method string, err error) error {
	if nle, ok := errors.AsType[*storeapi.NotLeaderError](err); ok {
		s.logNotLeaderRedirect(ctx, method, nle.LeaderAddr)
	}
	return mapStoreError(err)
}

func (s *documentServer) logNotLeaderRedirect(ctx context.Context, method, leaderAddr string) {
	if s.logger == nil {
		return
	}
	s.logger.DebugContext(ctx, "request redirected to shard leader",
		"rpc.service", documentServiceName,
		"rpc.method", method,
		"rpc.grpc.status_code", codes.Unavailable.String(),
		"reason", "not_leader",
		"scrap.role", "follower",
		"leader_addr", leaderAddr,
	)
}

func mapStoreError(err error) error {
	if nle, ok := errors.AsType[*storeapi.NotLeaderError](err); ok {
		st, detailErr := status.New(codes.Unavailable, "not shard leader").
			WithDetails(&scrapv1.LeaderHint{LeaderAddr: nle.LeaderAddr})
		if detailErr != nil {
			return status.Errorf(codes.Unavailable, "%v", err)
		}
		return st.Err()
	}

	switch {
	case errors.Is(err, storeapi.ErrAlreadyExists):
		return status.Errorf(codes.AlreadyExists, "%v", err)
	case errors.Is(err, storeapi.ErrNotFound), errors.Is(err, storeapi.ErrTxNotFound):
		return status.Errorf(codes.NotFound, "%v", err)
	case errors.Is(err, storeapi.ErrInvalidArgument):
		return status.Errorf(codes.InvalidArgument, "%v", err)
	case errors.Is(err, storeapi.ErrResourceExhausted):
		return resourceExhaustedStatus(err)
	case errors.Is(err, storeapi.ErrUnavailable), errors.Is(err, storeapi.ErrRebuilding):
		return availabilityStatus(err)
	case errors.Is(err, storeapi.ErrDataLoss):
		return dataLossStatus(err)
	default:
		return status.Errorf(codes.Internal, "%v", err)
	}
}

func availabilityStatus(err error) error {
	if errors.Is(err, storeapi.ErrRebuilding) {
		return unavailableStatus(storeapi.NewUnavailable(
			storeapi.UnavailableReasonProjectionRebuild,
			storeapi.ErrRebuilding.Error(),
		))
	}
	return unavailableStatus(err)
}

func unavailableStatus(err error) error {
	reason, ok := storeapi.UnavailableReason(err)
	if !ok {
		return status.Errorf(codes.Unavailable, "%v", err)
	}

	st, detailErr := status.New(codes.Unavailable, fmt.Sprintf("%v", err)).
		WithDetails(&errdetails.ErrorInfo{Reason: reason})
	if detailErr != nil {
		return status.Errorf(codes.Unavailable, "%v", err)
	}
	return st.Err()
}

func resourceExhaustedStatus(err error) error {
	reason, ok := storeapi.ResourceExhaustedReason(err)
	if !ok {
		return status.Errorf(codes.ResourceExhausted, "%v", err)
	}

	st, detailErr := status.New(codes.ResourceExhausted, fmt.Sprintf("%v", err)).
		WithDetails(&errdetails.ErrorInfo{Reason: reason})
	if detailErr != nil {
		return status.Errorf(codes.ResourceExhausted, "%v", err)
	}
	return st.Err()
}

func dataLossStatus(err error) error {
	reason, ok := storeapi.DataLossReason(err)
	if !ok || !publicDataLossReason(reason) {
		return status.Error(codes.DataLoss, storeapi.ErrDataLoss.Error())
	}

	st, detailErr := status.New(codes.DataLoss, storeapi.ErrDataLoss.Error()).
		WithDetails(&errdetails.ErrorInfo{Reason: reason})
	if detailErr != nil {
		return status.Error(codes.DataLoss, storeapi.ErrDataLoss.Error())
	}
	return st.Err()
}

func publicDataLossReason(reason string) bool {
	switch reason {
	case storeapi.DataLossReasonBackendRestoreCorrupt,
		storeapi.DataLossReasonBackendRestoreChecksumMismatch,
		storeapi.DataLossReasonBackendRestoreMetadataMismatch,
		storeapi.DataLossReasonBackendRestoreMissing:
		return true
	default:
		return false
	}
}
