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
