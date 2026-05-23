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
		return verification, fmt.Errorf("published metadata: backend store is required")
	}
	pointerKey, err := CurrentPointerObjectKey(cellID)
	if err != nil {
		return verification, err
	}
	pointerData, err := readVerifiedObject(ctx, store, pointerKey)
	if errors.Is(err, backend.ErrNotFound) {
		return verification, fmt.Errorf("%w: %w", ErrCurrentPointerNotFound, err)
	}
	if err != nil {
		return verification, err
	}
	pointer, err := UnmarshalCurrentPointer(pointerData)
	if err != nil {
		return verification, err
	}
	if pointer.GetCellId() != cellID {
		return verification, fmt.Errorf("published metadata: current pointer cell %q does not match requested cell %q", pointer.GetCellId(), cellID)
	}

	manifestKey, err := ManifestObjectKey(cellID, pointer.GetManifestId())
	if err != nil {
		return verification, err
	}
	manifestData, err := readVerifiedObject(ctx, store, manifestKey)
	if err != nil {
		return verification, err
	}
	manifestSum := sha256.Sum256(manifestData)
	if !bytes.Equal(pointer.GetManifestSha256(), manifestSum[:]) {
		return verification, fmt.Errorf("%w: manifest %q checksum does not match current pointer", backend.ErrChecksumMismatch, pointer.GetManifestId())
	}
	manifest, err := UnmarshalManifest(manifestData)
	if err != nil {
		return verification, err
	}
	if err := validatePointerManifest(pointer, manifest); err != nil {
		return verification, err
	}

	verified := 2
	verifiedArtifacts := 0
	verifiedRequiredObjects := 0
	verifiedBlockObjects := 0
	verifiedIndexObjects := 0
	verifiedEnvelopeObjects := 0
	for _, snapshot := range manifest.GetSnapshots() {
		if snapshot.GetKind() != publishedv1.ArtifactKind_ARTIFACT_KIND_SNAPSHOT {
			return verification, fmt.Errorf("published metadata: invalid snapshot artifact kind %s", snapshot.GetKind())
		}
		if err := verifyExpectedObject(ctx, store, snapshot.GetObjectKey(), snapshot.GetLength(), snapshot.GetSha256()); err != nil {
			return verification, err
		}
		verified++
		verifiedArtifacts++
	}
	for _, tail := range manifest.GetTails() {
		if tail.GetKind() != publishedv1.ArtifactKind_ARTIFACT_KIND_TAIL {
			return verification, fmt.Errorf("published metadata: invalid tail artifact kind %s", tail.GetKind())
		}
		if err := verifyExpectedObject(ctx, store, tail.GetObjectKey(), tail.GetLength(), tail.GetSha256()); err != nil {
			return verification, err
		}
		verified++
		verifiedArtifacts++
	}
	for _, object := range manifest.GetRequiredObjects() {
		if object.GetKind() == publishedv1.ObjectKind_OBJECT_KIND_UNSPECIFIED {
			return verification, fmt.Errorf("published metadata: required object %q has unspecified kind", object.GetObjectKey())
		}
		if err := verifyExpectedObject(ctx, store, object.GetObjectKey(), object.GetLength(), object.GetSha256()); err != nil {
			return verification, err
		}
		verified++
		verifiedRequiredObjects++
		switch object.GetKind() {
		case publishedv1.ObjectKind_OBJECT_KIND_BLOCK:
			verifiedBlockObjects++
		case publishedv1.ObjectKind_OBJECT_KIND_INDEX:
			verifiedIndexObjects++
		case publishedv1.ObjectKind_OBJECT_KIND_ENVELOPE:
			verifiedEnvelopeObjects++
		}
	}
	return CheckpointVerification{
		PointerKey:              pointerKey,
		ManifestKey:             manifestKey,
		Pointer:                 pointer,
		Manifest:                manifest,
		VerifiedObjects:         verified,
		VerifiedArtifacts:       verifiedArtifacts,
		VerifiedRequiredObjects: verifiedRequiredObjects,
		VerifiedBlockObjects:    verifiedBlockObjects,
		VerifiedIndexObjects:    verifiedIndexObjects,
		VerifiedEnvelopeObjects: verifiedEnvelopeObjects,
	}, nil
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
		return fmt.Errorf("published metadata: object key is required")
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
