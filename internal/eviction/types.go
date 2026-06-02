// Package eviction defines the operator-facing eviction campaign contract.
package eviction

import "errors"

const (
	ReasonEvidenceRun = "evidence_run"

	ApplyStatusCompleted                    = "completed"
	ApplyStatusCompletedWithSkips           = "completed_with_skips"
	ApplyStatusEvictedWithValidationFailure = "evicted_with_validation_failure"
	ApplyStatusNoEffect                     = "no_effect"
	ApplyStatusFailed                       = "failed"

	PlanStatusPending = "pending"
	PlanStatusRunning = "running"

	ApplyBlockStatusEvicted = "evicted"
	ApplyBlockStatusSkipped = "skipped"
	ApplyBlockStatusFailed  = "failed"

	ValidationStatusPassed = "passed"
	ValidationStatusFailed = "failed"

	SkipReasonHotResidencyWindow    = "hot_residency_window"
	SkipReasonLeaderHotCopyRequired = "leader_hot_copy_required"
	SkipReasonLocalStateNotHot      = "local_state_not_hot"
	SkipReasonPlanBounds            = "plan_bounds"
	SkipReasonShardFilter           = "shard_filter"

	HealthPressureOK       = "ok"
	HealthPressureDegraded = "degraded"

	RestoreFailureBackendUnavailable = "backend_restore_unavailable"
	RestoreFailureNone               = "none"
	RestoreFailureDataLoss           = "data_loss"
	RestoreFailureCanceled           = "canceled"
	RestoreFailureUnknown            = "unknown"

	RepairStateIdle = "idle"
)

type Member struct {
	Hostname string
	ID       string
}

var (
	ErrInvalidPlanRequest    = errors.New("eviction: invalid plan request")
	ErrTargetMemberMismatch  = errors.New("eviction: target member_hostname does not match local member")
	ErrPlanCapExceedsCeiling = errors.New("eviction: requested cap exceeds configured ceiling")
	ErrPlanNotFound          = errors.New("eviction: plan not found")
	ErrPlanExpired           = errors.New("eviction: plan expired")
	ErrApplyInProgress       = errors.New("eviction: apply already in progress")
	ErrApplyDisabled         = errors.New("eviction: apply disabled")
	ErrPlanStale             = errors.New("eviction: plan member identity is stale")
)

type Bounds struct {
	MaxBlocks int   `json:"max_blocks"`
	MaxBytes  int64 `json:"max_bytes"`
}

type ConfigSnapshot struct {
	Enabled                   bool  `json:"enabled"`
	HotResidencyWindowSeconds int64 `json:"hot_residency_window_seconds"`
	PlanTTLSeconds            int64 `json:"plan_ttl_seconds"`
	RecommendedMaxBlocks      int   `json:"recommended_max_blocks"`
	RecommendedMaxBytes       int64 `json:"recommended_max_bytes"`
	MaxValidateSamples        int   `json:"max_validate_samples"`
}

type PlanRequest struct {
	MemberHostname string  `json:"member_hostname"`
	ShardID        *uint64 `json:"shard_id,omitempty"`
	MaxBlocks      *int    `json:"max_blocks,omitempty"`
	MaxBytes       *int64  `json:"max_bytes,omitempty"`
	Reason         string  `json:"reason,omitempty"`
	Note           string  `json:"note,omitempty"`
}

