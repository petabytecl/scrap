package peer

import (
	"context"
	"io"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
)

type mockTransferStream struct {
	msgs []*scrapv1.TransferBlockResponse
	idx  int
	err  error
}

func (m *mockTransferStream) Recv() (*scrapv1.TransferBlockResponse, error) {
	if m.idx >= len(m.msgs) {
		if m.err != nil {
			return nil, m.err
		}
		return nil, io.EOF
	}
	msg := m.msgs[m.idx]
	m.idx++
	return msg, nil
}

func (m *mockTransferStream) Header() (metadata.MD, error) { return metadata.MD{}, nil }
func (m *mockTransferStream) Trailer() metadata.MD         { return nil }
func (m *mockTransferStream) CloseSend() error             { return nil }
func (m *mockTransferStream) Context() context.Context     { return context.Background() }
func (m *mockTransferStream) SendMsg(_ any) error          { return nil }
func (m *mockTransferStream) RecvMsg(_ any) error          { return nil }

func chunkMsg(data []byte) *scrapv1.TransferBlockResponse {
	return &scrapv1.TransferBlockResponse{
		Part: &scrapv1.TransferBlockResponse_ChunkData{ChunkData: data},
	}
}

func TestRecvBlockData_NegativeBlockSize(t *testing.T) {
	stream := &mockTransferStream{}
	_, _, err := recvBlockData(stream, -1, 0)
	if err == nil {
		t.Fatal("expected error for negative block size")
	}
}

func TestRecvBlockData_NegativeIdxSize(t *testing.T) {
	stream := &mockTransferStream{}
	_, _, err := recvBlockData(stream, 0, -1)
	if err == nil {
		t.Fatal("expected error for negative index size")
	}
}

func TestRecvBlockData_OversizedBlock(t *testing.T) {
	stream := &mockTransferStream{}
	_, _, err := recvBlockData(stream, maxTransferBytes+1, 0)
	if err == nil {
		t.Fatal("expected error for oversized block")
	}
}

func TestRecvBlockData_NetworkError(t *testing.T) {
	stream := &mockTransferStream{
		msgs: []*scrapv1.TransferBlockResponse{chunkMsg([]byte("abc"))},
		err:  status.Error(codes.Unavailable, "connection reset"),
	}
	_, _, err := recvBlockData(stream, 10, 0)
	if err == nil {
		t.Fatal("expected network error to propagate")
	}
}

func TestRecvBlockData_SizeMismatch(t *testing.T) {
	stream := &mockTransferStream{
		msgs: []*scrapv1.TransferBlockResponse{chunkMsg([]byte("short"))},
	}
	_, _, err := recvBlockData(stream, 100, 0)
	if err == nil {
		t.Fatal("expected size mismatch error")
	}
}

func TestRecvBlockData_HappyPath(t *testing.T) {
	blk := []byte("blockdata1")
	idx := []byte("idx1")
	stream := &mockTransferStream{
		msgs: []*scrapv1.TransferBlockResponse{
			chunkMsg(blk),
			chunkMsg(idx),
		},
	}

	gotBlk, gotIdx, err := recvBlockData(stream, int64(len(blk)), int64(len(idx)))
	if err != nil {
		t.Fatalf("recvBlockData: %v", err)
	}
	if string(gotBlk) != string(blk) {
		t.Fatalf("blk mismatch: got %q, want %q", gotBlk, blk)
	}
	if string(gotIdx) != string(idx) {
		t.Fatalf("idx mismatch: got %q, want %q", gotIdx, idx)
	}
}

func TestRecvBlockData_SplitChunk(t *testing.T) {
	combined := []byte("BBBBBiii")
	stream := &mockTransferStream{
		msgs: []*scrapv1.TransferBlockResponse{chunkMsg(combined)},
	}
	gotBlk, gotIdx, err := recvBlockData(stream, 5, 3)
	if err != nil {
		t.Fatalf("recvBlockData: %v", err)
	}
	if string(gotBlk) != "BBBBB" {
		t.Fatalf("blk: got %q, want %q", gotBlk, "BBBBB")
	}
	if string(gotIdx) != "iii" {
		t.Fatalf("idx: got %q, want %q", gotIdx, "iii")
	}
}
