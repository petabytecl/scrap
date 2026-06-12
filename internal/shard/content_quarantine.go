package shard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"google.golang.org/protobuf/proto"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/avscan"
	"github.com/petabytecl/scrap/internal/index"
	"github.com/petabytecl/scrap/internal/quarantine"
	storeapi "github.com/petabytecl/scrap/internal/store"
	"github.com/petabytecl/scrap/internal/ulid"
)

func (s *Shard) ReportDetections(ctx context.Context, block avscan.Block, detections []avscan.Detection) error {
	if len(detections) > avscan.MaxDetectionsPerBlock {
		return fmt.Errorf("shard: %w: detection count %d exceeds %d", avscan.ErrInvalidDetection, len(detections), avscan.MaxDetectionsPerBlock)
	}
	commands := make([]*scrapv1.RaftCommand, 0, len(detections))
	for _, detection := range detections {
		cmd, err := quarantineCommandFromDetection(block.BlockID, detection)
		if err != nil {
			return err
		}
		commands = append(commands, cmd)
	}
	for _, cmd := range commands {
		if err := s.proposeQuarantineDocument(ctx, cmd); err != nil {
			return err
		}
	}
	return nil
}

func (s *Shard) proposeQuarantineDocument(ctx context.Context, cmd *scrapv1.RaftCommand) error {
	quarantine := cmd.GetQuarantineDoc()
	if quarantine == nil {
		return errors.New("shard: quarantine command is required")
	}
	key := quarantineProposalKey(quarantine)
	doneCh := make(chan error, 1)
	if err := s.watchQuarantineProposal(key, doneCh); err != nil {
		return err
	}
	injectTraceContext(ctx, cmd)
	data, err := proto.Marshal(cmd)
	if err != nil {
		s.forgetProposal(key)
		return fmt.Errorf("shard: marshal quarantine document command: %w", err)
	}
	if err := s.Propose(ctx, data); err != nil {
		s.forgetProposal(key)
		return fmt.Errorf("shard: propose quarantine document: %w", err)
	}

	select {
	case applyErr := <-doneCh:
		return applyErr
	case <-ctx.Done():
		s.forgetProposal(key)
		return ctx.Err()
	}
}

func (s *Shard) watchQuarantineProposal(key string, doneCh chan error) error {
	s.proposalMu.Lock()
	defer s.proposalMu.Unlock()

	if s.proposals == nil {
		s.proposals = make(map[string]chan error)
	}
	if _, ok := s.proposals[key]; ok {
		return errors.New("shard: quarantine proposal already in flight")
	}
	s.proposals[key] = doneCh
	return nil
}

func quarantineCommandFromDetection(blockID uint64, detection avscan.Detection) (*scrapv1.RaftCommand, error) {
	if err := storeapi.ValidateDocumentIdentity(detection.TransactionID, detection.DocumentName, ""); err != nil {
		return nil, err
	}
	detectedAtUs := detection.DetectedAtUs
	if detectedAtUs <= 0 {
		return nil, errors.New("shard: quarantine detected_at_us is required")
	}
	scanType, err := quarantineScanTypeFromDetection(detection.ScanType)
	if err != nil {
		return nil, err
	}
	reason, err := quarantineReasonFromDetection(detection.Reason)
	if err != nil {
		return nil, err
	}
	return &scrapv1.RaftCommand{
		Command: &scrapv1.RaftCommand_QuarantineDoc{
			QuarantineDoc: &scrapv1.QuarantineDocument{
				TransactionId: detection.TransactionID,
				DocumentName:  detection.DocumentName,
				BlockId:       blockID,
				DetectedAtUs:  detectedAtUs,
				ScanType:      scanType,
				Reason:        reason,
			},
		},
	}, nil
}

func (s *Shard) applyQuarantineDocumentCommand(cmd *scrapv1.QuarantineDocument) error {
	key := quarantineProposalKey(cmd)
	applyErr := s.applyQuarantineDocumentCommandLocked(cmd)
	s.notifyProposal(key, applyErr)
	return applyErr
}

func (s *Shard) applyQuarantineDocumentCommandLocked(cmd *scrapv1.QuarantineDocument) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.applyQuarantineDocumentLocked(cmd)
}

