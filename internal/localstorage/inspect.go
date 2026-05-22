package localstorage

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"

	"github.com/petabytecl/scrap/internal/api"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/metastore"
)

func (a *Application) GetAdminDocument(_ context.Context, doc identity.Document) (*adminv1.AdminDocument, error) {
	document, err := a.metadata.HeadDocument(doc)
	if err != nil {
		return nil, mapError(err)
	}
	return &adminv1.AdminDocument{
		Document: &adminv1.DocumentTarget{
			TenantId:      document.Identity.TenantID,
			TransactionId: document.Identity.TransactionID,
			DocumentName:  document.Identity.DocumentName,
		},
		ShardId:        "local",
		BlockIds:       []string{document.Location.BlockID},
		Length:         document.Length,
		LogicalSha256:  append([]byte(nil), document.LogicalSHA256[:]...),
		RepairRequired: document.Availability == metastore.AvailabilityDegradedRepair,
	}, nil
}

func (a *Application) GetAdminBlock(ctx context.Context, target api.BlockTarget) (*adminv1.Block, error) {
	if target.ShardID != "local" {
		return nil, mapError(metastore.ErrNotFound)
	}
	documents, err := a.metadata.ListBlockDocuments(target.BlockID)
	if err != nil {
		return nil, mapError(err)
	}
	if len(documents) == 0 {
		return nil, mapError(metastore.ErrNotFound)
	}
	length, checksum, err := a.blockDigest(ctx, target.BlockID)
	if err != nil {
		return nil, mapError(err)
	}
	backendObjectKey := ""
	intent, err := a.metadata.GetUploadIntent(target.BlockID)
	if err != nil && !errors.Is(err, metastore.ErrNotFound) {
		return nil, mapError(err)
	}
	if err == nil {
		backendObjectKey = intent.BackendObjectKey
	}
	return &adminv1.Block{
		ShardId:          "local",
		BlockId:          target.BlockID,
		Length:           length,
		Checksum:         checksum,
		ReplicaMemberIds: []string{"local"},
		BackendObjectKey: backendObjectKey,
	}, nil
}

func (a *Application) blockDigest(ctx context.Context, blockID string) (uint64, []byte, error) {
	if err := ctx.Err(); err != nil {
		return 0, nil, err
	}
	file, err := os.Open(a.blocks.BlockPath(blockID))
	if err != nil {
		return 0, nil, err
	}
	defer file.Close()
	hasher := sha256.New()
	written, err := io.Copy(hasher, file)
	if err != nil {
		return 0, nil, err
	}
	if err := ctx.Err(); err != nil {
		return 0, nil, err
	}
	return uint64(written), hasher.Sum(nil), nil
}
