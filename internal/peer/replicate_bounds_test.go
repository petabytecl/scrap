package peer

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/security"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func TestReplicateDocumentRejectsInvalidInitBeforeSideEffects(t *testing.T) {
	tests := []struct {
		name string
		init *scrapv1.ReplicateDocumentInit
	}{
		{
			name: "missing transaction ID",
			init: validReplicateDocumentInit(func(init *scrapv1.ReplicateDocumentInit) {
				init.TransactionId = ""
			}),
		},
		{
			name: "oversized transaction ID",
			init: validReplicateDocumentInit(func(init *scrapv1.ReplicateDocumentInit) {
				init.TransactionId = strings.Repeat("t", storeapi.MaxTransactionIDBytes+1)
			}),
		},
		{
			name: "control character transaction ID",
			init: validReplicateDocumentInit(func(init *scrapv1.ReplicateDocumentInit) {
				init.TransactionId = "tx\nbad"
			}),
		},
		{
			name: "missing Document name",
			init: validReplicateDocumentInit(func(init *scrapv1.ReplicateDocumentInit) {
				init.DocumentName = ""
			}),
		},
		{
			name: "oversized Document name",
			init: validReplicateDocumentInit(func(init *scrapv1.ReplicateDocumentInit) {
				init.DocumentName = strings.Repeat("d", storeapi.MaxDocumentNameBytes+1)
			}),
		},
		{
			name: "control character Document name",
			init: validReplicateDocumentInit(func(init *scrapv1.ReplicateDocumentInit) {
				init.DocumentName = "doc\x00.xml"
			}),
		},
		{
			name: "missing content type",
			init: validReplicateDocumentInit(func(init *scrapv1.ReplicateDocumentInit) {
				init.ContentType = ""
			}),
		},
		{
			name: "oversized content type",
			init: validReplicateDocumentInit(func(init *scrapv1.ReplicateDocumentInit) {
				init.ContentType = strings.Repeat("c", storeapi.MaxContentTypeBytes+1)
			}),
		},
		{
			name: "control character content type",
			init: validReplicateDocumentInit(func(init *scrapv1.ReplicateDocumentInit) {
				init.ContentType = "text/\nxml"
			}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+"/sink", func(t *testing.T) {
			sink := &recordingReplicationSink{}
			srv := NewServer(t.TempDir(), WithReplicationSink(sink))
			defer func() { _ = srv.Close() }()

			err := srv.ReplicateDocument(replicateStreamWithInitAndChunks(tt.init, []byte("payload")))

			assertReplicateInvalidArgument(t, err)
			if sink.calls != 0 {
				t.Fatalf("replication sink calls = %d, want 0", sink.calls)
			}
		})

		t.Run(tt.name+"/local", func(t *testing.T) {
			dir := t.TempDir()
			srv := NewServer(dir)
			defer func() { _ = srv.Close() }()

			err := srv.ReplicateDocument(replicateStreamWithInitAndChunks(tt.init, []byte("payload")))

			assertReplicateInvalidArgument(t, err)
			assertNoLocalReplicationSideEffects(t, srv, dir)
		})
	}
}

func TestReplicateDocumentRejectsMalformedDigestBeforeSideEffects(t *testing.T) {
	tests := []struct {
		name string
		sha  []byte
	}{
		{name: "missing digest", sha: nil},
		{name: "truncated digest", sha: make([]byte, 31)},
		{name: "oversized digest", sha: make([]byte, 33)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			init := validReplicateDocumentInit(func(init *scrapv1.ReplicateDocumentInit) {
				init.Sha256 = tt.sha
			})

			dir := t.TempDir()
			srv := NewServer(dir)
			defer func() { _ = srv.Close() }()

			err := srv.ReplicateDocument(replicateStreamWithInitAndChunks(init, []byte("payload")))

			assertReplicateInvalidArgument(t, err)
			assertNoLocalReplicationSideEffects(t, srv, dir)
		})
	}
}

