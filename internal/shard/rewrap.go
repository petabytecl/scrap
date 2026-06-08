package shard

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/encryption"
	"github.com/petabytecl/scrap/internal/rewrap"
	storeapi "github.com/petabytecl/scrap/internal/store"
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

func (s *Shard) rewrapDocumentEnvelope(
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

	rewrapped, err := s.encryption.Transit.RewrapDataKey(ctx, encryption.RewrapDataKeyRequest{
		WrappedKey: envelope.WrappedDataKey,
		Context: encryption.DocumentKeyContext(encryption.DocumentIdentity{
			TransactionID: req.TransactionID,
			DocumentName:  req.DocumentName,
		}),
		KeyVersion: req.KeyVersion,
	})
	if err != nil {
		return result, mapEncryptionError(err)
	}
	if !rewrapped.Changed && (req.KeyVersion == 0 || rewrapped.Version == envelope.KeyVersion) {
		result.Status = rewrap.StatusOK
		result.Reason = rewrap.ReasonIdempotent
		return result, nil
	}
	if rewrapped.Version <= 0 {
		return result, fmt.Errorf("%w: rewrapped key version is missing", storeapi.ErrDataLoss)
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
	doneCh := make(chan error, 1)
	key := rewrapProposalKey(req.TransactionID, req.DocumentName)
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
				OldKeyVersion:      int32(result.OldKeyVersion), //nolint:gosec // Transit key versions are positive small ints.
				NewKeyVersion:      int32(result.NewKeyVersion), //nolint:gosec // Transit key versions are positive small ints.
				RewrappedAtUs:      result.RewrappedAt.UnixMicro(),
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

func (s *Shard) forgetProposal(key string) {
	s.proposalMu.Lock()
	delete(s.proposals, key)
	s.proposalMu.Unlock()
}

func (s *Shard) applyRewrapDocumentEnvelopeCommand(cmd *scrapv1.RewrapDocumentEnvelope) {
	key := rewrapProposalKey(cmd.GetTransactionId(), cmd.GetDocumentName())
	applyErr := s.applyRewrapDocumentEnvelope(cmd)

	s.proposalMu.Lock()
	defer s.proposalMu.Unlock()
	if ch, ok := s.proposals[key]; ok {
		ch <- applyErr
		delete(s.proposals, key)
	}
}

func rewrapProposalKey(txID, docName string) string {
	return "rewrap\x00" + txID + "\x00" + docName
}

func (s *Shard) applyRewrapDocumentEnvelope(cmd *scrapv1.RewrapDocumentEnvelope) error {
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
	return s.finishRewrapApplyLocked(cmd.GetBlockId(), current, changed)
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

func (s *Shard) finishRewrapApplyLocked(blockID uint64, current, changed bool) error {
	if changed && !current {
		return s.requeueBlockUploadAfterIndexAppend(blockID)
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
