package localstorage

import (
	"context"
	"strings"
	"testing"

	backendfs "github.com/petabytecl/scrap/internal/backend/fs"
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestPublishMetadataSnapshotRequiresConfiguredMutableBackend(t *testing.T) {
	app := openTestApplication(t)
	_, err := app.PublishMetadataSnapshot(context.Background())
	if err == nil || !strings.Contains(err.Error(), "backend store is not configured") {
		t.Fatalf("publish without backend error = %v, want missing backend", err)
	}
	testutil.RequireTruef(t, backendStoreIsNil(nil), "nil backend store should be detected")

	store, err := backendfs.Open(t.TempDir())
	testutil.RequireNoErrorf(t, err, "open backend store")
	testutil.RequireFalsef(t, backendStoreIsNil(store), "concrete backend store reported nil")
}
