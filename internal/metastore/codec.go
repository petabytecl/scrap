package metastore

import (
	"crypto/sha256"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/petabytecl/scrap/internal/blockstore"
	metastorev1 "github.com/petabytecl/scrap/internal/gen/scrap/metastore/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/safeconv"
)

var protoMarshal = proto.MarshalOptions{Deterministic: true}

const CurrentSchemaVersion uint32 = 1

func marshalDocument(document Document) ([]byte, error) {
	return marshalDocumentRecord(documentToProto(document))
}

func MarshalDocument(document Document) ([]byte, error) {
	document = normalizeDocumentDefaults(document)
	return marshalDocument(document)
}

func DocumentRecord(document Document) *metastorev1.DocumentRecord {
	return documentToProto(normalizeDocumentDefaults(document))
}

func TransactionRecord(transaction Transaction) *metastorev1.TransactionRecord {
	return transactionToProto(transaction)
}

func UploadIntentRecord(intent UploadIntent) *metastorev1.UploadIntentRecord {
	return uploadIntentToProto(intent)
}

func RepairStateRecord(state RepairState) *metastorev1.RepairStateRecord {
	return repairStateToProto(state)
}

func CommandReceiptRecord(receipt CommandReceipt) *metastorev1.CommandReceiptRecord {
	return commandReceiptToProto(receipt)
}

func unmarshalDocument(data []byte) (Document, error) {
	record, err := unmarshalDocumentRecord(data)
	if err != nil {
		return Document{}, err
	}
	return documentFromProto(record), nil
}

func UnmarshalDocument(data []byte) (Document, error) {
	return unmarshalDocument(data)
}

func marshalDocumentRecord(record *metastorev1.DocumentRecord) ([]byte, error) {
	if err := validateDocumentRecord(record); err != nil {
		return nil, err
	}
	return protoMarshal.Marshal(record)
}

func unmarshalDocumentRecord(data []byte) (*metastorev1.DocumentRecord, error) {
	var record metastorev1.DocumentRecord
	if err := proto.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	if err := validateDocumentRecord(&record); err != nil {
		return nil, err
	}
	return &record, nil
}

func marshalTransaction(transaction Transaction) ([]byte, error) {
	return marshalTransactionRecord(transactionToProto(transaction))
}

func unmarshalTransaction(data []byte) (Transaction, error) {
	record, err := unmarshalTransactionRecord(data)
	if err != nil {
		return Transaction{}, err
	}
	return transactionFromProto(record), nil
}

func marshalTransactionRecord(record *metastorev1.TransactionRecord) ([]byte, error) {
	if err := validateSchemaVersion("transaction", record.GetSchemaVersion()); err != nil {
		return nil, err
	}
	if err := validateTransactionState(record.GetState()); err != nil {
		return nil, err
	}
	return protoMarshal.Marshal(record)
}

func unmarshalTransactionRecord(data []byte) (*metastorev1.TransactionRecord, error) {
	var record metastorev1.TransactionRecord
	if err := proto.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	if err := validateSchemaVersion("transaction", record.GetSchemaVersion()); err != nil {
		return nil, err
	}
	if err := validateTransactionState(record.GetState()); err != nil {
		return nil, err
	}
	return &record, nil
}

func marshalUploadIntent(intent UploadIntent) ([]byte, error) {
	return marshalUploadIntentRecord(uploadIntentToProto(intent))
}

func unmarshalUploadIntent(data []byte) (UploadIntent, error) {
	record, err := unmarshalUploadIntentRecord(data)
	if err != nil {
		return UploadIntent{}, err
	}
	return uploadIntentFromProto(record), nil
}

func marshalUploadIntentRecord(record *metastorev1.UploadIntentRecord) ([]byte, error) {
	if err := validateSchemaVersion("upload intent", record.GetSchemaVersion()); err != nil {
		return nil, err
	}
	if err := validateInt32EnumUint16("upload intent", "state", int32(record.GetState())); err != nil {
		return nil, err
	}
	return protoMarshal.Marshal(record)
}

