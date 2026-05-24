package published

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"github.com/petabytecl/scrap/internal/backend"
	publishedv1 "github.com/petabytecl/scrap/internal/gen/scrap/published/v1"
)

var ErrCurrentPointerNotFound = errors.New("published metadata: current pointer not found")

type CheckpointVerification struct {
	PointerKey              string
	ManifestKey             string
	Pointer                 *publishedv1.CurrentPointer
	Manifest                *publishedv1.Manifest
	VerifiedObjects         int
	VerifiedArtifacts       int
	VerifiedRequiredObjects int
	VerifiedBlockObjects    int
	VerifiedIndexObjects    int
	VerifiedEnvelopeObjects int
}

func VerifyCurrentCheckpoint(ctx context.Context, store backend.Store, cellID string) (CheckpointVerification, error) {
	var verification CheckpointVerification
	if store == nil {
		return verification, errors.New("published metadata: backend store is required")
	}
	pointerKey, pointer, err := readCheckpointPointer(ctx, store, cellID)
	if err != nil {
		return verification, err
	}
	manifestKey, manifest, err := readCheckpointManifest(ctx, store, cellID, pointer)
	if err != nil {
		return verification, err
	}
	if err := validatePointerManifest(pointer, manifest); err != nil {
		return verification, err
	}

	stats, err := verifyManifestObjects(ctx, store, manifest)
	if err != nil {
		return verification, err
	}
	return CheckpointVerification{
		PointerKey:              pointerKey,
		ManifestKey:             manifestKey,
		Pointer:                 pointer,
		Manifest:                manifest,
		VerifiedObjects:         stats.objects,
		VerifiedArtifacts:       stats.artifacts,
		VerifiedRequiredObjects: stats.requiredObjects,
		VerifiedBlockObjects:    stats.blockObjects,
		VerifiedIndexObjects:    stats.indexObjects,
		VerifiedEnvelopeObjects: stats.envelopeObjects,
	}, nil
}

func readCheckpointPointer(ctx context.Context, store backend.Store, cellID string) (string, *publishedv1.CurrentPointer, error) {
	pointerKey, err := CurrentPointerObjectKey(cellID)
	if err != nil {
		return "", nil, err
	}
	pointerData, err := readVerifiedObject(ctx, store, pointerKey)
	if errors.Is(err, backend.ErrNotFound) {
		return "", nil, fmt.Errorf("%w: %w", ErrCurrentPointerNotFound, err)
	}
	if err != nil {
		return "", nil, err
	}
	pointer, err := UnmarshalCurrentPointer(pointerData)
	if err != nil {
		return "", nil, err
	}
	if pointer.GetCellId() != cellID {
		return "", nil, fmt.Errorf("published metadata: current pointer cell %q does not match requested cell %q", pointer.GetCellId(), cellID)
	}
	return pointerKey, pointer, nil
}

func readCheckpointManifest(ctx context.Context, store backend.Store, cellID string, pointer *publishedv1.CurrentPointer) (string, *publishedv1.Manifest, error) {
	manifestKey, err := ManifestObjectKey(cellID, pointer.GetManifestId())
	if err != nil {
		return "", nil, err
	}
	manifestData, err := readVerifiedObject(ctx, store, manifestKey)
	if err != nil {
		return "", nil, err
	}
	manifestSum := sha256.Sum256(manifestData)
	if !bytes.Equal(pointer.GetManifestSha256(), manifestSum[:]) {
		return "", nil, fmt.Errorf("%w: manifest %q checksum does not match current pointer", backend.ErrChecksumMismatch, pointer.GetManifestId())
	}
	manifest, err := UnmarshalManifest(manifestData)
	if err != nil {
		return "", nil, err
	}
	return manifestKey, manifest, nil
}

type manifestVerificationStats struct {
	objects         int
	artifacts       int
	requiredObjects int
	blockObjects    int
	indexObjects    int
	envelopeObjects int
}

func verifyManifestObjects(ctx context.Context, store backend.Store, manifest *publishedv1.Manifest) (manifestVerificationStats, error) {
	stats := manifestVerificationStats{objects: 2}
	if err := verifyManifestArtifacts(ctx, store, manifest, &stats); err != nil {
		return manifestVerificationStats{}, err
	}
	if err := verifyManifestRequiredObjects(ctx, store, manifest.GetRequiredObjects(), &stats); err != nil {
		return manifestVerificationStats{}, err
	}
	return stats, nil
}

