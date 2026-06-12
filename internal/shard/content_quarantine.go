package shard

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/avscan"
	"github.com/petabytecl/scrap/internal/index"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func (s *Shard) ReportDetections(ctx context.Context, block avscan.Block, detections []avscan.Detection) error {
	for _, detection := range detections {
		if err := s.proposeQuarantineDocument(ctx, block.BlockID, detection); err != nil {
			return err
		}
	}
	return nil
}

func (s *Shard) proposeQuarantineDocument(ctx context.Context, blockID uint64, detection avscan.Detection) error {
	cmd, err := quarantineCommandFromDetection(blockID, detection)
	if err != nil {
		return err
	}
	injectTraceContext(ctx, cmd)
	data, err := proto.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("shard: marshal quarantine document command: %w", err)
	}
	if err := s.Propose(ctx, data); err != nil {
		return fmt.Errorf("shard: propose quarantine document: %w", err)
	}
	return nil
}

func quarantineCommandFromDetection(blockID uint64, detection avscan.Detection) (*scrapv1.RaftCommand, error) {
	if err := storeapi.ValidateDocumentIdentity(detection.TransactionID, detection.DocumentName, ""); err != nil {
		return nil, err
	}
	detectedAtUs := detection.DetectedAtUs
	if detectedAtUs == 0 {
		detectedAtUs = time.Now().UnixMicro()
	}
	if detectedAtUs < 0 {
		return nil, fmt.Errorf("shard: quarantine detected_at_us is negative: %d", detectedAtUs)
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
	if cmd.GetDetectedAtUs() < 0 {
		return fmt.Errorf("shard: quarantine detected_at_us is negative: %d", cmd.GetDetectedAtUs())
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
