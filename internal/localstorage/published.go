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
	return a.publishMetadataSnapshot(ctx, store)
}

func (a *Application) publishMetadataSnapshot(ctx context.Context, store backend.MutableStore) (published.SnapshotPublication, error) {
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
	Snapshots      int
	Documents      int
	Transactions   int
	UploadIntents  int
	Tombstones     int
	Verified       int
	BlocksRestored int
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
		result.Transactions += len(contents.Transactions)
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
	transactions, err := drillApp.metadata.ListTransactions()
	if err != nil {
		return result, err
	}
	if len(transactions) != result.Transactions {
		return result, fmt.Errorf("localstorage: DR drill restored %d transactions, expected %d", len(transactions), result.Transactions)
	}
	restoredBlocks, err := drillApp.restoreImportedBlocks(ctx, documents, a.now())
	if err != nil {
		return result, err
	}
	result.BlocksRestored = restoredBlocks
	return result, nil
}

func (a *Application) restoreImportedBlocks(ctx context.Context, documents []metastore.Document, now time.Time) (int, error) {
	blockIDs := make(map[string]bool)
	for _, document := range documents {
		blockIDs[document.Location.BlockID] = true
	}
	for blockID := range blockIDs {
		if err := a.restoreBlockFromBackend(ctx, blockID); err != nil {
			return 0, err
		}
	}
	for _, document := range documents {
		if err := a.authority.UpdateDocumentRestoreState(
			ctx,
			document.Identity,
			metastore.RestoreStateHot,
			"drill restored local bytes",
			stableCommandID("dr-drill-restore", document.Identity.TenantID, document.Identity.TransactionID, document.Identity.DocumentName),
			now,
		); err != nil {
			return 0, err
		}
	}
	return len(blockIDs), nil
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
	for _, transaction := range contents.Transactions {
		if err := a.applyPublishedTransaction(ctx, manifestID, transaction); err != nil {
			return err
		}
	}
	return nil
}

func (a *Application) applyPublishedTransaction(ctx context.Context, manifestID string, transaction metastore.Transaction) error {
	switch transaction.State {
	case metastore.TransactionStateOpen:
		return nil
	case metastore.TransactionStateCompleted:
		if transaction.CompletedAt == nil {
			return fmt.Errorf("localstorage: completed published transaction %s/%s has no completed_at", transaction.Identity.TenantID, transaction.Identity.TransactionID)
		}
		_, err := a.authority.CompleteTransaction(
			ctx,
			transaction.Identity,
			*transaction.CompletedAt,
			transaction.Tags,
			importPublishedTransactionCommandID(manifestID, transaction),
		)
		return err
	case metastore.TransactionStateTimedOut:
		return fmt.Errorf("localstorage: published timed-out transaction import is not supported")
	default:
		return fmt.Errorf("localstorage: unsupported published transaction state %d", transaction.State)
	}
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

func importPublishedTransactionCommandID(manifestID string, transaction metastore.Transaction) string {
	return stableCommandID(
		"import-published-transaction",
		manifestID,
		transaction.Identity.TenantID,
		transaction.Identity.TransactionID,
	)
}
