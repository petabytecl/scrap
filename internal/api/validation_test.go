package api

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	scrapv1 "github.com/petabytecl/scrap/internal/gen/scrap/v1"
	"github.com/petabytecl/scrap/internal/identity"
)

func TestValidateWriteDocumentInitAcceptsValidRequest(t *testing.T) {
	expectedLength := uint64(123)
	idempotencyKey := "write-1"
	contentType := "application/pdf"
	workflowStage := "seal"
	init := validWriteInit()
	init.ExpectedLength = &expectedLength
	init.ExpectedSha256 = make([]byte, 32)
	init.ClientIdempotencyKey = &idempotencyKey
	init.ContentType = &contentType
	init.WorkflowStage = &workflowStage
	init.Identity.DocumentName = `billing\invoice.pdf`

	validated, err := ValidateWriteDocumentInit(init)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if validated.Identity.DocumentName != "billing/invoice.pdf" {
		t.Fatalf("document name = %q, want normalized path", validated.Identity.DocumentName)
	}
	if validated.ExpectedLength == nil || *validated.ExpectedLength != expectedLength {
		t.Fatalf("expected length = %v, want %d", validated.ExpectedLength, expectedLength)
	}
	if validated.Tags["workflow"] != "billing" {
		t.Fatalf("tag was not preserved: %#v", validated.Tags)
	}
}

func TestValidateWriteDocumentInitReturnsTypedViolations(t *testing.T) {
	init := validWriteInit()
	init.Identity.TenantId = ""
	init.DocumentClass = scrapv1.DocumentClass_DOCUMENT_CLASS_UNSPECIFIED
	init.PriorityClass = scrapv1.PriorityClass_PRIORITY_CLASS_CRITICAL_INGEST
	init.ExpectedSha256 = []byte{1, 2, 3}
	init.ClientIdempotencyKey = nil

	_, err := ValidateWriteDocumentInit(init)
	violations := requireBadRequest(t, err)
	requireViolation(t, violations, "identity.tenant_id", identity.ReasonRequired)
	requireViolation(t, violations, "document_class", reasonEnumUnspecified)
	requireViolation(t, violations, "expected_sha256", reasonInvalidSHA256)
	requireViolation(t, violations, "client_idempotency_key", reasonMissingIdempotencyKey)
}

func TestValidateWriteDocumentInitRejectsOversizedMetadata(t *testing.T) {
	init := validWriteInit()
	init.Tags = map[string]string{
		"oversized": strings.Repeat("x", MaxMetadataBytes),
	}

	_, err := ValidateWriteDocumentInit(init)
	violations := requireBadRequest(t, err)
	requireViolation(t, violations, "tags[oversized]", identity.ReasonTooLong)
	requireViolation(t, violations, "metadata", reasonMetadataTooLarge)
}

func TestValidateReadDocumentRequestNormalizesChunkHintAndRejectsOverflow(t *testing.T) {
	tinyHint := uint32(1)
	valid, err := ValidateReadDocumentRequest(&scrapv1.ReadDocumentRequest{
		Identity:      validDocumentIdentity(),
		ChunkSizeHint: &tinyHint,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if valid.ChunkSize != MinChunkSizeHint {
		t.Fatalf("chunk size = %d, want %d", valid.ChunkSize, MinChunkSizeHint)
	}

	length := uint64(2)
	_, err = ValidateReadDocumentRequest(&scrapv1.ReadDocumentRequest{
		Identity: validDocumentIdentity(),
		Range: &scrapv1.ReadRange{
			Offset: ^uint64(0),
			Length: &length,
		},
	})
	violations := requireBadRequest(t, err)
	requireViolation(t, violations, "range.length", reasonRangeOverflow)
}

func TestValidateFindDocumentsRequestValidatesFilter(t *testing.T) {
	start := timestamppb.New(time.Unix(20, 0))
	end := timestamppb.New(time.Unix(10, 0))
	docClass := scrapv1.DocumentClass_DOCUMENT_CLASS_UNSPECIFIED
	prefix := `folder\`

	_, err := ValidateFindDocumentsRequest(&scrapv1.FindDocumentsRequest{
		Transaction: validTransactionIdentity(),
		Filter: &scrapv1.DocumentFilter{
			DocumentNamePrefix: &prefix,
			DocumentClass:      &docClass,
			CreatedAt: &scrapv1.TimeRange{
				StartTime: start,
				EndTime:   end,
			},
		},
	})
	violations := requireBadRequest(t, err)
	requireViolation(t, violations, "filter.document_class", reasonEnumUnspecified)
	requireViolation(t, violations, "filter.created_at", reasonInvalidTimeRange)
}

func TestValidateCompleteTransactionRequestRejectsTooManyTags(t *testing.T) {
	req := &scrapv1.CompleteTransactionRequest{
		Transaction: validTransactionIdentity(),
		Tags:        make(map[string]string, MaxTags+1),
	}
	for i := range MaxTags + 1 {
		req.Tags[string(rune('a'+i))] = "value"
	}

	_, err := ValidateCompleteTransactionRequest(req)
	violations := requireBadRequest(t, err)
	requireViolation(t, violations, "tags", reasonTooManyTags)
}

func validWriteInit() *scrapv1.WriteDocumentInit {
	return &scrapv1.WriteDocumentInit{
		Identity:         validDocumentIdentity(),
		DocumentClass:    scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
		PriorityClass:    scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
		CreatedByService: "billing-etl",
		Tags: map[string]string{
			"workflow": "billing",
		},
	}
}

func validDocumentIdentity() *scrapv1.DocumentIdentity {
	return &scrapv1.DocumentIdentity{
		TenantId:      "tenant",
		TransactionId: "txn",
		DocumentName:  "invoice.xml",
	}
}

func validTransactionIdentity() *scrapv1.TransactionIdentity {
	return &scrapv1.TransactionIdentity{
		TenantId:      "tenant",
		TransactionId: "txn",
	}
}

func requireBadRequest(t *testing.T, err error) []*errdetails.BadRequest_FieldViolation {
	t.Helper()
	if err == nil {
		t.Fatal("expected error")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("error is not a status error: %v", err)
	}
	if st.Code() != codes.InvalidArgument {
		t.Fatalf("code = %s, want %s", st.Code(), codes.InvalidArgument)
	}
	for _, detail := range st.Details() {
		if badRequest, ok := detail.(*errdetails.BadRequest); ok {
			return badRequest.FieldViolations
		}
	}
	t.Fatalf("status details did not include BadRequest: %#v", st.Details())
	return nil
}

func requireViolation(t *testing.T, violations []*errdetails.BadRequest_FieldViolation, field, reason string) {
	t.Helper()
	for _, violation := range violations {
		if violation.Field == field && violation.Reason == reason {
			return
		}
	}
	t.Fatalf("missing violation field=%s reason=%s in %#v", field, reason, violations)
}
