package encryption_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"

	"github.com/petabytecl/scrap/internal/encryption"
	"github.com/petabytecl/scrap/internal/encryption/enctest"
)

func encryptTestDocument(t *testing.T, identity encryption.DocumentIdentity, body []byte) (encryption.DocumentConfig, enctest.EncryptedDocument) {
	t.Helper()
	cfg := encryption.DocumentConfig{Transit: encryption.NewFakeTransit(encryption.FakeConfig{})}
	doc, err := enctest.EncryptDocument(context.Background(), cfg, identity, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("EncryptDocument: %v", err)
	}
	return cfg, doc
}

func TestEncryptDocumentRoundTrips(t *testing.T) {
	identity := encryption.DocumentIdentity{TransactionID: "tx-1", DocumentName: "doc-a"}
	body := bytes.Repeat([]byte("scrap-payload-"), 20_000) // multi-frame

	cfg, doc := encryptTestDocument(t, identity, body)
	if len(doc.Frames) < 2 {
		t.Fatalf("frame count = %d, want multi-frame body", len(doc.Frames))
	}

	got, err := enctest.DecryptDocument(context.Background(), cfg.Transit, identity, doc.Envelope, doc.Frames, doc.PlaintextSHA256, doc.PlaintextSize)
	if err != nil {
		t.Fatalf("DecryptDocument: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatal("round-tripped plaintext does not match input")
	}
}

func TestDecryptDocumentRejectsTamperedFrame(t *testing.T) {
	identity := encryption.DocumentIdentity{TransactionID: "tx-1", DocumentName: "doc-a"}
	body := bytes.Repeat([]byte("scrap-payload-"), 20_000)
	cfg, doc := encryptTestDocument(t, identity, body)

	frames := cloneTestFrames(doc.Frames)
	frames[0][0] ^= 0xFF // flip a ciphertext bit

	_, err := enctest.DecryptDocument(context.Background(), cfg.Transit, identity, doc.Envelope, frames, doc.PlaintextSHA256, doc.PlaintextSize)
	if !errors.Is(err, encryption.ErrIntegrity) {
		t.Fatalf("DecryptDocument tampered frame = %v, want ErrIntegrity", err)
	}
}

func TestDecryptDocumentRejectsReorderedFrames(t *testing.T) {
	identity := encryption.DocumentIdentity{TransactionID: "tx-1", DocumentName: "doc-a"}
	body := bytes.Repeat([]byte("scrap-payload-"), 20_000)
	cfg, doc := encryptTestDocument(t, identity, body)

	frames := cloneTestFrames(doc.Frames)
	frames[0], frames[1] = frames[1], frames[0] // per-frame nonce+AAD binding must reject the swap

	_, err := enctest.DecryptDocument(context.Background(), cfg.Transit, identity, doc.Envelope, frames, doc.PlaintextSHA256, doc.PlaintextSize)
	if !errors.Is(err, encryption.ErrIntegrity) {
		t.Fatalf("DecryptDocument reordered frames = %v, want ErrIntegrity", err)
	}
}

func TestDecryptDocumentRejectsWrongIdentity(t *testing.T) {
	identity := encryption.DocumentIdentity{TransactionID: "tx-1", DocumentName: "doc-a"}
	body := []byte("small body")
	cfg, doc := encryptTestDocument(t, identity, body)

	// Identity is bound both at the transit key-context and the per-frame AAD.
	// A mismatched identity must fail; the fake enforces the key-context binding
	// first, so it surfaces before frame AAD verification is reached.
	other := encryption.DocumentIdentity{TransactionID: "tx-1", DocumentName: "doc-b"}
	got, err := enctest.DecryptDocument(context.Background(), cfg.Transit, other, doc.Envelope, doc.Frames, doc.PlaintextSHA256, doc.PlaintextSize)
	if err == nil {
		t.Fatal("DecryptDocument with wrong identity succeeded, want failure")
	}
	if got != nil {
		t.Fatal("DecryptDocument returned plaintext on identity mismatch")
	}
}

func TestDecryptDocumentRejectsMetadataMismatch(t *testing.T) {
	identity := encryption.DocumentIdentity{TransactionID: "tx-1", DocumentName: "doc-a"}
	body := []byte("small body")
	cfg, doc := encryptTestDocument(t, identity, body)

	var wrongSHA [sha256.Size]byte
	wrongSHA[0] = doc.PlaintextSHA256[0] ^ 0xFF
	_, err := enctest.DecryptDocument(context.Background(), cfg.Transit, identity, doc.Envelope, doc.Frames, wrongSHA, doc.PlaintextSize)
	if !errors.Is(err, encryption.ErrIntegrity) {
		t.Fatalf("DecryptDocument wrong SHA = %v, want ErrIntegrity", err)
	}

	_, err = enctest.DecryptDocument(context.Background(), cfg.Transit, identity, doc.Envelope, doc.Frames, doc.PlaintextSHA256, doc.PlaintextSize+1)
	if !errors.Is(err, encryption.ErrIntegrity) {
		t.Fatalf("DecryptDocument wrong size = %v, want ErrIntegrity", err)
	}
}

func TestDecryptDocumentRejectsMalformedEnvelope(t *testing.T) {
	identity := encryption.DocumentIdentity{TransactionID: "tx-1", DocumentName: "doc-a"}
	var sha [sha256.Size]byte

	for _, tt := range []struct {
		name     string
		envelope []byte
	}{
		{name: "empty", envelope: nil},
		{name: "not json", envelope: []byte("{not-json")},
		{name: "truncated nonce", envelope: envelopeWithShortNonce(t)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := encryption.DocumentConfig{Transit: encryption.NewFakeTransit(encryption.FakeConfig{})}
			_, err := enctest.DecryptDocument(context.Background(), cfg.Transit, identity, tt.envelope, nil, sha, 0)
			if !errors.Is(err, encryption.ErrInvalidEnvelope) {
				t.Fatalf("DecryptDocument %s = %v, want ErrInvalidEnvelope", tt.name, err)
			}
		})
	}
}

func envelopeWithShortNonce(t *testing.T) []byte {
	t.Helper()
	identity := encryption.DocumentIdentity{TransactionID: "tx-1", DocumentName: "doc-a"}
	_, doc := encryptTestDocument(t, identity, []byte("body"))

	var raw map[string]any
	if err := json.Unmarshal(doc.Envelope, &raw); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	raw["nonce_prefix"] = []byte{0x01, 0x02} // shorter than noncePrefixSize
	out, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal tampered envelope: %v", err)
	}
	return out
}

func cloneTestFrames(frames [][]byte) [][]byte {
	out := make([][]byte, len(frames))
	for i, frame := range frames {
		out[i] = append([]byte(nil), frame...)
	}
	return out
}