func unmarshalUploadIntentRecord(data []byte) (*metastorev1.UploadIntentRecord, error) {
	var record metastorev1.UploadIntentRecord
	if err := proto.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	if err := validateSchemaVersion("upload intent", record.GetSchemaVersion()); err != nil {
		return nil, err
	}
	if err := validateInt32EnumUint16("upload intent", "state", int32(record.GetState())); err != nil {
		return nil, err
	}
	return &record, nil
}

func marshalRepairState(state RepairState) ([]byte, error) {
	return marshalRepairStateRecord(repairStateToProto(state))
}

func unmarshalRepairState(data []byte) (RepairState, error) {
	record, err := unmarshalRepairStateRecord(data)
	if err != nil {
		return RepairState{}, err
	}
	return repairStateFromProto(record), nil
}

func marshalCommandReceipt(receipt CommandReceipt) ([]byte, error) {
	return marshalCommandReceiptRecord(commandReceiptToProto(receipt))
}

func unmarshalCommandReceipt(data []byte) (CommandReceipt, error) {
	record, err := unmarshalCommandReceiptRecord(data)
	if err != nil {
		return CommandReceipt{}, err
	}
	return commandReceiptFromProto(record), nil
}

func marshalRepairStateRecord(record *metastorev1.RepairStateRecord) ([]byte, error) {
	if err := validateSchemaVersion("repair state", record.GetSchemaVersion()); err != nil {
		return nil, err
	}
	return protoMarshal.Marshal(record)
}

func unmarshalRepairStateRecord(data []byte) (*metastorev1.RepairStateRecord, error) {
	var record metastorev1.RepairStateRecord
	if err := proto.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	if err := validateSchemaVersion("repair state", record.GetSchemaVersion()); err != nil {
		return nil, err
	}
	return &record, nil
}

func marshalCommandReceiptRecord(record *metastorev1.CommandReceiptRecord) ([]byte, error) {
	if err := validateCommandReceiptRecord(record); err != nil {
		return nil, err
	}
	return protoMarshal.Marshal(record)
}

func unmarshalCommandReceiptRecord(data []byte) (*metastorev1.CommandReceiptRecord, error) {
	var record metastorev1.CommandReceiptRecord
	if err := proto.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	if err := validateCommandReceiptRecord(&record); err != nil {
		return nil, err
	}
	return &record, nil
}

func validateCommandReceiptRecord(record *metastorev1.CommandReceiptRecord) error {
	if record == nil {
		return invalidRecord("command receipt", "record is required")
	}
	if err := validateSchemaVersion("command receipt", record.GetSchemaVersion()); err != nil {
		return err
	}
	if record.GetCommandId() == "" {
		return invalidRecord("command receipt", "command_id is required")
	}
	if len(record.GetCommandSha256()) != sha256.Size {
		return invalidRecord("command receipt", "command_sha256 must be 32 bytes")
	}
	return nil
}

func validateSchemaVersion(recordKind string, version uint32) error {
	if version != CurrentSchemaVersion {
		return fmt.Errorf("%w: %s record version %d", ErrUnsupportedSchemaVersion, recordKind, version)
	}
	return nil
}

func validateUint32EnumUint16(recordKind, field string, value uint32) error {
	if _, err := safeconv.Uint32ToUint16(field, value); err != nil {
		return invalidRecord(recordKind, "%s is out of range: %v", field, err)
	}
	return nil
}

func validateInt32EnumUint16(recordKind, field string, value int32) error {
	if _, err := safeconv.Int32ToUint16(field, value); err != nil {
		return invalidRecord(recordKind, "%s is out of range: %v", field, err)
	}
	return nil
}

func validateTransactionState(value uint32) error {
	if value == 0 {
		return invalidRecord("transaction", "state is required")
	}
	return validateUint32EnumUint16("transaction", "state", value)
}

