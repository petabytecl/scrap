package storageapp

import (
	"context"
	"time"

	"github.com/petabytecl/scrap/internal/identity"
)

type DocumentClass uint8

const (
	DocumentClassUnspecified DocumentClass = iota
	DocumentClassPermanent
	DocumentClassEphemeral
)

type PriorityClass uint8

const (
	PriorityClassUnspecified PriorityClass = iota
	PriorityClassCriticalIngest
	PriorityClassNormal
	PriorityClassBulk
)

type DocumentAvailability uint8

const (
	DocumentAvailabilityUnspecified DocumentAvailability = iota
	DocumentAvailabilityHot
	DocumentAvailabilityCold
	DocumentAvailabilityRestorePending
	DocumentAvailabilityCryptoUnavailable
	DocumentAvailabilityDegradedRepair
)

type DocumentLifecycleState uint8

const (
	DocumentLifecycleStateUnspecified DocumentLifecycleState = iota
	DocumentLifecycleStateActive
	DocumentLifecycleStateTransactionCompleted
	DocumentLifecycleStateTombstoned
	DocumentLifecycleStatePendingReclamation
)

type StorageSource uint8

const (
	StorageSourceUnspecified StorageSource = iota
	StorageSourceLocal
	StorageSourceBackend
)

type TransactionStateKind uint8

const (
	TransactionStateUnspecified TransactionStateKind = iota
	TransactionStateOpen
	TransactionStateCompleted
	TransactionStateTimedOut
)

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

type WriteDocumentInit struct {
	Identity             identity.Document
	DocumentClass        DocumentClass
	PriorityClass        PriorityClass
	ContentType          string
	ExpectedLength       *uint64
	ExpectedSHA256       []byte
	ClientIdempotencyKey string
	CreatedByService     string
	WorkflowStage        string
	Tags                 map[string]string
}

type HeadDocumentRequest struct {
	Identity identity.Document
}

type ReadRange struct {
	Offset uint64
	Length *uint64
}

type TimeRange struct {
	StartTime *time.Time
	EndTime   *time.Time
}

type ReadDocumentRequest struct {
	Identity  identity.Document
	Range     *ReadRange
	ChunkSize uint32
}

type FindDocumentsRequest struct {
	Transaction identity.Transaction
	Filter      DocumentFilter
}

type DocumentFilter struct {
	DocumentNameExact     string
	HasDocumentNameExact  bool
	DocumentNamePrefix    string
	HasDocumentNamePrefix bool
	DocumentClass         DocumentClass
	HasDocumentClass      bool
	ContentType           string
	HasContentType        bool
	WorkflowStage         string
	HasWorkflowStage      bool
	CreatedByService      string
	HasCreatedByService   bool
	CreatedAt             *TimeRange
	Tags                  map[string]string
}

type CompleteTransactionRequest struct {
	Transaction        identity.Transaction
	CompletedByService string
	Tags               map[string]string
}

type GetTransactionRequest struct {
	Transaction identity.Transaction
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
	DocumentClass               DocumentClass
	PriorityClass               PriorityClass
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
	Availability                DocumentAvailability
	LifecycleState              DocumentLifecycleState
	Tags                        map[string]string
}

type ReadDocumentMetadata struct {
	Metadata      DocumentMetadata
	SelectedRange ReadRange
	Source        StorageSource
}

type TransactionState struct {
	Transaction            identity.Transaction
	State                  TransactionStateKind
	DocumentCount          uint32
	PermanentDocumentCount uint32
	EphemeralDocumentCount uint32
	CreatedAt              time.Time
	CompletedAt            *time.Time
	TimeoutAt              *time.Time
	Tags                   map[string]string
}

type BlockTarget struct {
	ShardID string
	BlockID string
}

type MemberMutationRequest struct {
	OperationID   string
	StorageMember string
	Reason        string
}
