package shard

import (
	"crypto/sha256"
	"fmt"
	"io"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/block"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func classifyDuplicateDocumentWrite(
	contentType string,
	body io.Reader,
	existing docWithBlock,
) (storeapi.WriteResult, error) {
	sum, size, err := hashDuplicateDocumentBody(body)
	if err != nil {
		return storeapi.WriteResult{}, fmt.Errorf("shard: read duplicate document body: %w", err)
	}
	if duplicateDocumentMatches(contentType, sum, size, existing) {
		return storeapi.WriteResult{
			SHA256:    existing.SHA256,
			Size:      existing.TotalBytes,
			CreatedAt: existing.CreatedAt,
		}, nil
	}
	return storeapi.WriteResult{}, duplicateDocumentConflictError()
}

func hashDuplicateDocumentBody(body io.Reader) ([sha256.Size]byte, int64, error) {
	h := sha256.New()
	size, err := io.Copy(h, body)
	if err != nil {
		return [sha256.Size]byte{}, 0, err
	}
	var sum [sha256.Size]byte
	copy(sum[:], h.Sum(nil))
	return sum, size, nil
}

func drainDuplicateDocumentBody(body io.Reader) error {
	if _, err := io.Copy(io.Discard, body); err != nil {
		return fmt.Errorf("shard: drain duplicate document body: %w", err)
	}
	return nil
}

func duplicateDocumentConflictError() error {
	return fmt.Errorf("%w: immutable document conflict", storeapi.ErrAlreadyExists)
}

func corruptDuplicateMetadataError() error {
	return fmt.Errorf("%w: corrupt duplicate document metadata", storeapi.ErrDataLoss)
}

func duplicateDocumentMatches(contentType string, sum [sha256.Size]byte, size int64, existing docWithBlock) bool {
	return existing.ContentType == contentType &&
		existing.TotalBytes == size &&
		existing.SHA256 == sum
}

func commitDocumentMatchesExisting(doc *scrapv1.CommitDocument, existing block.IndexEntry) bool {
	if len(doc.Sha256) != sha256.Size {
		return false
	}
	var sum [sha256.Size]byte
	copy(sum[:], doc.Sha256)
	return duplicateDocumentMatches(doc.ContentType, sum, doc.TotalBytes, docWithBlock{IndexEntry: existing})
}
