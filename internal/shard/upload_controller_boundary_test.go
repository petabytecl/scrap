package shard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/backend"
)

func TestUploadObjectVerificationErrorDoesNotLeakBackendKey(t *testing.T) {
	prefix := backendKeyPrefix("cell-a", 7, uploadApplyTestBlockID, 0)
	core := newUploadControllerBoundaryCore(memoryUploadSource{
		uploadObjectBlock: []byte("sealed block bytes"),
		uploadObjectIndex: []byte("sealed index bytes"),
	})
	controller := newUploadBoundaryController(core, &mismatchingHeadBackend{})

	_, err := controller.uploadObject(context.Background(), uploadApplyTestBlockID, prefix, uploadObjectBlock, uploadSizeUnchecked)
	if !errors.Is(err, backend.ErrCorrupt) {
		t.Fatalf("uploadObject error = %v, want ErrCorrupt", err)
	}
	if strings.Contains(err.Error(), prefix) || strings.Contains(err.Error(), prefix+".blk") {
		t.Fatalf("uploadObject error leaked Backend key %q: %v", prefix+".blk", err)
	}
}

func TestUploadAndConfirmWithRetryCancellationDoesNotProposeConfirm(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	pending := uploadControllerBoundaryPending()
	core := newUploadControllerBoundaryCore(memoryUploadSource{
		uploadObjectBlock: []byte("sealed block bytes"),
		uploadObjectIndex: []byte("sealed index bytes"),
	})
	controller := newUploadBoundaryController(core, cancelingPutBackend{cancel: cancel})

	err := controller.uploadAndConfirmWithRetry(ctx, pending)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("uploadAndConfirmWithRetry error = %v, want context canceled", err)
	}
	if got := len(core.acceptedProposals); got != 0 {
		t.Fatalf("accepted ConfirmUpload proposals = %d, want 0", got)
	}
}

func TestUploadAndConfirmKeepsPendingWhenIndexVerificationFails(t *testing.T) {
	pending := uploadControllerBoundaryPending()
	core := newUploadControllerBoundaryCore(memoryUploadSource{
		uploadObjectBlock: []byte("sealed block bytes"),
		uploadObjectIndex: []byte("sealed index bytes"),
	})
	controller := newUploadBoundaryController(core, newIndexVerificationMismatchBackend())

	err := controller.uploadAndConfirmWithRetry(context.Background(), pending)
	if !errors.Is(err, backend.ErrCorrupt) {
		t.Fatalf("uploadAndConfirmWithRetry error = %v, want ErrCorrupt", err)
	}
	if got := len(core.acceptedProposals); got != 0 {
		t.Fatalf("accepted ConfirmUpload proposals = %d, want 0", got)
	}
}

func TestUploadAndConfirmRetriesAfterInterruptedConfirmProposal(t *testing.T) {
	pending := uploadControllerBoundaryPending()
	core := newUploadControllerBoundaryCore(memoryUploadSource{
		uploadObjectBlock: []byte("sealed block bytes"),
		uploadObjectIndex: []byte("sealed index bytes"),
	})
	core.proposeErrors = []error{errors.New("raft unavailable")}
	controller := newUploadBoundaryController(core, newSuccessfulBoundaryBackend())

	if err := controller.uploadAndConfirmWithRetry(context.Background(), pending); err == nil {
		t.Fatal("first uploadAndConfirmWithRetry error = nil, want interrupted proposal error")
	}
	if got := len(core.acceptedProposals); got != 0 {
		t.Fatalf("accepted proposals after interrupted confirm = %d, want 0", got)
	}

	if err := controller.uploadAndConfirmWithRetry(context.Background(), pending); err != nil {
		t.Fatalf("retry uploadAndConfirmWithRetry: %v", err)
	}
	if got := len(core.acceptedProposals); got != 1 {
		t.Fatalf("accepted proposals after retry = %d, want 1", got)
	}
	confirm := confirmUploadFromProposal(t, core.acceptedProposals[0])
	if confirm.GetBlockId() != pending.BlockID {
		t.Fatalf("confirmed BlockID = %d, want %d", confirm.GetBlockId(), pending.BlockID)
	}
	if confirm.GetUploadGeneration() != pending.UploadGeneration {
		t.Fatalf("confirmed generation = %d, want %d", confirm.GetUploadGeneration(), pending.UploadGeneration)
	}
}