func TestReplicateDocumentDuplicateInitMidStreamRejected(t *testing.T) {
	init := validReplicateDocumentInit()
	stream := &replicateDocumentStream{
		ctx: peerAuthContext(security.NewRoleSet(security.RolePeerMember), peerAuthExpectedIdentity()),
		requests: []*scrapv1.ReplicateDocumentRequest{
			{Part: &scrapv1.ReplicateDocumentRequest_Init{Init: init}},
			{Part: &scrapv1.ReplicateDocumentRequest_ChunkData{ChunkData: []byte("pay")}},
			{Part: &scrapv1.ReplicateDocumentRequest_Init{Init: init}},
			{Part: &scrapv1.ReplicateDocumentRequest_ChunkData{ChunkData: []byte("load")}},
		},
	}

	dir := t.TempDir()
	srv := NewServer(dir)
	defer func() { _ = srv.Close() }()

	err := srv.ReplicateDocument(stream)
	assertReplicateInvalidArgument(t, err)
}

func TestReplicateDocumentLocalShaMismatchRollsBackAppendedBytes(t *testing.T) {
	dir := t.TempDir()
	srv := NewServer(dir)
	defer func() { _ = srv.Close() }()

	wrong := sha256.Sum256([]byte("different"))
	init := validReplicateDocumentInit(func(init *scrapv1.ReplicateDocumentInit) {
		init.Sha256 = wrong[:]
	})
	err := srv.ReplicateDocument(replicateStreamWithInitAndChunks(init, []byte("payload")))
	if status.Code(err) != codes.DataLoss {
		t.Fatalf("ReplicateDocument error = %v (%s), want data loss", err, status.Code(err))
	}

	// The mismatched frames must not remain in the mirror Block.
	blkPath := filepath.Join(dir, fmt.Sprintf("%016x.blk", uint64(1)))
	info, statErr := os.Stat(blkPath)
	if statErr != nil {
		t.Fatalf("stat block: %v", statErr)
	}
	if got, want := info.Size(), int64(block.HeaderSize); got != want {
		t.Fatalf("block size after rollback = %d, want header-only %d", got, want)
	}

	// A correct retry must verify end-to-end at the same offset.
	if err := srv.ReplicateDocument(replicateStreamWithInitAndChunks(validReplicateDocumentInit(), []byte("payload"))); err != nil {
		t.Fatalf("ReplicateDocument retry after rollback: %v", err)
	}
}

func TestReplicateDocumentRejectsOversizedChunkBeforeBuffering(t *testing.T) {
	chunk := make([]byte, storeapi.MaxClientChunkBytes+1)
	assertReplicateDocumentBodyRejectedBeforeSideEffects(t, validReplicateDocumentInit(), codes.ResourceExhausted, chunk)
}

func TestReplicateDocumentRejectsDeclaredOverLimitBeforeSideEffects(t *testing.T) {
	init := validReplicateDocumentInit(func(init *scrapv1.ReplicateDocumentInit) {
		init.TotalBytes = storeapi.MaxDocumentBytes + 1
	})
	assertReplicateDocumentBodyRejectedBeforeSideEffects(t, init, codes.ResourceExhausted)
}

func TestReplicateDocumentRejectsEmptyBodyBeforeAcceptedState(t *testing.T) {
	assertReplicateDocumentBodyRejectedBeforeSideEffects(t, validReplicateDocumentInit(), codes.InvalidArgument)
}

func TestValidateReplicateDocumentChunkBounds(t *testing.T) {
	if _, err := validateReplicateDocumentChunk(0, make([]byte, storeapi.MaxClientChunkBytes+1)); !errors.Is(err, storeapi.ErrResourceExhausted) {
		t.Fatalf("oversized chunk error = %v, want resource exhausted", err)
	}
	if _, err := validateReplicateDocumentChunk(storeapi.MaxDocumentBytes, []byte("x")); !errors.Is(err, storeapi.ErrResourceExhausted) {
		t.Fatalf("over-limit running total error = %v, want resource exhausted", err)
	}
	got, err := validateReplicateDocumentChunk(10, []byte("xy"))
	if err != nil {
		t.Fatalf("valid chunk error = %v, want nil", err)
	}
	if want := int64(12); got != want {
		t.Fatalf("running total = %d, want %d", got, want)
	}
}

