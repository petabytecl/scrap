package store

import (
	"fmt"
	"io"
	"unicode"
)

const (
	MaxTransactionIDBytes  = 256
	MaxDocumentNameBytes   = 512
	MaxContentTypeBytes    = 255
	MaxTenantIDBytes       = 256
	MaxIdempotencyKeyBytes = 256
	MaxClientChunkBytes    = 1 << 20
	MaxDocumentBytes       = 128 << 20

	MaxConcurrentWrites = 64
	MaxConcurrentReads  = 256

	ResourceExhaustedReasonChunkTooLarge        = "chunk_too_large"
	ResourceExhaustedReasonDocumentTooLarge     = "document_too_large"
	ResourceExhaustedReasonConcurrentWrites     = "concurrent_writes"
	ResourceExhaustedReasonConcurrentReads      = "concurrent_reads"
	ResourceExhaustedReasonWriteBoundaryStalled = "write_boundary_stalled"
)

func ValidateWriteMetadata(txID, docName, contentType, tenantID, idempotencyKey string) error {
	for _, field := range []textField{
		{name: "transaction_id", value: txID, maxBytes: MaxTransactionIDBytes, required: true},
		{name: "document_name", value: docName, maxBytes: MaxDocumentNameBytes, required: true},
		{name: "content_type", value: contentType, maxBytes: MaxContentTypeBytes, required: true},
		{name: "tenant_id", value: tenantID, maxBytes: MaxTenantIDBytes},
		{name: "idempotency_key", value: idempotencyKey, maxBytes: MaxIdempotencyKeyBytes},
	} {
		if err := validateTextField(field); err != nil {
			return err
		}
	}
	return nil
}

func ValidateDocumentIdentity(txID, docName, tenantID string) error {
	for _, field := range []textField{
		{name: "transaction_id", value: txID, maxBytes: MaxTransactionIDBytes, required: true},
		{name: "document_name", value: docName, maxBytes: MaxDocumentNameBytes, required: true},
		{name: "tenant_id", value: tenantID, maxBytes: MaxTenantIDBytes},
	} {
		if err := validateTextField(field); err != nil {
			return err
		}
	}
	return nil
}

func ValidateTransactionLookup(txID, tenantID string) error {
	for _, field := range []textField{
		{name: "transaction_id", value: txID, maxBytes: MaxTransactionIDBytes, required: true},
		{name: "tenant_id", value: tenantID, maxBytes: MaxTenantIDBytes},
	} {
		if err := validateTextField(field); err != nil {
			return err
		}
	}
	return nil
}

func ValidateClientChunk(chunk []byte) error {
	if len(chunk) > MaxClientChunkBytes {
		return NewResourceExhausted(ResourceExhaustedReasonChunkTooLarge, "client chunk too large")
	}
	return nil
}

func NewDocumentBodyReader(body io.Reader) io.Reader {
	return newDocumentBodyReader(body, MaxDocumentBytes)
}

func newDocumentBodyReader(body io.Reader, maxBytes int64) io.Reader {
	return &documentBodyReader{
		body:     body,
		maxBytes: maxBytes,
	}
}

type textField struct {
	name     string
	value    string
	maxBytes int
	required bool
}

func validateTextField(field textField) error {
	if field.required && field.value == "" {
		return fmt.Errorf("%w: %s is required", ErrInvalidArgument, field.name)
	}
	if len(field.value) > field.maxBytes {
		return fmt.Errorf("%w: %s exceeds %d bytes", ErrInvalidArgument, field.name, field.maxBytes)
	}
	for _, r := range field.value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: %s contains control character", ErrInvalidArgument, field.name)
		}
	}
	return nil
}

type documentBodyReader struct {
	body       io.Reader
	maxBytes   int64
	totalBytes int64
}

func (r *documentBodyReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.totalBytes >= r.maxBytes {
		return r.readLimitProbe()
	}
	remaining := r.maxBytes - r.totalBytes
	if int64(len(p)) > remaining {
		p = p[:remaining]
	}
	n, err := r.body.Read(p)
	if r.totalBytes == 0 && n == 0 && err == io.EOF {
		return 0, fmt.Errorf("%w: document body is empty", ErrInvalidArgument)
	}
	r.totalBytes += int64(n)
	return n, err
}

func (r *documentBodyReader) readLimitProbe() (int, error) {
	var probe [1]byte
	n, err := r.body.Read(probe[:])
	if n > 0 {
		return 0, NewResourceExhausted(ResourceExhaustedReasonDocumentTooLarge, "document too large")
	}
	if err == nil {
		return 0, nil
	}
	return 0, err
}
