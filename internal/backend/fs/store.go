package fs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/closeutil"
	"github.com/petabytecl/scrap/internal/safeconv"
	"github.com/petabytecl/scrap/internal/safepath"
)

const (
	dataSuffix = ".data"
	metaSuffix = ".meta"
)

type Store struct {
	root       string
	objectsDir string
	profile    backend.CapacityProfile
}

var _ backend.MutableAdapter = (*Store)(nil)

type metadata struct {
	Key       string `json:"key"`
	Length    uint64 `json:"length"`
	SHA256Hex string `json:"sha256_hex"`
}

func Open(root string) (*Store, error) {
	return OpenWithCapacityProfile(root, backend.NonProductionFilesystemProfile("local-filesystem-non-production", 1<<40))
}

func OpenWithCapacityProfile(root string, profile backend.CapacityProfile) (*Store, error) {
	if profile.Provider != backend.ProviderFilesystem {
		return nil, fmt.Errorf("backend fs: capacity profile provider %q is not filesystem", profile.Provider)
	}
	if err := profile.Validate(); err != nil {
		return nil, err
	}
	objectsDir := filepath.Join(root, "objects")
	if err := os.MkdirAll(objectsDir, 0o700); err != nil {
		return nil, err
	}
	if err := syncDir(objectsDir); err != nil {
		return nil, err
	}
	return &Store{root: root, objectsDir: objectsDir, profile: profile.Clone()}, nil
}

func (s *Store) Provider() backend.Provider {
	return backend.ProviderFilesystem
}

func (s *Store) CapacityProfile() backend.CapacityProfile {
	return s.profile.Clone()
}

func (s *Store) PutObject(ctx context.Context, key string, reader io.Reader) (backend.Object, error) {
	if key == "" {
		return backend.Object{}, errors.New("backend fs: object key is required")
	}
	if existing, err := s.HeadObject(ctx, key); err == nil {
		incoming, err := readAndHash(ctx, reader, io.Discard)
		if err != nil {
			return backend.Object{}, err
		}
		if err := s.verifyObject(ctx, existing); err != nil {
			return backend.Object{}, err
		}
		if existing.Length == incoming.Length && existing.SHA256 == incoming.SHA256 {
			return existing, nil
		}
		return backend.Object{}, backend.ErrConflict
	} else if !errors.Is(err, backend.ErrNotFound) {
		return backend.Object{}, err
	}
	return s.writeObject(ctx, key, reader)
}

func (s *Store) PutMutableObject(ctx context.Context, key string, reader io.Reader) (backend.Object, error) {
	if key == "" {
		return backend.Object{}, errors.New("backend fs: object key is required")
	}
	return s.writeObject(ctx, key, reader)
}

func (s *Store) writeObject(ctx context.Context, key string, reader io.Reader) (backend.Object, error) {
	objectPath, err := s.validatedObjectPath(key, dataSuffix)
	if err != nil {
		return backend.Object{}, err
	}
	metaPath, err := s.validatedObjectPath(key, metaSuffix)
	if err != nil {
		return backend.Object{}, err
	}
	tempData, err := os.CreateTemp(s.objectsDir, "put-*.data.tmp")
	if err != nil {
		return backend.Object{}, err
	}
	tempDataPath, err := s.validatedObjectStorePath(tempData.Name())
	if err != nil {
		return backend.Object{}, errors.Join(err, tempData.Close(), s.removeObjectStorePath(tempData.Name()))
	}
	committed := false
	defer func() {
		if !committed {
			_ = s.removeObjectStorePath(tempDataPath)
		}
	}()

	written, err := readAndHash(ctx, reader, tempData)
	if closeErr := tempData.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return backend.Object{}, err
	}
	if err := syncFile(tempDataPath); err != nil {
		return backend.Object{}, err
	}
	meta := metadata{
		Key:       key,
		Length:    written.Length,
		SHA256Hex: hex.EncodeToString(written.SHA256[:]),
	}
	tempMetaPath, err := s.writeTempMetadata(meta)
	if err != nil {
		return backend.Object{}, err
	}
	defer func() {
		if !committed {
			_ = s.removeObjectStorePath(tempMetaPath)
		}
	}()
	// #nosec G703 -- source and destination are validated under objectsDir.
	if err := os.Rename(tempDataPath, objectPath); err != nil {
		return backend.Object{}, err
	}
	// #nosec G703 -- source and destination are validated under objectsDir.
	if err := os.Rename(tempMetaPath, metaPath); err != nil {
		return backend.Object{}, err
	}
	if err := syncDir(s.objectsDir); err != nil {
		return backend.Object{}, err
	}
	committed = true
	return backend.Object{Key: key, Length: written.Length, SHA256: written.SHA256}, nil
}

func (s *Store) HeadObject(ctx context.Context, key string) (backend.Object, error) {
	if err := ctx.Err(); err != nil {
		return backend.Object{}, err
	}
	meta, err := s.readMetadata(key)
	if err != nil {
		return backend.Object{}, err
	}
	return objectFromMetadata(meta)
}

