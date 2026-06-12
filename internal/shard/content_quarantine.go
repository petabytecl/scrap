package shard

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/avscan"
	"github.com/petabytecl/scrap/internal/index"
	storeapi "github.com/petabytecl/scrap/internal/store"
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

func (s *Shard) notifyProposal(key string, applyErr error) {
	s.proposalMu.Lock()
	defer s.proposalMu.Unlock()

	if ch, ok := s.proposals[key]; ok {
		ch <- applyErr
		delete(s.proposals, key)
	}
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

var _ avscan.DetectionReporter = (*Shard)(nil)
