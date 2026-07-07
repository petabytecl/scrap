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
	"time"

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

// openlogTruncationCandidate is a prep whose Document looked uncommitted
// before raft replay. The truncation decision is deferred: the projection is
// only a derived view at recovery time, and a Document committed on quorum
// but not yet applied locally is invisible in it. Truncating before replay
// would destroy committed Frames that replay then indexes past EOF (#463).
type openlogTruncationCandidate struct {
	prepName    string
	txID        string
	docName     string
	blockID     uint64
	startOffset int64
}

// recoverOpenlog is recovery phase A, run before raft opens: it resolves prep
// files against the projection but only RECORDS truncation candidates. Phase
// B (resolveOpenlogTruncations) makes the destructive decision after raft
// replay, when the projection reflects every locally-known committed entry.
func (s *Shard) recoverOpenlog() ([]openlogTruncationCandidate, error) {
	entries, err := os.ReadDir(s.openlogDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var prepFiles []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".prep") {
			prepFiles = append(prepFiles, e.Name())
		}
	}
	sort.Strings(prepFiles)

	var candidates []openlogTruncationCandidate
	for _, name := range prepFiles {
		candidate, deferred, err := s.recoverPrepFile(name)
		if err != nil {
			return nil, err
		}
		if deferred {
			candidates = append(candidates, candidate)
		}
	}

	return candidates, nil
}

func (s *Shard) recoverPrepFile(name string) (openlogTruncationCandidate, bool, error) {
	path := filepath.Join(s.openlogDir, name)
	data, err := os.ReadFile(path) //nolint:gosec // path constructed from known openlogDir + directory listing
	if err != nil {
		return openlogTruncationCandidate{}, false, fmt.Errorf("shard: read prep %s: %w", name, err)
	}

	entry := &scrapv1.OpenlogEntry{}
	if err := proto.Unmarshal(data, entry); err != nil {
		return openlogTruncationCandidate{}, false, fmt.Errorf("shard: unmarshal prep %s: %w", name, err)
	}

	if entry.StartOffset > math.MaxInt64 {
		return openlogTruncationCandidate{}, false, fmt.Errorf("shard: prep %s: start offset %d overflows int64", name, entry.StartOffset)
	}

	exists, err := s.documentVisibleInProjectionLenient(entry.TransactionId, entry.DocumentName)
	if err != nil {
		return openlogTruncationCandidate{}, false, fmt.Errorf("shard: resolve prep %s: %w", name, err)
	}
	if exists {
		_ = os.Remove(path) // best-effort cleanup of already-committed prep
		return openlogTruncationCandidate{}, false, nil
	}

	// Not visible yet — but the raft WAL may hold a committed CommitDocument
	// for it that simply never applied before the crash. Keep the prep and
	// defer the truncation decision until after replay (#463).
	return openlogTruncationCandidate{
		prepName:    name,
		txID:        entry.TransactionId,
		docName:     entry.DocumentName,
		blockID:     entry.BlockId,
		startOffset: safeUint64ToInt64(entry.StartOffset),
	}, true, nil
}

// finishOpenlogRecovery waits for raft replay to catch up with the commit
// index the local WAL recorded at boot, then makes the deferred truncation
// decisions (recovery phase B, #463). Runs after s.raft is assigned and
// before the Shard serves traffic.
func (s *Shard) finishOpenlogRecovery(candidates []openlogTruncationCandidate) error {
	if len(candidates) == 0 {
		return nil
	}
	s.waitForRaftReplay()
	return s.resolveOpenlogTruncations(candidates)
}

// waitForRaftReplay blocks until the apply loop has caught up with the boot
// commit index. Committed entries replay from the local log without quorum,
// so this terminates without peers; apply failures panic, so it cannot hang
// on a wedged apply.
func (s *Shard) waitForRaftReplay() {
	target := s.raft.CommitIndex()
	for s.raft.AppliedIndex() < target {
		time.Sleep(time.Millisecond)
	}
}

