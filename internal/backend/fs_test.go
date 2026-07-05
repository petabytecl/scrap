package backend_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/petabytecl/scrap/internal/backend"
)

const testDirectoryMode os.FileMode = 0o755

func TestFSBackendRoundTrip(t *testing.T) {
	ctx := context.Background()
	key := "cell-a/shards/0000000000000007/000000000000002a.blk"
	payload := []byte("0123456789abcdef")
	store := backend.NewFS(t.TempDir())

	put := putObject(ctx, t, store, key, payload)
	head := headObject(ctx, t, store, key, put, len(payload))

	assertFullGet(ctx, t, store, key, payload, head)
	assertRangedGet(ctx, t, store, key, head)
	assertList(ctx, t, store, key, put, len(payload))
	deleteObject(ctx, t, store, key)
}

func putObject(
	ctx context.Context,
	t *testing.T,
	store backend.Backend,
	key string,
	payload []byte,
) backend.PutResult {
	t.Helper()

	put, err := store.PutObject(ctx, key, bytes.NewReader(payload), int64(len(payload)), backend.PutOpts{})
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	if put.Size != int64(len(payload)) {
		t.Fatalf("PutObject size = %d, want %d", put.Size, len(payload))
	}
	if put.ETag == "" {
		t.Fatal("PutObject ETag should not be empty")
	}
	return put
}

func headObject(
	ctx context.Context,
	t *testing.T,
	store backend.Backend,
	key string,
	put backend.PutResult,
	wantSize int,
) backend.ObjectMeta {
	t.Helper()

	head, err := store.HeadObject(ctx, key)
	if err != nil {
		t.Fatalf("HeadObject: %v", err)
	}
	if head.Size != int64(wantSize) {
		t.Fatalf("HeadObject size = %d, want %d", head.Size, wantSize)
	}
	if head.ETag != put.ETag {
		t.Fatalf("HeadObject ETag = %q, want %q", head.ETag, put.ETag)
	}
	if head.ContentType != backend.DefaultContentType {
		t.Fatalf("HeadObject content type = %q, want %q", head.ContentType, backend.DefaultContentType)
	}
	return head
}

func assertFullGet(
	ctx context.Context,
	t *testing.T,
	store backend.Backend,
	key string,
	payload []byte,
	head backend.ObjectMeta,
) {
	t.Helper()

	full, fullMeta, err := store.GetObject(ctx, key, backend.GetOpts{})
	if err != nil {
		t.Fatalf("GetObject full: %v", err)
	}
	fullBytes, err := io.ReadAll(full)
	if closeErr := full.Close(); closeErr != nil {
		t.Fatalf("Close full reader: %v", closeErr)
	}
	if err != nil {
		t.Fatalf("Read full object: %v", err)
	}
	if !bytes.Equal(fullBytes, payload) {
		t.Fatalf("full object = %q, want %q", fullBytes, payload)
	}
	if fullMeta != head {
		t.Fatalf("full GetObject meta = %+v, want %+v", fullMeta, head)
	}
}

func assertRangedGet(
	ctx context.Context,
	t *testing.T,
	store backend.Backend,
	key string,
	head backend.ObjectMeta,
) {
	t.Helper()

	ranged, rangeMeta, err := store.GetObject(ctx, key, backend.GetOpts{
		Range: backend.ByteRange{Enabled: true, Offset: 4, Length: 6},
	})
	if err != nil {
		t.Fatalf("GetObject range: %v", err)
	}
	rangeBytes, err := io.ReadAll(ranged)
	if closeErr := ranged.Close(); closeErr != nil {
		t.Fatalf("Close range reader: %v", closeErr)
	}
	if err != nil {
		t.Fatalf("Read ranged object: %v", err)
	}
	if string(rangeBytes) != "456789" {
		t.Fatalf("range object = %q, want %q", rangeBytes, "456789")
	}
	if rangeMeta != head {
		t.Fatalf("range GetObject meta = %+v, want %+v", rangeMeta, head)
	}
}

