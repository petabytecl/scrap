package backendupload

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/blockstore"
	"github.com/petabytecl/scrap/internal/cryptoenv"
	storagev1 "github.com/petabytecl/scrap/internal/gen/scrap/storage/v1"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/safeconv"
	"github.com/petabytecl/scrap/internal/storageformat"
)

const (
	localNoopEnvelopeKeyID      = "local-non-production/noop"
	localNoopEnvelopeKeyVersion = 1
)

type BlockEnvelopeSource interface {
	OpenBlockEnvelope(context.Context, metastore.UploadIntent, backend.Object) (io.ReadCloser, error)
}

type BlockPayloadEncryptor interface {
	EncryptBlockPayload(context.Context, metastore.UploadIntent, io.Reader) (EncryptedBlockPayload, error)
}

type EncryptedBlockPayload struct {
	Reader   io.ReadCloser
	Envelope *storagev1.EnvelopeRecord
	Frames   []EncryptedFrame
}

type EncryptedFrame struct {
	FrameIndex      uint32
	PlaintextOffset uint64
	PlaintextLength uint64
	StoredOffset    uint64
	StoredLength    uint64
	PlaintextSHA256 [32]byte
	StoredSHA256    [32]byte
	AuthTag         []byte
}

type LocalBlockEnvelopeSource struct {
	CellID string
	KeyID  string
}

type TransitBlockEnvelopeSource struct {
	Transit cryptoenv.Transit
	CellID  string
	KeyID   string
	TempDir string
}