func newUploadBoundaryController(core *uploadControllerBoundaryCore, store backend.Backend) *uploadController {
	return newUploadController(core, UploadConfig{
		Backend:        store,
		CellID:         "cell-a",
		Concurrency:    1,
		RetryBaseDelay: time.Nanosecond,
	}, 7, slog.New(slog.DiscardHandler), noopWriteTelemetry{}, nil)
}

func newUploadControllerBoundaryCore(source uploadLocalSource) *uploadControllerBoundaryCore {
	return &uploadControllerBoundaryCore{
		sources: map[uint64]uploadLocalSource{
			uploadApplyTestBlockID: source,
		},
	}
}

func uploadControllerBoundaryPending() PendingUpload {
	return PendingUpload{
		BlockID:          uploadApplyTestBlockID,
		ShardID:          7,
		SealedSizeBytes:  18,
		SealedAtUs:       1716700000000000,
		UploadGeneration: 1716700001000000,
	}
}

type uploadControllerBoundaryCore struct {
	sources            map[uint64]uploadLocalSource
	proposeErrors      []error
	attemptedProposals [][]byte
	acceptedProposals  [][]byte
}

func (c *uploadControllerBoundaryCore) Propose(_ context.Context, data []byte) error {
	copied := append([]byte(nil), data...)
	c.attemptedProposals = append(c.attemptedProposals, copied)
	if len(c.proposeErrors) > 0 {
		err := c.proposeErrors[0]
		c.proposeErrors = c.proposeErrors[1:]
		if err != nil {
			return err
		}
	}
	c.acceptedProposals = append(c.acceptedProposals, copied)
	return nil
}

func (c *uploadControllerBoundaryCore) IsLeader() bool {
	return true
}

func (c *uploadControllerBoundaryCore) retryUploadObligations(context.Context) {}

func (c *uploadControllerBoundaryCore) pendingUploads() ([]PendingUpload, error) {
	return []PendingUpload{uploadControllerBoundaryPending()}, nil
}

func (c *uploadControllerBoundaryCore) localUploadSource(blockID uint64) (uploadLocalSource, uploadLocalAvailability) {
	source, ok := c.sources[blockID]
	if !ok {
		return nil, uploadLocalAvailability{status: uploadLocalAvailabilityMetadataLoss}
	}
	return source, readyUploadLocalAvailability()
}

type memoryUploadSource map[uploadObjectKind][]byte

func (s memoryUploadSource) Open(kind uploadObjectKind) (io.ReadCloser, int64, error) {
	data, ok := s[kind]
	if !ok {
		return nil, 0, fmt.Errorf("%w: missing %s upload source", backend.ErrPermanent, kind)
	}
	copied := append([]byte(nil), data...)
	return io.NopCloser(bytes.NewReader(copied)), int64(len(copied)), nil
}

type mismatchingHeadBackend struct {
	size int64
}

func (b *mismatchingHeadBackend) PutObject(_ context.Context, _ string, body io.Reader, size int64, _ backend.PutOpts) (backend.PutResult, error) {
	if _, err := io.Copy(io.Discard, body); err != nil {
		return backend.PutResult{}, fmt.Errorf("%w: read object: %w", backend.ErrPermanent, err)
	}
	b.size = size
	return backend.PutResult{Size: size, ETag: "put-validation"}, nil
}

func (b *mismatchingHeadBackend) HeadObject(context.Context, string) (backend.ObjectMeta, error) {
	return backend.ObjectMeta{Size: b.size, ETag: "head-validation"}, nil
}

func (b *mismatchingHeadBackend) GetObject(context.Context, string, backend.GetOpts) (io.ReadCloser, backend.ObjectMeta, error) {
	return nil, backend.ObjectMeta{}, backend.ErrPermanent
}

func (b *mismatchingHeadBackend) DeleteObject(context.Context, string) error {
	return backend.ErrPermanent
}

