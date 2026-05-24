package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/petabytecl/scrap/internal/authz"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	scrapv1 "github.com/petabytecl/scrap/internal/gen/scrap/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/operations"
	"github.com/petabytecl/scrap/internal/storageapp"
)

const reasonInvalidStreamOrder = "SCRAP_INVALID_STREAM_ORDER"

type DocumentApplication = storageapp.DocumentApplication

type TransactionApplication = storageapp.TransactionApplication

type ChunkReader = storageapp.ChunkReader

type ReadDocumentSender = storageapp.ReadDocumentSender

type PublicServer struct {
	documents    DocumentApplication
	transactions TransactionApplication
	operations   *operations.Store
	now          func() time.Time
}

type WriteDocumentResult = storageapp.WriteDocumentResult

type FindDocumentsResult = storageapp.FindDocumentsResult

type DocumentMetadata = storageapp.DocumentMetadata

type ReadDocumentMetadata = storageapp.ReadDocumentMetadata

type TransactionState = storageapp.TransactionState

type PublicOption func(*PublicServer)

func NewPublicServer(documents DocumentApplication, transactions TransactionApplication, options ...PublicOption) *PublicServer {
	server := &PublicServer{
		documents:    documents,
		transactions: transactions,
		now:          func() time.Time { return time.Now().UTC() },
	}
	for _, option := range options {
		option(server)
	}
	return server
}

func WithPublicAuditStore(store *operations.Store) PublicOption {
	return func(server *PublicServer) {
		server.operations = store
	}
}

func RegisterPublicServer(registrar grpc.ServiceRegistrar, server *PublicServer) {
	scrapv1.RegisterDocumentServiceServer(registrar, server)
	scrapv1.RegisterTransactionServiceServer(registrar, server)
}

func (s *PublicServer) WriteDocument(stream grpc.ClientStreamingServer[scrapv1.WriteDocumentRequest, scrapv1.WriteDocumentResponse]) error {
	if s.documents == nil {
		return status.Error(codes.Unimplemented, "document service is not configured")
	}

	first, err := stream.Recv()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return invalidArgument("message", reasonInvalidStreamOrder, "first write message must be init")
		}
		return err
	}
	initMessage := first.GetInit()
	if initMessage == nil {
		return invalidArgument("message", reasonInvalidStreamOrder, "first write message must be init")
	}
	init, err := ValidateWriteDocumentInit(initMessage)
	if err != nil {
		return err
	}

	result, err := s.documents.WriteDocument(stream.Context(), init, &writeChunkReader{stream: stream})
	if err != nil {
		return ToGRPCError(err)
	}
	if err := s.auditSuccessfulWrite(stream.Context(), init, result); err != nil {
		return err
	}
	return stream.SendAndClose(writeDocumentResultToProto(result))
}

func (s *PublicServer) HeadDocument(ctx context.Context, req *scrapv1.HeadDocumentRequest) (*scrapv1.HeadDocumentResponse, error) {
	if s.documents == nil {
		return nil, status.Error(codes.Unimplemented, "document service is not configured")
	}
	validated, err := ValidateHeadDocumentRequest(req)
	if err != nil {
		return nil, err
	}
	metadata, err := s.documents.HeadDocument(ctx, validated)
	if err != nil {
		return nil, ToGRPCError(err)
	}
	return &scrapv1.HeadDocumentResponse{Metadata: documentMetadataToProto(metadata)}, nil
}

func (s *PublicServer) ReadDocument(req *scrapv1.ReadDocumentRequest, stream grpc.ServerStreamingServer[scrapv1.ReadDocumentResponse]) error {
	if s.documents == nil {
		return status.Error(codes.Unimplemented, "document service is not configured")
	}
	validated, err := ValidateReadDocumentRequest(req)
	if err != nil {
		return err
	}
	return ToGRPCError(s.documents.ReadDocument(stream.Context(), validated, readDocumentSender{stream: stream}))
}

func (s *PublicServer) FindDocuments(ctx context.Context, req *scrapv1.FindDocumentsRequest) (*scrapv1.FindDocumentsResponse, error) {
	if s.documents == nil {
		return nil, status.Error(codes.Unimplemented, "document service is not configured")
	}
	validated, err := ValidateFindDocumentsRequest(req)
	if err != nil {
		return nil, err
	}
	result, err := s.documents.FindDocuments(ctx, validated)
	if err != nil {
		return nil, ToGRPCError(err)
	}
	response := &scrapv1.FindDocumentsResponse{
		Documents: make([]*scrapv1.DocumentMetadata, 0, len(result.Documents)),
	}
	for _, metadata := range result.Documents {
		response.Documents = append(response.Documents, documentMetadataToProto(metadata))
	}
	return response, nil
}

func (s *PublicServer) CompleteTransaction(ctx context.Context, req *scrapv1.CompleteTransactionRequest) (*scrapv1.CompleteTransactionResponse, error) {
	if s.transactions == nil {
		return nil, status.Error(codes.Unimplemented, "transaction service is not configured")
	}
	validated, err := ValidateCompleteTransactionRequest(req)
	if err != nil {
		return nil, err
	}
	state, err := s.transactions.CompleteTransaction(ctx, validated)
	if err != nil {
		return nil, ToGRPCError(err)
	}
	return &scrapv1.CompleteTransactionResponse{Transaction: transactionStateToProto(state)}, nil
}