type Plan struct {
	PlanID             string         `json:"plan_id"`
	GeneratedAtUs      int64          `json:"generated_at_us"`
	ExpiresAtUs        int64          `json:"expires_at_us"`
	MemberHostname     string         `json:"member_hostname"`
	MemberID           string         `json:"member_id"`
	ShardID            *uint64        `json:"shard_id,omitempty"`
	Reason             string         `json:"reason,omitempty"`
	Note               string         `json:"note,omitempty"`
	Config             ConfigSnapshot `json:"config"`
	RecommendedBounds  Bounds         `json:"recommended_bounds"`
	RequestedBounds    Bounds         `json:"requested_bounds,omitempty"`
	EffectiveBounds    Bounds         `json:"effective_bounds"`
	Selected           []PlanBlock    `json:"selected_blocks"`
	Skipped            []PlanBlock    `json:"skipped_candidates"`
	SkipCountsByReason map[string]int `json:"skip_counts_by_reason,omitempty"`
	CandidateBlocks    int            `json:"candidate_blocks"`
	CandidateBytes     int64          `json:"candidate_bytes"`
	EligibleBlocks     int            `json:"eligible_blocks"`
	EligibleBytes      int64          `json:"eligible_bytes"`
	SelectedBytes      int64          `json:"selected_bytes"`
}

type PlanBlock struct {
	BlockID                   uint64 `json:"block_id"`
	ShardID                   uint64 `json:"shard_id"`
	SizeBytes                 int64  `json:"size_bytes"`
	BackendKey                string `json:"backend_key,omitempty"`
	ConfirmedAtUs             int64  `json:"confirmed_at_us,omitempty"`
	RestoredAtUs              int64  `json:"restored_at_us,omitempty"`
	EligibleAtUs              int64  `json:"eligible_at_us,omitempty"`
	HotResidencyWindowSeconds int64  `json:"hot_residency_window_seconds,omitempty"`
	LocalState                string `json:"local_state,omitempty"`
	OpenReaders               int    `json:"open_readers"`
	RepairState               string `json:"repair_state,omitempty"`
	Reason                    string `json:"reason,omitempty"`
}

type ApplyRequest struct {
	PlanID string `json:"plan_id"`
}

type ApplyResult struct {
	PlanID                 string            `json:"plan_id"`
	Status                 string            `json:"status"`
	StartedAtUs            int64             `json:"started_at_us"`
	CompletedAtUs          int64             `json:"completed_at_us"`
	MemberHostname         string            `json:"member_hostname"`
	MemberID               string            `json:"member_id"`
	SelectedBlocks         int               `json:"selected_blocks"`
	EvictedBlocks          int               `json:"evicted_blocks"`
	SkippedBlocks          int               `json:"skipped_blocks"`
	FailedBlocks           int               `json:"failed_blocks"`
	ValidatedBlocks        int               `json:"validated_blocks,omitempty"`
	ValidationFailedBlocks int               `json:"validation_failed_blocks,omitempty"`
	BytesFreed             int64             `json:"bytes_freed"`
	Blocks                 []ApplyBlock      `json:"blocks"`
	Validations            []ValidationBlock `json:"validations,omitempty"`
}

type PlanStatus struct {
	PlanID      string       `json:"plan_id"`
	Status      string       `json:"status"`
	Plan        *Plan        `json:"plan,omitempty"`
	ApplyResult *ApplyResult `json:"apply_result,omitempty"`
}

type ApplyBlock struct {
	BlockID    uint64 `json:"block_id"`
	ShardID    uint64 `json:"shard_id"`
	SizeBytes  int64  `json:"size_bytes"`
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
	Error      string `json:"error,omitempty"`
	BytesFreed int64  `json:"bytes_freed,omitempty"`
}

type ValidationBlock struct {
	BlockID uint64 `json:"block_id"`
	ShardID uint64 `json:"shard_id"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
}

type HealthSnapshot struct {
	Pressure                string         `json:"eviction_pressure"`
	EvictedBlocks           int            `json:"evicted_blocks"`
	EvictedBytes            int64          `json:"evicted_bytes"`
	HotCleanupNeededBlocks  int            `json:"hot_cleanup_needed_blocks"`
	MetadataLossBlocks      int            `json:"metadata_loss_blocks"`
	UnexpectedLossBlocks    int            `json:"unexpected_loss_blocks"`
	QuarantinedBlocks       int            `json:"quarantined_blocks"`
	RestoreFailedBlocks     int            `json:"restore_failed_blocks"`
	RestoreFailuresByReason map[string]int `json:"restore_failures_by_reason,omitempty"`
}
