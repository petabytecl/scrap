package api

import (
	"fmt"
	"unicode"
	"unicode/utf8"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	scrapv1 "github.com/petabytecl/scrap/internal/gen/scrap/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/storageapp"
)

const (
	MinChunkSizeHint     uint32 = 64 * 1024
	DefaultChunkSizeHint uint32 = 1024 * 1024
	MaxChunkSizeHint     uint32 = 4 * 1024 * 1024

	MaxTags          = 64
	MaxTagKeyBytes   = 128
	MaxTagValueBytes = 1024
	MaxMetadataBytes = 16 * 1024
)

const (
	reasonEnumUnspecified       = "SCRAP_ENUM_UNSPECIFIED"
	reasonEnumUnknown           = "SCRAP_ENUM_UNKNOWN"
	reasonInvalidSHA256         = "SCRAP_INVALID_SHA256"
	reasonMissingIdempotencyKey = "SCRAP_IDEMPOTENCY_KEY_REQUIRED"
	reasonTooManyTags           = "SCRAP_TOO_MANY_TAGS"
	reasonMetadataTooLarge      = "SCRAP_METADATA_TOO_LARGE"
	reasonInvalidTimeRange      = "SCRAP_INVALID_TIME_RANGE"
	reasonInvalidTimestamp      = "SCRAP_INVALID_TIMESTAMP"
	reasonRangeOverflow         = "SCRAP_RANGE_OVERFLOW"
)

type WriteDocumentInit = storageapp.WriteDocumentInit

type HeadDocumentRequest = storageapp.HeadDocumentRequest

type ReadRange = storageapp.ReadRange

type TimeRange = storageapp.TimeRange

type ReadDocumentRequest = storageapp.ReadDocumentRequest

type FindDocumentsRequest = storageapp.FindDocumentsRequest

type DocumentFilter = storageapp.DocumentFilter

type CompleteTransactionRequest = storageapp.CompleteTransactionRequest

type GetTransactionRequest = storageapp.GetTransactionRequest

type violations struct {
	fields []*errdetails.BadRequest_FieldViolation
}

func ValidateWriteDocumentInit(init *scrapv1.WriteDocumentInit) (WriteDocumentInit, error) {
	var problems violations
	if init == nil {
		problems.add("init", identity.ReasonRequired, "write init is required")
		return WriteDocumentInit{}, problems.err()
	}

	doc := validateDocumentIdentity("identity", init.Identity, &problems)
	validateDocumentClass("document_class", init.DocumentClass, &problems)
	validatePriorityClass("priority_class", init.PriorityClass, &problems)
	validateOptionalText("content_type", init.ContentType, &problems)
	validateOptionalText("client_idempotency_key", init.ClientIdempotencyKey, &problems)
	validateRequiredText("created_by_service", init.CreatedByService, &problems)
	validateOptionalText("workflow_stage", init.WorkflowStage, &problems)
	validateTags("tags", init.Tags, &problems)

	if init.ExpectedSha256 != nil && len(init.ExpectedSha256) != 32 {
		problems.add("expected_sha256", reasonInvalidSHA256, "expected_sha256 must contain exactly 32 bytes")
	}
	if init.PriorityClass == scrapv1.PriorityClass_PRIORITY_CLASS_CRITICAL_INGEST &&
		(init.ClientIdempotencyKey == nil || *init.ClientIdempotencyKey == "") {
		problems.add("client_idempotency_key", reasonMissingIdempotencyKey, "critical ingest writes require a client idempotency key")
	}
	if size := writeMetadataSize(init); size > MaxMetadataBytes {
		problems.add("metadata", reasonMetadataTooLarge, fmt.Sprintf("metadata is %d bytes; maximum is %d bytes", size, MaxMetadataBytes))
	}
	if err := problems.err(); err != nil {
		return WriteDocumentInit{}, err
	}

	return WriteDocumentInit{
		Identity:             doc,
		DocumentClass:        documentClassFromProto(init.DocumentClass),
		PriorityClass:        priorityClassFromProto(init.PriorityClass),
		ContentType:          optionalString(init.ContentType),
		ExpectedLength:       cloneUint64(init.ExpectedLength),
		ExpectedSHA256:       cloneBytes(init.ExpectedSha256),
		ClientIdempotencyKey: optionalString(init.ClientIdempotencyKey),
		CreatedByService:     init.CreatedByService,
		WorkflowStage:        optionalString(init.WorkflowStage),
		Tags:                 cloneTags(init.Tags),
	}, nil
}

