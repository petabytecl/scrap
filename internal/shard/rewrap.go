package shard

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"time"

	"google.golang.org/protobuf/proto"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/encryption"
	"github.com/petabytecl/scrap/internal/rewrap"
	storeapi "github.com/petabytecl/scrap/internal/store"
	"github.com/petabytecl/scrap/internal/ulid"
)

func (s *Shard) RewrapDocument(ctx context.Context, req rewrap.Request) (rewrap.Result, error) {
	result := rewrap.Result{
		Status:        rewrap.StatusFailed,
		Reason:        rewrap.ReasonInternalError,
		TransactionID: req.TransactionID,
		DocumentName:  req.DocumentName,
	}
	if err := validateRewrapRequest(req); err != nil {
		return s.finishRewrap(result, err)
	}
	if err := s.requireLeader(); err != nil {
		return s.finishRewrap(result, err)
	}
	if !s.encryption.enabled() {
		return s.finishRewrap(result, storeapi.NewUnavailable(storeapi.UnavailableReasonCryptoUnavailable, "key material unavailable"))
	}

	entry, envelope, err := s.rewrapSnapshot(req)
	if err != nil {
		return s.finishRewrap(result, err)
	}
	result.BlockID = entry.blockID
	result.TransitMount = envelope.TransitMount
	result.TransitKey = envelope.TransitKey
	result.OldKeyVersion = envelope.KeyVersion
	result.NewKeyVersion = envelope.KeyVersion

	result, err = s.rewrapDocumentEnvelope(ctx, req, entry.blockID, envelope, result)
	return s.finishRewrap(result, err)
}

func (s *Shard) rewrapDocumentEnvelope( //nolint:cyclop // rewrap validates envelope identity, Transit routing, and monotonic key versions before proposing
	ctx context.Context,
	req rewrap.Request,
	blockID uint64,
	envelope encryption.Envelope,
	result rewrap.Result,
) (rewrap.Result, error) {
	if req.KeyVersion > 0 && envelope.KeyVersion == req.KeyVersion {
		result.Status = rewrap.StatusOK
		result.Reason = rewrap.ReasonIdempotent
		return result, nil
	}
	if req.KeyVersion > 0 && req.KeyVersion < envelope.KeyVersion {
		return result, fmt.Errorf("%w: rewrap target key version %d is below current %d", rewrap.ErrInvalidRequest, req.KeyVersion, envelope.KeyVersion)
	}

	rewrapped, err := s.encryption.Transit.RewrapDataKey(ctx, encryption.RewrapDataKeyRequest{
		WrappedKey: envelope.WrappedDataKey,
		Context: encryption.DocumentKeyContext(encryption.DocumentIdentity{
			TransactionID: req.TransactionID,
			DocumentName:  req.DocumentName,
		}),
		KeyVersion:   req.KeyVersion,
		TransitMount: envelope.TransitMount,
		TransitKey:   envelope.TransitKey,
	})
	if err != nil {
		return result, mapRewrapEncryptionError(err)
	}
	if !rewrapped.Changed && (req.KeyVersion == 0 || rewrapped.Version == envelope.KeyVersion) {
		result.Status = rewrap.StatusOK
		result.Reason = rewrap.ReasonIdempotent
		return result, nil
	}
	if rewrapped.Version <= 0 {
		return result, fmt.Errorf("%w: rewrapped key version is missing", storeapi.ErrDataLoss)
	}
	if rewrapped.Version < envelope.KeyVersion {
		return result, fmt.Errorf("%w: rewrap downgraded key version from %d to %d", storeapi.ErrDataLoss, envelope.KeyVersion, rewrapped.Version)
	}
	if req.KeyVersion > 0 && rewrapped.Version != req.KeyVersion {
		return result, fmt.Errorf("%w: rewrap returned key version %d, want %d", storeapi.ErrDataLoss, rewrapped.Version, req.KeyVersion)
	}

	updatedEnvelope, err := rewrappedEnvelopeBytes(envelope, rewrapped)
	if err != nil {
		return result, err
	}
	result.NewKeyVersion = rewrapped.Version
	result.Changed = true
	result.RewrappedAt = time.Now().UTC()

	if err := s.proposeRewrapDocument(ctx, blockID, req, result, updatedEnvelope); err != nil {
		return result, err
	}

	result.Status = rewrap.StatusOK
	result.Reason = rewrap.ReasonOK
	return result, nil
}

