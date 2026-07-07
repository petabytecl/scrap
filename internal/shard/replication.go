package shard

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/encryption"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

const (
	replicationChunkSize = 64 * 1024
	majorityDivisor      = 2
)

type DocumentReplicator interface {
	ReplicateDocument(ctx context.Context, addr string, init *scrapv1.ReplicateDocumentInit, chunks [][]byte) ([]byte, error)
}

func (s *Shard) replicateDocument(ctx context.Context, prep *scrapv1.OpenlogEntry, contentType string, result block.AppendResult, data, envelope []byte) error {
	if s.replicator == nil || len(s.peers) <= 1 {
		return nil
	}

	init := &scrapv1.ReplicateDocumentInit{
		TransactionId:      prep.TransactionId,
		DocumentName:       prep.DocumentName,
		ContentType:        contentType,
		BlockId:            prep.BlockId,
		StartOffset:        prep.StartOffset,
		FrameCount:         result.FrameCount,
		TotalBytes:         result.Size,
		Sha256:             result.SHA256[:],
		EncryptionEnvelope: envelope,
		ShardId:            s.shardID,
	}
	chunks := splitReplicationChunks(data)

	successes := 0
	var firstErr error
	for _, addr := range s.replicationAddrs() {
		sha, err := s.replicator.ReplicateDocument(ctx, addr, init, chunks)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if !bytes.Equal(sha, result.SHA256[:]) {
			if firstErr == nil {
				firstErr = fmt.Errorf("peer %s returned SHA-256 %x, want %x", addr, sha, result.SHA256)
			}
			continue
		}
		successes++
	}

	if quorumMet(len(s.peers), successes) {
		return nil
	}
	if firstErr != nil {
		return fmt.Errorf("%w: replicate document to quorum: %w", storeapi.ErrResourceExhausted, firstErr)
	}
	return fmt.Errorf("%w: replicate document to quorum", storeapi.ErrResourceExhausted)
}

func (s *Shard) replicationAddrs() []string {
	if len(s.peers) <= 1 {
		return nil
	}
	selfAddr := s.peers[s.raftID]
	addrs := make([]string, 0, len(s.peers)-1)
	for id, addr := range s.peers {
		if id == s.raftID || addr == selfAddr {
			continue
		}
		addrs = append(addrs, addr)
	}
	return addrs
}

func quorumMet(totalVoters, successfulPeers int) bool {
	quorum := totalVoters/majorityDivisor + 1
	return 1+successfulPeers >= quorum
}

func splitReplicationChunks(data []byte) [][]byte {
	if len(data) == 0 {
		return nil
	}
	chunks := make([][]byte, 0, (len(data)+replicationChunkSize-1)/replicationChunkSize)
	for start := 0; start < len(data); start += replicationChunkSize {
		end := min(start+replicationChunkSize, len(data))
		chunks = append(chunks, data[start:end])
	}
	return chunks
}

func (s *Shard) AppendReplicatedDocument(ctx context.Context, init *scrapv1.ReplicateDocumentInit, body io.Reader) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.advanceReplicaBlockLocked(init.GetBlockId()); err != nil {
		return nil, err
	}
	if s.blockWriter == nil || s.blockWriter.BlockID() != init.GetBlockId() {
		return nil, fmt.Errorf("shard: replica block %d is not open", init.GetBlockId())
	}

	// Validate offset before appending to prevent poisoning the follower block with data at the wrong position.
	wantOffset := safeUint64ToInt64(init.GetStartOffset())
	if err := s.ensureReplicaAppendOffsetLocked(ctx, init, wantOffset); err != nil {
		return nil, err
	}

	attempt, err := s.beginOpenlogWriteAttempt(openlogWriteAttemptConfig{
		txID:        init.GetTransactionId(),
		docName:     init.GetDocumentName(),
		contentType: init.GetContentType(),
		blockID:     init.GetBlockId(),
		startOffset: wantOffset,
	})
	if err != nil {
		return nil, err
	}

	result, err := s.appendReplicatedPayload(ctx, init, body)
	if err != nil {
		s.abortReplicatedWriteAttempt(attempt, wantOffset)
		return nil, fmt.Errorf("shard: append replicated document: %w", err)
	}
	if err := validateReplicatedAppend(init, result, wantOffset); err != nil {
		s.abortReplicatedWriteAttempt(attempt, wantOffset)
		return nil, err
	}
	return result.SHA256[:], nil
}

