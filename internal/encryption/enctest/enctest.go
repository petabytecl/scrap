// Package enctest provides buffering document-encryption helpers for tests.
// They materialize whole Documents in memory, which production code must
// never do (CONTEXT.md bounded-memory invariant, ADR 0028): the streaming
// DocumentEncryptor/DocumentDecryptor in internal/encryption are the only
// production API. Keeping these helpers out of the production package makes
// the unbounded-memory path impossible to reintroduce by accident (#458).
package enctest

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"

	"github.com/petabytecl/scrap/internal/encryption"
)

// EncryptedDocument is a fully buffered encrypted Document fixture.
type EncryptedDocument struct {
	Envelope        []byte
	Frames          [][]byte
	PlaintextSHA256 [sha256.Size]byte
	PlaintextSize   int64
	CiphertextSize  int64
}

// EncryptDocument streams body through a DocumentEncryptor and buffers every
// ciphertext frame in memory.
func EncryptDocument(ctx context.Context, cfg encryption.DocumentConfig, identity encryption.DocumentIdentity, body io.Reader) (EncryptedDocument, error) {
	encryptor, err := encryption.NewDocumentEncryptor(ctx, cfg, identity, body)
	if err != nil {
		return EncryptedDocument{}, err
	}
	defer encryptor.Close()

	var frames [][]byte
	for {
		frame, _, err := encryptor.NextFrame()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return EncryptedDocument{}, err
		}
		frames = append(frames, frame)
	}
	info, err := encryptor.Finalize()
	if err != nil {
		return EncryptedDocument{}, err
	}
	return EncryptedDocument{
		Envelope:        info.Envelope,
		Frames:          frames,
		PlaintextSHA256: info.PlaintextSHA256,
		PlaintextSize:   info.PlaintextSize,
		CiphertextSize:  info.CiphertextSize,
	}, nil
}

// DecryptDocument streams frames through a DocumentDecryptor and buffers the
// whole plaintext in memory.
func DecryptDocument(
	ctx context.Context,
	transit encryption.Transit,
	identity encryption.DocumentIdentity,
	envelopeBytes []byte,
	frames [][]byte,
	expectedSHA [sha256.Size]byte,
	expectedSize int64,
) ([]byte, error) {
	decryptor, err := encryption.NewDocumentDecryptor(ctx, transit, identity, envelopeBytes, expectedSHA, expectedSize)
	if err != nil {
		return nil, err
	}
	defer decryptor.Close()

	reader := decryptor.Reader(encryption.NewSliceFrameSource(frames))
	defer func() { _ = reader.Close() }()
	plaintext, err := io.ReadAll(reader)
	if err != nil {
		// Never hand back a partial plaintext prefix from a failed decrypt:
		// the pre-streaming helper discarded buffered bytes on any error, and
		// a test observing bytes from an integrity failure would be testing
		// the wrong contract.
		return nil, err
	}
	return plaintext, nil
}