func (s *Shard) applyQuarantineDocumentLocked(cmd *scrapv1.QuarantineDocument) error {
	if s.idx == nil {
		return fmt.Errorf("%w: projection is nil", storeapi.ErrDataLoss)
	}
	if err := storeapi.ValidateDocumentIdentity(cmd.GetTransactionId(), cmd.GetDocumentName(), ""); err != nil {
		return err
	}
	scanType, err := contentQuarantineScanTypeFromCommand(cmd.GetScanType())
	if err != nil {
		return err
	}
	reason, err := contentQuarantineReasonFromCommand(cmd.GetReason())
	if err != nil {
		return err
	}
	if cmd.GetDetectedAtUs() <= 0 {
		return errors.New("shard: quarantine detected_at_us is required")
	}
	err = s.idx.PutContentQuarantine(index.ContentQuarantine{
		TransactionID: cmd.GetTransactionId(),
		DocumentName:  cmd.GetDocumentName(),
		BlockID:       cmd.GetBlockId(),
		DetectedAtUs:  cmd.GetDetectedAtUs(),
		ScanType:      scanType,
		Reason:        reason,
	})
	if err != nil {
		return fmt.Errorf("shard: apply quarantine document: %w", err)
	}
	return nil
}

func (s *Shard) ListContentQuarantines(_ context.Context, filter quarantine.ListFilter) ([]quarantine.Record, error) {
	filter, err := filter.Validate()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.idx == nil {
		return nil, fmt.Errorf("%w: projection is nil", storeapi.ErrDataLoss)
	}
	records, err := s.idx.ListContentQuarantines(filter.TransactionID, filter.Limit)
	if err != nil {
		return nil, mapContentQuarantineIndexError(err)
	}
	out := make([]quarantine.Record, 0, len(records))
	for _, record := range records {
		out = append(out, contentQuarantineRecord(s.shardID, record))
	}
	return out, nil
}

func (s *Shard) InspectContentQuarantine(_ context.Context, identity quarantine.Identity) (quarantine.Record, error) {
	record, err := s.contentQuarantineSnapshot(identity)
	if err != nil {
		return quarantine.Record{}, err
	}
	return contentQuarantineRecord(s.shardID, record), nil
}

func (s *Shard) ConfirmContentQuarantine(ctx context.Context, identity quarantine.Identity) (quarantine.Result, error) {
	result := quarantine.Result{
		Status: quarantine.StatusFailed,
		Reason: quarantine.ReasonInvalidRequest,
	}
	record, err := s.contentQuarantineSnapshot(identity)
	if err != nil {
		return finishQuarantineResult(result, err)
	}
	confirmedAt := time.Now().UTC()
	result.Document = contentQuarantineRecord(s.shardID, record)
	result.Document.Lifecycle = quarantine.LifecycleConfirmed
	result.Document.ConfirmedAt = &confirmedAt

	proposalID := ulid.New().String()
	err = s.proposeQuarantineLifecycle(ctx, quarantineLifecycleProposal{
		key: quarantineLifecycleProposalKey("confirm", proposalID, identity.TransactionID, identity.DocumentName),
		cmd: &scrapv1.RaftCommand{
			Command: &scrapv1.RaftCommand_ConfirmQuarantine{
				ConfirmQuarantine: &scrapv1.ConfirmQuarantine{
					TransactionId: identity.TransactionID,
					DocumentName:  identity.DocumentName,
					ConfirmedAtUs: confirmedAt.UnixMicro(),
					ProposalId:    proposalID,
				},
			},
		},
	})
	if err != nil {
		return finishQuarantineResult(result, err)
	}
	result.Status = quarantine.StatusOK
	result.Reason = quarantine.ReasonOK
	result.Changed = true
	return result, nil
}

