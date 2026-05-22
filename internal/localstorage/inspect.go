package localstorage

import (
	"context"

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