func ValidateHeadDocumentRequest(req *scrapv1.HeadDocumentRequest) (HeadDocumentRequest, error) {
	var problems violations
	if req == nil {
		problems.add("request", identity.ReasonRequired, "request is required")
		return HeadDocumentRequest{}, problems.err()
	}
	doc := validateDocumentIdentity("identity", req.Identity, &problems)
	if err := problems.err(); err != nil {
		return HeadDocumentRequest{}, err
	}
	return HeadDocumentRequest{Identity: doc}, nil
}

func ValidateReadDocumentRequest(req *scrapv1.ReadDocumentRequest) (ReadDocumentRequest, error) {
	var problems violations
	if req == nil {
		problems.add("request", identity.ReasonRequired, "request is required")
		return ReadDocumentRequest{}, problems.err()
	}

	doc := validateDocumentIdentity("identity", req.Identity, &problems)
	readRange := validateReadRange("range", req.Range, &problems)
	if err := problems.err(); err != nil {
		return ReadDocumentRequest{}, err
	}
	return ReadDocumentRequest{
		Identity:  doc,
		Range:     readRange,
		ChunkSize: clampChunkSize(req.ChunkSizeHint),
	}, nil
}

func ValidateFindDocumentsRequest(req *scrapv1.FindDocumentsRequest) (FindDocumentsRequest, error) {
	var problems violations
	if req == nil {
		problems.add("request", identity.ReasonRequired, "request is required")
		return FindDocumentsRequest{}, problems.err()
	}

	transaction := validateTransactionIdentity("transaction", req.Transaction, &problems)
	filter := validateDocumentFilter(req.Filter, &problems)
	if err := problems.err(); err != nil {
		return FindDocumentsRequest{}, err
	}
	return FindDocumentsRequest{Transaction: transaction, Filter: filter}, nil
}

func ValidateCompleteTransactionRequest(req *scrapv1.CompleteTransactionRequest) (CompleteTransactionRequest, error) {
	var problems violations
	if req == nil {
		problems.add("request", identity.ReasonRequired, "request is required")
		return CompleteTransactionRequest{}, problems.err()
	}

	transaction := validateTransactionIdentity("transaction", req.Transaction, &problems)
	validateOptionalText("completed_by_service", req.CompletedByService, &problems)
	validateTags("tags", req.Tags, &problems)
	if size := completeTransactionMetadataSize(req); size > MaxMetadataBytes {
		problems.add("metadata", reasonMetadataTooLarge, fmt.Sprintf("metadata is %d bytes; maximum is %d bytes", size, MaxMetadataBytes))
	}
	if err := problems.err(); err != nil {
		return CompleteTransactionRequest{}, err
	}
	return CompleteTransactionRequest{
		Transaction:        transaction,
		CompletedByService: optionalString(req.CompletedByService),
		Tags:               cloneTags(req.Tags),
	}, nil
}

func ValidateGetTransactionRequest(req *scrapv1.GetTransactionRequest) (GetTransactionRequest, error) {
	var problems violations
	if req == nil {
		problems.add("request", identity.ReasonRequired, "request is required")
		return GetTransactionRequest{}, problems.err()
	}

	transaction := validateTransactionIdentity("transaction", req.Transaction, &problems)
	if err := problems.err(); err != nil {
		return GetTransactionRequest{}, err
	}
	return GetTransactionRequest{Transaction: transaction}, nil
}

func validateDocumentIdentity(field string, value *scrapv1.DocumentIdentity, problems *violations) identity.Document {
	if value == nil {
		problems.add(field, identity.ReasonRequired, "document identity is required")
		return identity.Document{}
	}
	doc, identityProblems := identity.NewDocument(value.TenantId, value.TransactionId, value.DocumentName)
	addIdentityProblems(field, identityProblems, problems)
	return doc
}

func validateTransactionIdentity(field string, value *scrapv1.TransactionIdentity, problems *violations) identity.Transaction {
	if value == nil {
		problems.add(field, identity.ReasonRequired, "transaction identity is required")
		return identity.Transaction{}
	}
	transaction, identityProblems := identity.NewTransaction(value.TenantId, value.TransactionId)
	addIdentityProblems(field, identityProblems, problems)
	return transaction
}

func addIdentityProblems(field string, identityProblems []identity.Problem, problems *violations) {
	for _, problem := range identityProblems {
		problems.add(field+"."+problem.Field, problem.Reason, problem.Description)
	}
}

func validateDocumentClass(field string, value scrapv1.DocumentClass, problems *violations) {
	if value == scrapv1.DocumentClass_DOCUMENT_CLASS_UNSPECIFIED {
		problems.add(field, reasonEnumUnspecified, "document_class must be specified")
		return
	}
	if _, ok := scrapv1.DocumentClass_name[int32(value)]; !ok {
		problems.add(field, reasonEnumUnknown, "document_class is not recognized")
	}
}