func (s *Shard) ReleaseContentQuarantine(ctx context.Context, identity quarantine.Identity) (quarantine.Result, error) {
	result := quarantine.Result{
		Status: quarantine.StatusFailed,
		Reason: quarantine.ReasonInvalidRequest,
	}
	record, err := s.contentQuarantineSnapshot(identity)
	if err != nil {
		return finishQuarantineResult(result, err)
	}
	result.Document = contentQuarantineRecord(s.shardID, record)

	proposalID := ulid.New().String()
	err = s.proposeQuarantineLifecycle(ctx, quarantineLifecycleProposal{
		key: quarantineLifecycleProposalKey("release", proposalID, identity.TransactionID, identity.DocumentName),
		cmd: &scrapv1.RaftCommand{
			Command: &scrapv1.RaftCommand_ReleaseQuarantine{
				ReleaseQuarantine: &scrapv1.ReleaseQuarantine{
					TransactionId: identity.TransactionID,
					DocumentName:  identity.DocumentName,
					ReleasedAtUs:  time.Now().UTC().UnixMicro(),
					ProposalId:    proposalID,
				},
			},
		},
	})
	if err != nil {
		return finishQuarantineResult(result, err)
	}
	result.Status = quarantine.StatusOK
	result.Reason = quarantine.ReasonOK
	result.Changed = true
	return result, nil
}

type quarantineLifecycleProposal struct {
	key string
	cmd *scrapv1.RaftCommand
}

func (s *Shard) proposeQuarantineLifecycle(ctx context.Context, proposal quarantineLifecycleProposal) error {
	doneCh := make(chan error, 1)
	if err := s.watchQuarantineProposal(proposal.key, doneCh); err != nil {
		return err
	}
	injectTraceContext(ctx, proposal.cmd)
	data, err := proto.Marshal(proposal.cmd)
	if err != nil {
		s.forgetProposal(proposal.key)
		return fmt.Errorf("shard: marshal quarantine lifecycle command: %w", err)
	}
	if err := s.Propose(ctx, data); err != nil {
		s.forgetProposal(proposal.key)
		return fmt.Errorf("shard: propose quarantine lifecycle: %w", err)
	}

	select {
	case applyErr := <-doneCh:
		return applyErr
	case <-ctx.Done():
		s.forgetProposal(proposal.key)
		return ctx.Err()
	}
}

func (s *Shard) contentQuarantineSnapshot(identity quarantine.Identity) (index.ContentQuarantine, error) {
	if err := identity.Validate(); err != nil {
		return index.ContentQuarantine{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.idx == nil {
		return index.ContentQuarantine{}, fmt.Errorf("%w: projection is nil", storeapi.ErrDataLoss)
	}
	record, err := s.idx.GetContentQuarantine(identity.TransactionID, identity.DocumentName)
	if err != nil {
		return index.ContentQuarantine{}, mapContentQuarantineIndexError(err)
	}
	return record, nil
}

func (s *Shard) applyConfirmQuarantineCommand(cmd *scrapv1.ConfirmQuarantine) error {
	key := quarantineLifecycleProposalKey("confirm", cmd.GetProposalId(), cmd.GetTransactionId(), cmd.GetDocumentName())
	applyErr := s.applyConfirmQuarantineCommandLocked(cmd)
	s.notifyProposal(key, applyErr)
	return applyErr
}

func (s *Shard) applyConfirmQuarantineCommandLocked(cmd *scrapv1.ConfirmQuarantine) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.idx == nil {
		return fmt.Errorf("%w: projection is nil", storeapi.ErrDataLoss)
	}
	if err := storeapi.ValidateDocumentIdentity(cmd.GetTransactionId(), cmd.GetDocumentName(), ""); err != nil {
		return err
	}
	if cmd.GetConfirmedAtUs() <= 0 {
		return errors.New("shard: quarantine confirmed_at_us is required")
	}
	if err := s.idx.ConfirmContentQuarantine(cmd.GetTransactionId(), cmd.GetDocumentName(), cmd.GetConfirmedAtUs()); err != nil {
		return fmt.Errorf("shard: apply confirm quarantine: %w", mapContentQuarantineIndexError(err))
	}
	return nil
}

func (s *Shard) applyReleaseQuarantineCommand(cmd *scrapv1.ReleaseQuarantine) error {
	key := quarantineLifecycleProposalKey("release", cmd.GetProposalId(), cmd.GetTransactionId(), cmd.GetDocumentName())
	applyErr := s.applyReleaseQuarantineCommandLocked(cmd)
	s.notifyProposal(key, applyErr)
	return applyErr
}

func (s *Shard) applyReleaseQuarantineCommandLocked(cmd *scrapv1.ReleaseQuarantine) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.idx == nil {
		return fmt.Errorf("%w: projection is nil", storeapi.ErrDataLoss)
	}
	if err := storeapi.ValidateDocumentIdentity(cmd.GetTransactionId(), cmd.GetDocumentName(), ""); err != nil {
		return err
	}
	if cmd.GetReleasedAtUs() <= 0 {
		return errors.New("shard: quarantine released_at_us is required")
	}
	if err := s.idx.ReleaseContentQuarantine(cmd.GetTransactionId(), cmd.GetDocumentName()); err != nil {
		return fmt.Errorf("shard: apply release quarantine: %w", mapContentQuarantineIndexError(err))
	}
	return nil
}