func (s LocalBlockEnvelopeSource) OpenBlockEnvelope(ctx context.Context, intent metastore.UploadIntent, blockObject backend.Object) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	record, err := buildLocalEnvelopeRecord(intent, blockObject, s.CellID, s.KeyID)
	if err != nil {
		return nil, err
	}
	data, err := storageformat.MarshalEnvelopeRecord(record)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (s TransitBlockEnvelopeSource) OpenBlockEnvelope(ctx context.Context, intent metastore.UploadIntent, blockObject backend.Object) (io.ReadCloser, error) {
	material, err := cryptoenv.CreateEnvelopeRecord(ctx, s.Transit, cryptoenv.EnvelopeRequest{
		BlockID:     intent.BlockID,
		CellID:      s.CellID,
		BlockObject: blockObject,
		KeyID:       s.KeyID,
		CreatedAt:   intent.UpdatedAt,
	})
	if err != nil {
		return nil, err
	}
	data, err := storageformat.MarshalEnvelopeRecord(material.Record)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (s TransitBlockEnvelopeSource) EncryptBlockPayload(ctx context.Context, intent metastore.UploadIntent, plaintext io.Reader) (EncryptedBlockPayload, error) {
	if err := ctx.Err(); err != nil {
		return EncryptedBlockPayload{}, err
	}
	material, err := cryptoenv.CreateEnvelopeRecord(ctx, s.Transit, cryptoenv.EnvelopeRequest{
		BlockID:     intent.BlockID,
		CellID:      s.CellID,
		BlockObject: backend.Object{Key: intent.BackendObjectKey},
		KeyID:       s.KeyID,
		CreatedAt:   intent.UpdatedAt,
	})
	if err != nil {
		return EncryptedBlockPayload{}, err
	}
	tempDir, err := s.ensureTempDir()
	if err != nil {
		return EncryptedBlockPayload{}, err
	}
	tmp, err := os.CreateTemp(tempDir, "scrap-backend-block-*.enc")
	if err != nil {
		return EncryptedBlockPayload{}, err
	}
	frames, err := encryptBlockFrames(tmp, plaintext, material)
	if err != nil {
		_ = tmp.Close()
		removeTempFile(tmp.Name())
		return EncryptedBlockPayload{}, err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		_ = tmp.Close()
		removeTempFile(tmp.Name())
		return EncryptedBlockPayload{}, err
	}
	return EncryptedBlockPayload{
		Reader:   removeOnCloseFile{File: tmp, path: tmp.Name()},
		Envelope: material.Record,
		Frames:   frames,
	}, nil
}

func (s TransitBlockEnvelopeSource) ensureTempDir() (string, error) {
	if s.TempDir == "" {
		return "", nil
	}
	if err := os.MkdirAll(s.TempDir, 0o700); err != nil {
		return "", err
	}
	return s.TempDir, nil
}

func encryptBlockFrames(writer io.Writer, plaintext io.Reader, material cryptoenv.EnvelopeMaterial) ([]EncryptedFrame, error) {
	buf := make([]byte, blockstore.DefaultFrameSize)
	var frames []EncryptedFrame
	var frameIndex uint32
	var plaintextOffset uint64
	var storedOffset uint64
	for {
		n, readErr := plaintext.Read(buf)
		if n > 0 {
			frame, err := writeEncryptedBlockFrame(writer, material, frameIndex, plaintextOffset, storedOffset, buf[:n])
			if err != nil {
				return nil, err
			}
			frames = append(frames, frame)
			frameIndex++
			plaintextOffset += frame.PlaintextLength
			storedOffset += frame.StoredLength
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	if len(frames) == 0 {
		return nil, errors.New("backendupload: encrypted block payload is empty")
	}
	return frames, nil
}

func writeEncryptedBlockFrame(writer io.Writer, material cryptoenv.EnvelopeMaterial, frameIndex uint32, plaintextOffset, storedOffset uint64, plaintext []byte) (EncryptedFrame, error) {
	framePlaintext := append([]byte(nil), plaintext...)
	plaintextLength, err := safeconv.IntToUint64("encrypted frame plaintext length", len(framePlaintext))
	if err != nil {
		return EncryptedFrame{}, err
	}
	ciphertext, authTag, err := cryptoenv.SealPayloadFrame(material.PlaintextDEK, material.Record, frameIndex, plaintextOffset, framePlaintext)
	if err != nil {
		return EncryptedFrame{}, err
	}
	ciphertextLength, err := safeconv.IntToUint64("encrypted frame ciphertext length", len(ciphertext))
	if err != nil {
		return EncryptedFrame{}, err
	}
	written, err := writer.Write(ciphertext)
	if err != nil {
		return EncryptedFrame{}, err
	}
	if written != len(ciphertext) {
		return EncryptedFrame{}, io.ErrShortWrite
	}
	return EncryptedFrame{
		FrameIndex:      frameIndex,
		PlaintextOffset: plaintextOffset,
		PlaintextLength: plaintextLength,
		StoredOffset:    storedOffset,
		StoredLength:    ciphertextLength,
		PlaintextSHA256: sha256.Sum256(framePlaintext),
		StoredSHA256:    sha256.Sum256(ciphertext),
		AuthTag:         append([]byte(nil), authTag...),
	}, nil
}

func removeTempFile(path string) {
	// #nosec G703 -- path comes directly from os.CreateTemp in the process temp dir.
	_ = os.Remove(path)
}

type removeOnCloseFile struct {
	*os.File
	path string
}

func (f removeOnCloseFile) Close() error {
	closeErr := f.File.Close()
	// #nosec G703 -- path comes directly from os.CreateTemp in the process temp dir.
	removeErr := os.Remove(f.path)
	if closeErr != nil {
		return closeErr
	}
	return removeErr
}

func buildLocalEnvelopeRecord(intent metastore.UploadIntent, blockObject backend.Object, cellID, keyID string) (*storagev1.EnvelopeRecord, error) {
	if intent.BlockID == "" {
		return nil, errors.New("backendupload: upload intent block id is required")
	}
	if blockObject.Key == "" {
		return nil, errors.New("backendupload: uploaded block object is required")
	}
	if cellID == "" {
		cellID = "local"
	}
	if keyID == "" {
		keyID = localNoopEnvelopeKeyID
	}
	createdAt := intent.UpdatedAt
	if createdAt.IsZero() {
		createdAt = time.Unix(0, 0).UTC()
	}
	record := &storagev1.EnvelopeRecord{
		SchemaVersion: storageformat.CurrentSchemaVersion,
		EnvelopeId:    "local-noop:" + intent.BlockID,
		BlockId:       intent.BlockID,
		KeyId:         keyID,
		KeyVersion:    localNoopEnvelopeKeyVersion,
		WrappedDek:    []byte("local-non-production-noop"),
		DekAlgorithm:  "none",
		AeadAlgorithm: "none",
		AadContext:    localEnvelopeAAD(cellID, intent.BlockID, blockObject),
		CreatedAt:     timestamppb.New(createdAt),
	}
	digest, err := storageformat.EnvelopeRecordSHA256(record)
	if err != nil {
		return nil, err
	}
	record.EnvelopeSha256 = digest
	return record, nil
}

func localEnvelopeAAD(cellID, blockID string, blockObject backend.Object) []byte {
	return cryptoenv.EnvelopeAAD(cellID, blockID, blockObject)
}
