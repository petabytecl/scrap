package published

import (
	"bytes"
	"crypto/sha256"
	"testing"
	"time"

	publishedv1 "github.com/petabytecl/scrap/internal/gen/scrap/published/v1"
)

func TestPublishedMetadataObjectKeysFollowLayout(t *testing.T) {
	pointerKey, err := CurrentPointerObjectKey("cell-a")
	if err != nil {
		t.Fatalf("current pointer key: %v", err)
	}
	manifestKey, err := ManifestObjectKey("cell-a", "manifest-1")
	if err != nil {
		t.Fatalf("manifest key: %v", err)
	}
	snapshotKey, err := SnapshotObjectKey("cell-a", "local", "snapshot-1")
	if err != nil {
		t.Fatalf("snapshot key: %v", err)
	}
	tailKey, err := TailObjectKey("cell-a", "local", 7, 12)
	if err != nil {
		t.Fatalf("tail key: %v", err)
	}
	if pointerKey != "cells/cell-a/metadata/v1/current.pointer" ||
		manifestKey != "cells/cell-a/metadata/v1/manifests/manifest-1.manifest" ||
		snapshotKey != "cells/cell-a/metadata/v1/snapshots/shard=local/snapshot-1.snap" ||
		tailKey != "cells/cell-a/metadata/v1/tails/shard=local/00000000000000000007-00000000000000000012.tail" {
		t.Fatalf("keys = %q %q %q %q, want documented layout", pointerKey, manifestKey, snapshotKey, tailKey)
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
	if err != nil {
		t.Fatalf("build manifest: %v", err)
	}
	snapshot.ObjectKey = "mutated"
	required.ObjectKey = "mutated"
	if manifest.GetSchemaVersion() != CurrentSchemaVersion ||
		manifest.GetCellId() != "cell-a" ||
		manifest.GetSourceNamespace() != "billing-prod" ||
		manifest.GetGeneration() != 2 ||
		manifest.GetSnapshots()[0].GetObjectKey() == "mutated" ||
		manifest.GetRequiredObjects()[0].GetObjectKey() == "mutated" {
		t.Fatalf("manifest = %#v, want independent manifest", manifest)
	}

	data, err := MarshalManifest(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	pointer, err := BuildCurrentPointer(manifest, data)
	if err != nil {
		t.Fatalf("build pointer: %v", err)
	}
	sum := sha256.Sum256(data)
	if pointer.GetManifestId() != manifest.GetManifestId() ||
		pointer.GetGeneration() != manifest.GetGeneration() ||
		!bytes.Equal(pointer.GetManifestSha256(), sum[:]) ||
		!pointer.GetPublishedAt().AsTime().Equal(publishedAt) {
		t.Fatalf("pointer = %#v, want manifest checksum pointer", pointer)
	}
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
