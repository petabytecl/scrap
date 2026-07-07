package store

import "errors"

const (
	ResourceExhaustedReasonUploadPressure = "upload_pressure"

	PreconditionReasonContentQuarantined = "QUARANTINED_AV"

	DataLossReasonBackendRestoreCorrupt          = "backend_restore_corrupt"
	DataLossReasonBackendRestoreChecksumMismatch = "backend_restore_checksum_mismatch"
	DataLossReasonBackendRestoreMetadataMismatch = "backend_restore_metadata_mismatch"
	DataLossReasonBackendRestoreMissing          = "backend_restore_missing"
	UnavailableReasonBackendRestoreUnavailable   = "backend_restore_unavailable"
	UnavailableReasonCryptoUnavailable           = "crypto_unavailable"
	UnavailableReasonLifecycleMarkerInvalid      = "lifecycle_marker_invalid"
	UnavailableReasonProjectionRebuild           = "projection_rebuild_in_progress"
	UnavailableReasonShardRouteUnavailable       = "shard_route_unavailable"
	UnavailableReasonShardRoutingPending         = "shard_routing_pending"
	UnavailableReasonUploadPending               = "upload_pending"
)

var (
	ErrAlreadyExists      = errors.New("document already exists")
	ErrNotFound           = errors.New("document not found")
	ErrTxNotFound         = errors.New("transaction not found")
	ErrInvalidArgument    = errors.New("invalid argument")
	ErrResourceExhausted  = errors.New("resource exhausted")
	ErrFailedPrecondition = errors.New("failed precondition")
	ErrUnavailable        = errors.New("temporarily unavailable")
	ErrDataLoss           = errors.New("data corruption detected")
	ErrRebuilding         = errors.New("projection rebuild in progress")
)

type PreconditionError struct {
	Reason  string
	Message string
}

func NewPrecondition(reason, message string) *PreconditionError {
	return &PreconditionError{
		Reason:  reason,
		Message: message,
	}
}

func (e *PreconditionError) Error() string {
	if e.Message == "" {
		return ErrFailedPrecondition.Error()
	}
	return ErrFailedPrecondition.Error() + ": " + e.Message
}

func (e *PreconditionError) Unwrap() error {
	return ErrFailedPrecondition
}

func PreconditionReason(err error) (string, bool) {
	var preconditionErr *PreconditionError
	if !errors.As(err, &preconditionErr) || preconditionErr.Reason == "" {
		return "", false
	}
	return preconditionErr.Reason, true
}

type ResourceExhaustedError struct {
	Reason  string
	Message string
}

func NewResourceExhausted(reason, message string) *ResourceExhaustedError {
	return &ResourceExhaustedError{
		Reason:  reason,
		Message: message,
	}
}

func (e *ResourceExhaustedError) Error() string {
	if e.Message == "" {
		return ErrResourceExhausted.Error()
	}
	return ErrResourceExhausted.Error() + ": " + e.Message
}

func (e *ResourceExhaustedError) Unwrap() error {
	return ErrResourceExhausted
}

func ResourceExhaustedReason(err error) (string, bool) {
	var resourceErr *ResourceExhaustedError
	if !errors.As(err, &resourceErr) || resourceErr.Reason == "" {
		return "", false
	}
	return resourceErr.Reason, true
}

type DataLossError struct {
	Reason  string
	Message string
}

func NewDataLoss(reason, message string) *DataLossError {
	return &DataLossError{
		Reason:  reason,
		Message: message,
	}
}

func (e *DataLossError) Error() string {
	if e.Message == "" {
		return ErrDataLoss.Error()
	}
	return ErrDataLoss.Error() + ": " + e.Message
}

func (e *DataLossError) Unwrap() error {
	return ErrDataLoss
}

func DataLossReason(err error) (string, bool) {
	var dataLossErr *DataLossError
	if !errors.As(err, &dataLossErr) || dataLossErr.Reason == "" {
		return "", false
	}
	return dataLossErr.Reason, true
}

type UnavailableError struct {
	Reason  string
	Message string
}

func NewUnavailable(reason, message string) *UnavailableError {
	return &UnavailableError{
		Reason:  reason,
		Message: message,
	}
}

func (e *UnavailableError) Error() string {
	if e.Message == "" {
		return ErrUnavailable.Error()
	}
	return ErrUnavailable.Error() + ": " + e.Message
}

func (e *UnavailableError) Unwrap() error {
	return ErrUnavailable
}

func UnavailableReason(err error) (string, bool) {
	var unavailableErr *UnavailableError
	if !errors.As(err, &unavailableErr) || unavailableErr.Reason == "" {
		return "", false
	}
	return unavailableErr.Reason, true
}

func IsAlreadyExists(err error) bool {
	return errors.Is(err, ErrAlreadyExists)
}

func IsRebuilding(err error) bool {
	return errors.Is(err, ErrRebuilding)
}

type NotLeaderError struct {
	LeaderAddr string
}

func (e *NotLeaderError) Error() string {
	if e.LeaderAddr == "" {
		return "not shard leader; leader unknown"
	}
	return "not shard leader; leader at " + e.LeaderAddr
}
