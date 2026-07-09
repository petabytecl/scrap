package shard

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/petabytecl/scrap/internal/encryption"
	"github.com/petabytecl/scrap/internal/rewrap"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func TestRewrapKeyVersion32BoundsCheck(t *testing.T) {
	got, err := rewrapKeyVersion32(math.MaxInt32)
	if err != nil {
		t.Fatalf("rewrapKeyVersion32 valid: %v", err)
	}
	if got != math.MaxInt32 {
		t.Fatalf("rewrapKeyVersion32 valid = %d, want %d", got, math.MaxInt32)
	}

	for _, version := range []int{-1, math.MaxInt32 + 1} {
		_, err := rewrapKeyVersion32(version)
		if !errors.Is(err, storeapi.ErrDataLoss) {
			t.Fatalf("rewrapKeyVersion32(%d) error = %v, want ErrDataLoss", version, err)
		}
	}
}

func TestRewrapDocumentEnvelopeRejectsDowngrade(t *testing.T) {
	req := rewrap.Request{
		TransactionID: "tx-1",
		DocumentName:  "doc.xml",
		KeyVersion:    1,
	}
	envelope := encryption.Envelope{
		Version:          encryption.EnvelopeVersion,
		TransitMount:     encryption.DefaultTransitMountPath,
		TransitKey:       encryption.DefaultTransitKeyName,
		KeyVersion:       3,
		WrappedDataKey:   "fake",
		PayloadAlgorithm: encryption.PayloadAlgorithmAES256GCM,
		NoncePrefix:      make([]byte, 8),
		PlaintextSHA256:  make([]byte, 32),
		PlaintextLength:  1,
		CiphertextLength: 1,
	}
	s := &Shard{encryption: EncryptionConfig{Transit: encryption.NewFakeTransit(encryption.FakeConfig{})}}
	_, err := s.rewrapDocumentEnvelope(context.Background(), req, 1, envelope, rewrap.Result{
		OldKeyVersion: 3,
		NewKeyVersion: 3,
	})
	if !errors.Is(err, rewrap.ErrInvalidRequest) {
		t.Fatalf("rewrapDocumentEnvelope downgrade = %v, want ErrInvalidRequest", err)
	}
}
