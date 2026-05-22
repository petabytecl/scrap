package writepath

import (
	"context"
	"io"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Server struct {
	store *Store
}

func NewServer(store *Store) *Server {
	return &Server{store: store}
}

func (s *Server) WriteDocument(stream StorageWriteDocumentServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	if first.Metadata == nil {
		return status.Error(codes.InvalidArgument, "first write message must include metadata")
	}
	if len(first.Chunk) != 0 {
		return status.Error(codes.InvalidArgument, "metadata message must not include bytes")
	}

	result, err := s.store.WriteDocument(first.Metadata, func() ([]byte, error) {
		msg, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				return nil, io.EOF
			}
			return nil, err
		}
		if msg.Metadata != nil {
			return nil, status.Error(codes.InvalidArgument, "metadata is only allowed in first write message")
		}
		return msg.Chunk, nil
	})
	if err != nil {
		return err
	}
	return stream.SendAndClose(result)
}

func (s *Server) HeadDocument(ctx context.Context, req *HeadDocumentRequest) (*HeadDocumentResponse, error) {
	return s.store.HeadDocument(req)
}

func (s *Server) ReadDocument(req *ReadDocumentRequest, stream StorageReadDocumentServer) error {
	return s.store.ReadDocument(req, stream.Send)
}
