package store

import "errors"

const ResourceExhaustedReasonUploadPressure = "upload_pressure"

var (
	ErrAlreadyExists     = errors.New("document already exists")
	ErrNotFound          = errors.New("document not found")
	ErrTxNotFound        = errors.New("transaction not found")
	ErrInvalidArgument   = errors.New("invalid argument")
	ErrResourceExhausted = errors.New("resource exhausted")
	ErrDataLoss          = errors.New("data corruption detected")
	ErrRebuilding        = errors.New("projection rebuild in progress")
)

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