// resolveOpenlogTruncations is recovery phase B, run after raft replay has
// caught up to the boot commit index: with the projection now authoritative
// for everything the local WAL committed, decide each deferred truncation.
func (s *Shard) resolveOpenlogTruncations(candidates []openlogTruncationCandidate) error {
	for _, candidate := range candidates {
		if err := s.resolveOpenlogTruncation(candidate); err != nil {
			return err
		}
	}
	return nil
}

func (s *Shard) resolveOpenlogTruncation(candidate openlogTruncationCandidate) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := filepath.Join(s.openlogDir, candidate.prepName)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		// Replay applied the commit and its apply-side cleanup already removed
		// the prep — the bytes are committed; nothing to truncate.
		return nil
	}

	exists, err := s.documentVisibleInProjectionLenient(candidate.txID, candidate.docName)
	if err != nil {
		return fmt.Errorf("shard: resolve prep %s after replay: %w", candidate.prepName, err)
	}
	if exists {
		// Committed on quorum before the crash, applied during replay. The
		// bytes are authoritative — truncating them would break invariant 1.
		_ = os.Remove(path) // best-effort cleanup of already-committed prep
		return nil
	}

	// The prep's Document never committed, but another Document may have
	// committed at or past its offset in the same Block (an ambiguous propose
	// kept this prep's bytes, then later writes committed above it). Truncating
	// to the prep offset would destroy those committed Frames while their .idx
	// entries survive pointing past EOF. Refuse and keep the prep; a corrupt or
	// unreadable index fails closed the same way.
	committedAbove, err := s.blockIndexHasEntryAtOrPast(candidate.blockID, candidate.startOffset)
	if err != nil {
		return fmt.Errorf("shard: inspect block %d index during recovery: %w", candidate.blockID, err)
	}
	if committedAbove {
		s.logger.Error("shard: openlog recovery kept prep; committed documents sit at or past its offset",
			"block_id", candidate.blockID, "start_offset", candidate.startOffset, "prep", candidate.prepName)
		return nil
	}

	blkPath := s.blockPath(candidate.blockID)
	if err := truncateFile(blkPath, candidate.startOffset); err != nil {
		return fmt.Errorf("shard: truncate block %d: %w", candidate.blockID, err)
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

// removeOverhangPreps deletes prep files for aborted writes whose Block bytes
// at or past wantOffset were just reclaimed by a replica overhang rollback.
// Best-effort like cleanupCommittedOpenlogPreps: a survivor is re-truncated at
// the same offset by recovery, which is harmless only until new Documents
// commit there — hence the removal happens before the next append.
func (s *Shard) removeOverhangPreps(blockID uint64, wantOffset int64) {
	entries, err := os.ReadDir(s.openlogDir)
	if err != nil {
		return
	}
	for _, dirEntry := range entries {
		if dirEntry.IsDir() || !strings.HasSuffix(dirEntry.Name(), ".prep") {
			continue
		}
		path := filepath.Join(s.openlogDir, dirEntry.Name())
		data, err := os.ReadFile(path) //nolint:gosec // path constructed from known openlogDir + directory listing
		if err != nil {
			continue
		}
		entry := &scrapv1.OpenlogEntry{}
		if err := proto.Unmarshal(data, entry); err != nil {
			continue
		}
		if entry.GetBlockId() != blockID || safeUint64ToInt64(entry.GetStartOffset()) < wantOffset {
			continue
		}
		_ = os.Remove(path) // best-effort cleanup after overhang rollback
	}
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
	// os.File.Truncate grows the file with zero bytes when size exceeds the
	// current length. During recovery the target offset must only ever shrink
	// the Block; growing it would inject a non-Frame zero region that every
	// frame scanner reads as corruption. Never extend.
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	if size >= info.Size() {
		return f.Close()
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
