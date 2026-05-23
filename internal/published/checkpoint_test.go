package published

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/backend"
	publishedv1 "github.com/petabytecl/scrap/internal/gen/scrap/published/v1"
	"github.com/petabytecl/scrap/internal/metastore"
)

func TestVerifyCurrentCheckpointVerifiesManifestArtifactsAndRequiredObjects(t *testing.T) {
	ctx := context.Background()
	store := openPublisherBackend(t)
	blockObject, err := store.PutObject(ctx, "objects/block-1.blk", bytes.NewReader([]byte("block bytes")))
	if err != nil {
		t.Fatalf("put block object: %v", err)
	}
	indexObject, err := store.PutObject(ctx, "objects/block-1.idx", bytes.NewReader([]byte("index bytes")))
	if err != nil {
		t.Fatalf("put index object: %v", err)
	}
	envelopeObject, err := store.PutObject(ctx, "objects/block-1.env", bytes.NewReader([]byte("envelope bytes")))
	if err != nil {
		t.Fatalf("put envelope object: %v", err)
	}
	metadata := staticSnapshotMetadata{
		documents: []metastore.Document{publishedTestDocument("block-1", []byte("block bytes"))},
		intents: []metastore.UploadIntent{
			{
				BlockID:           "block-1",
				BackendObjectKey:  blockObject.Key,
				IndexObjectKey:    indexObject.Key,
				EnvelopeObjectKey: envelopeObject.Key,
				State:             metastore.UploadStateUploaded,
			},
		},
	}
	publication := publishTestSnapshot(t, ctx, store, metadata, "snapshot-1", "manifest-1", 7, 42, time.Unix(100, 0).UTC())

	verified, err := VerifyCurrentCheckpoint(ctx, store, "cell-a")
	if err != nil {
		t.Fatalf("verify current checkpoint: %v", err)
	}
	if verified.PointerKey != publication.PointerKey ||
		verified.ManifestKey != publication.ManifestKey ||
		verified.Pointer.GetManifestId() != "manifest-1" ||
		verified.Manifest.GetGeneration() != 7 ||
		verified.VerifiedObjects != 6 ||
		verified.VerifiedArtifacts != 1 ||
		verified.VerifiedRequiredObjects != 3 ||
		verified.VerifiedBlockObjects != 1 ||
		verified.VerifiedIndexObjects != 1 ||
		verified.VerifiedEnvelopeObjects != 1 {
		t.Fatalf("verified checkpoint = %#v, want pointer, manifest, snapshot, block, index, and envelope verified", verified)
	}
}

func TestVerifyCurrentCheckpointFailsWhenRequiredObjectIsMissing(t *testing.T) {
	ctx := context.Background()
	store := openPublisherBackend(t)
	manifest, err := BuildManifest(ManifestOptions{
		CellID:          "cell-a",
		SourceNamespace: "source-a",
		ManifestID:      "manifest-1",
		Generation:      7,
		PublishedAt:     time.Unix(100, 0).UTC(),
		RequiredObjects: []*publishedv1.ObjectRef{
			{
				Kind:      publishedv1.ObjectKind_OBJECT_KIND_BLOCK,
				ObjectKey: "objects/missing.blk",
				Length:    5,
				Sha256:    bytes.Repeat([]byte{1}, 32),
			},
		},
	})
	if err != nil {
		t.Fatalf("build manifest: %v", err)
	}
	manifestData, err := MarshalManifest(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	manifestKey, err := ManifestObjectKey("cell-a", "manifest-1")
	if err != nil {
		t.Fatalf("manifest key: %v", err)
	}
	if _, err := store.PutObject(ctx, manifestKey, bytes.NewReader(manifestData)); err != nil {
		t.Fatalf("put manifest: %v", err)
	}
	pointer, err := BuildCurrentPointer(manifest, manifestData)
	if err != nil {
		t.Fatalf("build current pointer: %v", err)
	}
	pointerData, err := MarshalCurrentPointer(pointer)
	if err != nil {
		t.Fatalf("marshal current pointer: %v", err)
	}
	pointerKey, err := CurrentPointerObjectKey("cell-a")
	if err != nil {
		t.Fatalf("pointer key: %v", err)
	}
	if _, err := store.PutMutableObject(ctx, pointerKey, bytes.NewReader(pointerData)); err != nil {
		t.Fatalf("put pointer: %v", err)
	}

	_, err = VerifyCurrentCheckpoint(ctx, store, "cell-a")
	if !errors.Is(err, backend.ErrNotFound) {
		t.Fatalf("verify error = %v, want missing required object", err)
	}
}
