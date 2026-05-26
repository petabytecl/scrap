package store

import (
	"context"
	"io"
	"time"
)

type Store interface {
	WriteDocument(ctx context.Context, txID, docName, contentType, idempotencyKey string, body io.Reader) (WriteResult, error)
	HeadDocument(ctx context.Context, txID, docName string) (DocumentMeta, error)
	ReadDocument(ctx context.Context, txID, docName string) (io.ReadCloser, DocumentMeta, error)
	FindDocuments(ctx context.Context, txID string) ([]DocumentMeta, error)
}

type WriteResult struct {
	SHA256Checksum string
	Size           int64
	CreatedAt      time.Time
}

type DocumentMeta struct {
	Name           string
	ContentType    string
	Size           int64
	SHA256Checksum string
	CreatedAt      time.Time
}
