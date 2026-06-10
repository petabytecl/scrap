package shard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/encryption"
	"github.com/petabytecl/scrap/internal/rewrap"
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

	encrypted, err := encryption.EncryptDocument(ctx, s.encryption.documentConfig(), encryption.DocumentIdentity{
		TransactionID: txID,
		DocumentName:  docName,
	}, body)
	if err != nil {
		return block.AppendResult{}, nil, nil, err
	}

	result, err := s.blockWriter.AppendDocumentFrames(txID, docName, contentType, block.DocumentFrames{
		Payloads: encrypted.Frames,
		SHA256:   encrypted.PlaintextSHA256,
		Size:     encrypted.PlaintextSize,
	})
	if err != nil {
		return block.AppendResult{}, nil, nil, err
	}
	return result, joinFramePayloads(encrypted.Frames), encrypted.Envelope, nil
}

func joinFramePayloads(frames [][]byte) []byte {
	var total int
	for _, frame := range frames {
		total += len(frame)
	}
	if total == 0 {
		return nil
	}
	out := make([]byte, 0, total)
	for _, frame := range frames {
		out = append(out, frame...)
	}
	return out
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
		return fmt.Errorf("%w: %w", rewrap.ErrInvalidRequest, err)
	default:
		return err
	}
}
