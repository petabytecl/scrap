package api

import (
	"context"
	"errors"
	"io"
	"time"

	scrapv1 "github.com/petabytecl/scrap/internal/gen/scrap/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const reasonInvalidStreamOrder = "SCRAP_INVALID_STREAM_ORDER"

type DocumentApplication interface {
	WriteDocument(context.Context, WriteDocumentInit, ChunkReader) (WriteDocumentResult, error)
	HeadDocument(context.Context, HeadDocumentRequest) (DocumentMetadata, error)
	ReadDocument(context.Context, ReadDocumentRequest, ReadDocumentSender) error
	FindDocuments(context.Context, FindDocumentsRequest) (FindDocumentsResult, error)
}

type TransactionApplication interface {
	CompleteTransaction(context.Context, CompleteTransactionRequest) (TransactionState, error)
	GetTransaction(context.Context, GetTransactionRequest) (TransactionState, error)
}

type ChunkReader interface {
	Recv() ([]byte, error)
}

type ReadDocumentSender interface {
	SendMetadata(ReadDocumentMetadata) error
	SendChunk([]byte) error
}

type PublicServer struct {
	documents    DocumentApplication
	transactions TransactionApplication
}

type WriteDocumentResult struct {
	Metadata             DocumentMetadata
	DesiredReplicaCount  uint32
	AchievedReplicaCount uint32
	RepairRequired       bool
	IdempotentReplay     bool
}

type FindDocumentsResult struct {
	Documents []DocumentMetadata
}

type DocumentMetadata struct {
	Identity                    identity.Document
	DocumentClass               scrapv1.DocumentClass
	PriorityClass               scrapv1.PriorityClass
	ContentType                 string
	HasContentType              bool
	Length                      uint64
	LogicalSHA256               []byte
	DocumentIdentityFingerprint []byte
	CreatedByService            string
	WorkflowStage               string
	HasWorkflowStage            bool
	CreatedAt                   time.Time
	FinalizedAt                 time.Time
	Availability                scrapv1.DocumentAvailability
	LifecycleState              scrapv1.DocumentLifecycleState
	Tags                        map[string]string
}

type ReadDocumentMetadata struct {
	Metadata      DocumentMetadata
	SelectedRange ReadRange
	Source        scrapv1.StorageSource
}

type TransactionState struct {
	Transaction            identity.Transaction
	State                  scrapv1.TransactionStateKind
	DocumentCount          uint32
	PermanentDocumentCount uint32
	EphemeralDocumentCount uint32
	CreatedAt              time.Time
	CompletedAt            *time.Time
	TimeoutAt              *time.Time
	Tags                   map[string]string
}

func NewPublicServer(documents DocumentApplication, transactions TransactionApplication) *PublicServer {
	return &PublicServer{
		documents:    documents,
		transactions: transactions,
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
		return err
	}
	return stream.SendAndClose(result.toProto())
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
		return nil, err
	}
	return &scrapv1.HeadDocumentResponse{Metadata: metadata.toProto()}, nil
}

func (s *PublicServer) ReadDocument(req *scrapv1.ReadDocumentRequest, stream grpc.ServerStreamingServer[scrapv1.ReadDocumentResponse]) error {
	if s.documents == nil {
		return status.Error(codes.Unimplemented, "document service is not configured")
	}
	validated, err := ValidateReadDocumentRequest(req)
	if err != nil {
		return err
	}
	return s.documents.ReadDocument(stream.Context(), validated, readDocumentSender{stream: stream})
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
		return nil, err
	}
	response := &scrapv1.FindDocumentsResponse{
		Documents: make([]*scrapv1.DocumentMetadata, 0, len(result.Documents)),
	}
	for _, metadata := range result.Documents {
		response.Documents = append(response.Documents, metadata.toProto())
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
		return nil, err
	}
	return &scrapv1.CompleteTransactionResponse{Transaction: state.toProto()}, nil
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
		return nil, err
	}
	return &scrapv1.GetTransactionResponse{Transaction: state.toProto()}, nil
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
				Metadata:      metadata.Metadata.toProto(),
				SelectedRange: metadata.SelectedRange.toProto(),
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

func (r WriteDocumentResult) toProto() *scrapv1.WriteDocumentResponse {
	return &scrapv1.WriteDocumentResponse{
		Metadata:             r.Metadata.toProto(),
		DesiredReplicaCount:  r.DesiredReplicaCount,
		AchievedReplicaCount: r.AchievedReplicaCount,
		RepairRequired:       r.RepairRequired,
		IdempotentReplay:     r.IdempotentReplay,
	}
}

func (m DocumentMetadata) toProto() *scrapv1.DocumentMetadata {
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

func (r ReadRange) toProto() *scrapv1.ReadRange {
	return &scrapv1.ReadRange{
		Offset: r.Offset,
		Length: cloneUint64(r.Length),
	}
}

func (s TransactionState) toProto() *scrapv1.TransactionState {
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

func invalidArgument(field string, reason string, description string) error {
	var problems violations
	problems.add(field, reason, description)
	return problems.err()
}
