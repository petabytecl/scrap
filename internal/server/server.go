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
	rpc.AddSpanAttributes(telemetry.DocumentIdentityAttributes(
		txID,
		docName,
		s.identifierMode,
	)...)

	if txID == "" || docName == "" {
		rpcCode = codes.InvalidArgument
		return status.Error(codes.InvalidArgument, "transaction_id and document_name are required")
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
// It closes pw (with error if needed) and waits on done before returning on failure.
func (s *documentServer) recvChunks(ctx context.Context, stream grpc.ClientStreamingServer[scrapv1.WriteDocumentRequest, scrapv1.WriteDocumentResponse], pw *io.PipeWriter, done <-chan struct{}, writeErr *error) error {
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
				if *writeErr != nil {
					return s.mapStoreError(ctx, "WriteDocument", *writeErr)
				}
				return status.Errorf(codes.Internal, "write chunk: %v", err)
			}
		}
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

	rc, meta, err := s.store.ReadDocument(ctx, req.GetTransactionId(), req.GetDocumentName())
	if err != nil {
		mappedErr := s.mapStoreError(ctx, "ReadDocument", err)
		rpcCode = status.Code(mappedErr)
		return mappedErr
	}
	defer func() { _ = rc.Close() }()

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
		rpcCode = codes.Internal
		return status.Errorf(codes.Internal, "send metadata: %v", err)
	}

	buf := make([]byte, readBufSize)
	for {
		n, readErr := rc.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			if err := stream.Send(&scrapv1.ReadDocumentResponse{
				Part: &scrapv1.ReadDocumentResponse_ChunkData{
					ChunkData: chunk,
				},
			}); err != nil {
				rpcCode = codes.Internal
				return status.Errorf(codes.Internal, "send chunk: %v", err)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			rpcCode = codes.Internal
			return status.Errorf(codes.Internal, "read document: %v", readErr)
		}
	}

	return nil
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

	docs, err := s.store.FindDocuments(ctx, req.GetTransactionId())
	if err != nil {
		mappedErr := s.mapStoreError(ctx, "FindDocuments", err)
		rpcCode = status.Code(mappedErr)
		return nil, mappedErr
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

func (s *documentServer) mapStoreError(ctx context.Context, method string, err error) error {
	var nle *storeapi.NotLeaderError
	if errors.As(err, &nle) {
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
	var nle *storeapi.NotLeaderError
	if errors.As(err, &nle) {
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
	case errors.Is(err, storeapi.ErrDataLoss):
		return status.Errorf(codes.DataLoss, "%v", err)
	default:
		return status.Errorf(codes.Internal, "%v", err)
	}
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
