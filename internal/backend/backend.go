package backend

import (
	"context"
	"errors"
	"io"
)

var (
	ErrThrottled        = errors.New("backend: throttled")
	ErrTransient        = errors.New("backend: transient failure")
	ErrAuth             = errors.New("backend: authentication or authorization failed")
	ErrNotFound         = errors.New("backend: object not found")
	ErrConflict         = errors.New("backend: object already exists with different content")
	ErrChecksumMismatch = errors.New("backend: checksum mismatch")
	ErrInvalidRange     = errors.New("backend: invalid range")
	ErrRestorePending   = errors.New("backend: archive restore pending")
	ErrPermanent        = errors.New("backend: permanent failure")
)

type Object struct {
	Key    string
	Length uint64
	SHA256 [32]byte
}

type Range struct {
	Offset uint64
	Length *uint64
}

type Store interface {
	PutObject(context.Context, string, io.Reader) (Object, error)
	HeadObject(context.Context, string) (Object, error)
	ReadObjectRange(context.Context, string, Range, io.Writer) error
}

type MutableStore interface {
	Store
	PutMutableObject(context.Context, string, io.Reader) (Object, error)
}
