package metastore

import (
	"fmt"
	"time"

	"github.com/petabytecl/scrap/internal/identity"
)

type (
	DocumentClass        uint16
	PriorityClass        uint16
	Availability         uint16
	LifecycleState       uint16
	TransactionStateKind uint16
	RestoreState         uint16
	UploadState          uint16
)

const (
	DocumentClassPermanent DocumentClass = 1
	DocumentClassEphemeral DocumentClass = 2
)

const (
	PriorityClassCriticalIngest PriorityClass = 1
	PriorityClassNormal         PriorityClass = 2
	PriorityClassBulk           PriorityClass = 3
)

const (
	AvailabilityHot               Availability = 1
	AvailabilityCold              Availability = 2
	AvailabilityRestorePending    Availability = 3
	AvailabilityCryptoUnavailable Availability = 4
	AvailabilityDegradedRepair    Availability = 5
)

const (
	LifecycleStateActive               LifecycleState = 1
	LifecycleStateTransactionCompleted LifecycleState = 2
	LifecycleStateTombstoned           LifecycleState = 3
	LifecycleStatePendingReclamation   LifecycleState = 4
)

const (
	TransactionStateOpen      TransactionStateKind = 1
	TransactionStateCompleted TransactionStateKind = 2
	TransactionStateTimedOut  TransactionStateKind = 3
)

const (
	RestoreStateHot               RestoreState = 1
	RestoreStateCold              RestoreState = 2
	RestoreStateRestorePending    RestoreState = 3
	RestoreStateCryptoUnavailable RestoreState = 4
)

const (
	UploadStateNotRequired UploadState = 1
	UploadStatePending     UploadState = 2
	UploadStateUploaded    UploadState = 3
	UploadStateFailed      UploadState = 4
)

type Document struct {
	Identity                    identity.Document
	DocumentClass               DocumentClass
	PriorityClass               PriorityClass
	ContentType                 string
	HasContentType              bool
	Length                      uint64
	LogicalSHA256               [32]byte
	StoredSHA256                [32]byte
	DocumentIdentityFingerprint [16]byte
	CreatedByService            string
	WorkflowStage               string
	HasWorkflowStage            bool
	CreatedAt                   time.Time
	FinalizedAt                 time.Time
	Availability                Availability
	LifecycleState              LifecycleState
	RestoreState                RestoreState
	UploadState                 UploadState
	TombstonedAt                *time.Time
	TombstoneOperationID        string
	HasTombstoneOperationID     bool
	Tags                        map[string]string
	Location                    Location
	ClientIdempotencyKey        string
	HasClientIdempotencyKey     bool
}

func (d Document) IsHot() bool {
	return d.RestoreState == RestoreStateHot && d.Availability == AvailabilityHot
}

func (d Document) RequiresBackendRestore() bool {
	return d.Availability == AvailabilityCold || d.Availability == AvailabilityRestorePending
}

func (d Document) IsRestoreQueued() bool {
	return d.RestoreState == RestoreStateRestorePending
}

func (d Document) IsCryptoUnavailable() bool {
	return d.Availability == AvailabilityCryptoUnavailable
}

type Location struct {
	BlockID       string
	StoredOffset  uint64
	StoredLength  uint64
	LogicalSHA256 [32]byte
	StoredSHA256  [32]byte
	Frames        []FrameRecord
	Replicas      []ReplicaRef
}

type FrameRecord struct {
	FrameOffset   uint64
	SegmentOffset uint64
	SegmentLength uint64
	SHA256        [32]byte
}

type ReplicaRef struct {
	MemberID     string
	BlockID      string
	StoredOffset uint64
	StoredLength uint64
	StoredSHA256 [32]byte
}

type Transaction struct {
	Identity               identity.Transaction
	State                  TransactionStateKind
	DocumentCount          uint32
	PermanentDocumentCount uint32
	EphemeralDocumentCount uint32
	CreatedAt              time.Time
	CompletedAt            *time.Time
	TimeoutAt              *time.Time
	Tags                   map[string]string
}

func (t Transaction) IsOpen() bool {
	return t.State == TransactionStateOpen
}

func (t Transaction) IsCompleted() bool {
	return t.State == TransactionStateCompleted
}

func (t Transaction) AppendDocument(class DocumentClass) (Transaction, error) {
	if !t.IsOpen() {
		return t, closedTransactionError(t)
	}
	updated := t
	updated.DocumentCount++
	switch class {
	case DocumentClassPermanent:
		updated.PermanentDocumentCount++
	case DocumentClassEphemeral:
		updated.EphemeralDocumentCount++
	}
	return updated, nil
}

func (t Transaction) Complete(completedAt time.Time, tags map[string]string) (Transaction, bool, error) {
	if t.IsCompleted() {
		return t, false, nil
	}
	if !t.IsOpen() {
		return t, false, closedTransactionError(t)
	}
	updated := t
	updated.State = TransactionStateCompleted
	updated.CompletedAt = &completedAt
	updated.Tags = cloneTags(tags)
	return updated, true, nil
}

func closedTransactionError(transaction Transaction) error {
	return fmt.Errorf("%w: transaction %s/%s is not open", ErrTransactionClosed, transaction.Identity.TenantID, transaction.Identity.TransactionID)
}

type UploadIntent struct {
	BlockID           string
	BackendObjectKey  string
	IndexObjectKey    string
	EnvelopeObjectKey string
	State             UploadState
	LastError         string
	HasLastError      bool
	UpdatedAt         time.Time
}

type RepairState struct {
	Identity    identity.Document
	PhysicalRef string
	IncidentID  string
	Quarantined bool
	UpdatedAt   time.Time
}

type CommandReceipt struct {
	CommandID     string
	CommandSHA256 [32]byte
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
	CreatedAfter          *time.Time
	CreatedBefore         *time.Time
	Tags                  map[string]string
}
