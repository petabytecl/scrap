package published

import (
	"bytes"
	"crypto/sha256"
	"testing"
	"time"

	publishedv1 "github.com/petabytecl/scrap/internal/gen/scrap/published/v1"
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestPublishedMetadataObjectKeysFollowLayout(t *testing.T) {
	pointerKey, err := CurrentPointerObjectKey("cell-a")
	testutil.RequireNoErrorf(t, err, "current pointer key")
	manifestKey, err := ManifestObjectKey("cell-a", "manifest-1")
	testutil.RequireNoErrorf(t, err, "manifest key")
	snapshotKey, err := SnapshotObjectKey("cell-a", "local", "snapshot-1")
	testutil.RequireNoErrorf(t, err, "snapshot key")
	tailKey, err := TailObjectKey("cell-a", "local", 7, 12)
	testutil.RequireNoErrorf(t, err, "tail key")
	if pointerKey != "cells/cell-a/metadata/v1/current.pointer" ||
		manifestKey != "cells/cell-a/metadata/v1/manifests/manifest-1.manifest" ||
		snapshotKey != "cells/cell-a/metadata/v1/snapshots/shard=local/snapshot-1.snap" ||
		tailKey != "cells/cell-a/metadata/v1/tails/shard=local/00000000000000000007-00000000000000000012.tail" {
		t.Fatalf("keys = %q %q %q %q, want documented layout", pointerKey, manifestKey, snapshotKey, tailKey)
	}
}

func TestPublishedMetadataObjectKeysValidateInputs(t *testing.T) {
	tests := map[string]func() (string, error){
		"current pointer missing cell": func() (string, error) {
			return CurrentPointerObjectKey("")
		},
		"manifest missing cell": func() (string, error) {
			return ManifestObjectKey("", "manifest-1")
		},
		"manifest missing id": func() (string, error) {
			return ManifestObjectKey("cell-a", "")
		},
		"snapshot missing cell": func() (string, error) {
			return SnapshotObjectKey("", "shard-a", "snapshot-1")
		},
		"snapshot missing shard": func() (string, error) {
			return SnapshotObjectKey("cell-a", "", "snapshot-1")
		},
		"snapshot missing id": func() (string, error) {
			return SnapshotObjectKey("cell-a", "shard-a", "")
		},
		"tail missing cell": func() (string, error) {
			return TailObjectKey("", "shard-a", 1, 2)
		},
		"tail missing shard": func() (string, error) {
			return TailObjectKey("cell-a", "", 1, 2)
		},
		"tail invalid range": func() (string, error) {
			return TailObjectKey("cell-a", "shard-a", 2, 1)
		},
	}
	for name, call := range tests {
		t.Run(name, func(t *testing.T) {
			if key, err := call(); err == nil {
				t.Fatalf("key = %q, want validation error", key)
			}
		})
	}
}

func TestBuildManifestClonesInputsAndBuildsPointerChecksum(t *testing.T) {
	publishedAt := time.Unix(100, 0).UTC()
	snapshot := &publishedv1.ArtifactRef{
		Kind:       publishedv1.ArtifactKind_ARTIFACT_KIND_SNAPSHOT,
		ShardId:    "local",
		ObjectKey:  "cells/cell-a/metadata/v1/snapshots/shard=local/snapshot-1.snap",
		Length:     128,
		Sha256:     []byte{1, 2, 3},
		FirstIndex: 1,
		LastIndex:  10,
	}
	required := &publishedv1.ObjectRef{
		Kind:      publishedv1.ObjectKind_OBJECT_KIND_BLOCK,
		ObjectKey: "blocks/block-1.blk",
		Length:    1024,
		Sha256:    []byte{4, 5, 6},
	}
	manifest, err := BuildManifest(ManifestOptions{
		CellID:          "cell-a",
		SourceNamespace: "billing-prod",
		ManifestID:      "manifest-1",
		Generation:      2,
		PublishedAt:     publishedAt,
		ShardWatermarks: []*publishedv1.ShardWatermark{{ShardId: "local", HighWatermark: 10}},
		Snapshots:       []*publishedv1.ArtifactRef{snapshot},
		RequiredObjects: []*publishedv1.ObjectRef{required},
		ProducerBuild:   "test-build",
		ProducerSchema:  "published.v1",
	})
	testutil.RequireNoErrorf(t, err, "build manifest")
	snapshot.ObjectKey = "mutated"
	required.ObjectKey = "mutated"
	testutil.RequireEqualf(t, manifest.GetSchemaVersion(), CurrentSchemaVersion, "manifest schema version")
	testutil.RequireEqualf(t, manifest.GetCellId(), "cell-a", "manifest cell id")
	testutil.RequireEqualf(t, manifest.GetSourceNamespace(), "billing-prod", "manifest source namespace")
	testutil.RequireEqualf(t, manifest.GetGeneration(), uint64(2), "manifest generation")
	testutil.RequireFalsef(t, manifest.GetSnapshots()[0].GetObjectKey() == "mutated", "manifest snapshot object key was mutated")
	testutil.RequireFalsef(t, manifest.GetRequiredObjects()[0].GetObjectKey() == "mutated", "manifest required object key was mutated")

	data, err := MarshalManifest(manifest)
	testutil.RequireNoErrorf(t, err, "marshal manifest")
	pointer, err := BuildCurrentPointer(manifest, data)
	testutil.RequireNoErrorf(t, err, "build pointer")
	sum := sha256.Sum256(data)
	testutil.RequireEqualf(t, pointer.GetManifestId(), manifest.GetManifestId(), "pointer manifest id")
	testutil.RequireEqualf(t, pointer.GetGeneration(), manifest.GetGeneration(), "pointer generation")
	testutil.RequireTruef(t, bytes.Equal(pointer.GetManifestSha256(), sum[:]), "pointer manifest checksum")
	testutil.RequireTruef(t, pointer.GetPublishedAt().AsTime().Equal(publishedAt), "pointer published_at = %s, want %s", pointer.GetPublishedAt().AsTime(), publishedAt)
}

func TestBuildManifestPreservesNilReferenceSlots(t *testing.T) {
	manifest, err := BuildManifest(ManifestOptions{
		CellID:          "cell-a",
		SourceNamespace: "billing-prod",
		ManifestID:      "manifest-1",
		Generation:      2,
		PublishedAt:     time.Unix(100, 0).UTC(),
		ShardWatermarks: []*publishedv1.ShardWatermark{nil},
		Snapshots:       []*publishedv1.ArtifactRef{nil},
		Tails:           []*publishedv1.ArtifactRef{nil},
		RequiredObjects: []*publishedv1.ObjectRef{nil},
	})
	testutil.RequireNoErrorf(t, err, "build manifest")
	testutil.RequireEqualf(t, len(manifest.GetShardWatermarks()), 1, "watermark count")
	testutil.RequireNilf(t, manifest.GetShardWatermarks()[0], "watermark nil slot")
	testutil.RequireNilf(t, manifest.GetSnapshots()[0], "snapshot nil slot")
	testutil.RequireNilf(t, manifest.GetTails()[0], "tail nil slot")
	testutil.RequireNilf(t, manifest.GetRequiredObjects()[0], "required object nil slot")
}

func TestBuildManifestValidatesRequiredFields(t *testing.T) {
	valid := ManifestOptions{
		CellID:          "cell-a",
		SourceNamespace: "billing-prod",
		ManifestID:      "manifest-1",
		Generation:      1,
		PublishedAt:     time.Unix(100, 0).UTC(),
	}
	tests := map[string]ManifestOptions{
		"missing cell":       withManifestCell(valid, ""),
		"missing source":     withManifestSource(valid, ""),
		"missing manifest":   withManifestID(valid, ""),
		"missing generation": withManifestGeneration(valid, 0),
		"missing published":  withManifestPublishedAt(valid, time.Time{}),
	}
	for name, options := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := BuildManifest(options); err == nil {
				t.Fatal("BuildManifest succeeded, want validation error")
			}
		})
	}
}

func TestBuildCurrentPointerValidatesInputs(t *testing.T) {
	if _, err := BuildCurrentPointer(nil, []byte("manifest")); err == nil {
		t.Fatal("nil manifest succeeded")
	}
	if _, err := BuildCurrentPointer(&publishedv1.Manifest{SchemaVersion: CurrentSchemaVersion}, nil); err == nil {
		t.Fatal("empty manifest data succeeded")
	}
	if _, err := BuildCurrentPointer(&publishedv1.Manifest{SchemaVersion: CurrentSchemaVersion + 1}, []byte("manifest")); err == nil {
		t.Fatal("unsupported manifest schema version succeeded")
	}
}

func withManifestCell(options ManifestOptions, value string) ManifestOptions {
	options.CellID = value
	return options
}

func withManifestSource(options ManifestOptions, value string) ManifestOptions {
	options.SourceNamespace = value
	return options
}

func withManifestID(options ManifestOptions, value string) ManifestOptions {
	options.ManifestID = value
	return options
}

func withManifestGeneration(options ManifestOptions, value uint64) ManifestOptions {
	options.Generation = value
	return options
}

func withManifestPublishedAt(options ManifestOptions, value time.Time) ManifestOptions {
	options.PublishedAt = value
	return options
}
