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
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestVerifyCurrentCheckpointVerifiesManifestArtifactsAndRequiredObjects(t *testing.T) {
	ctx := context.Background()
	store := openPublisherBackend(t)
	blockObject, err := store.PutObject(ctx, "objects/block-1.blk", bytes.NewReader([]byte("block bytes")))
	testutil.RequireNoErrorf(t, err, "put block object")
	indexObject, err := store.PutObject(ctx, "objects/block-1.idx", bytes.NewReader([]byte("index bytes")))
	testutil.RequireNoErrorf(t, err, "put index object")
	envelopeObject, err := store.PutObject(ctx, "objects/block-1.env", bytes.NewReader([]byte("envelope bytes")))
	testutil.RequireNoErrorf(t, err, "put envelope object")
	metadata := staticSnapshotMetadata{
		documents: []metastore.Document{publishedTestDocument([]byte("block bytes"))},
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
	publication := publishTestSnapshot(ctx, t, store, metadata, "snapshot-1", "manifest-1", 7, 42, time.Unix(100, 0).UTC())

	verified, err := VerifyCurrentCheckpoint(ctx, store, "cell-a")
	testutil.RequireNoErrorf(t, err, "verify current checkpoint")
	requireVerifiedCheckpoint(t, verified, publication)
}

func requireVerifiedCheckpoint(t *testing.T, verified CheckpointVerification, publication SnapshotPublication) {
	t.Helper()
	testutil.RequireEqualf(t, verified.PointerKey, publication.PointerKey, "pointer key")
	testutil.RequireEqualf(t, verified.ManifestKey, publication.ManifestKey, "manifest key")
	testutil.RequireEqualf(t, verified.Pointer.GetManifestId(), "manifest-1", "pointer manifest id")
	testutil.RequireEqualf(t, verified.Manifest.GetGeneration(), uint64(7), "manifest generation")
	testutil.RequireEqualf(t, verified.VerifiedObjects, 6, "verified object count")
	testutil.RequireEqualf(t, verified.VerifiedArtifacts, 1, "verified artifact count")
	testutil.RequireEqualf(t, verified.VerifiedRequiredObjects, 3, "verified required object count")
	testutil.RequireEqualf(t, verified.VerifiedBlockObjects, 1, "verified block object count")
	testutil.RequireEqualf(t, verified.VerifiedIndexObjects, 1, "verified index object count")
	testutil.RequireEqualf(t, verified.VerifiedEnvelopeObjects, 1, "verified envelope object count")
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
	testutil.RequireNoErrorf(t, err, "build manifest")
	manifestData, err := MarshalManifest(manifest)
	testutil.RequireNoErrorf(t, err, "marshal manifest")
	manifestKey, err := ManifestObjectKey("cell-a", "manifest-1")
	testutil.RequireNoErrorf(t, err, "manifest key")
	if _, err := store.PutObject(ctx, manifestKey, bytes.NewReader(manifestData)); err != nil {
		t.Fatalf("put manifest: %v", err)
	}
	pointer, err := BuildCurrentPointer(manifest, manifestData)
	testutil.RequireNoErrorf(t, err, "build current pointer")
	pointerData, err := MarshalCurrentPointer(pointer)
	testutil.RequireNoErrorf(t, err, "marshal current pointer")
	pointerKey, err := CurrentPointerObjectKey("cell-a")
	testutil.RequireNoErrorf(t, err, "pointer key")
	if _, err := store.PutMutableObject(ctx, pointerKey, bytes.NewReader(pointerData)); err != nil {
		t.Fatalf("put pointer: %v", err)
	}

	_, err = VerifyCurrentCheckpoint(ctx, store, "cell-a")
	if !errors.Is(err, backend.ErrNotFound) {
		t.Fatalf("verify error = %v, want missing required object", err)
	}
}
