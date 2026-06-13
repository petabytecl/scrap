package backend

import (
	"context"
	"io"
)

const DefaultContentType = "application/octet-stream"

type Backend interface {
	PutObject(ctx context.Context, key string, body io.Reader, size int64, opts PutOpts) (PutResult, error)
	HeadObject(ctx context.Context, key string) (ObjectMeta, error)
	GetObject(ctx context.Context, key string, opts GetOpts) (io.ReadCloser, ObjectMeta, error)
	DeleteObject(ctx context.Context, key string) error
	ListObjects(ctx context.Context, prefix string, opts ListOpts) (ObjectIterator, error)
}

type PutOpts struct{}

type PutResult struct {
	ETag string
	Size int64
}

type ObjectMeta struct {
	ETag        string
	Size        int64
	ContentType string
}

type ByteRange struct {
	Enabled bool
	Offset  int64
	Length  int64
}

type GetOpts struct {
	Range ByteRange
}

type ListOpts struct{}

type ObjectInfo struct {
	Key         string
	ETag        string
	Size        int64
	ContentType string
}

type ObjectIterator interface {
	Next() (ObjectInfo, error)
}