func (s *Shard) abortReplicatedWriteAttempt(attempt *openlogWriteAttempt, startOffset int64) {
	if s.blockWriter != nil {
		_ = s.blockWriter.Truncate(startOffset)
	}
	attempt.cleanupOnAbort()
}

// ensureReplicaAppendOffsetLocked reconciles the replica Block offset with the
// leader's declared start offset: repair from a peer when behind, roll back an
// uncommitted overhang when ahead.
func (s *Shard) ensureReplicaAppendOffsetLocked(ctx context.Context, init *scrapv1.ReplicateDocumentInit, wantOffset int64) error {
	currentOffset := s.blockWriter.Offset()
	if currentOffset < wantOffset {
		if err := s.repairReplicaOffsetLocked(ctx, init.GetBlockId(), wantOffset); err != nil {
			return fmt.Errorf("shard: replica offset %d, want %d: repair failed: %w", currentOffset, wantOffset, err)
		}
		currentOffset = s.blockWriter.Offset()
	}
	if currentOffset > wantOffset {
		if err := s.rollbackReplicaOverhangLocked(init.GetBlockId(), wantOffset, currentOffset); err != nil {
			return fmt.Errorf("shard: replica offset %d, want %d: %w", currentOffset, wantOffset, err)
		}
		currentOffset = s.blockWriter.Offset()
	}
	if currentOffset != wantOffset {
		return fmt.Errorf("shard: replica offset %d, want %d", currentOffset, wantOffset)
	}
	return nil
}

// rollbackReplicaOverhangLocked reclaims replica Block bytes past wantOffset.
// A replica ends up ahead of the leader when it accepted a replicated append
// whose write the leader then aborted: the leader truncates its own Block, but
// replicas that already appended keep the Frames. Those Frames were never
// committed — a commit indexes Frames the leader kept — so rolling back to
// wantOffset restores replication instead of rejecting every later append
// until restart recovery truncates the same region (and, until then, applying
// committed entries whose bytes this replica never received). Refuse when the
// open Block's index references the overhang: an indexed Document there means
// committed bytes.
func (s *Shard) rollbackReplicaOverhangLocked(blockID uint64, wantOffset, currentOffset int64) error {
	indexed, err := s.blockIndexHasEntryAtOrPast(blockID, wantOffset)
	if err != nil {
		return err
	}
	if indexed {
		return fmt.Errorf("shard: replica overhang past offset %d holds indexed documents", wantOffset)
	}
	boundary, err := s.replicaRollbackTargetIsDocumentBoundary(blockID, wantOffset)
	if err != nil {
		return err
	}
	if !boundary {
		return fmt.Errorf("shard: replica rollback offset %d is not a document boundary", wantOffset)
	}
	if err := s.blockWriter.Truncate(wantOffset); err != nil {
		return fmt.Errorf("shard: roll back replica overhang: %w", err)
	}
	// The aborted writes' preps still point at the overhang. With the bytes
	// reclaimed they must not survive: Openlog recovery would truncate at
	// their start offset again and cut whatever commits there afterwards.
	s.removeOverhangPreps(blockID, wantOffset)
	s.logger.Warn("shard: rolled back replica overhang from aborted leader write",
		"block_id", blockID, "from_offset", currentOffset, "to_offset", wantOffset)
	return nil
}