func validatePriorityClass(field string, value scrapv1.PriorityClass, problems *violations) {
	if value == scrapv1.PriorityClass_PRIORITY_CLASS_UNSPECIFIED {
		problems.add(field, reasonEnumUnspecified, "priority_class must be specified")
		return
	}
	if _, ok := scrapv1.PriorityClass_name[int32(value)]; !ok {
		problems.add(field, reasonEnumUnknown, "priority_class is not recognized")
	}
}

func validateOptionalDocumentClass(field string, value *scrapv1.DocumentClass, problems *violations) {
	if value == nil {
		return
	}
	validateDocumentClass(field, *value, problems)
}

func validateReadRange(field string, value *scrapv1.ReadRange, problems *violations) *ReadRange {
	if value == nil {
		return nil
	}
	if value.Length != nil && value.Offset+*value.Length < value.Offset {
		problems.add(field+".length", reasonRangeOverflow, "range offset and length overflow uint64")
	}
	return &ReadRange{Offset: value.Offset, Length: cloneUint64(value.Length)}
}

func validateDocumentFilter(filter *scrapv1.DocumentFilter, problems *violations) DocumentFilter {
	if filter == nil {
		return DocumentFilter{}
	}

	out := DocumentFilter{Tags: cloneTags(filter.Tags)}
	if filter.DocumentNameExact != nil {
		normalized, problem := identity.NormalizeDocumentName(*filter.DocumentNameExact)
		if problem != nil {
			problems.add("filter.document_name_exact", problem.Reason, problem.Description)
		} else {
			out.DocumentNameExact = normalized
			out.HasDocumentNameExact = true
		}
	}
	if filter.DocumentNamePrefix != nil {
		normalized, problem := identity.NormalizeDocumentNamePrefix(*filter.DocumentNamePrefix)
		if problem != nil {
			problems.add("filter.document_name_prefix", problem.Reason, problem.Description)
		} else {
			out.DocumentNamePrefix = normalized
			out.HasDocumentNamePrefix = true
		}
	}
	validateOptionalDocumentClass("filter.document_class", filter.DocumentClass, problems)
	validateOptionalText("filter.content_type", filter.ContentType, problems)
	validateOptionalText("filter.workflow_stage", filter.WorkflowStage, problems)
	validateOptionalText("filter.created_by_service", filter.CreatedByService, problems)
	out.CreatedAt = validateTimeRange("filter.created_at", filter.CreatedAt, problems)
	validateTags("filter.tags", filter.Tags, problems)

	if filter.DocumentClass != nil {
		out.DocumentClass = documentClassFromProto(*filter.DocumentClass)
		out.HasDocumentClass = true
	}
	if filter.ContentType != nil {
		out.ContentType = *filter.ContentType
		out.HasContentType = true
	}
	if filter.WorkflowStage != nil {
		out.WorkflowStage = *filter.WorkflowStage
		out.HasWorkflowStage = true
	}
	if filter.CreatedByService != nil {
		out.CreatedByService = *filter.CreatedByService
		out.HasCreatedByService = true
	}
	return out
}

func validateTimeRange(field string, value *scrapv1.TimeRange, problems *violations) *TimeRange {
	if value == nil {
		return nil
	}
	validateTimestamp(field+".start_time", value.StartTime, problems)
	validateTimestamp(field+".end_time", value.EndTime, problems)
	if invalidTimeRange(value) {
		problems.add(field, reasonInvalidTimeRange, "end_time must not be before start_time")
	}
	out := &TimeRange{}
	if validTimestamp(value.StartTime) {
		start := value.StartTime.AsTime()
		out.StartTime = &start
	}
	if validTimestamp(value.EndTime) {
		end := value.EndTime.AsTime()
		out.EndTime = &end
	}
	return out
}

func invalidTimeRange(value *scrapv1.TimeRange) bool {
	if !validTimestamp(value.StartTime) || !validTimestamp(value.EndTime) {
		return false
	}
	return value.EndTime.AsTime().Before(value.StartTime.AsTime())
}

func validTimestamp(value *timestamppb.Timestamp) bool {
	return value != nil && value.CheckValid() == nil
}

func validateTimestamp(field string, value *timestamppb.Timestamp, problems *violations) {
	if value == nil {
		return
	}
	if err := value.CheckValid(); err != nil {
		problems.add(field, reasonInvalidTimestamp, "timestamp is outside the valid protobuf range")
	}
}

