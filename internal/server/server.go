package server

import (
	"context"
	"io"
	"strings"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	storeapi "github.com/petabytecl/scrap/internal/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

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

	txID := first.GetTransactionId()
	docName := first.GetDocumentName()
	contentType := first.GetContentType()
	idempotencyKey := first.GetIdempotencyKey()

	if txID == "" || docName == "" {
		return status.Error(codes.InvalidArgument, "transaction_id and document_name are required")
	}

	pr, pw := io.Pipe()

	var writeResult storeapi.WriteResult
	var writeErr error
	done := make(chan struct{})

	go func() {
		defer close(done)
		writeResult, writeErr = s.store.WriteDocument(stream.Context(), txID, docName, contentType, idempotencyKey, pr)
	}()

	if len(first.GetChunkData()) > 0 {
		if _, err := pw.Write(first.GetChunkData()); err != nil {
			pw.Close()
			<-done
			return status.Errorf(codes.Internal, "write chunk: %v", err)
		}
	}

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
		if len(msg.GetChunkData()) > 0 {
			if _, err := pw.Write(msg.GetChunkData()); err != nil {
				pw.Close()
				<-done
				return status.Errorf(codes.Internal, "write chunk: %v", err)
			}
		}
	}

	pw.Close()
	<-done

	if writeErr != nil {
		return mapStoreError(writeErr)
	}

	return stream.SendAndClose(&scrapv1.WriteDocumentResponse{
		Sha256Checksum: writeResult.SHA256Checksum,
		Size:           writeResult.Size,
		CreatedAt:      timestamppb.New(writeResult.CreatedAt),
	})
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
		Sha256Checksum: meta.SHA256Checksum,
		CreatedAt:      timestamppb.New(meta.CreatedAt),
	}, nil
}

func (s *documentServer) ReadDocument(req *scrapv1.ReadDocumentRequest, stream grpc.ServerStreamingServer[scrapv1.ReadDocumentResponse]) error {
	rc, meta, err := s.store.ReadDocument(stream.Context(), req.GetTransactionId(), req.GetDocumentName())
	if err != nil {
		return mapStoreError(err)
	}
	defer rc.Close()

	if err := stream.Send(&scrapv1.ReadDocumentResponse{
		ContentType:    meta.ContentType,
		Size:           meta.Size,
		Sha256Checksum: meta.SHA256Checksum,
		CreatedAt:      timestamppb.New(meta.CreatedAt),
	}); err != nil {
		return status.Errorf(codes.Internal, "send metadata: %v", err)
	}

	buf := make([]byte, 64*1024)
	for {
		n, readErr := rc.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			if err := stream.Send(&scrapv1.ReadDocumentResponse{
				ChunkData: chunk,
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
			Sha256Checksum: d.SHA256Checksum,
			CreatedAt:      timestamppb.New(d.CreatedAt),
		})
	}

	return &scrapv1.FindDocumentsResponse{Documents: pbDocs}, nil
}

func mapStoreError(err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "already exists"):
		return status.Errorf(codes.AlreadyExists, "%v", err)
	case strings.Contains(msg, "not found"):
		return status.Errorf(codes.NotFound, "%v", err)
	case strings.Contains(msg, "mismatch"), strings.Contains(msg, "corrupt"):
		return status.Errorf(codes.DataLoss, "%v", err)
	default:
		return status.Errorf(codes.Internal, "%v", err)
	}
}

