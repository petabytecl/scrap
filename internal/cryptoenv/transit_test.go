package cryptoenv

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/petabytecl/scrap/internal/backend"
	storagev1 "github.com/petabytecl/scrap/internal/gen/scrap/storage/v1"
	"github.com/petabytecl/scrap/internal/testutil"
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
	testutil.RequireNoErrorf(t, err, "create envelope")
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
	testutil.RequireNoErrorf(t, ValidateEnvelopeRecordForRestore(ctx, transit, record), "validate restore material")
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
	testutil.RequireNoErrorf(t, err, "create envelope")
	transit.SetUnavailable(true)

	err = ValidateEnvelopeRecordForRestore(ctx, transit, material.Record)
	if !errors.Is(err, ErrUnavailable) || !IsUnavailable(err) {
		t.Fatalf("error = %v, want unavailable transit classification", err)
	}
}

func TestUnwrapEnvelopeDataKeyCopiesPlaintextAndHandlesNoopRecords(t *testing.T) {
	ctx := context.Background()
	transit := NewFakeTransit(map[string]uint32{"transit/backend": 1})
	material, err := CreateEnvelopeRecord(ctx, transit, EnvelopeRequest{
		BlockID:     "block-1",
		CellID:      "cell-a",
		BlockObject: backend.Object{Key: "blocks/block-1.blk"},
		KeyID:       "transit/backend",
		CreatedAt:   time.Unix(100, 0).UTC(),
	})
	testutil.RequireNoErrorf(t, err, "create envelope")

	first, err := UnwrapEnvelopeDataKey(ctx, transit, material.Record)
	testutil.RequireNoErrorf(t, err, "unwrap first")
	first[0] ^= 0xff
	second, err := UnwrapEnvelopeDataKey(ctx, transit, material.Record)
	testutil.RequireNoErrorf(t, err, "unwrap second")
	if bytes.Equal(first, second) {
		t.Fatal("unwrapped plaintext DEK shared mutable backing storage")
	}

	noop, err := UnwrapEnvelopeDataKey(ctx, transit, &storagev1.EnvelopeRecord{AeadAlgorithm: "none"})
	testutil.RequireNoErrorf(t, err, "unwrap noop envelope")
	testutil.RequireNilf(t, noop, "noop envelope plaintext DEK")
	noop, err = UnwrapEnvelopeDataKey(ctx, nil, &storagev1.EnvelopeRecord{AeadAlgorithm: "none"})
	testutil.RequireNoErrorf(t, err, "unwrap noop envelope without transit")
	testutil.RequireNilf(t, noop, "noop envelope plaintext DEK without transit")

	_, err = UnwrapEnvelopeDataKey(ctx, transit, nil)
	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("nil record error = %v, want invalid envelope", err)
	}
}

func TestPayloadFrameSealOpenAuthenticatesCiphertextAndMetadata(t *testing.T) {
	ctx := context.Background()
	transit := NewFakeTransit(map[string]uint32{"transit/backend": 1})
	material, err := CreateEnvelopeRecord(ctx, transit, EnvelopeRequest{
		BlockID:     "block-1",
		CellID:      "cell-a",
		BlockObject: backend.Object{Key: "blocks/block-1.blk"},
		KeyID:       "transit/backend",
		CreatedAt:   time.Unix(100, 0).UTC(),
	})
	testutil.RequireNoErrorf(t, err, "create envelope")
	plaintext := []byte("backend frame plaintext")

	ciphertext, authTag, err := SealPayloadFrame(material.PlaintextDEK, material.Record, 3, 12, plaintext)
	testutil.RequireNoErrorf(t, err, "seal payload frame")
	testutil.RequireEqualf(t, len(authTag), 16, "auth tag length")
	testutil.RequireFalsef(t, bytes.Equal(ciphertext, plaintext), "ciphertext matched plaintext")

	opened, err := OpenPayloadFrame(material.PlaintextDEK, material.Record, 3, 12, uint64(len(plaintext)), ciphertext)
	testutil.RequireNoErrorf(t, err, "open payload frame")
	testutil.RequireTruef(t, bytes.Equal(opened, plaintext), "opened plaintext = %q, want %q", opened, plaintext)

	tampered := append([]byte(nil), ciphertext...)
	tampered[0] ^= 0xff
	_, err = OpenPayloadFrame(material.PlaintextDEK, material.Record, 3, 12, uint64(len(plaintext)), tampered)
	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("tampered ciphertext error = %v, want invalid envelope", err)
	}
	_, err = OpenPayloadFrame(material.PlaintextDEK, material.Record, 4, 12, uint64(len(plaintext)), ciphertext)
	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("wrong frame index error = %v, want invalid envelope", err)
	}
	_, err = OpenPayloadFrame(material.PlaintextDEK, material.Record, 3, 13, uint64(len(plaintext)), ciphertext)
	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("wrong plaintext offset error = %v, want invalid envelope", err)
	}
}

func TestPayloadFrameRejectsInvalidEnvelopeMaterial(t *testing.T) {
	ctx := context.Background()
	transit := NewFakeTransit(map[string]uint32{"transit/backend": 1})
	material, err := CreateEnvelopeRecord(ctx, transit, EnvelopeRequest{
		BlockID:     "block-1",
		BlockObject: backend.Object{Key: "blocks/block-1.blk"},
		KeyID:       "transit/backend",
		CreatedAt:   time.Unix(100, 0).UTC(),
	})
	testutil.RequireNoErrorf(t, err, "create envelope")

	_, _, err = SealPayloadFrame(nil, material.Record, 0, 0, []byte("frame"))
	if !errors.Is(err, ErrKeyMaterialUnavailable) {
		t.Fatalf("missing DEK error = %v, want key material unavailable", err)
	}
	_, _, err = SealPayloadFrame(material.PlaintextDEK, nil, 0, 0, []byte("frame"))
	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("nil record error = %v, want invalid envelope", err)
	}
	badDEK := proto.Clone(material.Record).(*storagev1.EnvelopeRecord)
	badDEK.DekAlgorithm = "aes-128"
	_, _, err = SealPayloadFrame(material.PlaintextDEK, badDEK, 0, 0, []byte("frame"))
	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("bad DEK algorithm error = %v, want invalid envelope", err)
	}
	badAEAD := proto.Clone(material.Record).(*storagev1.EnvelopeRecord)
	badAEAD.AeadAlgorithm = "chacha20-poly1305"
	_, _, err = SealPayloadFrame(material.PlaintextDEK, badAEAD, 0, 0, []byte("frame"))
	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("bad AEAD algorithm error = %v, want invalid envelope", err)
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
	testutil.RequireNoErrorf(t, err, "create envelope")

	first, err := RewrapEnvelopeRecord(ctx, transit, material.Record, "transit/backend-v2", time.Unix(200, 0).UTC())
	testutil.RequireNoErrorf(t, err, "rewrap first envelope")
	second, err := RewrapEnvelopeRecord(ctx, transit, material.Record, "transit/backend-v2", time.Unix(200, 0).UTC())
	testutil.RequireNoErrorf(t, err, "rewrap second envelope")
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
	testutil.RequireNoErrorf(t, ValidateEnvelopeRecordForRestore(ctx, transit, first), "validate rewrapped material")

	keyless, err := RewrapEnvelopeRecord(ctx, keylessRewrapTransit{Transit: transit}, material.Record, "transit/backend-v2", time.Unix(200, 0).UTC())
	testutil.RequireNoErrorf(t, err, "rewrap with keyless adapter response")
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
	testutil.RequireNoErrorf(t, err, "create envelope")
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
