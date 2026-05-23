package backendupload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/blockstore"
	"github.com/petabytecl/scrap/internal/closeutil"
	"github.com/petabytecl/scrap/internal/metastore"
)

type BlockSource interface {
	OpenBlock(context.Context, string) (io.ReadCloser, error)
}

type LocalBlockSource struct {
	Blocks *blockstore.Store
}

func (s LocalBlockSource) OpenBlock(ctx context.Context, blockID string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.Blocks == nil {
		return nil, errors.New("backendupload: block source is not configured")
	}
	sealed, err := s.Blocks.IsSealed(blockID)
	if err != nil {
		return nil, err
	}
	if !sealed {
		return nil, blockstore.ErrBlockOpen
	}
	file, err := os.Open(s.Blocks.BlockPath(blockID))
	if err != nil {
		return nil, err
	}
	return file, nil
}

type Uploader struct {
	Backend  backend.Store
	Source   BlockSource
	Index    BlockIndexSource
	Envelope BlockEnvelopeSource
}

type UploadResult struct {
	Block    backend.Object
	Index    *backend.Object
	Envelope *backend.Object
}

func (u Uploader) UploadBlock(ctx context.Context, intent metastore.UploadIntent) (UploadResult, error) {
	if u.Backend == nil {
		return UploadResult{}, errors.New("backendupload: backend store is not configured")
	}
	if u.Source == nil {
		return UploadResult{}, errors.New("backendupload: block source is not configured")
	}
	if intent.BlockID == "" {
		return UploadResult{}, errors.New("backendupload: upload intent block id is required")
	}
	if intent.BackendObjectKey == "" {
		return UploadResult{}, errors.New("backendupload: upload intent backend object key is required")
	}
	if intent.EnvelopeObjectKey != "" && u.Envelope == nil {
		return UploadResult{}, errors.New("backendupload: block envelope source is not configured")
	}
	reader, err := u.Source.OpenBlock(ctx, intent.BlockID)
	if err != nil {
		return UploadResult{}, err
	}
	defer closeutil.Ignore(reader)
	blockObject, err := u.Backend.PutObject(ctx, intent.BackendObjectKey, reader)
	if err != nil {
		return UploadResult{}, err
	}
	result := UploadResult{Block: blockObject}
	if intent.EnvelopeObjectKey != "" {
		envelopeReader, err := u.Envelope.OpenBlockEnvelope(ctx, intent, blockObject)
		if err != nil {
			return result, err
		}
		defer closeutil.Ignore(envelopeReader)
		envelopeObject, err := u.Backend.PutObject(ctx, intent.EnvelopeObjectKey, envelopeReader)
		if err != nil {
			return result, err
		}
		result.Envelope = &envelopeObject
	}
	if intent.IndexObjectKey == "" {
		return result, u.verifyUpload(ctx, intent, result)
	}
	if u.Index == nil {
		return result, errors.New("backendupload: block index source is not configured")
	}
	indexReader, err := u.Index.OpenBlockIndex(ctx, intent, blockObject)
	if err != nil {
		return result, err
	}
	defer closeutil.Ignore(indexReader)
	indexObject, err := u.Backend.PutObject(ctx, intent.IndexObjectKey, indexReader)
	if err != nil {
		return result, err
	}
	result.Index = &indexObject
	return result, u.verifyUpload(ctx, intent, result)
}

func (u Uploader) verifyUpload(ctx context.Context, intent metastore.UploadIntent, result UploadResult) error {
	if err := u.verifyObject(ctx, intent.BackendObjectKey, result.Block); err != nil {
		return err
	}
	if intent.IndexObjectKey != "" {
		if result.Index == nil {
			return errors.New("backendupload: uploaded block index object is missing")
		}
		if err := u.verifyObject(ctx, intent.IndexObjectKey, *result.Index); err != nil {
			return err
		}
	}
	if intent.EnvelopeObjectKey != "" {
		if result.Envelope == nil {
			return errors.New("backendupload: uploaded block envelope object is missing")
		}
		if err := u.verifyObject(ctx, intent.EnvelopeObjectKey, *result.Envelope); err != nil {
			return err
		}
	}
	return nil
}

func (u Uploader) verifyObject(ctx context.Context, key string, expected backend.Object) error {
	if key == "" {
		return errors.New("backendupload: backend object key is required for verification")
	}
	if expected.Key != key {
		return fmt.Errorf("backendupload: verify object %q: upload result key %q does not match expected key: %w", key, expected.Key, backend.ErrChecksumMismatch)
	}
	actual, err := u.Backend.HeadObject(ctx, key)
	if err != nil {
		return fmt.Errorf("backendupload: verify object %q: head object: %w", key, err)
	}
	if actual.Key != expected.Key {
		return fmt.Errorf("backendupload: verify object %q: metadata key %q does not match upload result key %q: %w", key, actual.Key, expected.Key, backend.ErrChecksumMismatch)
	}
	if actual.Length != expected.Length {
		return fmt.Errorf("backendupload: verify object %q: metadata length %d does not match upload result length %d: %w", key, actual.Length, expected.Length, backend.ErrChecksumMismatch)
	}
	if actual.SHA256 != expected.SHA256 {
		return fmt.Errorf("backendupload: verify object %q: metadata sha256 %x does not match upload result sha256 %x: %w", key, actual.SHA256, expected.SHA256, backend.ErrChecksumMismatch)
	}
	return nil
}
