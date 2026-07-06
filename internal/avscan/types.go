package avscan

import (
	"context"
	"errors"
	"io"
	"time"
)

const (
	DefaultInterval       = 30 * time.Second
	MaxDetectionsPerBlock = 8192
)

var (
	ErrEngineUnavailable            = errors.New("avscan: engine unavailable")
	ErrScanPanic                    = errors.New("avscan: scan panic")
	ErrBlockSource                  = errors.New("avscan: Block source unavailable")
	ErrProgressNotFound             = errors.New("avscan: scanner progress not found")
	ErrInvalidDetection             = errors.New("avscan: invalid detection")
	ErrDetectionReporterUnavailable = errors.New("avscan: detection reporter unavailable")
	ErrQuarantineFailed             = errors.New("avscan: quarantine report failed")
)

type Status string

const (
	StatusIdle     Status = "idle"
	StatusScanning Status = "scanning"
	StatusDegraded Status = "degraded"
)

type Reason string

const (
	ReasonNone              Reason = "none"
	ReasonNotLeader         Reason = "not_leader"
	ReasonListFailed        Reason = "list_failed"
	ReasonEngineUnavailable Reason = "engine_unavailable"
	ReasonScanFailed        Reason = "scan_failed"
	ReasonScanPanic         Reason = "scan_panic"
	ReasonCanceled          Reason = "canceled"
	ReasonIOBudget          Reason = "io_budget"
	ReasonPaused            Reason = "paused"
	ReasonProgressFailed    Reason = "progress_failed"
	ReasonQuarantineFailed  Reason = "quarantine_failed"
)

type ResultStatus string

const (
	ResultClean    ResultStatus = "clean"
	ResultDetected ResultStatus = "detected"
)

type Block struct {
	BlockID   uint64
	SizeBytes int64
	Open      func(context.Context) (io.ReadCloser, error)
	// Restored marks a Block brought back from the Backend after eviction. A
	// restored Block may never have been scanned (eviction does not gate on
	// scan state), so it stays scan-eligible even below the durable frontier.
	Restored bool
}

func (b Block) OpenBytes(ctx context.Context) (io.ReadCloser, error) {
	if b.Open == nil {
		return nil, ErrBlockSource
	}
	return b.Open(ctx)
}

type Result struct {
	Status           ResultStatus
	ScannedDocuments int
	Detections       []Detection
}

type DetectionScanType string

const (
	DetectionScanTypeInitial DetectionScanType = "initial"
	DetectionScanTypeRescan  DetectionScanType = "rescan"
)

type DetectionReason string

const (
	DetectionReasonScannerDetection DetectionReason = "scanner_detection"
)

type Detection struct {
	TransactionID string
	DocumentName  string
	DetectedAtUs  int64
	ScanType      DetectionScanType
	Reason        DetectionReason
}

type Snapshot struct {
	Status             Status
	LastReason         Reason
	LagBlocks          int
	InFlightBlocks     int
	LastScannedBlockID uint64
	ScannedBlocks      uint64
	FailedBlocks       uint64
	LastUpdated        time.Time
}

type Progress struct {
	LastScannedBlockID          uint64
	LastSignatureVersionScanned string
}

type BlockLister interface {
	ListSealedBlocks(context.Context) ([]Block, error)
}

type LeaderChecker interface {
	IsLeader() bool
}

type Engine interface {
	Scan(context.Context, Block) (Result, error)
}

type DetectionReporter interface {
	ReportDetections(context.Context, Block, []Detection) error
}

type ProgressStore interface {
	LoadScannerProgress(context.Context) (Progress, error)
	SaveScannerProgress(context.Context, Progress) error
}

type SignatureVersionProvider interface {
	SignatureVersion(context.Context) (string, error)
}

type IOBudget interface {
	Wait(context.Context, int64) error
}

type PauseController interface {
	IsPaused() bool
	Wait(context.Context) error
}

type Ticker interface {
	C() <-chan time.Time
	Stop()
}

type TickerFactory interface {
	NewTicker(time.Duration) Ticker
}

type Metrics interface {
	RecordRun(shardID uint64, status, reason string, duration time.Duration)
	RecordBlock(shardID uint64, status, reason string)
	RecordFailure(shardID uint64, reason string)
	SetLag(shardID uint64, blocks int)
	SetInFlight(shardID uint64, blocks int)
	RecordDuplicate(shardID uint64)
}