func validateDocumentRecord(record *metastorev1.DocumentRecord) error {
	if record == nil {
		return invalidRecord("document", "record is required")
	}
	if err := validateSchemaVersion("document", record.GetSchemaVersion()); err != nil {
		return err
	}
	if err := validateDocumentRequiredFields(record); err != nil {
		return err
	}
	if err := validateDocumentEnumFields(record); err != nil {
		return err
	}
	if err := validateDocumentTimestamps(record); err != nil {
		return err
	}
	if err := validateLocation(record.GetLocation()); err != nil {
		return err
	}
	if err := validateEnvelopeRef(record.GetEnvelopeRef()); err != nil {
		return err
	}
	return nil
}

func validateDocumentRequiredFields(record *metastorev1.DocumentRecord) error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "tenant_id", value: record.GetTenantId()},
		{name: "transaction_id", value: record.GetTransactionId()},
		{name: "document_name", value: record.GetDocumentName()},
		{name: "created_by_service", value: record.GetCreatedByService()},
	} {
		if field.value == "" {
			return invalidRecord("document", "%s is required", field.name)
		}
	}
	for _, field := range []struct {
		name string
		data []byte
		size int
	}{
		{name: "logical_sha256", data: record.GetLogicalSha256(), size: 32},
		{name: "stored_sha256", data: record.GetStoredSha256(), size: 32},
		{name: "document_identity_fingerprint", data: record.GetDocumentIdentityFingerprint(), size: 16},
	} {
		if len(field.data) != field.size {
			return invalidRecord("document", "%s must be %d bytes", field.name, field.size)
		}
	}
	return nil
}

func validateDocumentEnumFields(record *metastorev1.DocumentRecord) error {
	for _, field := range []struct {
		name  string
		value uint32
	}{
		{name: "document_class", value: record.GetDocumentClass()},
		{name: "priority_class", value: record.GetPriorityClass()},
		{name: "availability", value: record.GetAvailability()},
		{name: "lifecycle_state", value: record.GetLifecycleState()},
	} {
		if field.value == 0 {
			return invalidRecord("document", "%s is required", field.name)
		}
		if err := validateUint32EnumUint16("document", field.name, field.value); err != nil {
			return err
		}
	}
	return validateDocumentStateEnums(record)
}

func validateDocumentStateEnums(record *metastorev1.DocumentRecord) error {
	if record.GetRestoreState() == metastorev1.RestoreState_RESTORE_STATE_UNSPECIFIED {
		return invalidRecord("document", "restore_state is required")
	}
	if err := validateInt32EnumUint16("document", "restore_state", int32(record.GetRestoreState())); err != nil {
		return err
	}
	if record.GetUploadState() == metastorev1.UploadState_UPLOAD_STATE_UNSPECIFIED {
		return invalidRecord("document", "upload_state is required")
	}
	return validateInt32EnumUint16("document", "upload_state", int32(record.GetUploadState()))
}

func validateDocumentTimestamps(record *metastorev1.DocumentRecord) error {
	if err := validateTimestamp("document", "created_at", record.GetCreatedAt()); err != nil {
		return err
	}
	return validateTimestamp("document", "finalized_at", record.GetFinalizedAt())
}

func validateLocation(location *metastorev1.Location) error {
	if location == nil {
		return invalidRecord("document.location", "location is required")
	}
	if location.GetBlockId() == "" {
		return invalidRecord("document.location", "block_id is required")
	}
	if location.GetStoredLength() > 0 && len(location.GetFrames()) == 0 {
		return invalidRecord("document.location", "frames are required for stored bytes")
	}
	if err := validateLocationFrames(location.GetFrames()); err != nil {
		return err
	}
	return validateLocationReplicas(location.GetReplicas())
}

func validateLocationFrames(frames []*metastorev1.FrameRecord) error {
	for i, frame := range frames {
		if frame.GetSegmentLength() == 0 {
			return invalidRecord("document.location", "frame %d segment_length is required", i)
		}
		if len(frame.GetSha256()) != sha256.Size {
			return invalidRecord("document.location", "frame %d sha256 must be 32 bytes", i)
		}
	}
	return nil
}

