package encryption_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/petabytecl/scrap/internal/encryption"
)

func TestFakeTransitSupportsDataKeyUnwrapAndRewrap(t *testing.T) {
	ctx := context.Background()
	transit := encryption.NewFakeTransit(encryption.FakeConfig{
		KeyName: "scrap-documents",
	})

	dataKey := generateFakeDataKey(t, transit)
	assertFakeUnwrap(t, transit, dataKey.WrappedKey, dataKey.Plaintext)

	transit.Rotate()
	rewrapped, err := transit.RewrapDataKey(ctx, encryption.RewrapDataKeyRequest{
		WrappedKey: dataKey.WrappedKey,
		Context:    []byte("transaction-a/document-a"),
	})
	if err != nil {
		t.Fatalf("RewrapDataKey: %v", err)
	}
	if !rewrapped.Changed || rewrapped.WrappedKey == dataKey.WrappedKey || rewrapped.Version != 2 {
		t.Fatalf("rewrapped = %+v, want changed version 2", rewrapped)
	}
	assertFakeUnwrap(t, transit, rewrapped.WrappedKey, dataKey.Plaintext)
}

func TestFakeTransitUnwrapsAcrossInstances(t *testing.T) {
	writer := encryption.NewFakeTransit(encryption.FakeConfig{
		KeyName: "scrap-documents",
	})
	reader := encryption.NewFakeTransit(encryption.FakeConfig{
		KeyName: "scrap-documents",
	})

	dataKey := generateFakeDataKey(t, writer)
	assertFakeUnwrap(t, reader, dataKey.WrappedKey, dataKey.Plaintext)
}

func generateFakeDataKey(t *testing.T, transit *encryption.FakeTransit) encryption.DataKey {
	t.Helper()
	dataKey, err := transit.GenerateDataKey(context.Background(), encryption.GenerateDataKeyRequest{
		Context: []byte("transaction-a/document-a"),
		Bits:    256,
	})
	if err != nil {
		t.Fatalf("GenerateDataKey: %v", err)
	}
	if len(dataKey.Plaintext) != 32 {
		t.Fatalf("plaintext data key length = %d, want 32", len(dataKey.Plaintext))
	}
	if dataKey.WrappedKey == "" || dataKey.Version != 1 {
		t.Fatalf("data key = %+v, want wrapped version 1", dataKey)
	}
	return dataKey
}

func assertFakeUnwrap(t *testing.T, transit *encryption.FakeTransit, wrappedKey string, wantPlaintext []byte) {
	t.Helper()
	unwrapped, err := transit.UnwrapDataKey(context.Background(), encryption.UnwrapDataKeyRequest{
		WrappedKey: wrappedKey,
		Context:    []byte("transaction-a/document-a"),
	})
	if err != nil {
		t.Fatalf("UnwrapDataKey: %v", err)
	}
	if !bytes.Equal(unwrapped.Plaintext, wantPlaintext) {
		t.Fatal("UnwrapDataKey returned different plaintext")
	}
}

func TestFakeTransitFailsClosedWithTypedErrors(t *testing.T) {
	tests := []struct {
		name string
		cfg  encryption.FakeConfig
		want error
	}{
		{name: "outage", cfg: encryption.FakeConfig{Unavailable: true}, want: encryption.ErrUnavailable},
		{name: "auth denied", cfg: encryption.FakeConfig{AuthDenied: true}, want: encryption.ErrAuthDenied},
		{name: "missing key", cfg: encryption.FakeConfig{MissingKey: true}, want: encryption.ErrMissingKey},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transit := encryption.NewFakeTransit(tt.cfg)
			_, err := transit.GenerateDataKey(context.Background(), encryption.GenerateDataKeyRequest{Bits: 256})
			if !errors.Is(err, tt.want) {
				t.Fatalf("GenerateDataKey error = %v, want %v", err, tt.want)
			}
			if got := encryption.ErrorClass(err); got == "" {
				t.Fatalf("ErrorClass(%v) is empty", err)
			}
		})
	}
}

func TestFakeTransitMinimumVersionFailure(t *testing.T) {
	ctx := context.Background()
	transit := encryption.NewFakeTransit(encryption.FakeConfig{})
	dataKey, err := transit.GenerateDataKey(ctx, encryption.GenerateDataKeyRequest{Bits: 256})
	if err != nil {
		t.Fatalf("GenerateDataKey: %v", err)
	}

	transit.RequireMinimumVersion(2)
	_, err = transit.UnwrapDataKey(ctx, encryption.UnwrapDataKeyRequest{WrappedKey: dataKey.WrappedKey})
	if !errors.Is(err, encryption.ErrMinimumVersion) {
		t.Fatalf("UnwrapDataKey error = %v, want minimum version", err)
	}
	if got := encryption.ErrorClass(err); got != encryption.ClassMinimumVersion {
		t.Fatalf("ErrorClass() = %q, want %q", got, encryption.ClassMinimumVersion)
	}
}

func TestFakeTransitRejectsFutureRewrapVersion(t *testing.T) {
	ctx := context.Background()
	transit := encryption.NewFakeTransit(encryption.FakeConfig{})
	dataKey, err := transit.GenerateDataKey(ctx, encryption.GenerateDataKeyRequest{Bits: 256})
	if err != nil {
		t.Fatalf("GenerateDataKey: %v", err)
	}

	_, err = transit.RewrapDataKey(ctx, encryption.RewrapDataKeyRequest{
		WrappedKey: dataKey.WrappedKey,
		KeyVersion: dataKey.Version + 1,
	})
	if !errors.Is(err, encryption.ErrInvalidRequest) {
		t.Fatalf("RewrapDataKey error = %v, want invalid request", err)
	}
}

func TestFakeTransitCannotSatisfyProduction(t *testing.T) {
	transit := encryption.NewFakeTransit(encryption.FakeConfig{})
	if encryption.ProductionCapable(transit) {
		t.Fatal("fake Transit reported production capable")
	}
}
