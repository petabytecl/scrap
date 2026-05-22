package localstorage

import (
	"context"
	"fmt"
	"time"

	"github.com/petabytecl/scrap/internal/backend"
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