// blockIndexHasEntryAtOrPast reports whether the Block's index references any
// committed Document at or past wantOffset. A missing index means no committed
// entries; any other read failure is surfaced so callers can fail closed
// rather than truncate bytes they cannot prove uncommitted. Used by both the
// replica overhang rollback and openlog recovery to refuse truncating a region
// that already holds committed Frames.
func (s *Shard) blockIndexHasEntryAtOrPast(blockID uint64, wantOffset int64) (bool, error) {
	ir, err := block.OpenIndexReader(s.idxPath(blockID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("shard: open block index: %w", err)
	}
	defer func() { _ = ir.Close() }()
	for _, entry := range ir.Entries() {
		if entry.FirstFrameOff >= wantOffset {
			return true, nil
		}
	}
	return false, nil
}

// replicaRollbackTargetIsDocumentBoundary reports whether wantOffset lands
// exactly between whole Documents in the Block. The overhang of an aborted
// leader write always starts at a Document boundary; any other offset (a
// malformed init from a buggy peer) must be refused before Truncate physically
// cuts a Document mid-frame. Scan failures fail closed as "not a boundary".
func (s *Shard) replicaRollbackTargetIsDocumentBoundary(blockID uint64, wantOffset int64) (bool, error) {
	f, err := os.Open(s.blockPath(blockID))
	if err != nil {
		return false, fmt.Errorf("shard: open replica block for rollback scan: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(block.HeaderSize, io.SeekStart); err != nil {
		return false, fmt.Errorf("shard: seek replica block: %w", err)
	}
	offset := int64(block.HeaderSize)
	for offset < wantOffset {
		_, payload, err := block.ReadFrame(f)
		if err != nil {
			return false, fmt.Errorf("shard: scan replica block for rollback: %w", err)
		}
		offset += int64(block.FrameHeaderSize) + int64(len(payload))
	}
	if offset != wantOffset {
		return false, nil
	}
	// The first overhang Frame exists (the caller saw currentOffset > wantOffset)
	// and must start a Document; a continuation Frame means wantOffset splits one.
	hdr, _, err := block.ReadFrame(f)
	if err != nil {
		return false, fmt.Errorf("shard: read overhang frame: %w", err)
	}
	return hdr.FrameSeq == 0, nil
}

func validateReplicatedAppend(init *scrapv1.ReplicateDocumentInit, result block.AppendResult, wantOffset int64) error {
	if result.FirstFrameOffset != wantOffset {
		return fmt.Errorf("shard: replicated first frame offset %d, want %d", result.FirstFrameOffset, init.GetStartOffset())
	}
	if result.FrameCount != init.GetFrameCount() {
		return fmt.Errorf("shard: replicated frame count %d, want %d", result.FrameCount, init.GetFrameCount())
	}
	if result.Size != init.GetTotalBytes() {
		return fmt.Errorf("shard: replicated size %d, want %d", result.Size, init.GetTotalBytes())
	}
	if !bytes.Equal(result.SHA256[:], init.GetSha256()) {
		return fmt.Errorf("shard: replicated SHA-256 %x, want %x", result.SHA256, init.GetSha256())
	}
	return nil
}

func (s *Shard) appendReplicatedPayload(ctx context.Context, init *scrapv1.ReplicateDocumentInit, body io.Reader) (block.AppendResult, error) {
	if len(init.GetEncryptionEnvelope()) == 0 {
		return s.blockWriter.AppendDocument(init.GetTransactionId(), init.GetDocumentName(), init.GetContentType(), body)
	}

	envelope, err := encryption.ParseEnvelope(init.GetEncryptionEnvelope())
	if err != nil {
		return block.AppendResult{}, mapEncryptionError(err)
	}
	storedBytes, err := io.ReadAll(body)
	if err != nil {
		return block.AppendResult{}, fmt.Errorf("shard: read replicated encrypted payload: %w", err)
	}
	if int64(len(storedBytes)) != envelope.CiphertextLength {
		return block.AppendResult{}, fmt.Errorf("%w: replicated ciphertext length %d, want %d", storeapi.ErrDataLoss, len(storedBytes), envelope.CiphertextLength)
	}
	var sha [sha256.Size]byte
	if len(init.GetSha256()) != sha256.Size {
		return block.AppendResult{}, fmt.Errorf("%w: replicated SHA-256 length %d, want %d", storeapi.ErrDataLoss, len(init.GetSha256()), sha256.Size)
	}
	copy(sha[:], init.GetSha256())
	frames, err := splitReplicatedStoredFrames(storedBytes, init.GetFrameCount())
	if err != nil {
		return block.AppendResult{}, err
	}
	if !s.encryption.enabled() {
		return block.AppendResult{}, storeapi.NewUnavailable(storeapi.UnavailableReasonCryptoUnavailable, "key material unavailable")
	}
	// Streaming verify: authenticate every frame and the whole-Document
	// digest without ever materializing the plaintext.
	decryptor, err := encryption.NewDocumentDecryptor(ctx, s.encryption.Transit, encryption.DocumentIdentity{
		TransactionID: init.GetTransactionId(),
		DocumentName:  init.GetDocumentName(),
	}, init.GetEncryptionEnvelope(), sha, init.GetTotalBytes())
	if err != nil {
		return block.AppendResult{}, mapEncryptionError(err)
	}
	defer decryptor.Close()
	if err := decryptor.Verify(encryption.NewSliceFrameSource(frames)); err != nil {
		return block.AppendResult{}, mapEncryptionError(err)
	}
	return s.blockWriter.AppendDocumentFrames(init.GetTransactionId(), init.GetDocumentName(), init.GetContentType(), block.DocumentFrames{
		Payloads: frames,
		SHA256:   sha,
		Size:     init.GetTotalBytes(),
	})
}

// maxReplicatedFrameCount bounds a peer-declared frame count by the largest
// frame count a maximum-size Document can produce, so a malformed init message
// cannot drive the frames allocation below.
const maxReplicatedFrameCount = storeapi.MaxDocumentBytes/block.MaxFramePayload + 1

func splitReplicatedStoredFrames(data []byte, frameCount uint32) ([][]byte, error) {
	if frameCount == 0 {
		if len(data) != 0 {
			return nil, fmt.Errorf("shard: replicated payload has %d bytes for zero frames", len(data))
		}
		return nil, nil
	}
	if frameCount > maxReplicatedFrameCount {
		return nil, fmt.Errorf("shard: replicated frame count %d exceeds max %d", frameCount, maxReplicatedFrameCount)
	}
	frames := make([][]byte, 0, frameCount)
	remaining := data
	for frameSeq := range frameCount {
		if frameSeq == frameCount-1 {
			if len(remaining) > block.MaxFramePayload {
				return nil, fmt.Errorf("shard: final replicated frame has %d bytes", len(remaining))
			}
			frames = append(frames, append([]byte(nil), remaining...))
			return frames, nil
		}
		if len(remaining) < block.MaxFramePayload {
			return nil, fmt.Errorf("shard: replicated frame %d truncated", frameSeq)
		}
		frames = append(frames, append([]byte(nil), remaining[:block.MaxFramePayload]...))
		remaining = remaining[block.MaxFramePayload:]
	}
	return frames, nil
}

func (s *Shard) advanceReplicaBlockLocked(targetBlockID uint64) error {
	if s.blockWriter != nil && s.blockWriter.BlockID() > targetBlockID {
		return s.reopenPreviousReplicaBlockLocked(targetBlockID)
	}
	for s.blockWriter != nil && s.blockWriter.BlockID() < targetBlockID {
		if err := s.idxWriter.Close(); err != nil {
			return fmt.Errorf("shard: close replica index: %w", err)
		}
		if err := s.blockWriter.Close(); err != nil {
			return fmt.Errorf("shard: close replica block: %w", err)
		}
		if err := s.openNewBlock(); err != nil {
			return fmt.Errorf("shard: open replica block: %w", err)
		}
	}
	return nil
}

func (s *Shard) reopenPreviousReplicaBlockLocked(targetBlockID uint64) error {
	currentBlockID := s.blockWriter.BlockID()
	if currentBlockID != targetBlockID+1 {
		return fmt.Errorf("shard: replica block %d is ahead of target block %d", currentBlockID, targetBlockID)
	}
	if s.blockWriter.Offset() != block.HeaderSize {
		return fmt.Errorf("shard: replica block %d is ahead of target block %d and is not empty", currentBlockID, targetBlockID)
	}
	bw, err := block.OpenWriter(s.blockPath(targetBlockID), s.shardID, targetBlockID)
	if err != nil {
		return fmt.Errorf("shard: reopen replica block %d: %w", targetBlockID, err)
	}
	iw, err := block.OpenIndexWriter(s.idxPath(targetBlockID))
	if err != nil {
		_ = bw.Close()
		return fmt.Errorf("shard: reopen replica index %d: %w", targetBlockID, err)
	}

	if err := s.idxWriter.Close(); err != nil {
		_ = iw.Close()
		_ = bw.Close()
		return fmt.Errorf("shard: close empty replica index: %w", err)
	}
	if err := s.blockWriter.Close(); err != nil {
		_ = iw.Close()
		_ = bw.Close()
		return fmt.Errorf("shard: close empty replica block: %w", err)
	}
	s.blockWriter = bw
	s.idxWriter = iw
	s.nextBlockID = currentBlockID

	if err := removeEmptyReplicaBlockFiles(s.blockPath(currentBlockID), s.idxPath(currentBlockID)); err != nil {
		return err
	}
	return nil
}

func removeEmptyReplicaBlockFiles(paths ...string) error {
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("shard: remove empty replica block file %s: %w", path, err)
		}
	}
	return nil
}