func validateLocationReplicas(replicas []*metastorev1.ReplicaRef) error {
	for i, replica := range replicas {
		if replica.GetMemberId() == "" {
			return invalidRecord("document.location", "replica %d member_id is required", i)
		}
		if replica.GetBlockId() == "" {
			return invalidRecord("document.location", "replica %d block_id is required", i)
		}
		if len(replica.GetStoredSha256()) != sha256.Size {
			return invalidRecord("document.location", "replica %d stored_sha256 must be 32 bytes", i)
		}
	}
	return nil
}

func validateEnvelopeRef(ref *metastorev1.EnvelopeRef) error {
	if ref == nil {
		return nil
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "envelope_id", value: ref.GetEnvelopeId()},
		{name: "key_id", value: ref.GetKeyId()},
	} {
		if field.value == "" {
			return invalidRecord("document.envelope_ref", "%s is required", field.name)
		}
	}
	if ref.GetKeyVersion() == 0 {
		return invalidRecord("document.envelope_ref", "key_version is required")
	}
	if len(ref.GetEnvelopeSha256()) != sha256.Size {
		return invalidRecord("document.envelope_ref", "envelope_sha256 must be 32 bytes")
	}
	return nil
}

func validateTimestamp(recordKind, field string, value *timestamppb.Timestamp) error {
	if value == nil {
		return invalidRecord(recordKind, "%s is required", field)
	}
	if err := value.CheckValid(); err != nil {
		return fmt.Errorf("%w: %s %s is invalid: %w", ErrInvalidRecord, recordKind, field, err)
	}
	if value.AsTime().IsZero() {
		return invalidRecord(recordKind, "%s must be non-zero", field)
	}
	return nil
}

func invalidRecord(recordKind, format string, args ...any) error {
	return fmt.Errorf("%w: %s %s", ErrInvalidRecord, recordKind, fmt.Sprintf(format, args...))
}

func normalizeDocumentDefaults(document Document) Document {
	if document.CreatedAt.IsZero() {
		document.CreatedAt = document.FinalizedAt
	}
	if document.FinalizedAt.IsZero() {
		document.FinalizedAt = document.CreatedAt
	}
	if document.Availability == 0 {
		document.Availability = AvailabilityHot
	}
	if document.LifecycleState == 0 {
		document.LifecycleState = LifecycleStateActive
	}
	if document.RestoreState == 0 {
		document.RestoreState = RestoreStateHot
	}
	if document.UploadState == 0 {
		document.UploadState = UploadStatePending
	}
	return document
}

func documentToProto(document Document) *metastorev1.DocumentRecord {
	return &metastorev1.DocumentRecord{
		SchemaVersion:               CurrentSchemaVersion,
		TenantId:                    document.Identity.TenantID,
		TransactionId:               document.Identity.TransactionID,
		DocumentName:                document.Identity.DocumentName,
		DocumentClass:               uint32(document.DocumentClass),
		PriorityClass:               uint32(document.PriorityClass),
		ContentType:                 optionalStringPointer(document.ContentType, document.HasContentType),
		Length:                      document.Length,
		LogicalSha256:               document.LogicalSHA256[:],
		StoredSha256:                document.StoredSHA256[:],
		DocumentIdentityFingerprint: document.DocumentIdentityFingerprint[:],
		CreatedByService:            document.CreatedByService,
		WorkflowStage:               optionalStringPointer(document.WorkflowStage, document.HasWorkflowStage),
		CreatedAt:                   timestamppb.New(document.CreatedAt),
		FinalizedAt:                 timestamppb.New(document.FinalizedAt),
		Availability:                uint32(document.Availability),
		LifecycleState:              uint32(document.LifecycleState),
		RestoreState:                metastorev1.RestoreState(document.RestoreState),
		UploadState:                 metastorev1.UploadState(document.UploadState),
		TombstonedAt:                optionalTime(document.TombstonedAt),
		TombstoneOperationId:        optionalStringPointer(document.TombstoneOperationID, document.HasTombstoneOperationID),
		Tags:                        cloneTags(document.Tags),
		Location:                    locationToProto(document.Location),
		ClientIdempotencyKey:        optionalStringPointer(document.ClientIdempotencyKey, document.HasClientIdempotencyKey),
	}
}

