package shard

// Openlog recovery: persist in-flight write preparation records and repair
// partial Block writes before the Shard starts serving.

import (
	"fmt"
	"math"
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
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // path constructed from known openlogDir + ULID
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
		if err := s.recoverPrepFile(name); err != nil {
			return err
		}
	}

	return nil
}

func (s *Shard) recoverPrepFile(name string) error {
	path := filepath.Join(s.openlogDir, name)
	data, err := os.ReadFile(path) //nolint:gosec // path constructed from known openlogDir + directory listing
	if err != nil {
		return fmt.Errorf("shard: read prep %s: %w", name, err)
	}

	entry := &scrapv1.OpenlogEntry{}
	if err := proto.Unmarshal(data, entry); err != nil {
		return fmt.Errorf("shard: unmarshal prep %s: %w", name, err)
	}

	if entry.StartOffset > math.MaxInt64 {
		return fmt.Errorf("shard: prep %s: start offset %d overflows int64", name, entry.StartOffset)
	}

	exists, err := s.documentVisibleInProjectionLenient(entry.TransactionId, entry.DocumentName)
	if err != nil {
		return fmt.Errorf("shard: resolve prep %s: %w", name, err)
	}
	if exists {
		_ = os.Remove(path) // best-effort cleanup of already-committed prep
		return nil
	}

	blkPath := s.blockPath(entry.BlockId)
	if err := truncateFile(blkPath, safeUint64ToInt64(entry.StartOffset)); err != nil {
		return fmt.Errorf("shard: truncate block %d: %w", entry.BlockId, err)
	}
	if err := syncDir(filepath.Dir(blkPath)); err != nil {
		return fmt.Errorf("shard: sync truncated block dir: %w", err)
	}
	_ = os.Remove(path) // best-effort cleanup after truncation
	return nil
}

func (s *Shard) cleanupCommittedOpenlogPreps(doc *scrapv1.CommitDocument) {
	entries, err := os.ReadDir(s.openlogDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".prep") {
			continue
		}
		s.cleanupCommittedOpenlogPrep(entry.Name(), doc)
	}
}

func (s *Shard) cleanupCommittedOpenlogPrep(name string, doc *scrapv1.CommitDocument) {
	path := filepath.Join(s.openlogDir, name)
	data, err := os.ReadFile(path) //nolint:gosec // path constructed from known openlogDir + directory listing
	if err != nil {
		return
	}
	entry := &scrapv1.OpenlogEntry{}
	if err := proto.Unmarshal(data, entry); err != nil {
		return
	}
	if !openlogPrepMatchesCommit(entry, doc) {
		return
	}
	_ = os.Remove(path) // best-effort cleanup; recovery also handles completed prep files.
}

func openlogPrepMatchesCommit(entry *scrapv1.OpenlogEntry, doc *scrapv1.CommitDocument) bool {
	return entry.GetTransactionId() == doc.GetTransactionId() &&
		entry.GetDocumentName() == doc.GetDocumentName() &&
		entry.GetBlockId() == doc.GetBlockId() &&
		entry.GetStartOffset() == doc.GetFirstFrameOff()
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
	if truncErr == nil {
		truncErr = f.Sync()
	}
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
