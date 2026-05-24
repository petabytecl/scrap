package backendupload

import (
	"bytes"
	"context"
	"testing"

	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/cryptoenv"
	"github.com/petabytecl/scrap/internal/storageformat"
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestTransitBlockEnvelopeSourceStoresWrappedKeyMaterial(t *testing.T) {
	ctx := context.Background()
	blocks := openTestBlockStore(t)
	store := openTestBackendStore(t)
	record, err := blocks.Append(ctx, bytes.NewReader([]byte("transit envelope bytes")))
	testutil.RequireNoErrorf(t, err, "append block")
	if _, err := blocks.SealCurrent(ctx); err != nil {
		t.Fatalf("seal block: %v", err)
	}
	transit := cryptoenv.NewFakeTransit(map[string]uint32{"transit/backend": 4})
	intent := testUploadIntent(record.BlockID)
	intent.EnvelopeObjectKey = "objects/" + record.BlockID + ".env"

	if _, err := (Uploader{
		Backend: store,
		Source:  LocalBlockSource{Blocks: blocks},
		Index:   staticBlockIndexSource{body: []byte("index bytes")},
		Envelope: TransitBlockEnvelopeSource{
			Transit: transit,
			CellID:  "cell-a",
			KeyID:   "transit/backend",
		},
	}).UploadBlock(ctx, intent); err != nil {
		t.Fatalf("upload block: %v", err)
	}

	var got bytes.Buffer
	testutil.RequireNoErrorf(t, store.ReadObjectRange(ctx, intent.EnvelopeObjectKey, backend.Range{}, &got), "read envelope object")
	envelope, err := storageformat.UnmarshalEnvelopeRecord(got.Bytes())
	testutil.RequireNoErrorf(t, err, "unmarshal envelope")
	key, err := transit.UnwrapDataKey(ctx, cryptoenv.UnwrapDataKeyRequest{
		KeyID:      envelope.GetKeyId(),
		KeyVersion: envelope.GetKeyVersion(),
		WrappedDEK: envelope.GetWrappedDek(),
		AAD:        envelope.GetAadContext(),
		Algorithm:  envelope.GetDekAlgorithm(),
	})
	testutil.RequireNoErrorf(t, err, "unwrap envelope")
	if bytes.Equal(envelope.GetWrappedDek(), key.PlaintextDEK) {
		t.Fatal("envelope object stored plaintext DEK")
	}
	if envelope.GetKeyId() != "transit/backend" ||
		envelope.GetKeyVersion() != 4 ||
		envelope.GetAeadAlgorithm() != cryptoenv.DefaultAEADAlgorithm {
		t.Fatalf("envelope = %#v, want transit key/version and encrypted AEAD algorithm", envelope)
	}
}