func documentFromProto(record *metastorev1.DocumentRecord) Document {
	document := Document{
		Identity: identity.Document{
			TenantID:      record.GetTenantId(),
			TransactionID: record.GetTransactionId(),
			DocumentName:  record.GetDocumentName(),
		},
		DocumentClass:           documentClassFromProto(record.GetDocumentClass()),
		PriorityClass:           priorityClassFromProto(record.GetPriorityClass()),
		ContentType:             record.GetContentType(),
		HasContentType:          record.ContentType != nil,
		Length:                  record.GetLength(),
		CreatedByService:        record.GetCreatedByService(),
		WorkflowStage:           record.GetWorkflowStage(),
		HasWorkflowStage:        record.WorkflowStage != nil,
		Availability:            availabilityFromProto(record.GetAvailability()),
		LifecycleState:          lifecycleStateFromProto(record.GetLifecycleState()),
		RestoreState:            restoreStateFromProto(record.GetRestoreState()),
		UploadState:             uploadStateEnumFromProto(record.GetUploadState()),
		TombstoneOperationID:    record.GetTombstoneOperationId(),
		HasTombstoneOperationID: record.TombstoneOperationId != nil,
		Tags:                    cloneTags(record.GetTags()),
		Location:                locationFromProto(record.GetLocation()),
		ClientIdempotencyKey:    record.GetClientIdempotencyKey(),
		HasClientIdempotencyKey: record.ClientIdempotencyKey != nil,
	}
	copy(document.LogicalSHA256[:], record.GetLogicalSha256())
	copy(document.StoredSHA256[:], record.GetStoredSha256())
	copy(document.DocumentIdentityFingerprint[:], record.GetDocumentIdentityFingerprint())
	document.Location.LogicalSHA256 = document.LogicalSHA256
	if record.GetCreatedAt() != nil {
		document.CreatedAt = record.GetCreatedAt().AsTime()
	}
	if record.GetFinalizedAt() != nil {
		document.FinalizedAt = record.GetFinalizedAt().AsTime()
	}
	if record.GetTombstonedAt() != nil {
		tombstonedAt := record.GetTombstonedAt().AsTime()
		document.TombstonedAt = &tombstonedAt
	}
	return document
}

func documentClassFromProto(value uint32) DocumentClass {
	converted, err := safeconv.Uint32ToUint16("document_class", value)
	if err != nil {
		return 0
	}
	return DocumentClass(converted)
}

func priorityClassFromProto(value uint32) PriorityClass {
	converted, err := safeconv.Uint32ToUint16("priority_class", value)
	if err != nil {
		return 0
	}
	return PriorityClass(converted)
}

func availabilityFromProto(value uint32) Availability {
	converted, err := safeconv.Uint32ToUint16("availability", value)
	if err != nil {
		return 0
	}
	return Availability(converted)
}

func lifecycleStateFromProto(value uint32) LifecycleState {
	converted, err := safeconv.Uint32ToUint16("lifecycle_state", value)
	if err != nil {
		return 0
	}
	return LifecycleState(converted)
}

func restoreStateFromProto(value metastorev1.RestoreState) RestoreState {
	converted, err := safeconv.Int32ToUint16("restore_state", int32(value))
	if err != nil {
		return 0
	}
	return RestoreState(converted)
}

func uploadStateEnumFromProto(value metastorev1.UploadState) UploadState {
	converted, err := safeconv.Int32ToUint16("upload_state", int32(value))
	if err != nil {
		return 0
	}
	return UploadState(converted)
}

