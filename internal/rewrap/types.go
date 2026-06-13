// Package rewrap defines bounded request, result, and health contracts for
// durable Document envelope key-version updates.
package rewrap

import (
	"errors"
	"time"
)

const (
	StatusOK       = "ok"
	StatusFailed   = "failed"
	StatusDegraded = "degraded"

	ReasonOK                = "ok"
	ReasonIdempotent        = "idempotent"
	ReasonCryptoUnavailable = "crypto_unavailable"
	ReasonNotEncrypted      = "not_encrypted"
	ReasonNotFound          = "not_found"
	ReasonDataLoss          = "data_loss"
	ReasonInvalidRequest    = "invalid_request"
	ReasonNotLeader         = "not_leader"
	ReasonStaleEnvelope     = "stale_envelope"
	ReasonInternalError     = "internal_error"
)

var (
	ErrInvalidRequest = errors.New("rewrap invalid request")
	ErrNotEncrypted   = errors.New("rewrap document is not encrypted")
	ErrStaleEnvelope  = errors.New("rewrap stale envelope")
)

type Request struct {
	TransactionID string `json:"transaction_id"`
	DocumentName  string `json:"document_name"`
	KeyVersion    int    `json:"key_version,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

type Result struct {
	Status        string    `json:"status"`
	Reason        string    `json:"reason"`
	Changed       bool      `json:"changed"`
	TransactionID string    `json:"transaction_id"`
	DocumentName  string    `json:"document_name"`
	BlockID       uint64    `json:"block_id"`
	TransitMount  string    `json:"transit_mount,omitempty"`
	TransitKey    string    `json:"transit_key,omitempty"`
	OldKeyVersion int       `json:"old_key_version,omitempty"`
	NewKeyVersion int       `json:"new_key_version,omitempty"`
	RewrappedAt   time.Time `json:"rewrapped_at,omitempty"`
}

type HealthSnapshot struct {
	Status           string         `json:"status"`
	LastResult       string         `json:"last_result,omitempty"`
	LastReason       string         `json:"last_reason,omitempty"`
	LastTransitMount string         `json:"last_transit_mount,omitempty"`
	LastTransitKey   string         `json:"last_transit_key,omitempty"`
	LastOldVersion   int            `json:"last_old_version,omitempty"`
	LastNewVersion   int            `json:"last_new_version,omitempty"`
	LastChanged      bool           `json:"last_changed,omitempty"`
	LastAt           time.Time      `json:"last_at,omitempty"`
	FailuresByReason map[string]int `json:"failures_by_reason,omitempty"`
}
