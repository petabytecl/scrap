package backend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	iofs "io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const directoryMode iofs.FileMode = 0o755

type fsBackend struct {
	rootDir string
}

func NewFS(rootDir string) Backend {
	return &fsBackend{rootDir: rootDir}
}

func (b *fsBackend) PutObject(
	ctx context.Context,
	key string,
	body io.Reader,
	size int64,
	_ PutOpts,
) (PutResult, error) {
	if err := ctx.Err(); err != nil {
		return PutResult{}, classifyFSError("put object", err)
	}
	if size < 0 {
		return PutResult{}, fmt.Errorf("put object: negative size %d: %w", size, ErrPermanent)
	}

	path, err := b.pathForKey(key)
	if err != nil {
		return PutResult{}, err
	}

	tmpPath, err := writeTempObject(path, body, size)
	if err != nil {
		return PutResult{}, err
	}
	if err := commitTempObject(tmpPath, path); err != nil {
		return PutResult{}, err
	}

	meta, err := objectMeta(path)
	if err != nil {
		return PutResult{}, err
	}
	return PutResult{ETag: meta.ETag, Size: meta.Size}, nil
}

func (b *fsBackend) HeadObject(ctx context.Context, key string) (ObjectMeta, error) {
	if err := ctx.Err(); err != nil {
		return ObjectMeta{}, classifyFSError("head object", err)
	}

	path, err := b.pathForKey(key)
	if err != nil {
		return ObjectMeta{}, err
	}
	return objectMeta(path)
}

func (b *fsBackend) GetObject(ctx context.Context, key string, opts GetOpts) (io.ReadCloser, ObjectMeta, error) {
	if err := ctx.Err(); err != nil {
		return nil, ObjectMeta{}, classifyFSError("get object", err)
	}

	path, err := b.pathForKey(key)
	if err != nil {
		return nil, ObjectMeta{}, err
	}

	// #nosec G304 -- pathForKey validates the key and anchors it under rootDir.
	file, err := os.Open(path)
	if err != nil {
		return nil, ObjectMeta{}, classifyFSError("get object", err)
	}

	meta, err := objectMetaFromFile(file)
	if err != nil {
		_ = file.Close()
		return nil, ObjectMeta{}, err
	}
	if !opts.Range.Enabled {
		return file, meta, nil
	}

	reader, err := rangedReader(file, meta.Size, opts.Range)
	if err != nil {
		_ = file.Close()
		return nil, ObjectMeta{}, err
	}
	return reader, meta, nil
}

func (b *fsBackend) DeleteObject(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return classifyFSError("delete object", err)
	}

	path, err := b.pathForKey(key)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return classifyFSError("delete object", err)
	}
	return syncDirectory(filepath.Dir(path))
}

func (b *fsBackend) ListObjects(ctx context.Context, prefix string, _ ListOpts) (ObjectIterator, error) {
	if err := ctx.Err(); err != nil {
		return nil, classifyFSError("list objects", err)
	}
	if err := validatePrefix(prefix); err != nil {
		return nil, err
	}

	root, err := b.rootPath()
	if err != nil {
		return nil, fmt.Errorf("list objects: %w", err)
	}

	objects, err := listObjects(ctx, root, prefix)
	if err != nil {
		return nil, err
	}
	return &sliceIterator{objects: objects}, nil
}

func (b *fsBackend) pathForKey(key string) (string, error) {
	if err := validateKey(key); err != nil {
		return "", err
	}

	root, err := b.rootPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, filepath.FromSlash(key)), nil
}

func (b *fsBackend) rootPath() (string, error) {
	if b.rootDir == "" {
		return "", fmt.Errorf("backend root is empty: %w", ErrPermanent)
	}

	root, err := filepath.Abs(b.rootDir)
	if err != nil {
		return "", classifyFSError("resolve backend root", err)
	}
	return root, nil
}

func writeTempObject(path string, body io.Reader, size int64) (string, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, directoryMode); err != nil {
		return "", classifyFSError("create object directory", err)
	}

	tmp, err := os.CreateTemp(dir, ".scrap-put-*")
	if err != nil {
		return "", classifyFSError("create temporary object", err)
	}
	tmpPath := tmp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpPath)
		}
	}()

	limit := size + 1
	if size == math.MaxInt64 {
		limit = size
	}
	written, err := io.Copy(tmp, io.LimitReader(body, limit))
	if err != nil {
		_ = tmp.Close()
		return "", classifyFSError("write object", err)
	}
	if written != size {
		_ = tmp.Close()
		return "", fmt.Errorf("write object: copied %d bytes, expected %d: %w", written, size, ErrCorrupt)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", classifyFSError("sync object", err)
	}
	if err := tmp.Close(); err != nil {
		return "", classifyFSError("close object", err)
	}

	removeTemp = false
	return tmpPath, nil
}

func commitTempObject(tmpPath, path string) error {
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return classifyFSError("commit object", err)
	}
	return syncDirectory(filepath.Dir(path))
}

func objectMeta(path string) (ObjectMeta, error) {
	// #nosec G304 -- callers pass paths derived from pathForKey or WalkDir.
	file, err := os.Open(path)
	if err != nil {
		return ObjectMeta{}, classifyFSError("head object", err)
	}
	defer func() {
		_ = file.Close()
	}()
	return objectMetaFromFile(file)
}

