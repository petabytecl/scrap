package node

import (
	"testing"

	scrapv1 "github.com/petabytecl/scrap/internal/gen/scrap/v1"
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestPublicTenantExtractorsCoverTenantScopedMethods(t *testing.T) {
	extractors := publicTenantExtractors()
	cases := []struct {
		name   string
		method string
		req    any
	}{
		{
			name:   "write document init",
			method: scrapv1.DocumentService_WriteDocument_FullMethodName,
			req: &scrapv1.WriteDocumentRequest{
				Message: &scrapv1.WriteDocumentRequest_Init{Init: &scrapv1.WriteDocumentInit{Identity: authzDocumentIdentity("tenant-a")}},
			},
		},
		{
			name:   "head document",
			method: scrapv1.DocumentService_HeadDocument_FullMethodName,
			req:    &scrapv1.HeadDocumentRequest{Identity: authzDocumentIdentity("tenant-a")},
		},
		{
			name:   "read document",
			method: scrapv1.DocumentService_ReadDocument_FullMethodName,
			req:    &scrapv1.ReadDocumentRequest{Identity: authzDocumentIdentity("tenant-a")},
		},
		{
			name:   "find documents",
			method: scrapv1.DocumentService_FindDocuments_FullMethodName,
			req:    &scrapv1.FindDocumentsRequest{Transaction: transactionIdentity("tenant-a")},
		},
		{
			name:   "complete transaction",
			method: scrapv1.TransactionService_CompleteTransaction_FullMethodName,
			req:    &scrapv1.CompleteTransactionRequest{Transaction: transactionIdentity("tenant-a")},
		},
		{
			name:   "get transaction",
			method: scrapv1.TransactionService_GetTransaction_FullMethodName,
			req:    &scrapv1.GetTransactionRequest{Transaction: transactionIdentity("tenant-a")},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			extractor := extractors[tc.method]
			if extractor == nil {
				t.Fatalf("missing tenant extractor for %s", tc.method)
			}
			tenantID, ok := extractor(tc.req)
			testutil.RequireTruef(t, ok, "tenant extracted")
			testutil.RequireEqualf(t, tenantID, "tenant-a", "tenant id")
		})
	}
}

func TestWriteDocumentTenantExtractorIgnoresChunkMessages(t *testing.T) {
	extractor := publicTenantExtractors()[scrapv1.DocumentService_WriteDocument_FullMethodName]
	_, ok := extractor(&scrapv1.WriteDocumentRequest{
		Message: &scrapv1.WriteDocumentRequest_Chunk{Chunk: &scrapv1.WriteDocumentChunk{Data: []byte("data")}},
	})
	testutil.RequireFalsef(t, ok, "write chunk tenant")
}

func authzDocumentIdentity(tenantID string) *scrapv1.DocumentIdentity {
	return &scrapv1.DocumentIdentity{
		TenantId:      tenantID,
		TransactionId: "txn-1",
		DocumentName:  "invoice.xml",
	}
}

func transactionIdentity(tenantID string) *scrapv1.TransactionIdentity {
	return &scrapv1.TransactionIdentity{
		TenantId:      tenantID,
		TransactionId: "txn-1",
	}
}