func verifyManifestArtifacts(ctx context.Context, store backend.Store, manifest *publishedv1.Manifest, stats *manifestVerificationStats) error {
	for _, snapshot := range manifest.GetSnapshots() {
		if err := verifyArtifactRef(ctx, store, snapshot, publishedv1.ArtifactKind_ARTIFACT_KIND_SNAPSHOT); err != nil {
			return err
		}
		stats.objects++
		stats.artifacts++
	}
	for _, tail := range manifest.GetTails() {
		if err := verifyArtifactRef(ctx, store, tail, publishedv1.ArtifactKind_ARTIFACT_KIND_TAIL); err != nil {
			return err
		}
		stats.objects++
		stats.artifacts++
	}
	return nil
}

func verifyArtifactRef(ctx context.Context, store backend.Store, artifact *publishedv1.ArtifactRef, kind publishedv1.ArtifactKind) error {
	if artifact.GetKind() != kind {
		return fmt.Errorf("published metadata: invalid %s artifact kind %s", kind.String(), artifact.GetKind().String())
	}
	return verifyExpectedObject(ctx, store, artifact.GetObjectKey(), artifact.GetLength(), artifact.GetSha256())
}

func verifyManifestRequiredObjects(ctx context.Context, store backend.Store, objects []*publishedv1.ObjectRef, stats *manifestVerificationStats) error {
	for _, object := range objects {
		if err := verifyRequiredObject(ctx, store, object, stats); err != nil {
			return err
		}
	}
	return nil
}

func verifyRequiredObject(ctx context.Context, store backend.Store, object *publishedv1.ObjectRef, stats *manifestVerificationStats) error {
	if object.GetKind() == publishedv1.ObjectKind_OBJECT_KIND_UNSPECIFIED {
		return fmt.Errorf("published metadata: required object %q has unspecified kind", object.GetObjectKey())
	}
	if err := verifyExpectedObject(ctx, store, object.GetObjectKey(), object.GetLength(), object.GetSha256()); err != nil {
		return err
	}
	stats.objects++
	stats.requiredObjects++
	switch object.GetKind() {
	case publishedv1.ObjectKind_OBJECT_KIND_UNSPECIFIED:
	case publishedv1.ObjectKind_OBJECT_KIND_BLOCK:
		stats.blockObjects++
	case publishedv1.ObjectKind_OBJECT_KIND_INDEX:
		stats.indexObjects++
	case publishedv1.ObjectKind_OBJECT_KIND_ENVELOPE:
		stats.envelopeObjects++
	}
	return nil
}

func validatePointerManifest(pointer *publishedv1.CurrentPointer, manifest *publishedv1.Manifest) error {
	if manifest.GetCellId() != pointer.GetCellId() {
		return fmt.Errorf("published metadata: manifest cell %q does not match pointer cell %q", manifest.GetCellId(), pointer.GetCellId())
	}
	if manifest.GetSourceNamespace() != pointer.GetSourceNamespace() {
		return fmt.Errorf("published metadata: manifest source namespace %q does not match pointer source namespace %q", manifest.GetSourceNamespace(), pointer.GetSourceNamespace())
	}
	if manifest.GetManifestId() != pointer.GetManifestId() {
		return fmt.Errorf("published metadata: manifest id %q does not match pointer manifest id %q", manifest.GetManifestId(), pointer.GetManifestId())
	}
	if manifest.GetGeneration() != pointer.GetGeneration() {
		return fmt.Errorf("published metadata: manifest generation %d does not match pointer generation %d", manifest.GetGeneration(), pointer.GetGeneration())
	}
	return nil
}

func readVerifiedObject(ctx context.Context, store backend.Store, key string) ([]byte, error) {
	var data bytes.Buffer
	if err := store.ReadObjectRange(ctx, key, backend.Range{}, &data); err != nil {
		return nil, err
	}
	return data.Bytes(), nil
}

func verifyExpectedObject(ctx context.Context, store backend.Store, key string, length uint64, sha256Bytes []byte) error {
	if key == "" {
		return errors.New("published metadata: object key is required")
	}
	if len(sha256Bytes) != sha256.Size {
		return fmt.Errorf("%w: object %q checksum is %d bytes", backend.ErrChecksumMismatch, key, len(sha256Bytes))
	}
	object, err := store.HeadObject(ctx, key)
	if err != nil {
		return err
	}
	if object.Length != length || !bytes.Equal(object.SHA256[:], sha256Bytes) {
		return fmt.Errorf("%w: object %q metadata does not match published ref", backend.ErrChecksumMismatch, key)
	}
	var zero uint64
	return store.ReadObjectRange(ctx, key, backend.Range{Length: &zero}, io.Discard)
}
