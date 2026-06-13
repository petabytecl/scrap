package quarantine

import (
	"errors"
	"fmt"
	"time"

	storeapi "github.com/petabytecl/scrap/internal/store"
)

const (
	// DefaultListLimit is used when an admin list request omits a limit.
	DefaultListLimit = 50
	// MaxListLimit caps one admin list response.
	MaxListLimit = 100

	// StatusOK marks a completed quarantine operation.
	StatusOK = "ok"
	// StatusFailed marks a rejected quarantine operation.
	StatusFailed = "failed"

	// ReasonOK marks a successful quarantine operation.
	ReasonOK = "ok"
	// ReasonInvalidRequest marks invalid admin input.
	ReasonInvalidRequest = "invalid_request"
	// ReasonNotFound marks a missing active quarantine record.
	ReasonNotFound = "not_found"
	// ReasonUnauthenticated marks a missing admin identity.
	ReasonUnauthenticated = "unauthenticated"
	// ReasonPermissionDenied marks an admin caller without an accepted role.
	ReasonPermissionDenied = "permission_denied"
	// ReasonRateLimited marks an admin request rejected by rate limits.
	ReasonRateLimited = "rate_limited"
	// ReasonMethodNotAllowed marks an unsupported admin HTTP method.
	ReasonMethodNotAllowed = "method_not_allowed"
	// ReasonNotLeader marks a leader-gated operation on a follower.
	ReasonNotLeader = "not_leader"
	// ReasonUnavailable marks a routed but unavailable Shard.
	ReasonUnavailable = "unavailable"
	// ReasonFailedPrecondition marks a non-corruption precondition failure.
	ReasonFailedPrecondition = "failed_precondition"
	// ReasonDataLoss marks corrupt quarantine Projection state.
	ReasonDataLoss = "data_loss"
	// ReasonAuditFailed marks failure to persist the required audit event.
	ReasonAuditFailed = "audit_failed"
	// ReasonInternalError marks unexpected failures.
	ReasonInternalError = "internal_error"

	// LifecycleActive means the Document is blocked by active Content Quarantine.
	LifecycleActive = "active"
	// LifecycleConfirmed means the active quarantine has been confirmed.
	LifecycleConfirmed = "confirmed"
	// LifecycleReleased means the active quarantine gate was removed.
	LifecycleReleased = "released"

	// ScanTypeInitial is the initial Content Scanner pass.
	ScanTypeInitial = "initial"
	// ScanTypeRescan is a Content Scanner rescan pass.
	ScanTypeRescan = "rescan"

	// ReasonScannerDetection is the bounded scanner-detection reason.
	ReasonScannerDetection = "scanner_detection"
)

// ErrInvalidRequest is returned for malformed quarantine admin requests.
var ErrInvalidRequest = errors.New("quarantine invalid request")

// ErrNotFound is returned when no active quarantine record exists.
var ErrNotFound = errors.New("quarantine active record not found")

// Identity addresses one Content Quarantine record.
type Identity struct {
	TransactionID string `json:"transaction_id"`
	DocumentName  string `json:"document_name"`
}

// Validate rejects malformed Document identity inputs.
func (i Identity) Validate() error {
	if err := storeapi.ValidateDocumentIdentity(i.TransactionID, i.DocumentName, ""); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	return nil
}

// ListFilter bounds a Content Quarantine list request.
type ListFilter struct {
	TransactionID string `json:"transaction_id,omitempty"`
	Limit         int    `json:"limit,omitempty"`
}

// Validate rejects malformed list inputs and applies the default limit.
func (f ListFilter) Validate() (ListFilter, error) {
	if f.TransactionID != "" {
		if err := storeapi.ValidateTransactionLookup(f.TransactionID, ""); err != nil {
			return ListFilter{}, fmt.Errorf("%w: %w", ErrInvalidRequest, err)
		}
	}
	switch {
	case f.Limit == 0:
		f.Limit = DefaultListLimit
	case f.Limit < 0 || f.Limit > MaxListLimit:
		return ListFilter{}, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidRequest, MaxListLimit)
	}
	return f, nil
}

// Record is the bounded admin representation of one active Content Quarantine.
type Record struct {
	ShardID       uint64     `json:"shard_id"`
	TransactionID string     `json:"transaction_id"`
	DocumentName  string     `json:"document_name"`
	BlockID       uint64     `json:"block_id"`
	DetectedAt    time.Time  `json:"detected_at"`
	ScanType      string     `json:"scan_type"`
	Reason        string     `json:"reason"`
	Lifecycle     string     `json:"lifecycle"`
	ConfirmedAt   *time.Time `json:"confirmed_at,omitempty"`
}

// Result is the bounded admin response for confirm and release decisions.
type Result struct {
	Status   string  `json:"status"`
	Reason   string  `json:"reason"`
	Changed  bool    `json:"changed"`
	Document *Record `json:"document,omitempty"`
}