func validateRewrapRequest(req rewrap.Request) error {
	switch {
	case req.TransactionID == "":
		return fmt.Errorf("%w: transaction_id is required", rewrap.ErrInvalidRequest)
	case req.DocumentName == "":
		return fmt.Errorf("%w: document_name is required", rewrap.ErrInvalidRequest)
	case req.KeyVersion < 0:
		return fmt.Errorf("%w: key_version must be non-negative", rewrap.ErrInvalidRequest)
	default:
		return nil
	}
}

func (s *Shard) rewrapSnapshot(req rewrap.Request) (docWithBlock, encryption.Envelope, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, err := s.findDocEntry(req.TransactionID, req.DocumentName)
	if err != nil {
		return docWithBlock{}, encryption.Envelope{}, err
	}
	if len(entry.EncryptionEnvelope) == 0 {
		return docWithBlock{}, encryption.Envelope{}, rewrap.ErrNotEncrypted
	}
	envelope, err := encryption.ParseEnvelope(entry.EncryptionEnvelope)
	if err != nil {
		return docWithBlock{}, encryption.Envelope{}, mapEncryptionError(err)
	}
	entry.EncryptionEnvelope = append([]byte(nil), entry.EncryptionEnvelope...)
	return entry, envelope, nil
}

func rewrappedEnvelopeBytes(envelope encryption.Envelope, rewrapped encryption.RewrappedKey) ([]byte, error) {
	envelope.WrappedDataKey = rewrapped.WrappedKey
	envelope.KeyVersion = rewrapped.Version
	return encryption.MarshalEnvelope(envelope)
}

func (s *Shard) proposeRewrapDocument(
	ctx context.Context,
	blockID uint64,
	req rewrap.Request,
	result rewrap.Result,
	envelope []byte,
) error {
	oldKeyVersion, err := rewrapKeyVersion32(result.OldKeyVersion)
	if err != nil {
		return err
	}
	newKeyVersion, err := rewrapKeyVersion32(result.NewKeyVersion)
	if err != nil {
		return err
	}
	if err := s.validateLeaderHistoricalRewrapUploadRequeue(blockID); err != nil {
		return err
	}

	doneCh := make(chan error, 1)
	proposalID := ulid.New().String()
	key := rewrapProposalKey(proposalID, req.TransactionID, req.DocumentName)
	s.proposalMu.Lock()
	s.proposals[key] = doneCh
	s.proposalMu.Unlock()

	cmd := &scrapv1.RaftCommand{
		Command: &scrapv1.RaftCommand_RewrapDoc{
			RewrapDoc: &scrapv1.RewrapDocumentEnvelope{
				TransactionId:      req.TransactionID,
				DocumentName:       req.DocumentName,
				BlockId:            blockID,
				EncryptionEnvelope: envelope,
				OldKeyVersion:      oldKeyVersion,
				NewKeyVersion:      newKeyVersion,
				RewrappedAtUs:      result.RewrappedAt.UnixMicro(),
				ProposalId:         proposalID,
			},
		},
	}
	injectTraceContext(ctx, cmd)
	data, err := proto.Marshal(cmd)
	if err != nil {
		s.forgetProposal(key)
		return fmt.Errorf("shard: marshal rewrap command: %w", err)
	}
	if err := s.raft.Propose(ctx, data); err != nil {
		s.forgetProposal(key)
		return fmt.Errorf("shard: propose rewrap: %w", err)
	}

	select {
	case applyErr := <-doneCh:
		return applyErr
	case <-ctx.Done():
		s.forgetProposal(key)
		return ctx.Err()
	}
}

func rewrapKeyVersion32(version int) (int32, error) {
	if version < 0 || version > math.MaxInt32 {
		return 0, fmt.Errorf("%w: rewrap key version out of range", storeapi.ErrDataLoss)
	}
	return int32(version), nil
}

func (s *Shard) forgetProposal(key string) {
	s.proposalMu.Lock()
	delete(s.proposals, key)
	s.proposalMu.Unlock()
}

func (s *Shard) applyRewrapDocumentEnvelopeCommand(cmd *scrapv1.RewrapDocumentEnvelope, entryIndex uint64) error {
	key := rewrapProposalKey(cmd.GetProposalId(), cmd.GetTransactionId(), cmd.GetDocumentName())
	applyErr := s.applyRewrapDocumentEnvelope(cmd, rewrapUploadGeneration(cmd, entryIndex))

	s.proposalMu.Lock()
	if ch, ok := s.proposals[key]; ok {
		ch <- applyErr
		delete(s.proposals, key)
	}
	s.proposalMu.Unlock()

	if errors.Is(applyErr, rewrap.ErrStaleEnvelope) {
		return nil
	}
	return applyErr
}