func (s *Shard) notifyProposal(key string, applyErr error) {
	s.proposalMu.Lock()
	defer s.proposalMu.Unlock()

	if ch, ok := s.proposals[key]; ok {
		ch <- applyErr
		delete(s.proposals, key)
	}
}

func quarantineLifecycleProposalKey(action, proposalID, txID, docName string) string {
	if proposalID != "" {
		return "quarantine-" + action + "-proposal\x00" + proposalID
	}
	return "quarantine-" + action + "\x00" + txID + "\x00" + docName
}

func contentQuarantineRecord(shardID uint64, record index.ContentQuarantine) quarantine.Record {
	out := quarantine.Record{
		ShardID:       shardID,
		TransactionID: record.TransactionID,
		DocumentName:  record.DocumentName,
		BlockID:       record.BlockID,
		DetectedAt:    time.UnixMicro(record.DetectedAtUs).UTC(),
		ScanType:      contentQuarantineScanTypeString(record.ScanType),
		Reason:        contentQuarantineReasonString(record.Reason),
		Lifecycle:     quarantine.LifecycleActive,
	}
	if record.ConfirmedAtUs > 0 {
		confirmedAt := time.UnixMicro(record.ConfirmedAtUs).UTC()
		out.Lifecycle = quarantine.LifecycleConfirmed
		out.ConfirmedAt = &confirmedAt
	}
	return out
}

func contentQuarantineScanTypeString(scanType index.ContentQuarantineScanType) string {
	switch scanType {
	case index.ContentQuarantineScanTypeInitial:
		return quarantine.ScanTypeInitial
	case index.ContentQuarantineScanTypeRescan:
		return quarantine.ScanTypeRescan
	default:
		return ""
	}
}

func contentQuarantineReasonString(reason index.ContentQuarantineReason) string {
	if reason == index.ContentQuarantineReasonScannerDetection {
		return quarantine.ReasonScannerDetection
	}
	return ""
}

func mapContentQuarantineIndexError(err error) error {
	switch {
	case errors.Is(err, index.ErrContentQuarantineNotFound):
		return quarantine.ErrNotFound
	case errors.Is(err, index.ErrInvalidContentQuarantine):
		return fmt.Errorf("%w: %w", storeapi.ErrDataLoss, err)
	default:
		return err
	}
}

func finishQuarantineResult(result quarantine.Result, err error) (quarantine.Result, error) {
	if err == nil {
		result.Status = quarantine.StatusOK
		result.Reason = quarantine.ReasonOK
		return result, nil
	}
	result.Status = quarantine.StatusFailed
	result.Reason = quarantineReasonForError(err)
	return result, err
}

func quarantineReasonForError(err error) string {
	switch {
	case err == nil:
		return quarantine.ReasonOK
	case errors.Is(err, quarantine.ErrInvalidRequest):
		return quarantine.ReasonInvalidRequest
	case errors.Is(err, quarantine.ErrNotFound), errors.Is(err, storeapi.ErrNotFound), errors.Is(err, storeapi.ErrTxNotFound):
		return quarantine.ReasonNotFound
	case isStoreNotLeader(err):
		return quarantine.ReasonNotLeader
	case errors.Is(err, storeapi.ErrUnavailable):
		return quarantine.ReasonUnavailable
	case errors.Is(err, storeapi.ErrDataLoss):
		return quarantine.ReasonDataLoss
	default:
		return quarantine.ReasonInternalError
	}
}

func isStoreNotLeader(err error) bool {
	var notLeader *storeapi.NotLeaderError
	return errors.As(err, &notLeader)
}