func validateRequiredText(field, value string, problems *violations) {
	if value == "" {
		problems.add(field, identity.ReasonRequired, field+" is required")
		return
	}
	validateText(field, value, problems)
}

func validateOptionalText(field string, value *string, problems *violations) {
	if value == nil {
		return
	}
	if *value == "" {
		problems.add(field, identity.ReasonRequired, field+" must not be empty when set")
		return
	}
	validateText(field, *value, problems)
}

func validateText(field, value string, problems *violations) {
	switch {
	case !utf8.ValidString(value):
		problems.add(field, identity.ReasonInvalidUTF8, field+" must be valid UTF-8")
	case containsControl(value):
		problems.add(field, identity.ReasonControlChar, field+" must not contain control characters")
	}
}

func validateTags(field string, tags map[string]string, problems *violations) {
	if len(tags) > MaxTags {
		problems.add(field, reasonTooManyTags, fmt.Sprintf("tags contain %d entries; maximum is %d", len(tags), MaxTags))
	}
	for key, value := range tags {
		validateTagKey(field, key, problems)
		validateTagValue(field+"["+key+"]", value, problems)
	}
}

func validateTagKey(field, key string, problems *violations) {
	switch {
	case key == "":
		problems.add(field, identity.ReasonRequired, "tag key is required")
	case len(key) > MaxTagKeyBytes:
		problems.add(field, identity.ReasonTooLong, "tag key exceeds maximum byte length")
	case !utf8.ValidString(key):
		problems.add(field, identity.ReasonInvalidUTF8, "tag key must be valid UTF-8")
	case containsControl(key):
		problems.add(field, identity.ReasonControlChar, "tag key must not contain control characters")
	}
}

func validateTagValue(field, value string, problems *violations) {
	switch {
	case len(value) > MaxTagValueBytes:
		problems.add(field, identity.ReasonTooLong, "tag value exceeds maximum byte length")
	case !utf8.ValidString(value):
		problems.add(field, identity.ReasonInvalidUTF8, "tag value must be valid UTF-8")
	case containsControl(value):
		problems.add(field, identity.ReasonControlChar, "tag value must not contain control characters")
	}
}

func writeMetadataSize(init *scrapv1.WriteDocumentInit) int {
	if init == nil {
		return 0
	}
	size := documentIdentitySize(init.Identity)
	size += len(optionalString(init.ContentType))
	size += len(init.ExpectedSha256)
	size += len(optionalString(init.ClientIdempotencyKey))
	size += len(init.CreatedByService)
	size += len(optionalString(init.WorkflowStage))
	size += tagsSize(init.Tags)
	return size
}

func completeTransactionMetadataSize(req *scrapv1.CompleteTransactionRequest) int {
	if req == nil {
		return 0
	}
	return transactionIdentitySize(req.Transaction) + len(optionalString(req.CompletedByService)) + tagsSize(req.Tags)
}

func documentIdentitySize(value *scrapv1.DocumentIdentity) int {
	if value == nil {
		return 0
	}
	return len(value.TenantId) + len(value.TransactionId) + len(value.DocumentName)
}

func transactionIdentitySize(value *scrapv1.TransactionIdentity) int {
	if value == nil {
		return 0
	}
	return len(value.TenantId) + len(value.TransactionId)
}

func tagsSize(tags map[string]string) int {
	var size int
	for key, value := range tags {
		size += len(key) + len(value)
	}
	return size
}

func clampChunkSize(value *uint32) uint32 {
	if value == nil {
		return DefaultChunkSizeHint
	}
	switch {
	case *value < MinChunkSizeHint:
		return MinChunkSizeHint
	case *value > MaxChunkSizeHint:
		return MaxChunkSizeHint
	default:
		return *value
	}
}

func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func cloneUint64(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	return append([]byte(nil), value...)
}

func cloneTags(tags map[string]string) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	out := make(map[string]string, len(tags))
	for key, value := range tags {
		out[key] = value
	}
	return out
}

func (v *violations) add(field, reason, description string) {
	v.fields = append(v.fields, &errdetails.BadRequest_FieldViolation{
		Field:       field,
		Reason:      reason,
		Description: description,
	})
}

func (v violations) err() error {
	if len(v.fields) == 0 {
		return nil
	}
	badRequest := &errdetails.BadRequest{FieldViolations: v.fields}
	st, err := status.New(codes.InvalidArgument, "invalid request").WithDetails(badRequest)
	if err != nil {
		return status.Error(codes.InvalidArgument, "invalid request")
	}
	return st.Err()
}

func containsControl(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}
