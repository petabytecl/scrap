package encryption_test

// Malformed-envelope rejection matrix (#471c): the envelope validation branches
// are the only gate between attacker- or bitrot-controlled .idx bytes and a KMS
// round trip / AES-GCM setup, but most rejection branches were untested. Each
// case mutates one field of an otherwise valid envelope and asserts
// ParseEnvelope fails closed with ErrInvalidEnvelope.

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/petabytecl/scrap/internal/encryption"
)

func validEnvelopeForTest() encryption.Envelope {
	return encryption.Envelope{
		Version:          1,
		TransitMount:     "transit",
		TransitKey:       "scrap-documents",
		KeyVersion:       2,
		WrappedDataKey:   "vault:v2:wrapped-key",
		PayloadAlgorithm: "AES-256-GCM",
		NoncePrefix:      bytes.Repeat([]byte{0x11}, 8),
		PlaintextSHA256:  bytes.Repeat([]byte{0x22}, 32),
		PlaintextLength:  1024,
		CiphertextLength: 1040,
	}
}

func TestParseEnvelopeAcceptsValid(t *testing.T) {
	data, err := json.Marshal(validEnvelopeForTest())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := encryption.ParseEnvelope(data); err != nil {
		t.Fatalf("ParseEnvelope(valid) = %v, want nil", err)
	}
}

func TestParseEnvelopeRejectsMalformed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*encryption.Envelope)
	}{
		{name: "wrong version", mutate: func(e *encryption.Envelope) { e.Version = 2 }},
		{name: "empty transit mount", mutate: func(e *encryption.Envelope) { e.TransitMount = "" }},
		{name: "empty transit key", mutate: func(e *encryption.Envelope) { e.TransitKey = "" }},
		{name: "zero key version", mutate: func(e *encryption.Envelope) { e.KeyVersion = 0 }},
		{name: "negative key version", mutate: func(e *encryption.Envelope) { e.KeyVersion = -1 }},
		{name: "empty wrapped data key", mutate: func(e *encryption.Envelope) { e.WrappedDataKey = "" }},
		{name: "wrong payload algorithm", mutate: func(e *encryption.Envelope) { e.PayloadAlgorithm = "AES-128-GCM" }},
		{name: "short nonce prefix", mutate: func(e *encryption.Envelope) { e.NoncePrefix = bytes.Repeat([]byte{1}, 7) }},
		{name: "long nonce prefix", mutate: func(e *encryption.Envelope) { e.NoncePrefix = bytes.Repeat([]byte{1}, 9) }},
		{name: "nil nonce prefix", mutate: func(e *encryption.Envelope) { e.NoncePrefix = nil }},
		{name: "short plaintext sha", mutate: func(e *encryption.Envelope) { e.PlaintextSHA256 = bytes.Repeat([]byte{1}, 31) }},
		{name: "long plaintext sha", mutate: func(e *encryption.Envelope) { e.PlaintextSHA256 = bytes.Repeat([]byte{1}, 33) }},
		{name: "negative plaintext length", mutate: func(e *encryption.Envelope) { e.PlaintextLength = -1 }},
		{name: "negative ciphertext length", mutate: func(e *encryption.Envelope) { e.CiphertextLength = -1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envelope := validEnvelopeForTest()
			tt.mutate(&envelope)
			data, err := json.Marshal(envelope)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if _, err := encryption.ParseEnvelope(data); !errors.Is(err, encryption.ErrInvalidEnvelope) {
				t.Fatalf("ParseEnvelope error = %v, want ErrInvalidEnvelope", err)
			}
		})
	}
}

func TestParseEnvelopeRejectsEmptyAndGarbage(t *testing.T) {
	if _, err := encryption.ParseEnvelope(nil); !errors.Is(err, encryption.ErrInvalidEnvelope) {
		t.Fatalf("ParseEnvelope(nil) = %v, want ErrInvalidEnvelope", err)
	}
	if _, err := encryption.ParseEnvelope([]byte("{not json")); !errors.Is(err, encryption.ErrInvalidEnvelope) {
		t.Fatalf("ParseEnvelope(garbage) = %v, want ErrInvalidEnvelope", err)
	}
}