func rewrapUploadGeneration(cmd *scrapv1.RewrapDocumentEnvelope, entryIndex uint64) int64 {
	return uploadGenerationFromApplyIndex(entryIndex, cmd.GetRewrappedAtUs())
}

func rewrapProposalKey(proposalID, txID, docName string) string {
	if proposalID != "" {
		return "rewrap-proposal\x00" + proposalID
	}
	return "rewrap\x00" + txID + "\x00" + docName
}

func (s *Shard) applyRewrapDocumentEnvelope(cmd *scrapv1.RewrapDocumentEnvelope, uploadGeneration int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := validateRewrapCommand(cmd); err != nil {
		return err
	}

	current := s.blockWriter != nil && s.blockWriter.BlockID() == cmd.GetBlockId()
	changed, err := s.replaceRewrapEnvelopeLocked(cmd, current)
	if err != nil {
		return err
	}
	return s.finishRewrapApplyLocked(cmd.GetBlockId(), current, changed, uploadGeneration)
}

func validateRewrapCommand(cmd *scrapv1.RewrapDocumentEnvelope) error {
	if cmd.GetBlockId() == 0 || cmd.GetTransactionId() == "" || cmd.GetDocumentName() == "" || len(cmd.GetEncryptionEnvelope()) == 0 {
		return fmt.Errorf("%w: invalid rewrap command", storeapi.ErrDataLoss)
	}
	if _, err := encryption.ParseEnvelope(cmd.GetEncryptionEnvelope()); err != nil {
		return mapEncryptionError(err)
	}
	return nil
}

func (s *Shard) replaceRewrapEnvelopeLocked(cmd *scrapv1.RewrapDocumentEnvelope, current bool) (bool, error) {
	if current && s.idxWriter != nil {
		if err := s.idxWriter.Close(); err != nil {
			return false, fmt.Errorf("shard: close current index for rewrap: %w", err)
		}
		s.idxWriter = nil
	}

	alreadyApplied, err := validateRewrapCurrentEnvelope(s.idxPath(cmd.GetBlockId()), cmd)
	if err != nil {
		return false, s.reopenCurrentIndexAfterRewrapErrorLocked(current, cmd.GetBlockId(), err)
	}
	if alreadyApplied {
		reopenErr := s.reopenCurrentIndexAfterRewrapLocked(current, cmd.GetBlockId())
		return true, reopenErr
	}
	_, changed, replaceErr := block.ReplaceDocumentEnvelope(
		s.idxPath(cmd.GetBlockId()),
		cmd.GetTransactionId(),
		cmd.GetDocumentName(),
		cmd.GetEncryptionEnvelope(),
	)
	reopenErr := s.reopenCurrentIndexAfterRewrapLocked(current, cmd.GetBlockId())
	if replaceErr != nil || reopenErr != nil {
		return false, errors.Join(mapRewrapIndexError(replaceErr), reopenErr)
	}
	return changed, nil
}

func (s *Shard) validateLeaderHistoricalRewrapUploadRequeue(blockID uint64) error {
	if !s.upload.Enabled {
		return nil
	}
	s.mu.Lock()
	current := s.blockWriter != nil && s.blockWriter.BlockID() == blockID
	path := s.blockPath(blockID)
	s.mu.Unlock()
	if current {
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("shard: rewrap requires local Block data for upload requeue: %w", err)
	}
	return nil
}

func (s *Shard) reopenCurrentIndexAfterRewrapErrorLocked(current bool, blockID uint64, err error) error {
	if reopenErr := s.reopenCurrentIndexAfterRewrapLocked(current, blockID); reopenErr != nil {
		return reopenErr
	}
	return err
}

func validateRewrapCurrentEnvelope(path string, cmd *scrapv1.RewrapDocumentEnvelope) (bool, error) {
	ir, err := block.OpenIndexReader(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = ir.Close() }()

	entry, err := ir.Find(cmd.GetTransactionId(), cmd.GetDocumentName())
	if err != nil {
		return false, mapRewrapIndexError(err)
	}
	current, err := encryption.ParseEnvelope(entry.EncryptionEnvelope)
	if err != nil {
		return false, mapEncryptionError(err)
	}
	if current.KeyVersion == int(cmd.GetOldKeyVersion()) {
		return false, nil
	}
	if current.KeyVersion == int(cmd.GetNewKeyVersion()) {
		return true, nil
	}
	return false, fmt.Errorf(
		"%w: current key version %d does not match command old key version %d",
		rewrap.ErrStaleEnvelope,
		current.KeyVersion,
		cmd.GetOldKeyVersion(),
	)
}

