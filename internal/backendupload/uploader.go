package backendupload

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/blockstore"
	"github.com/petabytecl/scrap/internal/closeutil"
	storagev1 "github.com/petabytecl/scrap/internal/gen/scrap/storage/v1"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/storageformat"
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
	if err := u.validateUploadIntent(intent); err != nil {
		return UploadResult{}, err
	}
	reader, err := u.Source.OpenBlock(ctx, intent.BlockID)
	if err != nil {
		return UploadResult{}, err
	}
	defer closeutil.Ignore(reader)
	payload := reader
	var envelopeRecord *storagev1.EnvelopeRecord
	var indexOptions BlockIndexOptions
	if encryptor, ok := u.Envelope.(BlockPayloadEncryptor); ok && intent.EnvelopeObjectKey != "" {
		encrypted, err := encryptor.EncryptBlockPayload(ctx, intent, reader)
		if err != nil {
			return UploadResult{}, err
		}
		defer closeutil.Ignore(encrypted.Reader)
		payload = encrypted.Reader
		envelopeRecord = encrypted.Envelope
		indexOptions.EncryptedFrames = encrypted.Frames
	}
	blockObject, err := u.Backend.PutObject(ctx, intent.BackendObjectKey, payload)
	if err != nil {
		return UploadResult{}, err
	}
	result := UploadResult{Block: blockObject}
	if err := u.uploadEnvelope(ctx, intent, blockObject, envelopeRecord, &result); err != nil {
		return result, err
	}
	if intent.IndexObjectKey == "" {
		return result, u.verifyUpload(ctx, intent, result)
	}
	if err := u.uploadIndex(ctx, intent, blockObject, indexOptions, &result); err != nil {
		return result, err
	}
	return result, u.verifyUpload(ctx, intent, result)
}

func (u Uploader) validateUploadIntent(intent metastore.UploadIntent) error {
	if u.Backend == nil {
		return errors.New("backendupload: backend store is not configured")
	}
	if u.Source == nil {
		return errors.New("backendupload: block source is not configured")
	}
	if intent.BlockID == "" {
		return errors.New("backendupload: upload intent block id is required")
	}
	if intent.BackendObjectKey == "" {
		return errors.New("backendupload: upload intent backend object key is required")
	}
	if intent.EnvelopeObjectKey != "" && u.Envelope == nil {
		return errors.New("backendupload: block envelope source is not configured")
	}
	return u.validateEncryptedUploadIntent(intent)
}

func (u Uploader) validateEncryptedUploadIntent(intent metastore.UploadIntent) error {
	_, encryptsPayload := u.Envelope.(BlockPayloadEncryptor)
	if !encryptsPayload || intent.EnvelopeObjectKey == "" || intent.IndexObjectKey != "" {
		return nil
	}
	return errors.New("backendupload: encrypted block upload requires a block index object key")
}

func (u Uploader) uploadEnvelope(ctx context.Context, intent metastore.UploadIntent, blockObject backend.Object, record *storagev1.EnvelopeRecord, result *UploadResult) error {
	if intent.EnvelopeObjectKey == "" {
		return nil
	}
	var envelopeReader io.ReadCloser
	if record != nil {
		data, err := storageformat.MarshalEnvelopeRecord(record)
		if err != nil {
			return err
		}
		envelopeReader = io.NopCloser(bytes.NewReader(data))
	} else {
		var err error
		envelopeReader, err = u.Envelope.OpenBlockEnvelope(ctx, intent, blockObject)
		if err != nil {
			return err
		}
	}
	defer closeutil.Ignore(envelopeReader)
	envelopeObject, err := u.Backend.PutObject(ctx, intent.EnvelopeObjectKey, envelopeReader)
	if err != nil {
		return err
	}
	result.Envelope = &envelopeObject
	return nil
}

func (u Uploader) uploadIndex(ctx context.Context, intent metastore.UploadIntent, blockObject backend.Object, options BlockIndexOptions, result *UploadResult) error {
	if u.Index == nil {
		return errors.New("backendupload: block index source is not configured")
	}
	var indexReader io.ReadCloser
	var err error
	if indexed, ok := u.Index.(BlockIndexSourceWithOptions); ok {
		indexReader, err = indexed.OpenBlockIndexWithOptions(ctx, intent, blockObject, options)
	} else {
		indexReader, err = u.Index.OpenBlockIndex(ctx, intent, blockObject)
	}
	if err != nil {
		return err
	}
	defer closeutil.Ignore(indexReader)
	indexObject, err := u.Backend.PutObject(ctx, intent.IndexObjectKey, indexReader)
	if err != nil {
		return err
	}
	result.Index = &indexObject
	return nil
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