func assertList(
	ctx context.Context,
	t *testing.T,
	store backend.Backend,
	key string,
	put backend.PutResult,
	wantSize int,
) {
	t.Helper()

	iter, err := store.ListObjects(ctx, "cell-a/shards/0000000000000007/", backend.ListOpts{})
	if err != nil {
		t.Fatalf("ListObjects: %v", err)
	}
	info, err := iter.Next()
	if err != nil {
		t.Fatalf("Next first object: %v", err)
	}
	if info.Key != key {
		t.Fatalf("listed key = %q, want %q", info.Key, key)
	}
	if info.Size != int64(wantSize) {
		t.Fatalf("listed size = %d, want %d", info.Size, wantSize)
	}
	if info.ETag != put.ETag {
		t.Fatalf("listed ETag = %q, want %q", info.ETag, put.ETag)
	}
	if _, err := iter.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next after final object error = %v, want EOF", err)
	}
}

func deleteObject(ctx context.Context, t *testing.T, store backend.Backend, key string) {
	t.Helper()

	if err := store.DeleteObject(ctx, key); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}
	if _, err := store.HeadObject(ctx, key); !errors.Is(err, backend.ErrNotFound) {
		t.Fatalf("HeadObject after delete error = %v, want ErrNotFound", err)
	}
}

func TestFSBackendRejectsInvalidKeysAndPrefixes(t *testing.T) {
	ctx := context.Background()
	store := backend.NewFS(t.TempDir())

	invalidKeys := []string{
		"",
		".",
		"/absolute/key",
		"../escape",
		"cell-a/../escape",
		`cell-a\escape`,
		"cell-a/\x00/escape",
	}
	for _, key := range invalidKeys {
		t.Run("put "+key, func(t *testing.T) {
			_, err := store.PutObject(ctx, key, bytes.NewReader([]byte("x")), 1, backend.PutOpts{})
			if !errors.Is(err, backend.ErrPermanent) {
				t.Fatalf("PutObject(%q) error = %v, want ErrPermanent", key, err)
			}
		})
	}

	invalidPrefixes := []string{
		"/absolute",
		"../escape",
		"cell-a//escape",
		`cell-a\escape`,
		"cell-a/\x00/escape",
	}
	for _, prefix := range invalidPrefixes {
		t.Run("list "+prefix, func(t *testing.T) {
			_, err := store.ListObjects(ctx, prefix, backend.ListOpts{})
			if !errors.Is(err, backend.ErrPermanent) {
				t.Fatalf("ListObjects(%q) error = %v, want ErrPermanent", prefix, err)
			}
		})
	}
}

func TestFSBackendClassifiesMissingObjects(t *testing.T) {
	ctx := context.Background()
	store := backend.NewFS(t.TempDir())
	key := "cell-a/shards/0000000000000007/000000000000002b.blk"

	if _, err := store.HeadObject(ctx, key); !errors.Is(err, backend.ErrNotFound) {
		t.Fatalf("HeadObject missing error = %v, want ErrNotFound", err)
	}
	if _, _, err := store.GetObject(ctx, key, backend.GetOpts{}); !errors.Is(err, backend.ErrNotFound) {
		t.Fatalf("GetObject missing error = %v, want ErrNotFound", err)
	}
	// DeleteObject is idempotent: deleting an absent key is a no-op, matching S3
	// semantics so retried/concurrent deletes behave identically across backends.
	if err := store.DeleteObject(ctx, key); err != nil {
		t.Fatalf("DeleteObject missing error = %v, want nil (idempotent)", err)
	}
}