func (s *Store) ReadObjectRange(ctx context.Context, key string, selected backend.Range, writer io.Writer) error {
	object, err := s.HeadObject(ctx, key)
	if err != nil {
		return err
	}
	if selected.Offset > object.Length {
		return backend.ErrInvalidRange
	}
	readLength := object.Length - selected.Offset
	if selected.Length != nil {
		if *selected.Length > object.Length-selected.Offset {
			return backend.ErrInvalidRange
		}
		readLength = *selected.Length
	}
	if err := s.verifyObject(ctx, object); err != nil {
		return err
	}
	if readLength == 0 {
		return nil
	}
	file, err := s.openObjectFile(key, dataSuffix)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return backend.ErrNotFound
		}
		return err
	}
	defer closeutil.Ignore(file)
	readOffset, err := safeconv.Uint64ToInt64("read range offset", selected.Offset)
	if err != nil {
		return backend.ErrInvalidRange
	}
	readLengthInt, err := safeconv.Uint64ToInt64("read range length", readLength)
	if err != nil {
		return backend.ErrInvalidRange
	}
	section := io.NewSectionReader(file, readOffset, readLengthInt)
	return copyWithContext(ctx, writer, section)
}

func (s *Store) verifyObject(ctx context.Context, object backend.Object) error {
	file, err := s.openObjectFile(object.Key, dataSuffix)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return backend.ErrNotFound
		}
		return err
	}
	defer closeutil.Ignore(file)
	read, err := readAndHash(ctx, file, io.Discard)
	if err != nil {
		return err
	}
	if read.Length != object.Length || read.SHA256 != object.SHA256 {
		return backend.ErrChecksumMismatch
	}
	return nil
}

func (s *Store) readMetadata(key string) (metadata, error) {
	path, err := s.validatedObjectPath(key, metaSuffix)
	if err != nil {
		return metadata{}, err
	}
	data, err := readValidatedFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return metadata{}, backend.ErrNotFound
	}
	if err != nil {
		return metadata{}, err
	}
	var meta metadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return metadata{}, err
	}
	if meta.Key != key {
		return metadata{}, fmt.Errorf("backend fs: metadata key %q does not match requested key %q", meta.Key, key)
	}
	return meta, nil
}

func objectFromMetadata(meta metadata) (backend.Object, error) {
	sum, err := hex.DecodeString(meta.SHA256Hex)
	if err != nil {
		return backend.Object{}, err
	}
	if len(sum) != sha256.Size {
		return backend.Object{}, fmt.Errorf("backend fs: metadata sha256 is %d bytes", len(sum))
	}
	object := backend.Object{Key: meta.Key, Length: meta.Length}
	copy(object.SHA256[:], sum)
	return object, nil
}

func (s *Store) writeTempMetadata(meta metadata) (string, error) {
	data, err := json.Marshal(meta)
	if err != nil {
		return "", err
	}
	file, err := os.CreateTemp(s.objectsDir, "put-*.meta.tmp")
	if err != nil {
		return "", err
	}
	path, err := s.validatedObjectStorePath(file.Name())
	if err != nil {
		return "", errors.Join(err, file.Close(), s.removeObjectStorePath(file.Name()))
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = s.removeObjectStorePath(path)
		return "", err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = s.removeObjectStorePath(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = s.removeObjectStorePath(path)
		return "", err
	}
	return path, nil
}

func (s *Store) objectPath(key, suffix string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(s.objectsDir, hex.EncodeToString(sum[:])+suffix)
}

func (s *Store) validatedObjectPath(key, suffix string) (string, error) {
	return s.validatedObjectStorePath(s.objectPath(key, suffix))
}

func (s *Store) validatedObjectStorePath(path string) (string, error) {
	return safepath.UnderDir(s.objectsDir, path)
}

func (s *Store) openObjectFile(key, suffix string) (*os.File, error) {
	path, err := s.validatedObjectPath(key, suffix)
	if err != nil {
		return nil, err
	}
	// #nosec G304 -- object paths are hash-derived and validated under objectsDir.
	return os.Open(path)
}

func (s *Store) removeObjectStorePath(path string) error {
	path, err := s.validatedObjectStorePath(path)
	if err != nil {
		return err
	}
	// #nosec G703 -- path is validated under objectsDir before removal.
	return os.Remove(path)
}

func readValidatedFile(path string) ([]byte, error) {
	// #nosec G304 G703 -- callers validate paths under the configured storage directory.
	return os.ReadFile(path)
}

func readAndHash(ctx context.Context, reader io.Reader, writer io.Writer) (backend.Object, error) {
	hasher := sha256.New()
	counter := &countingWriter{}
	multi := io.MultiWriter(writer, hasher, counter)
	if err := copyWithContext(ctx, multi, reader); err != nil {
		return backend.Object{}, err
	}
	var object backend.Object
	object.Length = counter.n
	copy(object.SHA256[:], hasher.Sum(nil))
	return object, nil
}

func copyWithContext(ctx context.Context, writer io.Writer, reader io.Reader) error {
	buf := make([]byte, 1024*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := reader.Read(buf)
		if n > 0 {
			written, err := writer.Write(buf[:n])
			if err != nil {
				return err
			}
			if written != n {
				return io.ErrShortWrite
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func syncFile(path string) error {
	// #nosec G304 G703 -- callers pass paths validated under the configured storage directory.
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}

func syncDir(path string) error {
	// #nosec G304 G703 -- callers pass configured storage directories.
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

type countingWriter struct {
	n uint64
}

func (w *countingWriter) Write(p []byte) (int, error) {
	w.n += uint64(len(p))
	return len(p), nil
}
