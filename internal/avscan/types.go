package avscan

import (
	"context"
	"errors"
	"time"
)

const (
	DefaultInterval = 30 * time.Second
)

var (
	ErrEngineUnavailable = errors.New("avscan: engine unavailable")
	ErrScanPanic         = errors.New("avscan: scan panic")
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
)

type ResultStatus string

const (
	ResultClean    ResultStatus = "clean"
	ResultDetected ResultStatus = "detected"
)

type Block struct {
	BlockID   uint64
	BlkPath   string
	IdxPath   string
	SizeBytes int64
}

type Result struct {
	Status           ResultStatus
	ScannedDocuments int
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

type BlockLister interface {
	ListSealedBlocks(context.Context) ([]Block, error)
}

type LeaderChecker interface {
	IsLeader() bool
}

type Engine interface {
	Scan(context.Context, Block) (Result, error)
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
