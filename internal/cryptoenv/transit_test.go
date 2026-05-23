package cryptoenv

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/backend"
)

func TestCreateEnvelopeRecordUsesTransitWrappedDEK(t *testing.T) {
	ctx := context.Background()
	transit := NewFakeTransit(map[string]uint32{"transit/backend": 7})
	blockObject := backend.Object{Key: "blocks/block-1.blk", Length: 128, SHA256: [32]byte{1, 2, 3}}

	material, err := CreateEnvelopeRecord(ctx, transit, EnvelopeRequest{
		BlockID:     "block-1",
		CellID:      "cell-a",
		BlockObject: blockObject,
		KeyID:       "transit/backend",
		CreatedAt:   time.Unix(100, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("create envelope: %v", err)
	}
	record := material.Record
	if record.GetEnvelopeId() != "openbao-transit:block-1" ||
		record.GetKeyId() != "transit/backend" ||
		record.GetKeyVersion() != 7 ||
		record.GetDekAlgorithm() != DefaultDEKAlgorithm ||
		record.GetAeadAlgorithm() != DefaultAEADAlgorithm ||
		len(record.GetEnvelopeSha256()) != 32 {
		t.Fatalf("record = %#v, want transit envelope fields", record)
	}
	if len(material.PlaintextDEK) == 0 {
		t.Fatal("plaintext DEK was not returned to caller")
	}
	if bytes.Equal(record.GetWrappedDek(), material.PlaintextDEK) {
		t.Fatal("envelope record stored plaintext DEK")
	}
	if err := ValidateEnvelopeRecordForRestore(ctx, transit, record); err != nil {
		t.Fatalf("validate restore material: %v", err)
	}
}

func TestValidateEnvelopeRecordForRestoreClassifiesUnavailableTransit(t *testing.T) {
	ctx := context.Background()
	transit := NewFakeTransit(map[string]uint32{"transit/backend": 1})
	material, err := CreateEnvelopeRecord(ctx, transit, EnvelopeRequest{
		BlockID:     "block-1",
		BlockObject: backend.Object{Key: "blocks/block-1.blk", Length: 128, SHA256: [32]byte{1}},
		KeyID:       "transit/backend",
		CreatedAt:   time.Unix(100, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("create envelope: %v", err)
	}
	transit.SetUnavailable(true)

	err = ValidateEnvelopeRecordForRestore(ctx, transit, material.Record)
	if !errors.Is(err, ErrUnavailable) || !IsUnavailable(err) {
		t.Fatalf("error = %v, want unavailable transit classification", err)
	}
}

func TestRewrapEnvelopeRecordIsDeterministicAndPreservesAAD(t *testing.T) {
	ctx := context.Background()
	transit := NewFakeTransit(map[string]uint32{
		"transit/backend-v1": 1,
		"transit/backend-v2": 2,
	})
	material, err := CreateEnvelopeRecord(ctx, transit, EnvelopeRequest{
		BlockID:     "block-1",
		CellID:      "cell-a",
		BlockObject: backend.Object{Key: "blocks/block-1.blk", Length: 128, SHA256: [32]byte{1}},
		KeyID:       "transit/backend-v1",
		CreatedAt:   time.Unix(100, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("create envelope: %v", err)
	}

	first, err := RewrapEnvelopeRecord(ctx, transit, material.Record, "transit/backend-v2", time.Unix(200, 0).UTC())
	if err != nil {
		t.Fatalf("rewrap first envelope: %v", err)
	}
	second, err := RewrapEnvelopeRecord(ctx, transit, material.Record, "transit/backend-v2", time.Unix(200, 0).UTC())
	if err != nil {
		t.Fatalf("rewrap second envelope: %v", err)
	}
	if first.GetEnvelopeId() != material.Record.GetEnvelopeId() ||
		first.GetKeyId() != "transit/backend-v2" ||
		first.GetKeyVersion() != 2 ||
		!bytes.Equal(first.GetAadContext(), material.Record.GetAadContext()) ||
		!bytes.Equal(first.GetWrappedDek(), second.GetWrappedDek()) ||
		!bytes.Equal(first.GetEnvelopeSha256(), second.GetEnvelopeSha256()) {
		t.Fatalf("rewrapped records = %#v / %#v, want deterministic rewrap preserving envelope identity and AAD", first, second)
	}
	if bytes.Equal(first.GetWrappedDek(), material.Record.GetWrappedDek()) {
		t.Fatal("rewrap did not change wrapped DEK")
	}
	if err := ValidateEnvelopeRecordForRestore(ctx, transit, first); err != nil {
		t.Fatalf("validate rewrapped material: %v", err)
	}

	keyless, err := RewrapEnvelopeRecord(ctx, keylessRewrapTransit{Transit: transit}, material.Record, "transit/backend-v2", time.Unix(200, 0).UTC())
	if err != nil {
		t.Fatalf("rewrap with keyless adapter response: %v", err)
	}
	if keyless.GetKeyId() != "transit/backend-v2" {
		t.Fatalf("keyless adapter rewrap key_id = %q, want destination fallback", keyless.GetKeyId())
	}
}

func TestFakeTransitRejectsAADOrAlgorithmMismatch(t *testing.T) {
	ctx := context.Background()
	transit := NewFakeTransit(map[string]uint32{
		"transit/backend-v1": 1,
		"transit/backend-v2": 2,
	})
	material, err := CreateEnvelopeRecord(ctx, transit, EnvelopeRequest{
		BlockID:     "block-1",
		CellID:      "cell-a",
		BlockObject: backend.Object{Key: "blocks/block-1.blk", Length: 128, SHA256: [32]byte{1}},
		KeyID:       "transit/backend-v1",
		CreatedAt:   time.Unix(100, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("create envelope: %v", err)
	}
	_, err = transit.UnwrapDataKey(ctx, UnwrapDataKeyRequest{
		KeyID:      material.Record.GetKeyId(),
		KeyVersion: material.Record.GetKeyVersion(),
		WrappedDEK: material.Record.GetWrappedDek(),
		AAD:        []byte("wrong aad"),
		Algorithm:  material.Record.GetDekAlgorithm(),
	})
	if !errors.Is(err, ErrKeyMaterialUnavailable) {
		t.Fatalf("unwrap error = %v, want key material unavailable for AAD mismatch", err)
	}
	_, err = transit.RewrapDataKey(ctx, RewrapDataKeyRequest{
		SourceKeyID:      material.Record.GetKeyId(),
		SourceKeyVersion: material.Record.GetKeyVersion(),
		DestinationKeyID: "transit/backend-v2",
		WrappedDEK:       material.Record.GetWrappedDek(),
		AAD:              material.Record.GetAadContext(),
		Algorithm:        "wrong-algorithm",
	})
	if !errors.Is(err, ErrKeyMaterialUnavailable) {
		t.Fatalf("rewrap error = %v, want key material unavailable for algorithm mismatch", err)
	}
}

type keylessRewrapTransit struct {
	Transit
}

func (t keylessRewrapTransit) RewrapDataKey(ctx context.Context, req RewrapDataKeyRequest) (WrappedKey, error) {
	wrapped, err := t.Transit.RewrapDataKey(ctx, req)
	wrapped.KeyID = ""
	return wrapped, err
}
