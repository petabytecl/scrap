package localstorage

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"time"

	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/published"
)

const (
	localPublishedCellID          = "local"
	localPublishedSourceNamespace = "local"
	localPublishedShardID         = "local"
)

func (a *Application) PublishMetadataSnapshot(ctx context.Context) (published.SnapshotPublication, error) {
	store, ok := a.backendStore.(backend.MutableStore)
	if a.backendStore == nil {
		return published.SnapshotPublication{}, fmt.Errorf("localstorage: backend store is not configured")
	}
	if !ok {
		return published.SnapshotPublication{}, fmt.Errorf("localstorage: backend store does not support mutable metadata pointers")
	}
	now := a.now()
	highWatermark := a.authority.AppliedIndex()
	generation := highWatermark
	if generation == 0 {
		generation = uint64(now.UnixNano())
		if generation == 0 {
			generation = 1
		}
	}
	id := publishedSnapshotID(generation, now)
	return published.PublishSnapshot(ctx, published.SnapshotPublishOptions{
		Backend:         store,
		Metadata:        a.metadata,
		CellID:          localPublishedCellID,
		SourceNamespace: localPublishedSourceNamespace,
		ShardID:         localPublishedShardID,
		SnapshotID:      id,
		ManifestID:      id,
		Generation:      generation,
		HighWatermark:   highWatermark,
		PublishedAt:     now,
		ProducerBuild:   "scrap-local",
		ProducerSchema:  "scrap.published.v1",
	})
}

func publishedSnapshotID(generation uint64, publishedAt time.Time) string {
	return fmt.Sprintf("%020d-%s", generation, publishedAt.UTC().Format("20060102T150405.000000000Z"))
}

type MetadataRestoreResult struct {
	Snapshots     int
	Documents     int
	UploadIntents int
	Tombstones    int
	Verified      int
}

func (a *Application) RestorePublishedMetadataCheckpoint(ctx context.Context, apply bool) (MetadataRestoreResult, error) {
	var result MetadataRestoreResult
	checkpoint, err := a.verifyCurrentCheckpoint(ctx)
	if err != nil {
		return result, err
	}
	result.Verified = checkpoint.VerifiedObjects
	now := a.now()
	for _, snapshot := range checkpoint.Manifest.GetSnapshots() {
		var data bytes.Buffer
		if err := a.backendStore.ReadObjectRange(ctx, snapshot.GetObjectKey(), backend.Range{}, &data); err != nil {
			return result, err
		}
		contents, err := published.ReadSnapshotContents(bytes.NewReader(data.Bytes()))
		if err != nil {
			return result, err
		}
		result.Snapshots++
		result.Tombstones += contents.Tombstones
		result.Documents += len(contents.Documents)
		result.UploadIntents += len(contents.UploadIntents)
		if !apply {
			continue
		}
		if err := a.applyPublishedSnapshotContents(ctx, checkpoint.Manifest.GetManifestId(), contents, now); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (a *Application) RunDRDrill(ctx context.Context, execute bool) (MetadataRestoreResult, error) {
	if !execute {
		return a.RestorePublishedMetadataCheckpoint(ctx, false)
	}
	drillDir, err := os.MkdirTemp("", "scrap-dr-drill-*")
	if err != nil {
		return MetadataRestoreResult{}, err
	}
	defer os.RemoveAll(drillDir)
	drillApp, err := Open(drillDir)
	if err != nil {
		return MetadataRestoreResult{}, err
	}
	defer drillApp.Close()
	drillApp.now = a.now
	drillApp.SetBackendStore(a.backendStore)
	result, err := drillApp.RestorePublishedMetadataCheckpoint(ctx, true)
	if err != nil {
		return result, err
	}
	documents, err := drillApp.metadata.ListDocuments(metastore.DocumentFilter{})
	if err != nil {
		return result, err
	}
	if len(documents) != result.Documents {
		return result, fmt.Errorf("localstorage: DR drill restored %d documents, expected %d", len(documents), result.Documents)
	}
	return result, nil
}

func (a *Application) applyPublishedSnapshotContents(ctx context.Context, manifestID string, contents published.SnapshotContents, now time.Time) error {
	for _, document := range contents.Documents {
		document.Availability = metastore.AvailabilityCold
		document.RestoreState = metastore.RestoreStateCold
		document.UploadState = metastore.UploadStateUploaded
		if err := a.authority.CommitDocument(ctx, document, importPublishedDocumentCommandID(manifestID, document), now); err != nil {
			return err
		}
	}
	for _, intent := range contents.UploadIntents {
		if err := a.authority.RecordUploadIntent(ctx, intent, importPublishedIntentCommandID(manifestID, intent), now); err != nil {
			return err
		}
		if err := a.authority.UpdateUploadIntentState(ctx, intent.BlockID, metastore.UploadStateUploaded, "", importPublishedIntentStateCommandID(manifestID, intent), now); err != nil {
			return err
		}
	}
	return nil
}

func importPublishedDocumentCommandID(manifestID string, document metastore.Document) string {
	return stableCommandID(
		"import-published-document",
		manifestID,
		document.Identity.TenantID,
		document.Identity.TransactionID,
		document.Identity.DocumentName,
		document.Location.BlockID,
	)
}

func importPublishedIntentCommandID(manifestID string, intent metastore.UploadIntent) string {
	return stableCommandID("import-published-intent", manifestID, intent.BlockID, intent.BackendObjectKey, intent.IndexObjectKey, intent.EnvelopeObjectKey)
}

func importPublishedIntentStateCommandID(manifestID string, intent metastore.UploadIntent) string {
	return stableCommandID("import-published-intent-state", manifestID, intent.BlockID)
}