func transactionStateFromProto(value uint32) TransactionStateKind {
	converted, err := safeconv.Uint32ToUint16("transaction_state", value)
	if err != nil {
		return 0
	}
	return TransactionStateKind(converted)
}

func transactionToProto(transaction Transaction) *metastorev1.TransactionRecord {
	return &metastorev1.TransactionRecord{
		SchemaVersion:          CurrentSchemaVersion,
		TenantId:               transaction.Identity.TenantID,
		TransactionId:          transaction.Identity.TransactionID,
		State:                  uint32(transaction.State),
		DocumentCount:          transaction.DocumentCount,
		PermanentDocumentCount: transaction.PermanentDocumentCount,
		EphemeralDocumentCount: transaction.EphemeralDocumentCount,
		CreatedAt:              timestamppb.New(transaction.CreatedAt),
		CompletedAt:            optionalTime(transaction.CompletedAt),
		TimeoutAt:              optionalTime(transaction.TimeoutAt),
		Tags:                   cloneTags(transaction.Tags),
	}
}

func transactionFromProto(record *metastorev1.TransactionRecord) Transaction {
	transaction := Transaction{
		Identity: identity.Transaction{
			TenantID:      record.GetTenantId(),
			TransactionID: record.GetTransactionId(),
		},
		State:                  transactionStateFromProto(record.GetState()),
		DocumentCount:          record.GetDocumentCount(),
		PermanentDocumentCount: record.GetPermanentDocumentCount(),
		EphemeralDocumentCount: record.GetEphemeralDocumentCount(),
		Tags:                   cloneTags(record.GetTags()),
	}
	if record.GetCreatedAt() != nil {
		transaction.CreatedAt = record.GetCreatedAt().AsTime()
	}
	if record.GetCompletedAt() != nil {
		completedAt := record.GetCompletedAt().AsTime()
		transaction.CompletedAt = &completedAt
	}
	if record.GetTimeoutAt() != nil {
		timeoutAt := record.GetTimeoutAt().AsTime()
		transaction.TimeoutAt = &timeoutAt
	}
	return transaction
}

func uploadIntentToProto(intent UploadIntent) *metastorev1.UploadIntentRecord {
	return &metastorev1.UploadIntentRecord{
		SchemaVersion:     CurrentSchemaVersion,
		BlockId:           intent.BlockID,
		BackendObjectKey:  intent.BackendObjectKey,
		IndexObjectKey:    intent.IndexObjectKey,
		EnvelopeObjectKey: intent.EnvelopeObjectKey,
		State:             metastorev1.UploadState(intent.State),
		LastError:         optionalStringPointer(intent.LastError, intent.HasLastError),
		UpdatedAt:         timestamppb.New(intent.UpdatedAt),
	}
}

func uploadIntentFromProto(record *metastorev1.UploadIntentRecord) UploadIntent {
	intent := UploadIntent{
		BlockID:           record.GetBlockId(),
		BackendObjectKey:  record.GetBackendObjectKey(),
		IndexObjectKey:    record.GetIndexObjectKey(),
		EnvelopeObjectKey: record.GetEnvelopeObjectKey(),
		State:             uploadStateEnumFromProto(record.GetState()),
		LastError:         record.GetLastError(),
		HasLastError:      record.LastError != nil,
	}
	if record.GetUpdatedAt() != nil {
		intent.UpdatedAt = record.GetUpdatedAt().AsTime()
	}
	return intent
}

func repairStateToProto(state RepairState) *metastorev1.RepairStateRecord {
	return &metastorev1.RepairStateRecord{
		SchemaVersion: CurrentSchemaVersion,
		TenantId:      state.Identity.TenantID,
		TransactionId: state.Identity.TransactionID,
		DocumentName:  state.Identity.DocumentName,
		PhysicalRef:   state.PhysicalRef,
		IncidentId:    state.IncidentID,
		Quarantined:   state.Quarantined,
		UpdatedAt:     timestamppb.New(state.UpdatedAt),
	}
}

