package shard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/encryption"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func (s *Shard) appendDocumentPayload(
	ctx context.Context,
	txID string,
	docName string,
	contentType string,
	body io.Reader,
) (block.AppendResult, []byte, []byte, error) {
	if !s.encryption.enabled() {
		var bodyCopy bytes.Buffer
		result, err := s.blockWriter.AppendDocument(txID, docName, contentType, io.TeeReader(body, &bodyCopy))
		return result, bodyCopy.Bytes(), nil, err
	}

	encryptor, err := encryption.NewDocumentEncryptor(ctx, s.encryption.documentConfig(), encryption.DocumentIdentity{
		TransactionID: txID,
		DocumentName:  docName,
	}, body)
	if err != nil {
		return block.AppendResult{}, nil, nil, err
	}
	defer encryptor.Close()

	// Ciphertext frames stream straight into the Block; the single
	// replication buffer is the only whole-Document copy left on this path
	// (the peer replication transport still needs the body as bytes).
	var replication bytes.Buffer
	firstOffset, frameCount, err := s.blockWriter.AppendDocumentFrameSource(func() ([]byte, bool, error) {
		frame, last, err := encryptor.NextFrame()
		if err != nil {
			return nil, false, err
		}
		replication.Write(frame)
		return frame, last, nil
	})
	if err != nil {
		return block.AppendResult{}, nil, nil, err
	}
	info, err := encryptor.Finalize()
	if err != nil {
		return block.AppendResult{}, nil, nil, err
	}
	result := block.AppendResult{
		SHA256:           info.PlaintextSHA256,
		Size:             info.PlaintextSize,
		FrameCount:       frameCount,
		FirstFrameOffset: firstOffset,
	}
	return result, replication.Bytes(), info.Envelope, nil
}

func mapEncryptionError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, encryption.ErrUnavailable),
		errors.Is(err, encryption.ErrAuthDenied),
		errors.Is(err, encryption.ErrMissingKey),
		errors.Is(err, encryption.ErrMinimumVersion):
		return storeapi.NewUnavailable(storeapi.UnavailableReasonCryptoUnavailable, "key material unavailable")
	case errors.Is(err, encryption.ErrInvalidEnvelope), errors.Is(err, encryption.ErrIntegrity):
		return fmt.Errorf("%w: %w", storeapi.ErrDataLoss, err)
	case errors.Is(err, encryption.ErrInvalidRequest):
		return fmt.Errorf("%w: %w", storeapi.ErrDataLoss, err)
	default:
		return err
	}
}