func objectMetaFromFile(file *os.File) (ObjectMeta, error) {
	info, err := file.Stat()
	if err != nil {
		return ObjectMeta{}, classifyFSError("stat object", err)
	}
	if !info.Mode().IsRegular() {
		return ObjectMeta{}, fmt.Errorf("object is not a regular file: %w", ErrPermanent)
	}
	etag, err := etagForFile(file)
	if err != nil {
		return ObjectMeta{}, err
	}

	return ObjectMeta{
		ETag:        etag,
		Size:        info.Size(),
		ContentType: DefaultContentType,
	}, nil
}

func rangedReader(file *os.File, size int64, byteRange ByteRange) (io.ReadCloser, error) {
	if byteRange.Offset < 0 || byteRange.Length < 0 {
		return nil, fmt.Errorf("invalid byte range: %w", ErrPermanent)
	}
	if byteRange.Offset > size {
		return nil, fmt.Errorf("byte range starts past object size: %w", ErrPermanent)
	}

	length := byteRange.Length
	remaining := size - byteRange.Offset
	if length > remaining {
		length = remaining
	}

	return sectionReadCloser{
		SectionReader: io.NewSectionReader(file, byteRange.Offset, length),
		closer:        file,
	}, nil
}

func listObjects(ctx context.Context, root, prefix string) ([]ObjectInfo, error) {
	walkRoot := listWalkRoot(root, prefix)

	objects := make([]ObjectInfo, 0)
	err := filepath.WalkDir(walkRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return classifyFSError("walk object", walkErr)
		}
		if err := ctx.Err(); err != nil {
			return classifyFSError("list objects", err)
		}
		if skipListEntry(entry) {
			return nil
		}
		info, err := objectInfo(root, path, prefix)
		if err != nil {
			return err
		}
		if info.Key == "" {
			return nil
		}
		objects = append(objects, info)
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return objects, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list objects: %w", err)
	}

	sort.Slice(objects, func(i, j int) bool {
		return objects[i].Key < objects[j].Key
	})
	return objects, nil
}

func listWalkRoot(root, prefix string) string {
	if prefix == "" {
		return root
	}
	candidate := filepath.Join(root, filepath.FromSlash(prefix))
	for candidate != root {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		candidate = filepath.Dir(candidate)
	}
	return root
}

func skipListEntry(entry os.DirEntry) bool {
	return entry.IsDir() || entry.Type()&iofs.ModeType != 0 || strings.HasPrefix(entry.Name(), ".scrap-put-")
}

func objectInfo(root, path, prefix string) (ObjectInfo, error) {
	key, err := filepath.Rel(root, path)
	if err != nil {
		return ObjectInfo{}, classifyFSError("resolve object key", err)
	}
	key = filepath.ToSlash(key)
	if !strings.HasPrefix(key, prefix) {
		return ObjectInfo{}, nil
	}

	meta, err := objectMeta(path)
	if err != nil {
		return ObjectInfo{}, err
	}
	return ObjectInfo{
		Key:         key,
		ETag:        meta.ETag,
		Size:        meta.Size,
		ContentType: meta.ContentType,
	}, nil
}

func syncDirectory(path string) error {
	// #nosec G304 -- callers pass paths derived from pathForKey.
	dir, err := os.Open(path)
	if err != nil {
		return classifyFSError("open object directory", err)
	}
	defer func() {
		_ = dir.Close()
	}()
	if err := dir.Sync(); err != nil {
		return classifyFSError("sync object directory", err)
	}
	return nil
}

func validateKey(key string) error {
	switch {
	case key == "":
		return fmt.Errorf("backend object key is empty: %w", ErrPermanent)
	case key == "." || !iofs.ValidPath(key):
		return fmt.Errorf("invalid backend object key %q: %w", key, ErrPermanent)
	case strings.Contains(key, "\\"):
		return fmt.Errorf("invalid backend object key %q: %w", key, ErrPermanent)
	case strings.ContainsRune(key, 0):
		return fmt.Errorf("invalid backend object key %q: %w", key, ErrPermanent)
	default:
		return nil
	}
}

func validatePrefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	if strings.HasPrefix(prefix, "/") || strings.Contains(prefix, "\\") || strings.ContainsRune(prefix, 0) {
		return fmt.Errorf("invalid backend object prefix %q: %w", prefix, ErrPermanent)
	}

	trimmed := strings.TrimSuffix(prefix, "/")
	if trimmed == "" {
		return fmt.Errorf("invalid backend object prefix %q: %w", prefix, ErrPermanent)
	}
	for _, part := range strings.Split(trimmed, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("invalid backend object prefix %q: %w", prefix, ErrPermanent)
		}
	}
	return nil
}

func etagForFile(file *os.File) (string, error) {
	offset, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return "", classifyFSError("seek object", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", classifyFSError("seek object", err)
	}

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", classifyFSError("hash object", err)
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return "", classifyFSError("seek object", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func classifyFSError(op string, err error) error {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%s: %w: %w", op, ErrTransient, err)
	case errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("%s: %w: %w", op, ErrNotFound, err)
	case errors.Is(err, os.ErrPermission):
		return fmt.Errorf("%s: %w: %w", op, ErrAuth, err)
	case errors.Is(err, os.ErrExist):
		return fmt.Errorf("%s: %w: %w", op, ErrConflict, err)
	default:
		return fmt.Errorf("%s: %w: %w", op, ErrPermanent, err)
	}
}

type sectionReadCloser struct {
	*io.SectionReader
	closer io.Closer
}

func (r sectionReadCloser) Close() error {
	return r.closer.Close()
}

type sliceIterator struct {
	objects []ObjectInfo
	next    int
}

func (i *sliceIterator) Next() (ObjectInfo, error) {
	if i.next >= len(i.objects) {
		return ObjectInfo{}, io.EOF
	}

	object := i.objects[i.next]
	i.next++
	return object, nil
}
