package store

import "errors"

var (
	ErrAlreadyExists     = errors.New("document already exists")
	ErrNotFound          = errors.New("document not found")
	ErrTxNotFound        = errors.New("transaction not found")
	ErrInvalidArgument   = errors.New("invalid argument")
	ErrResourceExhausted = errors.New("resource exhausted")
	ErrDataLoss          = errors.New("data corruption detected")
)

func IsAlreadyExists(err error) bool {
	return errors.Is(err, ErrAlreadyExists)
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