func (s *PublicServer) GetTransaction(ctx context.Context, req *scrapv1.GetTransactionRequest) (*scrapv1.GetTransactionResponse, error) {
	if s.transactions == nil {
		return nil, status.Error(codes.Unimplemented, "transaction service is not configured")
	}
	validated, err := ValidateGetTransactionRequest(req)
	if err != nil {
		return nil, err
	}
	state, err := s.transactions.GetTransaction(ctx, validated)
	if err != nil {
		return nil, ToGRPCError(err)
	}
	return &scrapv1.GetTransactionResponse{Transaction: transactionStateToProto(state)}, nil
}

type writeChunkReader struct {
	stream grpc.ClientStreamingServer[scrapv1.WriteDocumentRequest, scrapv1.WriteDocumentResponse]
}

func (r *writeChunkReader) Recv() ([]byte, error) {
	msg, err := r.stream.Recv()
	if err != nil {
		return nil, err
	}
	chunk := msg.GetChunk()
	if chunk == nil {
		return nil, invalidArgument("message", reasonInvalidStreamOrder, "write messages after init must be chunks")
	}
	return cloneBytes(chunk.Data), nil
}

type readDocumentSender struct {
	stream grpc.ServerStreamingServer[scrapv1.ReadDocumentResponse]
}

func (s readDocumentSender) SendMetadata(metadata ReadDocumentMetadata) error {
	return s.stream.Send(&scrapv1.ReadDocumentResponse{
		Message: &scrapv1.ReadDocumentResponse_Metadata{
			Metadata: &scrapv1.ReadDocumentMetadata{
				Metadata:      documentMetadataToProto(metadata.Metadata),
				SelectedRange: readRangeToProto(metadata.SelectedRange),
				Source:        metadata.Source,
			},
		},
	})
}

func (s readDocumentSender) SendChunk(data []byte) error {
	return s.stream.Send(&scrapv1.ReadDocumentResponse{
		Message: &scrapv1.ReadDocumentResponse_Chunk{
			Chunk: &scrapv1.ReadDocumentChunk{Data: cloneBytes(data)},
		},
	})
}

func (s *PublicServer) auditSuccessfulWrite(ctx context.Context, init WriteDocumentInit, result WriteDocumentResult) error {
	if s.operations == nil || !isAuditedWrite(init) {
		return nil
	}
	eventType := "document_write_critical"
	if init.PriorityClass != scrapv1.PriorityClass_PRIORITY_CLASS_CRITICAL_INGEST {
		eventType = "document_write_ephemeral"
	}
	actor := workloadIdentityFromContext(ctx)
	if actor == "" {
		actor = init.CreatedByService
	}
	if actor == "" {
		actor = "unknown-workload"
	}
	requestID, correlationID, traceID := publicRequestMetadata(ctx)
	operationID := publicDocumentOperationID(init.Identity, eventType)
	occurredAt := s.now()
	metadata := map[string]string{
		"decision":               "succeeded",
		"request_id":             requestID,
		"correlation_id":         correlationID,
		"document_class":         init.DocumentClass.String(),
		"priority_class":         init.PriorityClass.String(),
		"created_by_service":     init.CreatedByService,
		"idempotent_replay":      strconv.FormatBool(result.IdempotentReplay),
		"desired_replica_count":  strconv.FormatUint(uint64(result.DesiredReplicaCount), 10),
		"achieved_replica_count": strconv.FormatUint(uint64(result.AchievedReplicaCount), 10),
		"length":                 strconv.FormatUint(result.Metadata.Length, 10),
	}
	if traceID != "" {
		metadata["trace_id"] = traceID
	}
	if init.WorkflowStage != "" {
		metadata["workflow_stage"] = init.WorkflowStage
	}
	return s.operations.AppendAuditEvent(&adminv1.AuditEvent{
		EventId:       publicAuditEventID(eventType, operationID, requestID, correlationID, strconv.FormatInt(occurredAt.UnixNano(), 10)),
		EventType:     eventType,
		OperationId:   operationID,
		OperationType: "document-write",
		ActorIdentity: actor,
		OccurredAt:    timestamppb.New(occurredAt),
		Targets:       []*adminv1.Target{documentAuditTarget(init.Identity)},
		Metadata:      compactAuditMetadata(metadata),
	})
}

func isAuditedWrite(init WriteDocumentInit) bool {
	return init.PriorityClass == scrapv1.PriorityClass_PRIORITY_CLASS_CRITICAL_INGEST ||
		init.DocumentClass == scrapv1.DocumentClass_DOCUMENT_CLASS_EPHEMERAL
}

func workloadIdentityFromContext(ctx context.Context) string {
	workload, ok := authz.WorkloadIdentityFromContext(ctx)
	if !ok {
		return ""
	}
	return workload
}