func (s *Shard) finishRewrapApplyLocked(blockID uint64, current, changed bool, uploadGeneration int64) error {
	if changed && !current {
		return s.requeueBlockUploadAfterIndexAppend(blockID, uploadGeneration)
	}
	return nil
}

func (s *Shard) reopenCurrentIndexAfterRewrapLocked(current bool, blockID uint64) error {
	if !current || s.idxWriter != nil {
		return nil
	}
	idxWriter, err := block.OpenIndexWriter(s.idxPath(blockID))
	if err != nil {
		return fmt.Errorf("shard: reopen current index after rewrap: %w", err)
	}
	s.idxWriter = idxWriter
	return nil
}

func mapRewrapIndexError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, block.ErrDocNotFound):
		return fmt.Errorf("%w: %w", storeapi.ErrDataLoss, err)
	default:
		return err
	}
}

func mapRewrapEncryptionError(err error) error {
	if errors.Is(err, encryption.ErrInvalidRequest) {
		return fmt.Errorf("%w: %w", rewrap.ErrInvalidRequest, err)
	}
	return mapEncryptionError(err)
}

func (s *Shard) finishRewrap(result rewrap.Result, err error) (rewrap.Result, error) {
	if err != nil {
		result.Status = rewrap.StatusFailed
		result.Reason = rewrapReasonForError(err)
	}
	s.recordRewrapResult(result)
	return result, err
}

func rewrapReasonForError(err error) string {
	switch {
	case err == nil:
		return rewrap.ReasonOK
	case errors.Is(err, rewrap.ErrInvalidRequest):
		return rewrap.ReasonInvalidRequest
	case isNotLeader(err):
		return rewrap.ReasonNotLeader
	case errors.Is(err, rewrap.ErrStaleEnvelope):
		return rewrap.ReasonStaleEnvelope
	case errors.Is(err, rewrap.ErrNotEncrypted):
		return rewrap.ReasonNotEncrypted
	case errors.Is(err, storeapi.ErrUnavailable):
		return rewrap.ReasonCryptoUnavailable
	case errors.Is(err, storeapi.ErrTxNotFound), errors.Is(err, storeapi.ErrNotFound):
		return rewrap.ReasonNotFound
	case errors.Is(err, storeapi.ErrDataLoss):
		return rewrap.ReasonDataLoss
	default:
		return rewrap.ReasonInternalError
	}
}

func isNotLeader(err error) bool {
	var notLeader *storeapi.NotLeaderError
	return errors.As(err, &notLeader)
}

func (s *Shard) recordRewrapResult(result rewrap.Result) {
	s.rewrapHealthMu.Lock()
	defer s.rewrapHealthMu.Unlock()

	snapshot := s.rewrapHealthSnapshot
	if snapshot.FailuresByReason == nil {
		snapshot.FailuresByReason = map[string]int{}
	}
	snapshot.Status = rewrap.StatusOK
	if result.Status == rewrap.StatusFailed {
		snapshot.Status = rewrap.StatusDegraded
		snapshot.FailuresByReason[result.Reason]++
	}
	snapshot.LastResult = result.Status
	snapshot.LastReason = result.Reason
	snapshot.LastTransitMount = result.TransitMount
	snapshot.LastTransitKey = result.TransitKey
	snapshot.LastOldVersion = result.OldKeyVersion
	snapshot.LastNewVersion = result.NewKeyVersion
	snapshot.LastChanged = result.Changed
	snapshot.LastAt = result.RewrappedAt
	if snapshot.LastAt.IsZero() {
		snapshot.LastAt = time.Now().UTC()
	}
	s.rewrapHealthSnapshot = snapshot
}

func (s *Shard) RewrapHealthSnapshot() rewrap.HealthSnapshot {
	s.rewrapHealthMu.Lock()
	defer s.rewrapHealthMu.Unlock()

	snapshot := s.rewrapHealthSnapshot
	if snapshot.Status == "" {
		snapshot.Status = rewrap.StatusOK
	}
	if len(snapshot.FailuresByReason) > 0 {
		failures := make(map[string]int, len(snapshot.FailuresByReason))
		for reason, count := range snapshot.FailuresByReason {
			failures[reason] = count
		}
		snapshot.FailuresByReason = failures
	}
	return snapshot
}
