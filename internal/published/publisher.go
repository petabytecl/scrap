package published

import (
	"bytes"
	"context"
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

	documents, err := options.Metadata.ListDocuments(metastore.DocumentFilter{})
	if err != nil {
		return publication, err
	}
	transactions, err := options.Metadata.ListTransactions()
	if err != nil {
		return publication, err
	}
	intents, err := options.Metadata.ListUploadIntents()
	if err != nil {
		return publication, err
	}
	locationObjects, requiredObjects, err := publishedObjectRefs(ctx, options.Backend, documents, intents)
	if err != nil {
		return publication, err
	}

	var snapshotData bytes.Buffer
	if err := WriteMetadataSnapshotRecords(&snapshotData, SnapshotOptions{
		SourceNamespace: options.SourceNamespace,
		ShardID:         options.ShardID,
		HighWatermark:   options.HighWatermark,
		LocationObjects: locationObjects,
	}, documents, transactions); err != nil {
		return publication, err
	}
	snapshotKey, err := SnapshotObjectKey(options.CellID, options.ShardID, options.SnapshotID)
	if err != nil {
		return publication, err
	}
	snapshotObject, err := options.Backend.PutObject(ctx, snapshotKey, bytes.NewReader(snapshotData.Bytes()))
	if err != nil {
		return publication, err
	}

	manifestKey, err := ManifestObjectKey(options.CellID, options.ManifestID)
	if err != nil {
		return publication, err
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
		return publication, err
	}
	manifestData, err := MarshalManifest(manifest)
	if err != nil {
		return publication, err
	}
	manifestObject, err := options.Backend.PutObject(ctx, manifestKey, bytes.NewReader(manifestData))
	if err != nil {
		return publication, err
	}

	pointer, err := BuildCurrentPointer(manifest, manifestData)
	if err != nil {
		return publication, err
	}
	pointerData, err := MarshalCurrentPointer(pointer)
	if err != nil {
		return publication, err
	}
	pointerKey, err := CurrentPointerObjectKey(options.CellID)
	if err != nil {
		return publication, err
	}
	pointerObject, err := options.Backend.PutMutableObject(ctx, pointerKey, bytes.NewReader(pointerData))
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
		DocumentCount:    len(documents),
		TransactionCount: len(transactions),
	}, nil
}

func validatePublishOptions(options SnapshotPublishOptions) error {
	if options.Backend == nil {
		return fmt.Errorf("published metadata: backend store is required")
	}
	if options.Metadata == nil {
		return fmt.Errorf("published metadata: metadata source is required")
	}
	if options.CellID == "" {
		return fmt.Errorf("published metadata: cell id is required")
	}
	if options.SourceNamespace == "" {
		return fmt.Errorf("published metadata: source namespace is required")
	}
	if options.ShardID == "" {
		return fmt.Errorf("published metadata: shard id is required")
	}
	if options.SnapshotID == "" {
		return fmt.Errorf("published metadata: snapshot id is required")
	}
	if options.ManifestID == "" {
		return fmt.Errorf("published metadata: manifest id is required")
	}
	if options.Generation == 0 {
		return fmt.Errorf("published metadata: generation is required")
	}
	if options.PublishedAt.IsZero() {
		return fmt.Errorf("published metadata: published_at is required")
	}
	return nil
}

func publishedObjectRefs(ctx context.Context, store backend.Store, documents []metastore.Document, intents []metastore.UploadIntent) (map[string]LocationObjects, []*publishedv1.ObjectRef, error) {
	intentByBlockID := make(map[string]metastore.UploadIntent, len(intents))
	for _, intent := range intents {
		intentByBlockID[intent.BlockID] = intent
	}
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

	locationObjects := make(map[string]LocationObjects, len(blockIDs))
	var refs []*publishedv1.ObjectRef
	for _, blockID := range blockIDs {
		intent, ok := intentByBlockID[blockID]
		if !ok {
			return nil, nil, fmt.Errorf("published metadata: block %q has no upload intent", blockID)
		}
		if intent.State != metastore.UploadStateUploaded {
			return nil, nil, fmt.Errorf("published metadata: block %q is not uploaded", blockID)
		}
		if intent.BackendObjectKey == "" {
			return nil, nil, fmt.Errorf("published metadata: block %q has no backend object key", blockID)
		}
		locationObjects[blockID] = LocationObjects{
			BackendObjectKey:  intent.BackendObjectKey,
			IndexObjectKey:    intent.IndexObjectKey,
			EnvelopeObjectKey: intent.EnvelopeObjectKey,
		}
		blockRef, err := verifiedObjectRef(ctx, store, publishedv1.ObjectKind_OBJECT_KIND_BLOCK, intent.BackendObjectKey)
		if err != nil {
			return nil, nil, err
		}
		refs = append(refs, blockRef)
		if intent.IndexObjectKey != "" {
			indexRef, err := verifiedObjectRef(ctx, store, publishedv1.ObjectKind_OBJECT_KIND_INDEX, intent.IndexObjectKey)
			if err != nil {
				return nil, nil, err
			}
			refs = append(refs, indexRef)
		}
		if intent.EnvelopeObjectKey != "" {
			envelopeRef, err := verifiedObjectRef(ctx, store, publishedv1.ObjectKind_OBJECT_KIND_ENVELOPE, intent.EnvelopeObjectKey)
			if err != nil {
				return nil, nil, err
			}
			refs = append(refs, envelopeRef)
		}
	}
	sort.Slice(refs, func(i int, j int) bool {
		if refs[i].GetObjectKey() != refs[j].GetObjectKey() {
			return refs[i].GetObjectKey() < refs[j].GetObjectKey()
		}
		return refs[i].GetKind() < refs[j].GetKind()
	})
	return locationObjects, refs, nil
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
