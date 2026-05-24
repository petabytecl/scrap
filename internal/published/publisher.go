package published

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/petabytecl/scrap/internal/backend"
	publishedv1 "github.com/petabytecl/scrap/internal/gen/scrap/published/v1"
	"github.com/petabytecl/scrap/internal/metastore"
)

type SnapshotMetadataSource interface {
	ListDocuments(metastore.DocumentFilter) ([]metastore.Document, error)
	ListTransactions() ([]metastore.Transaction, error)
	ListUploadIntents() ([]metastore.UploadIntent, error)
}

type SnapshotPublishOptions struct {
	Backend         backend.MutableStore
	Metadata        SnapshotMetadataSource
	CellID          string
	SourceNamespace string
	ShardID         string
	SnapshotID      string
	ManifestID      string
	Generation      uint64
	HighWatermark   uint64
	PublishedAt     time.Time
	ProducerBuild   string
	ProducerSchema  string
}

type SnapshotPublication struct {
	PointerKey       string
	ManifestKey      string
	SnapshotKey      string
	PointerObject    backend.Object
	ManifestObject   backend.Object
	SnapshotObject   backend.Object
	Pointer          *publishedv1.CurrentPointer
	Manifest         *publishedv1.Manifest
	DocumentCount    int
	TransactionCount int
}

func PublishSnapshot(ctx context.Context, options SnapshotPublishOptions) (SnapshotPublication, error) {
	var publication SnapshotPublication
	if err := validatePublishOptions(options); err != nil {
		return publication, err
	}
	if err := ctx.Err(); err != nil {
		return publication, err
	}

	source, err := loadSnapshotSource(ctx, options)
	if err != nil {
		return publication, err
	}
	snapshotKey, snapshotObject, err := writeSnapshotArtifact(ctx, options, source)
	if err != nil {
		return publication, err
	}
	manifestKey, manifestObject, manifest, manifestData, err := publishManifestArtifact(ctx, options, source.requiredObjects, snapshotObject)
	if err != nil {
		return publication, err
	}
	pointerKey, pointerObject, pointer, err := publishCurrentPointer(ctx, options, manifest, manifestData)
	if err != nil {
		return publication, err
	}
	return SnapshotPublication{
		PointerKey:       pointerKey,
		ManifestKey:      manifestKey,
		SnapshotKey:      snapshotKey,
		PointerObject:    pointerObject,
		ManifestObject:   manifestObject,
		SnapshotObject:   snapshotObject,
		Pointer:          pointer,
		Manifest:         manifest,
		DocumentCount:    len(source.documents),
		TransactionCount: len(source.transactions),
	}, nil
}

type snapshotSource struct {
	documents       []metastore.Document
	transactions    []metastore.Transaction
	locationObjects map[string]LocationObjects
	requiredObjects []*publishedv1.ObjectRef
}

func loadSnapshotSource(ctx context.Context, options SnapshotPublishOptions) (snapshotSource, error) {
	documents, err := options.Metadata.ListDocuments(metastore.DocumentFilter{})
	if err != nil {
		return snapshotSource{}, err
	}
	transactions, err := options.Metadata.ListTransactions()
	if err != nil {
		return snapshotSource{}, err
	}
	intents, err := options.Metadata.ListUploadIntents()
	if err != nil {
		return snapshotSource{}, err
	}
	locationObjects, requiredObjects, err := publishedObjectRefs(ctx, options.Backend, documents, intents)
	if err != nil {
		return snapshotSource{}, err
	}
	return snapshotSource{
		documents:       documents,
		transactions:    transactions,
		locationObjects: locationObjects,
		requiredObjects: requiredObjects,
	}, nil
}

func writeSnapshotArtifact(ctx context.Context, options SnapshotPublishOptions, source snapshotSource) (string, backend.Object, error) {
	var snapshotData bytes.Buffer
	if err := WriteMetadataSnapshotRecords(&snapshotData, SnapshotOptions{
		SourceNamespace: options.SourceNamespace,
		ShardID:         options.ShardID,
		HighWatermark:   options.HighWatermark,
		LocationObjects: source.locationObjects,
	}, source.documents, source.transactions); err != nil {
		return "", backend.Object{}, err
	}
	snapshotKey, err := SnapshotObjectKey(options.CellID, options.ShardID, options.SnapshotID)
	if err != nil {
		return "", backend.Object{}, err
	}
	snapshotObject, err := options.Backend.PutObject(ctx, snapshotKey, bytes.NewReader(snapshotData.Bytes()))
	if err != nil {
		return "", backend.Object{}, err
	}
	return snapshotKey, snapshotObject, nil
}