func validReplicateDocumentInit(mutators ...func(*scrapv1.ReplicateDocumentInit)) *scrapv1.ReplicateDocumentInit {
	sha := sha256.Sum256([]byte("payload"))
	init := &scrapv1.ReplicateDocumentInit{
		TransactionId: "tx-peer-bounds",
		DocumentName:  "invoice.xml",
		ContentType:   "text/xml",
		BlockId:       1,
		TotalBytes:    7,
		FrameCount:    1,
		ShardId:       7,
		Sha256:        sha[:],
	}
	for _, mutate := range mutators {
		mutate(init)
	}
	return init
}

func replicateStreamWithInitAndChunks(init *scrapv1.ReplicateDocumentInit, chunks ...[]byte) *replicateDocumentStream {
	requests := []*scrapv1.ReplicateDocumentRequest{{
		Part: &scrapv1.ReplicateDocumentRequest_Init{Init: init},
	}}
	for _, chunk := range chunks {
		requests = append(requests, &scrapv1.ReplicateDocumentRequest{
			Part: &scrapv1.ReplicateDocumentRequest_ChunkData{ChunkData: chunk},
		})
	}
	return &replicateDocumentStream{
		ctx:      peerAuthContext(security.NewRoleSet(security.RolePeerMember), peerAuthExpectedIdentity()),
		requests: requests,
	}
}

func assertReplicateInvalidArgument(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, storeapi.ErrInvalidArgument) || status.Code(err) != codes.InvalidArgument {
		t.Fatalf("ReplicateDocument error = %v (%s), want invalid argument", err, status.Code(err))
	}
}

func assertReplicateDocumentBodyRejectedBeforeSideEffects(t *testing.T, init *scrapv1.ReplicateDocumentInit, code codes.Code, chunks ...[]byte) {
	t.Helper()
	t.Run("sink", func(t *testing.T) {
		sink := &recordingReplicationSink{}
		srv := NewServer(t.TempDir(), WithReplicationSink(sink))
		defer func() { _ = srv.Close() }()

		err := srv.ReplicateDocument(replicateStreamWithInitAndChunks(init, chunks...))

		assertReplicateBodyError(t, err, code)
		if sink.calls != 0 {
			t.Fatalf("replication sink calls = %d, want 0", sink.calls)
		}
	})

	t.Run("local", func(t *testing.T) {
		dir := t.TempDir()
		srv := NewServer(dir)
		defer func() { _ = srv.Close() }()

		err := srv.ReplicateDocument(replicateStreamWithInitAndChunks(init, chunks...))

		assertReplicateBodyError(t, err, code)
		assertNoLocalReplicationSideEffects(t, srv, dir)
	})
}

func assertReplicateBodyError(t *testing.T, err error, code codes.Code) {
	t.Helper()
	switch code {
	case codes.InvalidArgument:
		if !errors.Is(err, storeapi.ErrInvalidArgument) || status.Code(err) != code {
			t.Fatalf("ReplicateDocument error = %v (%s), want invalid argument", err, status.Code(err))
		}
	case codes.ResourceExhausted:
		if !errors.Is(err, storeapi.ErrResourceExhausted) || status.Code(err) != code {
			t.Fatalf("ReplicateDocument error = %v (%s), want resource exhausted", err, status.Code(err))
		}
	default:
		t.Fatalf("unhandled expected code %s", code)
	}
}

func assertNoLocalReplicationSideEffects(t *testing.T, srv *Server, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read block dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("local block dir entries = %v, want none", entries)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.writers) != 0 {
		t.Fatalf("local block writers = %d, want 0", len(srv.writers))
	}
}
