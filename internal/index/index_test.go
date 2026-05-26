package index_test

import (
	"path/filepath"
	"testing"

	"github.com/petabytecl/scrap/internal/index"
)

func TestPutGet(t *testing.T) {
	dir := t.TempDir()
	idx, err := index.Open(filepath.Join(dir, "pebble"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	if err := idx.Put("tx-001", 100, 3, true); err != nil {
		t.Fatalf("Put: %v", err)
	}

	entry, err := idx.Get("tx-001")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if len(entry.BlockIDs) != 1 || entry.BlockIDs[0] != 100 {
		t.Fatalf("BlockIDs: got %v, want [100]", entry.BlockIDs)
	}
	if entry.DocCount != 3 {
		t.Fatalf("DocCount: got %d, want 3", entry.DocCount)
	}
	if !entry.Completed {
		t.Fatal("Completed should be true")
	}
}

func TestGetNotFound(t *testing.T) {
	dir := t.TempDir()
	idx, err := index.Open(filepath.Join(dir, "pebble"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	_, err = idx.Get("nonexistent")
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

func TestAddBlockID(t *testing.T) {
	dir := t.TempDir()
	idx, err := index.Open(filepath.Join(dir, "pebble"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	if err := idx.Put("tx-multi", 100, 2, false); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if err := idx.AddBlockID("tx-multi", 200); err != nil {
		t.Fatalf("AddBlockID: %v", err)
	}

	entry, err := idx.Get("tx-multi")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if len(entry.BlockIDs) != 2 {
		t.Fatalf("BlockIDs length: got %d, want 2", len(entry.BlockIDs))
	}
	if entry.BlockIDs[0] != 100 || entry.BlockIDs[1] != 200 {
		t.Fatalf("BlockIDs: got %v, want [100 200]", entry.BlockIDs)
	}
}

func TestIncrementDocCount(t *testing.T) {
	dir := t.TempDir()
	idx, err := index.Open(filepath.Join(dir, "pebble"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	if err := idx.Put("tx-inc", 100, 0, false); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if err := idx.IncrementDocCount("tx-inc"); err != nil {
		t.Fatalf("IncrementDocCount: %v", err)
	}
	if err := idx.IncrementDocCount("tx-inc"); err != nil {
		t.Fatalf("IncrementDocCount: %v", err)
	}

	entry, err := idx.Get("tx-inc")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if entry.DocCount != 2 {
		t.Fatalf("DocCount: got %d, want 2", entry.DocCount)
	}
}

func TestExists(t *testing.T) {
	dir := t.TempDir()
	idx, err := index.Open(filepath.Join(dir, "pebble"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	if idx.Exists("tx-new") {
		t.Fatal("Exists should return false for missing key")
	}

	if err := idx.Put("tx-new", 100, 1, false); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if !idx.Exists("tx-new") {
		t.Fatal("Exists should return true after Put")
	}
}