func TestFSBackendRejectsSizeMismatch(t *testing.T) {
	ctx := context.Background()
	store := backend.NewFS(t.TempDir())
	key := "cell-a/shards/0000000000000007/000000000000002b.blk"

	_, err := store.PutObject(ctx, key, bytes.NewReader([]byte("abc")), 4, backend.PutOpts{})
	if !errors.Is(err, backend.ErrCorrupt) {
		t.Fatalf("PutObject size mismatch error = %v, want ErrCorrupt", err)
	}
	if _, err := store.HeadObject(ctx, key); !errors.Is(err, backend.ErrNotFound) {
		t.Fatalf("HeadObject after failed PutObject error = %v, want ErrNotFound", err)
	}

	_, err = store.PutObject(ctx, key, bytes.NewReader([]byte("abc")), -1, backend.PutOpts{})
	if !errors.Is(err, backend.ErrPermanent) {
		t.Fatalf("PutObject negative size error = %v, want ErrPermanent", err)
	}
}

func TestFSBackendRejectsInvalidRanges(t *testing.T) {
	ctx := context.Background()
	store := backend.NewFS(t.TempDir())
	key := "cell-a/shards/0000000000000007/000000000000002b.blk"

	putObject(ctx, t, store, key, []byte("abcdef"))
	assertInvalidRange(ctx, t, store, key, backend.ByteRange{Enabled: true, Offset: -1, Length: 1})
	assertInvalidRange(ctx, t, store, key, backend.ByteRange{Enabled: true, Offset: 7, Length: 1})
	assertRange(ctx, t, store, key, backend.ByteRange{Enabled: true, Offset: 4, Length: 99}, "ef")
}

func TestFSBackendListNoMatchIsEmpty(t *testing.T) {
	ctx := context.Background()
	store := backend.NewFS(t.TempDir())
	key := "cell-a/shards/0000000000000007/000000000000002b.blk"

	putObject(ctx, t, store, key, []byte("abcdef"))

	iter, err := store.ListObjects(ctx, "cell-a/shards/no-match/", backend.ListOpts{})
	if err != nil {
		t.Fatalf("ListObjects no-match: %v", err)
	}
	if _, err := iter.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next no-match error = %v, want EOF", err)
	}
}

func TestFSBackendListMatchesS3BytePrefixSemantics(t *testing.T) {
	ctx := context.Background()
	store := backend.NewFS(t.TempDir())

	putObject(ctx, t, store, "cell-a/shards/00ab/000000000000002b.blk", []byte("in-dir"))
	putObject(ctx, t, store, "cell-a/shards/00abc.blk", []byte("sibling"))
	putObject(ctx, t, store, "cell-a/shards/00zz.blk", []byte("other"))

	cases := map[string][]string{
		// A partial trailing segment that also names an existing directory
		// must still match sibling keys byte-wise, exactly as S3 does.
		"cell-a/shards/00ab": {
			"cell-a/shards/00ab/000000000000002b.blk",
			"cell-a/shards/00abc.blk",
		},
		"cell-a/sh": {
			"cell-a/shards/00ab/000000000000002b.blk",
			"cell-a/shards/00abc.blk",
			"cell-a/shards/00zz.blk",
		},
		"cell-a/shards/00ab/": {
			"cell-a/shards/00ab/000000000000002b.blk",
		},
		"cell": {
			"cell-a/shards/00ab/000000000000002b.blk",
			"cell-a/shards/00abc.blk",
			"cell-a/shards/00zz.blk",
		},
	}
	for prefix, want := range cases {
		got := listKeys(ctx, t, store, prefix)
		if len(got) != len(want) {
			t.Fatalf("ListObjects(%q) keys = %v, want %v", prefix, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("ListObjects(%q) keys = %v, want %v", prefix, got, want)
			}
		}
	}
}

func listKeys(ctx context.Context, t *testing.T, store backend.Backend, prefix string) []string {
	t.Helper()
	iter, err := store.ListObjects(ctx, prefix, backend.ListOpts{})
	if err != nil {
		t.Fatalf("ListObjects(%q): %v", prefix, err)
	}
	var keys []string
	for {
		info, err := iter.Next()
		if errors.Is(err, io.EOF) {
			return keys
		}
		if err != nil {
			t.Fatalf("Next(%q): %v", prefix, err)
		}
		keys = append(keys, info.Key)
	}
}

