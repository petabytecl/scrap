package writepath

import (
	"context"

	"google.golang.org/grpc"
)

const (
	StorageServiceName                 = "scrap.spike.v1.Storage"
	StorageWriteDocumentFullMethodName = "/scrap.spike.v1.Storage/WriteDocument"
	StorageHeadDocumentFullMethodName  = "/scrap.spike.v1.Storage/HeadDocument"
	StorageReadDocumentFullMethodName  = "/scrap.spike.v1.Storage/ReadDocument"
)

type StorageClient interface {
	WriteDocument(ctx context.Context, opts ...grpc.CallOption) (StorageWriteDocumentClient, error)
	HeadDocument(ctx context.Context, in *HeadDocumentRequest, opts ...grpc.CallOption) (*HeadDocumentResponse, error)
	ReadDocument(ctx context.Context, in *ReadDocumentRequest, opts ...grpc.CallOption) (StorageReadDocumentClient, error)
}

type storageClient struct {
	cc grpc.ClientConnInterface
}

func NewStorageClient(cc grpc.ClientConnInterface) StorageClient {
	return &storageClient{cc: cc}
}

func (c *storageClient) WriteDocument(ctx context.Context, opts ...grpc.CallOption) (StorageWriteDocumentClient, error) {
	stream, err := c.cc.NewStream(ctx, &StorageServiceDesc.Streams[0], StorageWriteDocumentFullMethodName, opts...)
	if err != nil {
		return nil, err
	}
	return &storageWriteDocumentClient{ClientStream: stream}, nil
}

type StorageWriteDocumentClient interface {
	Send(*WriteDocumentRequest) error
	CloseAndRecv() (*WriteDocumentResult, error)
	grpc.ClientStream
}

type storageWriteDocumentClient struct {
	grpc.ClientStream
}

func (c *storageWriteDocumentClient) Send(m *WriteDocumentRequest) error {
	return c.ClientStream.SendMsg(m)
}

func (c *storageWriteDocumentClient) CloseAndRecv() (*WriteDocumentResult, error) {
	if err := c.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	m := new(WriteDocumentResult)
	if err := c.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *storageClient) HeadDocument(ctx context.Context, in *HeadDocumentRequest, opts ...grpc.CallOption) (*HeadDocumentResponse, error) {
	out := new(HeadDocumentResponse)
	err := c.cc.Invoke(ctx, StorageHeadDocumentFullMethodName, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *storageClient) ReadDocument(ctx context.Context, in *ReadDocumentRequest, opts ...grpc.CallOption) (StorageReadDocumentClient, error) {
	stream, err := c.cc.NewStream(ctx, &StorageServiceDesc.Streams[1], StorageReadDocumentFullMethodName, opts...)
	if err != nil {
		return nil, err
	}
	x := &storageReadDocumentClient{ClientStream: stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type StorageReadDocumentClient interface {
	Recv() (*ReadDocumentResponse, error)
	grpc.ClientStream
}

type storageReadDocumentClient struct {
	grpc.ClientStream
}

func (c *storageReadDocumentClient) Recv() (*ReadDocumentResponse, error) {
	m := new(ReadDocumentResponse)
	if err := c.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

type StorageServer interface {
	WriteDocument(StorageWriteDocumentServer) error
	HeadDocument(context.Context, *HeadDocumentRequest) (*HeadDocumentResponse, error)
	ReadDocument(*ReadDocumentRequest, StorageReadDocumentServer) error
}

func RegisterStorageServer(s grpc.ServiceRegistrar, srv StorageServer) {
	s.RegisterService(&StorageServiceDesc, srv)
}

type StorageWriteDocumentServer interface {
	SendAndClose(*WriteDocumentResult) error
	Recv() (*WriteDocumentRequest, error)
	grpc.ServerStream
}

type storageWriteDocumentServer struct {
	grpc.ServerStream
}

func (s *storageWriteDocumentServer) SendAndClose(m *WriteDocumentResult) error {
	return s.ServerStream.SendMsg(m)
}

func (s *storageWriteDocumentServer) Recv() (*WriteDocumentRequest, error) {
	m := new(WriteDocumentRequest)
	if err := s.ServerStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

type StorageReadDocumentServer interface {
	Send(*ReadDocumentResponse) error
	grpc.ServerStream
}

type storageReadDocumentServer struct {
	grpc.ServerStream
}

func (s *storageReadDocumentServer) Send(m *ReadDocumentResponse) error {
	return s.ServerStream.SendMsg(m)
}

func storageWriteDocumentHandler(srv any, stream grpc.ServerStream) error {
	return srv.(StorageServer).WriteDocument(&storageWriteDocumentServer{ServerStream: stream})
}

func storageHeadDocumentHandler(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	in := new(HeadDocumentRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(StorageServer).HeadDocument(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: StorageHeadDocumentFullMethodName,
	}
	handler := func(ctx context.Context, req any) (any, error) {
		return srv.(StorageServer).HeadDocument(ctx, req.(*HeadDocumentRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func storageReadDocumentHandler(srv any, stream grpc.ServerStream) error {
	m := new(ReadDocumentRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(StorageServer).ReadDocument(m, &storageReadDocumentServer{ServerStream: stream})
}

var StorageServiceDesc = grpc.ServiceDesc{
	ServiceName: StorageServiceName,
	HandlerType: (*StorageServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "HeadDocument",
			Handler:    storageHeadDocumentHandler,
		},
	},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "WriteDocument",
			Handler:       storageWriteDocumentHandler,
			ClientStreams: true,
		},
		{
			StreamName:    "ReadDocument",
			Handler:       storageReadDocumentHandler,
			ServerStreams: true,
		},
	},
	Metadata: "scrap_spike_storage",
}
