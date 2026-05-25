package writepath

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/petabytecl/scrap/internal/testutil"
)

func TestGobCodecRoundTripAndRejectsInvalidData(t *testing.T) {
	codec := gobCodec{}
	if codec.Name() != codecName {
		t.Fatalf("codec name = %q, want %q", codec.Name(), codecName)
	}

	encoded, err := codec.Marshal(&WriteDocumentRequest{
		Metadata: &WriteDocumentMetadata{
			TenantID:      "tenant-a",
			TransactionID: "tx-codec",
			DocumentName:  "codec.xml",
		},
	})
	testutil.RequireNoErrorf(t, err, "marshal write request")

	var decoded WriteDocumentRequest
	testutil.RequireNoErrorf(t, codec.Unmarshal(encoded, &decoded), "unmarshal write request")
	if decoded.Metadata == nil || decoded.Metadata.DocumentName != "codec.xml" {
		t.Fatalf("decoded request = %+v, want metadata document codec.xml", decoded)
	}

	if _, err := codec.Marshal(make(chan int)); err == nil {
		t.Fatal("marshal accepted an unsupported channel value")
	}
	if err := codec.Unmarshal([]byte("not a gob payload"), &decoded); err == nil {
		t.Fatal("unmarshal accepted malformed data")
	}
}

func TestStorageGRPCWriteHeadAndReadDocument(t *testing.T) {
	store := openTestStore(t)
	client, cleanup := newWritePathTestClient(t, store)
	defer cleanup()

	ctx := context.Background()
	stream, err := client.WriteDocument(ctx)
	testutil.RequireNoErrorf(t, err, "open write stream")
	data := []byte("streamed through gob grpc")
	testutil.RequireNoErrorf(t, stream.Send(&WriteDocumentRequest{
		Metadata: &WriteDocumentMetadata{
			TenantID:       "tenant-a",
			TransactionID:  "tx-grpc",
			DocumentName:   "grpc.xml",
			ExpectedLength: int64(len(data)),
		},
	}), "send metadata")
	testutil.RequireNoErrorf(t, stream.Send(&WriteDocumentRequest{Chunk: data[:8]}), "send first chunk")
	testutil.RequireNoErrorf(t, stream.Send(&WriteDocumentRequest{Chunk: data[8:]}), "send second chunk")
	result, err := stream.CloseAndRecv()
	testutil.RequireNoErrorf(t, err, "close write stream")
	if result.ByteLength != int64(len(data)) {
		t.Fatalf("written length = %d, want %d", result.ByteLength, len(data))
	}

	head, err := client.HeadDocument(ctx, &HeadDocumentRequest{
		TenantID:      "tenant-a",
		TransactionID: "tx-grpc",
		DocumentName:  "grpc.xml",
	})
	testutil.RequireNoErrorf(t, err, "head document")
	if head.Document.LogicalSHA256 != result.LogicalSHA256 {
		t.Fatalf("head sha = %s, want %s", head.Document.LogicalSHA256, result.LogicalSHA256)
	}

	readStream, err := client.ReadDocument(ctx, &ReadDocumentRequest{
		TenantID:      "tenant-a",
		TransactionID: "tx-grpc",
		DocumentName:  "grpc.xml",
	})
	testutil.RequireNoErrorf(t, err, "read document")
	var got bytes.Buffer
	metadataSeen := false
	for {
		response, err := readStream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		testutil.RequireNoErrorf(t, err, "receive read response")
		if response.Document != nil {
			metadataSeen = true
			continue
		}
		_, err = got.Write(response.Chunk)
		testutil.RequireNoErrorf(t, err, "buffer read chunk")
	}
	if !metadataSeen {
		t.Fatal("read stream did not send document metadata")
	}
	if !bytes.Equal(got.Bytes(), data) {
		t.Fatalf("read bytes = %q, want %q", got.Bytes(), data)
	}
}

func TestStorageGRPCWriteRejectsInvalidStreamSequence(t *testing.T) {
	store := openTestStore(t)
	client, cleanup := newWritePathTestClient(t, store)
	defer cleanup()

	t.Run("missing metadata", func(t *testing.T) {
		stream, err := client.WriteDocument(context.Background())
		testutil.RequireNoErrorf(t, err, "open write stream")
		testutil.RequireNoErrorf(t, stream.Send(&WriteDocumentRequest{Chunk: []byte("bytes first")}), "send bytes first")
		_, err = stream.CloseAndRecv()
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("close status = %v, want %v; err=%v", status.Code(err), codes.InvalidArgument, err)
		}
	})

	t.Run("metadata carries bytes", func(t *testing.T) {
		stream, err := client.WriteDocument(context.Background())
		testutil.RequireNoErrorf(t, err, "open write stream")
		testutil.RequireNoErrorf(t, stream.Send(&WriteDocumentRequest{
			Metadata: &WriteDocumentMetadata{
				TenantID:      "tenant-a",
				TransactionID: "tx-bad",
				DocumentName:  "metadata-with-bytes.xml",
			},
			Chunk: []byte("not allowed"),
		}), "send metadata with bytes")
		_, err = stream.CloseAndRecv()
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("close status = %v, want %v; err=%v", status.Code(err), codes.InvalidArgument, err)
		}
	})

	t.Run("repeated metadata", func(t *testing.T) {
		stream, err := client.WriteDocument(context.Background())
		testutil.RequireNoErrorf(t, err, "open write stream")
		testutil.RequireNoErrorf(t, stream.Send(&WriteDocumentRequest{
			Metadata: &WriteDocumentMetadata{
				TenantID:       "tenant-a",
				TransactionID:  "tx-repeat",
				DocumentName:   "repeat.xml",
				ExpectedLength: 1,
			},
		}), "send metadata")
		testutil.RequireNoErrorf(t, stream.Send(&WriteDocumentRequest{
			Metadata: &WriteDocumentMetadata{
				TenantID:      "tenant-a",
				TransactionID: "tx-repeat",
				DocumentName:  "repeat.xml",
			},
		}), "send repeated metadata")
		_, err = stream.CloseAndRecv()
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("close status = %v, want %v; err=%v", status.Code(err), codes.InvalidArgument, err)
		}
	})
}

func newWritePathTestClient(t *testing.T, store *Store) (StorageClient, func()) {
	t.Helper()

	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer(grpc.ForceServerCodec(gobCodec{}))
	RegisterStorageServer(server, NewServer(store))
	go func() {
		_ = server.Serve(listener)
	}()

	dialer := func(ctx context.Context, _ string) (net.Conn, error) {
		return listener.DialContext(ctx)
	}
	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(gobCodec{})),
	)
	testutil.RequireNoErrorf(t, err, "grpc.NewClient")

	cleanup := func() {
		_ = conn.Close()
		server.Stop()
		_ = listener.Close()
	}
	return NewStorageClient(conn), cleanup
}
