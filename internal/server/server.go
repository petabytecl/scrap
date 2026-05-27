package server

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

const readBufSize = 64 * 1024 // 64 KiB read buffer for streaming document chunks

type documentServer struct {
	scrapv1.UnimplementedDocumentServiceServer
	store storeapi.Store
}

func Register(gs *grpc.Server, s storeapi.Store) {
	scrapv1.RegisterDocumentServiceServer(gs, &documentServer{store: s})
}

func (s *documentServer) WriteDocument(stream grpc.ClientStreamingServer[scrapv1.WriteDocumentRequest, scrapv1.WriteDocumentResponse]) error {
	first, err := stream.Recv()
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "receive init message: %v", err)
	}

	init := first.GetInit()
	if init == nil {
		return status.Error(codes.InvalidArgument, "first message must be init")
	}

	txID := init.GetTransactionId()
	docName := init.GetDocumentName()
	contentType := init.GetContentType()
	idempotencyKey := init.GetIdempotencyKey()

	if txID == "" || docName == "" {
		return status.Error(codes.InvalidArgument, "transaction_id and document_name are required")
	}

	pr, pw := io.Pipe()

	var writeResult storeapi.WriteResult
	var writeErr error
	done := make(chan struct{})

	go func() {
		defer close(done)
		defer func() { _ = pr.Close() }()
		writeResult, writeErr = s.store.WriteDocument(stream.Context(), txID, docName, contentType, idempotencyKey, pr)
	}()

	if recvErr := s.recvChunks(stream, pw, done, &writeErr); recvErr != nil {
		return recvErr
	}

	_ = pw.Close()
	<-done

	if writeErr != nil {
		return mapStoreError(writeErr)
	}

	return stream.SendAndClose(&scrapv1.WriteDocumentResponse{
		Sha256Checksum: hex.EncodeToString(writeResult.SHA256[:]),
		Size:           writeResult.Size,
		CreatedAt:      timestamppb.New(writeResult.CreatedAt),
	})
}

// recvChunks reads chunk messages from the stream and writes them into pw.
// It closes pw (with error if needed) and waits on done before returning on failure.
func (s *documentServer) recvChunks(stream grpc.ClientStreamingServer[scrapv1.WriteDocumentRequest, scrapv1.WriteDocumentResponse], pw *io.PipeWriter, done <-chan struct{}, writeErr *error) error {
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
					return mapStoreError(*writeErr)
				}
				return status.Errorf(codes.Internal, "write chunk: %v", err)
			}
		}
	}
}

func (s *documentServer) HeadDocument(ctx context.Context, req *scrapv1.HeadDocumentRequest) (*scrapv1.HeadDocumentResponse, error) {
	meta, err := s.store.HeadDocument(ctx, req.GetTransactionId(), req.GetDocumentName())
	if err != nil {
		return nil, mapStoreError(err)
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
	rc, meta, err := s.store.ReadDocument(stream.Context(), req.GetTransactionId(), req.GetDocumentName())
	if err != nil {
		return mapStoreError(err)
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
				return status.Errorf(codes.Internal, "send chunk: %v", err)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return status.Errorf(codes.Internal, "read document: %v", readErr)
		}
	}

	return nil
}

func (s *documentServer) FindDocuments(ctx context.Context, req *scrapv1.FindDocumentsRequest) (*scrapv1.FindDocumentsResponse, error) {
	docs, err := s.store.FindDocuments(ctx, req.GetTransactionId())
	if err != nil {
		return nil, mapStoreError(err)
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