func (b *mismatchingHeadBackend) ListObjects(context.Context, string, backend.ListOpts) (backend.ObjectIterator, error) {
	return nil, backend.ErrPermanent
}

type cancelingPutBackend struct {
	cancel context.CancelFunc
}

func (b cancelingPutBackend) PutObject(ctx context.Context, _ string, body io.Reader, _ int64, _ backend.PutOpts) (backend.PutResult, error) {
	if _, err := io.Copy(io.Discard, body); err != nil {
		return backend.PutResult{}, fmt.Errorf("%w: read object: %w", backend.ErrPermanent, err)
	}
	b.cancel()
	return backend.PutResult{}, ctx.Err()
}

func (cancelingPutBackend) HeadObject(context.Context, string) (backend.ObjectMeta, error) {
	return backend.ObjectMeta{}, backend.ErrNotFound
}

func (cancelingPutBackend) GetObject(context.Context, string, backend.GetOpts) (io.ReadCloser, backend.ObjectMeta, error) {
	return nil, backend.ObjectMeta{}, backend.ErrPermanent
}

func (cancelingPutBackend) DeleteObject(context.Context, string) error {
	return backend.ErrPermanent
}

func (cancelingPutBackend) ListObjects(context.Context, string, backend.ListOpts) (backend.ObjectIterator, error) {
	return nil, backend.ErrPermanent
}

type successfulBoundaryBackend struct {
	objects map[string]backend.ObjectMeta
}

func newSuccessfulBoundaryBackend() *successfulBoundaryBackend {
	return &successfulBoundaryBackend{objects: make(map[string]backend.ObjectMeta)}
}

func (b *successfulBoundaryBackend) PutObject(_ context.Context, key string, body io.Reader, size int64, _ backend.PutOpts) (backend.PutResult, error) {
	if _, err := io.Copy(io.Discard, body); err != nil {
		return backend.PutResult{}, fmt.Errorf("%w: read object: %w", backend.ErrPermanent, err)
	}
	meta := backend.ObjectMeta{
		Size:        size,
		ETag:        "validation",
		ContentType: backend.DefaultContentType,
	}
	b.objects[key] = meta
	return backend.PutResult{Size: meta.Size, ETag: meta.ETag}, nil
}

func (b *successfulBoundaryBackend) HeadObject(_ context.Context, key string) (backend.ObjectMeta, error) {
	meta, ok := b.objects[key]
	if !ok {
		return backend.ObjectMeta{}, backend.ErrNotFound
	}
	return meta, nil
}

func (b *successfulBoundaryBackend) GetObject(context.Context, string, backend.GetOpts) (io.ReadCloser, backend.ObjectMeta, error) {
	return nil, backend.ObjectMeta{}, backend.ErrPermanent
}

func (b *successfulBoundaryBackend) DeleteObject(context.Context, string) error {
	return backend.ErrPermanent
}

func (b *successfulBoundaryBackend) ListObjects(context.Context, string, backend.ListOpts) (backend.ObjectIterator, error) {
	return nil, backend.ErrPermanent
}

type indexVerificationMismatchBackend struct {
	*successfulBoundaryBackend
}

func newIndexVerificationMismatchBackend() *indexVerificationMismatchBackend {
	return &indexVerificationMismatchBackend{successfulBoundaryBackend: newSuccessfulBoundaryBackend()}
}

func (b *indexVerificationMismatchBackend) HeadObject(ctx context.Context, key string) (backend.ObjectMeta, error) {
	meta, err := b.successfulBoundaryBackend.HeadObject(ctx, key)
	if err != nil {
		return backend.ObjectMeta{}, err
	}
	if strings.HasSuffix(key, ".idx") {
		meta.ETag = "mismatched-index-validation"
	}
	return meta, nil
}

func confirmUploadFromProposal(t *testing.T, data []byte) *scrapv1.ConfirmUpload {
	t.Helper()

	var cmd scrapv1.RaftCommand
	if err := proto.Unmarshal(data, &cmd); err != nil {
		t.Fatalf("unmarshal proposal: %v", err)
	}
	confirm := cmd.GetConfirmUpload()
	if confirm == nil {
		t.Fatal("proposal did not contain ConfirmUpload")
	}
	return confirm
}