func publishManifestArtifact(
	ctx context.Context,
	options SnapshotPublishOptions,
	requiredObjects []*publishedv1.ObjectRef,
	snapshotObject backend.Object,
) (string, backend.Object, *publishedv1.Manifest, []byte, error) {
	manifestKey, err := ManifestObjectKey(options.CellID, options.ManifestID)
	if err != nil {
		return "", backend.Object{}, nil, nil, err
	}
	manifest, err := BuildManifest(ManifestOptions{
		CellID:          options.CellID,
		SourceNamespace: options.SourceNamespace,
		ManifestID:      options.ManifestID,
		Generation:      options.Generation,
		PublishedAt:     options.PublishedAt,
		ShardWatermarks: []*publishedv1.ShardWatermark{
			{
				ShardId:       options.ShardID,
				HighWatermark: options.HighWatermark,
			},
		},
		Snapshots: []*publishedv1.ArtifactRef{
			{
				Kind:      publishedv1.ArtifactKind_ARTIFACT_KIND_SNAPSHOT,
				ShardId:   options.ShardID,
				ObjectKey: snapshotObject.Key,
				Length:    snapshotObject.Length,
				Sha256:    append([]byte(nil), snapshotObject.SHA256[:]...),
				LastIndex: options.HighWatermark,
			},
		},
		RequiredObjects: requiredObjects,
		ProducerBuild:   options.ProducerBuild,
		ProducerSchema:  options.ProducerSchema,
	})
	if err != nil {
		return "", backend.Object{}, nil, nil, err
	}
	manifestData, err := MarshalManifest(manifest)
	if err != nil {
		return "", backend.Object{}, nil, nil, err
	}
	manifestObject, err := options.Backend.PutObject(ctx, manifestKey, bytes.NewReader(manifestData))
	if err != nil {
		return "", backend.Object{}, nil, nil, err
	}
	return manifestKey, manifestObject, manifest, manifestData, nil
}

func publishCurrentPointer(
	ctx context.Context,
	options SnapshotPublishOptions,
	manifest *publishedv1.Manifest,
	manifestData []byte,
) (string, backend.Object, *publishedv1.CurrentPointer, error) {
	pointer, err := BuildCurrentPointer(manifest, manifestData)
	if err != nil {
		return "", backend.Object{}, nil, err
	}
	pointerData, err := MarshalCurrentPointer(pointer)
	if err != nil {
		return "", backend.Object{}, nil, err
	}
	pointerKey, err := CurrentPointerObjectKey(options.CellID)
	if err != nil {
		return "", backend.Object{}, nil, err
	}
	pointerObject, err := options.Backend.PutMutableObject(ctx, pointerKey, bytes.NewReader(pointerData))
	if err != nil {
		return "", backend.Object{}, nil, err
	}
	return pointerKey, pointerObject, pointer, nil
}

func validatePublishOptions(options SnapshotPublishOptions) error {
	if options.Backend == nil {
		return errors.New("published metadata: backend store is required")
	}
	if options.Metadata == nil {
		return errors.New("published metadata: metadata source is required")
	}
	if options.CellID == "" {
		return errors.New("published metadata: cell id is required")
	}
	if options.SourceNamespace == "" {
		return errors.New("published metadata: source namespace is required")
	}
	if options.ShardID == "" {
		return errors.New("published metadata: shard id is required")
	}
	if options.SnapshotID == "" {
		return errors.New("published metadata: snapshot id is required")
	}
	if options.ManifestID == "" {
		return errors.New("published metadata: manifest id is required")
	}
	if options.Generation == 0 {
		return errors.New("published metadata: generation is required")
	}
	if options.PublishedAt.IsZero() {
		return errors.New("published metadata: published_at is required")
	}
	return nil
}

func publishedObjectRefs(ctx context.Context, store backend.Store, documents []metastore.Document, intents []metastore.UploadIntent) (map[string]LocationObjects, []*publishedv1.ObjectRef, error) {
	intentByBlockID := uploadIntentsByBlockID(intents)
	blockIDs := publishedBlockIDs(documents)

	locationObjects := make(map[string]LocationObjects, len(blockIDs))
	var refs []*publishedv1.ObjectRef
	for _, blockID := range blockIDs {
		intent, ok := intentByBlockID[blockID]
		if !ok {
			return nil, nil, fmt.Errorf("published metadata: block %q has no upload intent", blockID)
		}
		if err := validatePublishedIntent(blockID, intent); err != nil {
			return nil, nil, err
		}
		locationObjects[blockID] = locationObjectsForIntent(intent)
		blockRefs, err := verifiedIntentObjectRefs(ctx, store, intent)
		if err != nil {
			return nil, nil, err
		}
		refs = append(refs, blockRefs...)
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].GetObjectKey() != refs[j].GetObjectKey() {
			return refs[i].GetObjectKey() < refs[j].GetObjectKey()
		}
		return refs[i].GetKind() < refs[j].GetKind()
	})
	return locationObjects, refs, nil
}