func repairStateFromProto(record *metastorev1.RepairStateRecord) RepairState {
	state := RepairState{
		Identity: identity.Document{
			TenantID:      record.GetTenantId(),
			TransactionID: record.GetTransactionId(),
			DocumentName:  record.GetDocumentName(),
		},
		PhysicalRef: record.GetPhysicalRef(),
		IncidentID:  record.GetIncidentId(),
		Quarantined: record.GetQuarantined(),
	}
	if record.GetUpdatedAt() != nil {
		state.UpdatedAt = record.GetUpdatedAt().AsTime()
	}
	return state
}

func commandReceiptToProto(receipt CommandReceipt) *metastorev1.CommandReceiptRecord {
	return &metastorev1.CommandReceiptRecord{
		SchemaVersion: CurrentSchemaVersion,
		CommandId:     receipt.CommandID,
		CommandSha256: receipt.CommandSHA256[:],
	}
}

func commandReceiptFromProto(record *metastorev1.CommandReceiptRecord) CommandReceipt {
	receipt := CommandReceipt{
		CommandID: record.GetCommandId(),
	}
	copy(receipt.CommandSHA256[:], record.GetCommandSha256())
	return receipt
}

func locationToProto(location blockstore.Record) *metastorev1.Location {
	out := &metastorev1.Location{
		BlockId:      location.BlockID,
		StoredOffset: location.StoredOffset,
		StoredLength: location.StoredLength,
		Frames:       make([]*metastorev1.FrameRecord, 0, len(location.Frames)),
		Replicas:     make([]*metastorev1.ReplicaRef, 0, len(location.Replicas)),
	}
	for _, frame := range location.Frames {
		out.Frames = append(out.Frames, &metastorev1.FrameRecord{
			FrameOffset:   frame.FrameOffset,
			SegmentOffset: frame.SegmentOffset,
			SegmentLength: frame.SegmentLength,
			Sha256:        append([]byte(nil), frame.SHA256[:]...),
		})
	}
	for _, replica := range location.Replicas {
		out.Replicas = append(out.Replicas, &metastorev1.ReplicaRef{
			MemberId:     replica.MemberID,
			BlockId:      replica.BlockID,
			StoredOffset: replica.StoredOffset,
			StoredLength: replica.StoredLength,
			StoredSha256: append([]byte(nil), replica.StoredSHA256[:]...),
		})
	}
	return out
}

func locationFromProto(location *metastorev1.Location) blockstore.Record {
	if location == nil {
		return blockstore.Record{}
	}
	out := blockstore.Record{
		BlockID:       location.GetBlockId(),
		StoredOffset:  location.GetStoredOffset(),
		StoredLength:  location.GetStoredLength(),
		Frames:        make([]blockstore.FrameRecord, 0, len(location.GetFrames())),
		Replicas:      make([]blockstore.ReplicaRef, 0, len(location.GetReplicas())),
		LogicalSHA256: [32]byte{},
	}
	for _, frame := range location.GetFrames() {
		outFrame := blockstore.FrameRecord{
			FrameOffset:   frame.GetFrameOffset(),
			SegmentOffset: frame.GetSegmentOffset(),
			SegmentLength: frame.GetSegmentLength(),
		}
		copy(outFrame.SHA256[:], frame.GetSha256())
		out.Frames = append(out.Frames, outFrame)
	}
	for _, replica := range location.GetReplicas() {
		outReplica := blockstore.ReplicaRef{
			MemberID:     replica.GetMemberId(),
			BlockID:      replica.GetBlockId(),
			StoredOffset: replica.GetStoredOffset(),
			StoredLength: replica.GetStoredLength(),
		}
		copy(outReplica.StoredSHA256[:], replica.GetStoredSha256())
		out.Replicas = append(out.Replicas, outReplica)
	}
	return out
}

func optionalStringPointer(value string, present bool) *string {
	if !present {
		return nil
	}
	return &value
}

func optionalTime(value *time.Time) *timestamppb.Timestamp {
	if value == nil {
		return nil
	}
	return timestamppb.New(*value)
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
