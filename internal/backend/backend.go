package backend

import (
	"context"
	"errors"
	"io"
)

var (
	ErrNotFound         = errors.New("backend: object not found")
	ErrConflict         = errors.New("backend: object already exists with different content")
	ErrChecksumMismatch = errors.New("backend: checksum mismatch")
	ErrInvalidRange     = errors.New("backend: invalid range")
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