func quarantineProposalKey(cmd *scrapv1.QuarantineDocument) string {
	return fmt.Sprintf(
		"quarantine\x00%s\x00%s\x00%d\x00%d\x00%d\x00%d",
		cmd.GetTransactionId(),
		cmd.GetDocumentName(),
		cmd.GetBlockId(),
		cmd.GetDetectedAtUs(),
		cmd.GetScanType(),
		cmd.GetReason(),
	)
}

func quarantineScanTypeFromDetection(scanType avscan.DetectionScanType) (scrapv1.QuarantineScanType, error) {
	switch scanType {
	case avscan.DetectionScanTypeInitial:
		return scrapv1.QuarantineScanType_QUARANTINE_SCAN_TYPE_INITIAL, nil
	case avscan.DetectionScanTypeRescan:
		return scrapv1.QuarantineScanType_QUARANTINE_SCAN_TYPE_RESCAN, nil
	default:
		return scrapv1.QuarantineScanType_QUARANTINE_SCAN_TYPE_UNSPECIFIED, errors.New("shard: quarantine scan_type is required")
	}
}

func quarantineReasonFromDetection(reason avscan.DetectionReason) (scrapv1.QuarantineReason, error) {
	switch reason {
	case avscan.DetectionReasonScannerDetection:
		return scrapv1.QuarantineReason_QUARANTINE_REASON_SCANNER_DETECTION, nil
	default:
		return scrapv1.QuarantineReason_QUARANTINE_REASON_UNSPECIFIED, errors.New("shard: quarantine reason is required")
	}
}

func contentQuarantineScanTypeFromCommand(scanType scrapv1.QuarantineScanType) (index.ContentQuarantineScanType, error) {
	switch scanType {
	case scrapv1.QuarantineScanType_QUARANTINE_SCAN_TYPE_INITIAL:
		return index.ContentQuarantineScanTypeInitial, nil
	case scrapv1.QuarantineScanType_QUARANTINE_SCAN_TYPE_RESCAN:
		return index.ContentQuarantineScanTypeRescan, nil
	default:
		return 0, errors.New("shard: quarantine scan_type is required")
	}
}

func contentQuarantineReasonFromCommand(reason scrapv1.QuarantineReason) (index.ContentQuarantineReason, error) {
	switch reason {
	case scrapv1.QuarantineReason_QUARANTINE_REASON_SCANNER_DETECTION:
		return index.ContentQuarantineReasonScannerDetection, nil
	default:
		return 0, errors.New("shard: quarantine reason is required")
	}
}

func (s *Shard) ensureContentReadAllowedLocked(txID, docName string) error {
	scanStatus, err := s.contentScanStatusLocked(txID, docName)
	if err != nil {
		return err
	}
	if scanStatus != storeapi.ScanStatusQuarantined {
		return nil
	}
	return contentQuarantinedPrecondition()
}

func contentQuarantinedPrecondition() error {
	return storeapi.NewPrecondition(storeapi.PreconditionReasonContentQuarantined, "content quarantined")
}

func (s *Shard) contentScanStatusLocked(txID, docName string) (storeapi.ScanStatus, error) {
	if s.idx == nil {
		return storeapi.ScanStatusUnspecified, fmt.Errorf("%w: projection is nil", storeapi.ErrDataLoss)
	}
	_, err := s.idx.GetContentQuarantine(txID, docName)
	switch {
	case err == nil:
		return storeapi.ScanStatusQuarantined, nil
	case errors.Is(err, index.ErrContentQuarantineNotFound):
		return storeapi.ScanStatusUnscanned, nil
	default:
		return storeapi.ScanStatusUnspecified, fmt.Errorf("%w: content quarantine lookup: %w", storeapi.ErrDataLoss, err)
	}
}

type contentQuarantineReadCloser struct {
	inner   io.ReadCloser
	shard   *Shard
	txID    string
	docName string
}

func (r contentQuarantineReadCloser) Read(p []byte) (int, error) {
	n, readErr := r.inner.Read(p)
	if n <= 0 {
		return n, readErr
	}

	r.shard.mu.Lock()
	defer r.shard.mu.Unlock()

	if err := r.shard.ensureContentReadAllowedLocked(r.txID, r.docName); err != nil {
		return 0, err
	}
	return n, readErr
}

func (r contentQuarantineReadCloser) Close() error {
	return r.inner.Close()
}

var _ avscan.DetectionReporter = (*Shard)(nil)