func uploadIntentsByBlockID(intents []metastore.UploadIntent) map[string]metastore.UploadIntent {
	intentByBlockID := make(map[string]metastore.UploadIntent, len(intents))
	for _, intent := range intents {
		intentByBlockID[intent.BlockID] = intent
	}
	return intentByBlockID
}

func publishedBlockIDs(documents []metastore.Document) []string {
	blockIDs := make([]string, 0)
	seenBlocks := make(map[string]bool)
	for _, document := range documents {
		blockID := document.Location.BlockID
		if blockID == "" || seenBlocks[blockID] {
			continue
		}
		seenBlocks[blockID] = true
		blockIDs = append(blockIDs, blockID)
	}
	sort.Strings(blockIDs)
	return blockIDs
}

func validatePublishedIntent(blockID string, intent metastore.UploadIntent) error {
	if intent.State != metastore.UploadStateUploaded {
		return fmt.Errorf("published metadata: block %q is not uploaded", blockID)
	}
	if intent.BackendObjectKey == "" {
		return fmt.Errorf("published metadata: block %q has no backend object key", blockID)
	}
	return nil
}

func locationObjectsForIntent(intent metastore.UploadIntent) LocationObjects {
	return LocationObjects{
		BackendObjectKey:  intent.BackendObjectKey,
		IndexObjectKey:    intent.IndexObjectKey,
		EnvelopeObjectKey: intent.EnvelopeObjectKey,
	}
}

func verifiedIntentObjectRefs(ctx context.Context, store backend.Store, intent metastore.UploadIntent) ([]*publishedv1.ObjectRef, error) {
	refs, err := verifiedRequiredObjectRefs(ctx, store,
		requiredObject{kind: publishedv1.ObjectKind_OBJECT_KIND_BLOCK, key: intent.BackendObjectKey},
	)
	if err != nil {
		return nil, err
	}
	if intent.IndexObjectKey != "" {
		refs, err = appendVerifiedRequiredObjectRef(ctx, store, refs, requiredObject{kind: publishedv1.ObjectKind_OBJECT_KIND_INDEX, key: intent.IndexObjectKey})
		if err != nil {
			return nil, err
		}
	}
	if intent.EnvelopeObjectKey != "" {
		return appendVerifiedRequiredObjectRef(ctx, store, refs, requiredObject{kind: publishedv1.ObjectKind_OBJECT_KIND_ENVELOPE, key: intent.EnvelopeObjectKey})
	}
	return refs, nil
}

type requiredObject struct {
	kind publishedv1.ObjectKind
	key  string
}

func verifiedRequiredObjectRefs(ctx context.Context, store backend.Store, objects ...requiredObject) ([]*publishedv1.ObjectRef, error) {
	refs := make([]*publishedv1.ObjectRef, 0, len(objects))
	for _, object := range objects {
		var err error
		refs, err = appendVerifiedRequiredObjectRef(ctx, store, refs, object)
		if err != nil {
			return nil, err
		}
	}
	return refs, nil
}

func appendVerifiedRequiredObjectRef(ctx context.Context, store backend.Store, refs []*publishedv1.ObjectRef, object requiredObject) ([]*publishedv1.ObjectRef, error) {
	ref, err := verifiedObjectRef(ctx, store, object.kind, object.key)
	if err != nil {
		return nil, err
	}
	return append(refs, ref), nil
}

func verifiedObjectRef(ctx context.Context, store backend.Store, kind publishedv1.ObjectKind, key string) (*publishedv1.ObjectRef, error) {
	object, err := store.HeadObject(ctx, key)
	if err != nil {
		return nil, err
	}
	var zero uint64
	if err := store.ReadObjectRange(ctx, key, backend.Range{Length: &zero}, io.Discard); err != nil {
		return nil, err
	}
	return &publishedv1.ObjectRef{
		Kind:      kind,
		ObjectKey: object.Key,
		Length:    object.Length,
		Sha256:    append([]byte(nil), object.SHA256[:]...),
	}, nil
}
