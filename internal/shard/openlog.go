package shard

// Openlog recovery: persist in-flight write preparation records and repair
// partial Block writes before the Shard starts serving.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"google.golang.org/protobuf/proto"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
)

func (s *Shard) writePrepFile(writeID string, entry *scrapv1.OpenlogEntry) error {
	data, err := proto.Marshal(entry)
	if err != nil {
		return err
	}
	path := s.prepPath(writeID)
	f, err := os.Create(path) //nolint:gosec // path constructed from known openlogDir + ULID
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close() // best-effort close after write failure
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close() // best-effort close after sync failure
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return syncDir(s.openlogDir)
}

func (s *Shard) prepPath(writeID string) string {
	return filepath.Join(s.openlogDir, writeID+".prep")
}

func (s *Shard) recoverOpenlog() error {
	entries, err := os.ReadDir(s.openlogDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var prepFiles []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".prep") {
			prepFiles = append(prepFiles, e.Name())
		}
	}
	sort.Strings(prepFiles)

	for _, name := range prepFiles {
		path := filepath.Join(s.openlogDir, name)
		data, err := os.ReadFile(path) //nolint:gosec // path constructed from known openlogDir + directory listing
		if err != nil {
			return fmt.Errorf("shard: read prep %s: %w", name, err)
		}

		entry := &scrapv1.OpenlogEntry{}
		if err := proto.Unmarshal(data, entry); err != nil {
			return fmt.Errorf("shard: unmarshal prep %s: %w", name, err)
		}

		if s.docExistsInPebble(entry.TransactionId, entry.DocumentName) {
			_ = os.Remove(path) // best-effort cleanup of already-committed prep
			continue
		}

		blkPath := s.blockPath(entry.BlockId)
		if err := truncateFile(blkPath, safeUint64ToInt64(entry.StartOffset)); err != nil {
			return fmt.Errorf("shard: truncate block %d: %w", entry.BlockId, err)
		}
		_ = os.Remove(path) // best-effort cleanup after truncation
	}

	return nil
}

func truncateFile(path string, size int64) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0o644) //nolint:gosec // path constructed from known blocksDir + block ID
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	truncErr := f.Truncate(size)
	if closeErr := f.Close(); closeErr != nil && truncErr == nil {
		return closeErr
	}
	return truncErr
}

func syncDir(path string) error {
	f, err := os.Open(path) //nolint:gosec // path is the shard's own openlogDir
	if err != nil {
		return err
	}
	syncErr := f.Sync()
	if closeErr := f.Close(); closeErr != nil && syncErr == nil {
		return closeErr
	}
	return syncErr
}