func publicRequestMetadata(ctx context.Context) (string, string, string) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", "", ""
	}
	requestID := firstPublicMetadataValue(md, "x-request-id")
	correlationID := firstPublicMetadataValue(md, "x-correlation-id")
	if correlationID == "" {
		correlationID = requestID
	}
	return requestID, correlationID, firstPublicMetadataValue(md, "traceparent")
}

func firstPublicMetadataValue(md metadata.MD, key string) string {
	values := md.Get(key)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func publicDocumentOperationID(doc identity.Document, eventType string) string {
	return "document-write:" + hashAuditParts(eventType, doc.TenantID, doc.TransactionID, doc.DocumentName)
}

func publicAuditEventID(parts ...string) string {
	return "audit:" + hashAuditParts(parts...)
}

func hashAuditParts(parts ...string) string {
	hasher := sha256.New()
	for _, part := range parts {
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write([]byte(part))
	}
	return hex.EncodeToString(hasher.Sum(nil)[:16])
}

func documentAuditTarget(doc identity.Document) *adminv1.Target {
	return &adminv1.Target{
		Target: &adminv1.Target_Document{
			Document: &adminv1.DocumentTarget{
				TenantId:      doc.TenantID,
				TransactionId: doc.TransactionID,
				DocumentName:  doc.DocumentName,
			},
		},
	}
}

func compactAuditMetadata(metadata map[string]string) map[string]string {
	out := make(map[string]string)
	for key, value := range metadata {
		if value != "" && !publicAuditMetadataKeyIsSensitive(key) {
			out[key] = value
		}
	}
	return out
}

func publicAuditMetadataKeyIsSensitive(key string) bool {
	normalized := strings.ToLower(key)
	for _, marker := range []string{"secret", "token", "password", "credential"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func writeDocumentResultToProto(r WriteDocumentResult) *scrapv1.WriteDocumentResponse {
	return &scrapv1.WriteDocumentResponse{
		Metadata:             documentMetadataToProto(r.Metadata),
		DesiredReplicaCount:  r.DesiredReplicaCount,
		AchievedReplicaCount: r.AchievedReplicaCount,
		RepairRequired:       r.RepairRequired,
		IdempotentReplay:     r.IdempotentReplay,
	}
}

func documentMetadataToProto(m DocumentMetadata) *scrapv1.DocumentMetadata {
	return &scrapv1.DocumentMetadata{
		Identity:                    documentIdentityToProto(m.Identity),
		DocumentClass:               m.DocumentClass,
		PriorityClass:               m.PriorityClass,
		ContentType:                 optionalProtoString(m.ContentType, m.HasContentType),
		Length:                      m.Length,
		LogicalSha256:               cloneBytes(m.LogicalSHA256),
		DocumentIdentityFingerprint: cloneBytes(m.DocumentIdentityFingerprint),
		CreatedByService:            m.CreatedByService,
		WorkflowStage:               optionalProtoString(m.WorkflowStage, m.HasWorkflowStage),
		CreatedAt:                   timestamppb.New(m.CreatedAt),
		FinalizedAt:                 timestamppb.New(m.FinalizedAt),
		Availability:                m.Availability,
		LifecycleState:              m.LifecycleState,
		Tags:                        cloneTags(m.Tags),
	}
}

func readRangeToProto(r ReadRange) *scrapv1.ReadRange {
	return &scrapv1.ReadRange{
		Offset: r.Offset,
		Length: cloneUint64(r.Length),
	}
}

func transactionStateToProto(s TransactionState) *scrapv1.TransactionState {
	return &scrapv1.TransactionState{
		Transaction:            transactionIdentityToProto(s.Transaction),
		State:                  s.State,
		DocumentCount:          s.DocumentCount,
		PermanentDocumentCount: s.PermanentDocumentCount,
		EphemeralDocumentCount: s.EphemeralDocumentCount,
		CreatedAt:              timestamppb.New(s.CreatedAt),
		CompletedAt:            optionalProtoTime(s.CompletedAt),
		TimeoutAt:              optionalProtoTime(s.TimeoutAt),
		Tags:                   cloneTags(s.Tags),
	}
}

func documentIdentityToProto(doc identity.Document) *scrapv1.DocumentIdentity {
	return &scrapv1.DocumentIdentity{
		TenantId:      doc.TenantID,
		TransactionId: doc.TransactionID,
		DocumentName:  doc.DocumentName,
	}
}

func transactionIdentityToProto(transaction identity.Transaction) *scrapv1.TransactionIdentity {
	return &scrapv1.TransactionIdentity{
		TenantId:      transaction.TenantID,
		TransactionId: transaction.TransactionID,
	}
}

func optionalProtoString(value string, present bool) *string {
	if !present {
		return nil
	}
	return &value
}

func optionalProtoTime(value *time.Time) *timestamppb.Timestamp {
	if value == nil {
		return nil
	}
	return timestamppb.New(*value)
}

func invalidArgument(field, reason, description string) error {
	var problems violations
	problems.add(field, reason, description)
	return problems.err()
}