func TestFSBackendRejectsDirectoryObjects(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store := backend.NewFS(root)

	directoryKey := "cell-a/shards/0000000000000007/directory-object"
	if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(directoryKey)), testDirectoryMode); err != nil {
		t.Fatalf("MkdirAll directory object: %v", err)
	}
	if _, err := store.HeadObject(ctx, directoryKey); !errors.Is(err, backend.ErrPermanent) {
		t.Fatalf("HeadObject directory error = %v, want ErrPermanent", err)
	}
}

func TestFSBackendClassifiesCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	store := backend.NewFS(t.TempDir())
	key := "cell-a/shards/0000000000000007/000000000000002c.blk"

	if _, err := store.PutObject(ctx, key, bytes.NewReader([]byte("abc")), 3, backend.PutOpts{}); !errors.Is(err, backend.ErrTransient) {
		t.Fatalf("PutObject canceled error = %v, want ErrTransient", err)
	}
	if _, err := store.HeadObject(ctx, key); !errors.Is(err, backend.ErrTransient) {
		t.Fatalf("HeadObject canceled error = %v, want ErrTransient", err)
	}
	if _, _, err := store.GetObject(ctx, key, backend.GetOpts{}); !errors.Is(err, backend.ErrTransient) {
		t.Fatalf("GetObject canceled error = %v, want ErrTransient", err)
	}
	if err := store.DeleteObject(ctx, key); !errors.Is(err, backend.ErrTransient) {
		t.Fatalf("DeleteObject canceled error = %v, want ErrTransient", err)
	}
	if _, err := store.ListObjects(ctx, "", backend.ListOpts{}); !errors.Is(err, backend.ErrTransient) {
		t.Fatalf("ListObjects canceled error = %v, want ErrTransient", err)
	}
}

func TestFSBackendRequiresRoot(t *testing.T) {
	ctx := context.Background()
	store := backend.NewFS("")
	key := "cell-a/shards/0000000000000007/000000000000002d.blk"

	if _, err := store.PutObject(ctx, key, bytes.NewReader([]byte("abc")), 3, backend.PutOpts{}); !errors.Is(err, backend.ErrPermanent) {
		t.Fatalf("PutObject empty root error = %v, want ErrPermanent", err)
	}
	if _, err := store.ListObjects(ctx, "", backend.ListOpts{}); !errors.Is(err, backend.ErrPermanent) {
		t.Fatalf("ListObjects empty root error = %v, want ErrPermanent", err)
	}
}

func assertInvalidRange(
	ctx context.Context,
	t *testing.T,
	store backend.Backend,
	key string,
	byteRange backend.ByteRange,
) {
	t.Helper()

	reader, _, err := store.GetObject(ctx, key, backend.GetOpts{Range: byteRange})
	if reader != nil {
		_ = reader.Close()
	}
	if !errors.Is(err, backend.ErrPermanent) {
		t.Fatalf("GetObject invalid range %+v error = %v, want ErrPermanent", byteRange, err)
	}
}

func assertRange(
	ctx context.Context,
	t *testing.T,
	store backend.Backend,
	key string,
	byteRange backend.ByteRange,
	want string,
) {
	t.Helper()

	reader, _, err := store.GetObject(ctx, key, backend.GetOpts{Range: byteRange})
	if err != nil {
		t.Fatalf("GetObject range %+v: %v", byteRange, err)
	}
	got, err := io.ReadAll(reader)
	if closeErr := reader.Close(); closeErr != nil {
		t.Fatalf("Close range reader: %v", closeErr)
	}
	if err != nil {
		t.Fatalf("Read range: %v", err)
	}
	if string(got) != want {
		t.Fatalf("GetObject range %+v = %q, want %q", byteRange, got, want)
	}
}
