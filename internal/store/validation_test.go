package store

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestValidateWriteMetadataRejectsContractViolations(t *testing.T) {
	tests := []struct {
		name string
		txID string
		doc  string
		ct   string
	}{
		{name: "missing transaction", doc: "doc.xml", ct: "text/xml"},
		{name: "missing document", txID: "tx-1", ct: "text/xml"},
		{name: "missing content type", txID: "tx-1", doc: "doc.xml"},
		{name: "oversized transaction", txID: strings.Repeat("t", MaxTransactionIDBytes+1), doc: "doc.xml", ct: "text/xml"},
		{name: "oversized document", txID: "tx-1", doc: strings.Repeat("d", MaxDocumentNameBytes+1), ct: "text/xml"},
		{name: "oversized content type", txID: "tx-1", doc: "doc.xml", ct: strings.Repeat("c", MaxContentTypeBytes+1)},
		{name: "control transaction", txID: "tx-\n1", doc: "doc.xml", ct: "text/xml"},
		{name: "control document", txID: "tx-1", doc: "doc\x001.xml", ct: "text/xml"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateWriteMetadata(tt.txID, tt.doc, tt.ct, "", "")
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("ValidateWriteMetadata error = %v, want ErrInvalidArgument", err)
			}
		})
	}
}

func TestValidateWriteMetadataAcceptsExactLimits(t *testing.T) {
	err := ValidateWriteMetadata(
		strings.Repeat("t", MaxTransactionIDBytes),
		strings.Repeat("d", MaxDocumentNameBytes),
		strings.Repeat("c", MaxContentTypeBytes),
		strings.Repeat("n", MaxTenantIDBytes),
		strings.Repeat("i", MaxIdempotencyKeyBytes),
	)
	if err != nil {
		t.Fatalf("ValidateWriteMetadata exact limits: %v", err)
	}
}

func TestValidateDocumentIdentityAndLookupRejectInvalidTenant(t *testing.T) {
	if err := ValidateDocumentIdentity("tx-1", "doc.xml", "tenant\n1"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("ValidateDocumentIdentity error = %v, want ErrInvalidArgument", err)
	}
	if err := ValidateTransactionLookup("tx-1", strings.Repeat("t", MaxTenantIDBytes+1)); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("ValidateTransactionLookup error = %v, want ErrInvalidArgument", err)
	}
}

func TestValidateClientChunkRejectsOversizedChunk(t *testing.T) {
	err := ValidateClientChunk(make([]byte, MaxClientChunkBytes+1))
	if !errors.Is(err, ErrResourceExhausted) {
		t.Fatalf("ValidateClientChunk error = %v, want ErrResourceExhausted", err)
	}
	reason, ok := ResourceExhaustedReason(err)
	if !ok || reason != ResourceExhaustedReasonChunkTooLarge {
		t.Fatalf("resource exhausted reason = %q/%v, want chunk_too_large/true", reason, ok)
	}
}

func TestDocumentBodyReaderRejectsZeroByteDocument(t *testing.T) {
	reader := NewDocumentBodyReader(bytes.NewReader(nil))
	_, err := io.ReadAll(reader)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("ReadAll error = %v, want ErrInvalidArgument", err)
	}
}

func TestDocumentBodyReaderRejectsDocumentOverLimit(t *testing.T) {
	reader := newDocumentBodyReader(bytes.NewReader([]byte("123456")), 5)
	_, err := io.ReadAll(reader)
	if !errors.Is(err, ErrResourceExhausted) {
		t.Fatalf("ReadAll error = %v, want ErrResourceExhausted", err)
	}
	reason, ok := ResourceExhaustedReason(err)
	if !ok || reason != ResourceExhaustedReasonDocumentTooLarge {
		t.Fatalf("resource exhausted reason = %q/%v, want document_too_large/true", reason, ok)
	}
}

func TestDocumentBodyReaderAcceptsExactLimit(t *testing.T) {
	reader := newDocumentBodyReader(bytes.NewReader([]byte("12345")), 5)
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll exact limit: %v", err)
	}
	if string(got) != "12345" {
		t.Fatalf("ReadAll = %q, want 12345", got)
	}
}
