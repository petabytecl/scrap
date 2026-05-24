package published

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"google.golang.org/protobuf/proto"

	"github.com/petabytecl/scrap/internal/blockstore"
	publishedv1 "github.com/petabytecl/scrap/internal/gen/scrap/published/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/safeconv"
)

var (
	ErrSourceMismatch           = errors.New("published metadata: source ownership mismatch")
	ErrInvalidArtifact          = errors.New("published metadata: invalid artifact")
	ErrUnsupportedFormatVersion = errors.New("published metadata: unsupported format version")
)

type ImportOptions struct {
	SourceNamespace      string
	ShardID              string
	HighWatermark        uint64
	RequireHighWatermark bool
	FirstLogIndex        uint64
	LastLogIndex         uint64
	RequireLogRange      bool
}

type SnapshotContents struct {
	SourceNamespace string
	ShardID         string
	HighWatermark   uint64
	Documents       []metastore.Document
	Transactions    []metastore.Transaction
	UploadIntents   []metastore.UploadIntent
	Tombstones      int
}

type TailContents struct {
	SourceNamespace    string
	ShardID            string
	FirstLogIndex      uint64
	LastLogIndex       uint64
	FinalizedDocuments []metastore.Document
	Transactions       []metastore.Transaction
	UploadIntents      []metastore.UploadIntent
	ObjectStates       []*publishedv1.PublishedObjectState
	Tombstones         int
}

func ReadSnapshotContents(reader io.Reader) (SnapshotContents, error) {
	return ReadSnapshotContentsForImport(reader, ImportOptions{})
}

func ReadSnapshotContentsForImport(reader io.Reader, options ImportOptions) (SnapshotContents, error) {
	var contents SnapshotContents
	seen := false
	for {
		record, err := ReadSnapshotRecord(reader)
		if errors.Is(err, io.EOF) {
			return contents, validateSnapshotImportComplete(seen, options)
		}
		if err != nil {
			return contents, err
		}
		if err := applySnapshotImportRecord(&contents, record, options, !seen); err != nil {
			return contents, err
		}
		seen = true
	}
}

func validateSnapshotImportComplete(seen bool, options ImportOptions) error {
	if !seen && hasSnapshotConstraints(options) {
		return invalidArtifact("snapshot artifact is empty")
	}
	return nil
}

func applySnapshotImportRecord(contents *SnapshotContents, record *publishedv1.SnapshotRecord, options ImportOptions, first bool) error {
	if err := applySnapshotHeader(contents, record, options, first); err != nil {
		return err
	}
	switch typed := record.GetRecord().(type) {
	case *publishedv1.SnapshotRecord_Document:
		document, intent, err := importDocument(typed.Document)
		if err != nil {
			return err
		}
		contents.Documents = append(contents.Documents, document)
		contents.UploadIntents = append(contents.UploadIntents, intent)
	case *publishedv1.SnapshotRecord_Transaction:
		transaction, err := importTransaction(typed.Transaction)
		if err != nil {
			return err
		}
		contents.Transactions = append(contents.Transactions, transaction)
	case *publishedv1.SnapshotRecord_Tombstone:
		if err := importTombstone(typed.Tombstone); err != nil {
			return err
		}
		contents.Tombstones++
	default:
		return invalidArtifact("unsupported snapshot record %T", record.GetRecord())
	}
	return nil
}

func ReadTailContents(reader io.Reader) (TailContents, error) {
	return ReadTailContentsForImport(reader, ImportOptions{})
}

func ReadTailContentsForImport(reader io.Reader, options ImportOptions) (TailContents, error) {
	var contents TailContents
	seen := false
	for {
		record, err := ReadTailRecord(reader)
		if errors.Is(err, io.EOF) {
			return contents, validateTailImportComplete(contents, seen, options)
		}
		if err != nil {
			return contents, err
		}
		if err := applyTailImportRecord(&contents, record, options, !seen); err != nil {
			return contents, err
		}
		seen = true
	}
}

func validateTailImportComplete(contents TailContents, seen bool, options ImportOptions) error {
	if !seen && hasTailConstraints(options) {
		return invalidArtifact("tail artifact is empty")
	}
	if !seen {
		return nil
	}
	if (options.RequireLogRange || options.FirstLogIndex != 0) && contents.FirstLogIndex != options.FirstLogIndex {
		return invalidArtifact("tail first log index %d does not match expected %d", contents.FirstLogIndex, options.FirstLogIndex)
	}
	if (options.RequireLogRange || options.LastLogIndex != 0) && contents.LastLogIndex != options.LastLogIndex {
		return invalidArtifact("tail last log index %d does not match expected %d", contents.LastLogIndex, options.LastLogIndex)
	}
	return nil
}

func applyTailImportRecord(contents *TailContents, record *publishedv1.TailRecord, options ImportOptions, first bool) error {
	if err := applyTailHeader(contents, record, options, first); err != nil {
		return err
	}
	switch typed := record.GetMutation().(type) {
	case *publishedv1.TailRecord_FinalizedDocument:
		document, intent, err := importDocument(typed.FinalizedDocument)
		if err != nil {
			return err
		}
		contents.FinalizedDocuments = append(contents.FinalizedDocuments, document)
		contents.UploadIntents = append(contents.UploadIntents, intent)
	case *publishedv1.TailRecord_TransactionState:
		transaction, err := importTransaction(typed.TransactionState)
		if err != nil {
			return err
		}
		contents.Transactions = append(contents.Transactions, transaction)
	case *publishedv1.TailRecord_ObjectState:
		objectState, err := importObjectState(typed.ObjectState)
		if err != nil {
			return err
		}
		contents.ObjectStates = append(contents.ObjectStates, objectState)
	case *publishedv1.TailRecord_Tombstone:
		if err := importTombstone(typed.Tombstone); err != nil {
			return err
		}
		contents.Tombstones++
	default:
		return invalidArtifact("unsupported tail mutation %T", record.GetMutation())
	}
	return nil
}

func hasSnapshotConstraints(options ImportOptions) bool {
	return options.SourceNamespace != "" ||
		options.ShardID != "" ||
		options.HighWatermark != 0 ||
		options.RequireHighWatermark
}

func hasTailConstraints(options ImportOptions) bool {
	return options.SourceNamespace != "" ||
		options.ShardID != "" ||
		options.FirstLogIndex != 0 ||
		options.LastLogIndex != 0 ||
		options.RequireLogRange
}

func applySnapshotHeader(contents *SnapshotContents, record *publishedv1.SnapshotRecord, options ImportOptions, first bool) error {
	if record == nil {
		return invalidArtifact("snapshot record is required")
	}
	if err := validateSourceOwnership("snapshot", record.GetSourceNamespace(), record.GetShardId(), options); err != nil {
		return err
	}
	if (options.RequireHighWatermark || options.HighWatermark != 0) && record.GetHighWatermark() != options.HighWatermark {
		return invalidArtifact("snapshot high watermark %d does not match expected %d", record.GetHighWatermark(), options.HighWatermark)
	}
	if first {
		contents.SourceNamespace = record.GetSourceNamespace()
		contents.ShardID = record.GetShardId()
		contents.HighWatermark = record.GetHighWatermark()
		return nil
	}
	if contents.SourceNamespace != record.GetSourceNamespace() || contents.ShardID != record.GetShardId() {
		return fmt.Errorf("%w: snapshot record changed from %q/%q to %q/%q",
			ErrSourceMismatch,
			contents.SourceNamespace,
			contents.ShardID,
			record.GetSourceNamespace(),
			record.GetShardId(),
		)
	}
	if contents.HighWatermark != record.GetHighWatermark() {
		return invalidArtifact("snapshot record high watermark changed from %d to %d", contents.HighWatermark, record.GetHighWatermark())
	}
	return nil
}

func applyTailHeader(contents *TailContents, record *publishedv1.TailRecord, options ImportOptions, first bool) error {
	if record == nil {
		return invalidArtifact("tail record is required")
	}
	if err := validateSourceOwnership("tail", record.GetSourceNamespace(), record.GetShardId(), options); err != nil {
		return err
	}
	logIndex := record.GetLogIndex()
	if logIndex == 0 {
		return invalidArtifact("tail log index is required")
	}
	if (options.RequireLogRange || options.FirstLogIndex != 0) && logIndex < options.FirstLogIndex {
		return invalidArtifact("tail log index %d is before expected first index %d", logIndex, options.FirstLogIndex)
	}
	if (options.RequireLogRange || options.LastLogIndex != 0) && logIndex > options.LastLogIndex {
		return invalidArtifact("tail log index %d is after expected last index %d", logIndex, options.LastLogIndex)
	}
	if first {
		contents.SourceNamespace = record.GetSourceNamespace()
		contents.ShardID = record.GetShardId()
		contents.FirstLogIndex = logIndex
		contents.LastLogIndex = logIndex
		return nil
	}
	if contents.SourceNamespace != record.GetSourceNamespace() || contents.ShardID != record.GetShardId() {
		return fmt.Errorf("%w: tail record changed from %q/%q to %q/%q",
			ErrSourceMismatch,
			contents.SourceNamespace,
			contents.ShardID,
			record.GetSourceNamespace(),
			record.GetShardId(),
		)
	}
	if logIndex <= contents.LastLogIndex {
		return invalidArtifact("tail log index %d is not after previous index %d", logIndex, contents.LastLogIndex)
	}
	contents.LastLogIndex = logIndex
	return nil
}

func validateSourceOwnership(recordKind, sourceNamespace, shardID string, options ImportOptions) error {
	if sourceNamespace == "" {
		return invalidArtifact("%s source namespace is required", recordKind)
	}
	if shardID == "" {
		return invalidArtifact("%s shard id is required", recordKind)
	}
	if options.SourceNamespace != "" && sourceNamespace != options.SourceNamespace {
		return fmt.Errorf("%w: %s source namespace %q does not match expected %q", ErrSourceMismatch, recordKind, sourceNamespace, options.SourceNamespace)
	}
	if options.ShardID != "" && shardID != options.ShardID {
		return fmt.Errorf("%w: %s shard id %q does not match expected %q", ErrSourceMismatch, recordKind, shardID, options.ShardID)
	}
	return nil
}

func importDocument(document *publishedv1.PublishedDocument) (metastore.Document, metastore.UploadIntent, error) {
	if document == nil {
		return metastore.Document{}, metastore.UploadIntent{}, invalidArtifact("document record is required")
	}
	if err := validatePublishedDocumentFields(document); err != nil {
		return metastore.Document{}, metastore.UploadIntent{}, err
	}
	enums, err := importPublishedDocumentEnums(document)
	if err != nil {
		return metastore.Document{}, metastore.UploadIntent{}, err
	}
	locations := document.GetLocations()
	if len(locations) == 0 {
		return metastore.Document{}, metastore.UploadIntent{}, invalidArtifact("document %q has no locations", document.GetDocumentName())
	}
	location := locations[0]
	if err := validatePublishedLocation(location, document.GetDocumentName()); err != nil {
		return metastore.Document{}, metastore.UploadIntent{}, err
	}
	logicalSHA256, err := fixed32(document.GetLogicalSha256(), "logical sha256")
	if err != nil {
		return metastore.Document{}, metastore.UploadIntent{}, err
	}
	fingerprint, err := optionalFixed16(document.GetDocumentIdentityFingerprint(), "document identity fingerprint")
	if err != nil {
		return metastore.Document{}, metastore.UploadIntent{}, err
	}
	frames, err := importFrames(location.GetFrames())
	if err != nil {
		return metastore.Document{}, metastore.UploadIntent{}, err
	}

	imported := metastore.Document{
		Identity: identity.Document{
			TenantID:      document.GetTenantId(),
			TransactionID: document.GetTransactionId(),
			DocumentName:  document.GetDocumentName(),
		},
		DocumentClass:               metastore.DocumentClass(enums.documentClass),
		PriorityClass:               metastore.PriorityClass(enums.priorityClass),
		ContentType:                 document.GetContentType(),
		HasContentType:              document.ContentType != nil,
		Length:                      document.GetLength(),
		LogicalSHA256:               logicalSHA256,
		StoredSHA256:                logicalSHA256,
		DocumentIdentityFingerprint: fingerprint,
		CreatedByService:            document.GetCreatedByService(),
		WorkflowStage:               document.GetWorkflowStage(),
		HasWorkflowStage:            document.WorkflowStage != nil,
		Availability:                metastore.Availability(enums.availability),
		LifecycleState:              metastore.LifecycleState(enums.lifecycleState),
		UploadState:                 metastore.UploadStateUploaded,
		Tags:                        clonePublishedTags(document.GetTags()),
		Location: blockstore.Record{
			BlockID:       location.GetBlockId(),
			StoredOffset:  location.GetStoredOffset(),
			StoredLength:  location.GetStoredLength(),
			LogicalSHA256: logicalSHA256,
			Frames:        frames,
		},
	}
	imported.CreatedAt = document.GetCreatedAt().AsTime()
	imported.FinalizedAt = document.GetFinalizedAt().AsTime()
	intent := metastore.UploadIntent{
		BlockID:           location.GetBlockId(),
		BackendObjectKey:  location.GetBackendObjectKey(),
		IndexObjectKey:    location.GetIndexObjectKey(),
		EnvelopeObjectKey: location.GetEnvelopeObjectKey(),
		State:             metastore.UploadStateUploaded,
	}
	return imported, intent, nil
}

func validatePublishedDocumentFields(document *publishedv1.PublishedDocument) error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "tenant id", value: document.GetTenantId()},
		{name: "transaction id", value: document.GetTransactionId()},
		{name: "name", value: document.GetDocumentName()},
	} {
		if field.value == "" {
			return invalidArtifact("document %s is required", field.name)
		}
	}
	for _, field := range []struct {
		name  string
		value uint32
	}{
		{name: "class", value: document.GetDocumentClass()},
		{name: "priority class", value: document.GetPriorityClass()},
		{name: "availability", value: document.GetAvailability()},
		{name: "lifecycle state", value: document.GetLifecycleState()},
	} {
		if field.value == 0 {
			return invalidArtifact("document %s is required", field.name)
		}
	}
	if document.GetCreatedAt() == nil {
		return invalidArtifact("document %q created_at is required", document.GetDocumentName())
	}
	if document.GetFinalizedAt() == nil {
		return invalidArtifact("document %q finalized_at is required", document.GetDocumentName())
	}
	return nil
}

type publishedDocumentEnums struct {
	documentClass  uint16
	priorityClass  uint16
	availability   uint16
	lifecycleState uint16
}

func importPublishedDocumentEnums(document *publishedv1.PublishedDocument) (publishedDocumentEnums, error) {
	documentClass, err := safeconv.Uint32ToUint16("document class", document.GetDocumentClass())
	if err != nil {
		return publishedDocumentEnums{}, invalidArtifact("document class is out of range: %v", err)
	}
	priorityClass, err := safeconv.Uint32ToUint16("document priority class", document.GetPriorityClass())
	if err != nil {
		return publishedDocumentEnums{}, invalidArtifact("document priority class is out of range: %v", err)
	}
	availability, err := safeconv.Uint32ToUint16("document availability", document.GetAvailability())
	if err != nil {
		return publishedDocumentEnums{}, invalidArtifact("document availability is out of range: %v", err)
	}
	lifecycleState, err := safeconv.Uint32ToUint16("document lifecycle state", document.GetLifecycleState())
	if err != nil {
		return publishedDocumentEnums{}, invalidArtifact("document lifecycle state is out of range: %v", err)
	}
	return publishedDocumentEnums{
		documentClass:  documentClass,
		priorityClass:  priorityClass,
		availability:   availability,
		lifecycleState: lifecycleState,
	}, nil
}

func importTransaction(transaction *publishedv1.PublishedTransaction) (metastore.Transaction, error) {
	if transaction == nil {
		return metastore.Transaction{}, invalidArtifact("transaction record is required")
	}
	if transaction.GetTenantId() == "" {
		return metastore.Transaction{}, invalidArtifact("transaction tenant id is required")
	}
	if transaction.GetTransactionId() == "" {
		return metastore.Transaction{}, invalidArtifact("transaction id is required")
	}
	if transaction.GetState() == 0 {
		return metastore.Transaction{}, invalidArtifact("transaction state is required")
	}
	state, err := safeconv.Uint32ToUint16("transaction state", transaction.GetState())
	if err != nil {
		return metastore.Transaction{}, invalidArtifact("transaction state is out of range: %v", err)
	}
	if transaction.GetCreatedAt() == nil {
		return metastore.Transaction{}, invalidArtifact("transaction created_at is required")
	}
	imported := metastore.Transaction{
		Identity: identity.Transaction{
			TenantID:      transaction.GetTenantId(),
			TransactionID: transaction.GetTransactionId(),
		},
		State:                  metastore.TransactionStateKind(state),
		DocumentCount:          transaction.GetDocumentCount(),
		PermanentDocumentCount: transaction.GetPermanentDocumentCount(),
		EphemeralDocumentCount: transaction.GetEphemeralDocumentCount(),
		Tags:                   clonePublishedTags(transaction.GetTags()),
	}
	if transaction.GetCreatedAt() != nil {
		imported.CreatedAt = transaction.GetCreatedAt().AsTime()
	}
	if transaction.GetCompletedAt() != nil {
		completedAt := transaction.GetCompletedAt().AsTime()
		imported.CompletedAt = &completedAt
	}
	if transaction.GetTimeoutAt() != nil {
		timeoutAt := transaction.GetTimeoutAt().AsTime()
		imported.TimeoutAt = &timeoutAt
	}
	return imported, nil
}

func importFrames(frames []*publishedv1.PublishedFrame) ([]blockstore.FrameRecord, error) {
	if len(frames) == 0 {
		return nil, invalidArtifact("document frames are required")
	}
	out := make([]blockstore.FrameRecord, 0, len(frames))
	for i, frame := range frames {
		if frame == nil {
			return nil, invalidArtifact("frame %d is required", i)
		}
		if frame.GetSegmentLength() == 0 {
			return nil, invalidArtifact("frame %d segment length is required", i)
		}
		sum, err := fixed32(frame.GetSha256(), "frame sha256")
		if err != nil {
			return nil, err
		}
		out = append(out, blockstore.FrameRecord{
			FrameOffset:   frame.GetFrameOffset(),
			SegmentOffset: frame.GetSegmentOffset(),
			SegmentLength: frame.GetSegmentLength(),
			SHA256:        sum,
		})
	}
	return out, nil
}

func importObjectState(state *publishedv1.PublishedObjectState) (*publishedv1.PublishedObjectState, error) {
	if state == nil {
		return nil, invalidArtifact("object state is required")
	}
	if state.GetAvailableAtIndex() == 0 {
		return nil, invalidArtifact("object state available_at_index is required")
	}
	if err := validateObjectRef(state.GetObject()); err != nil {
		return nil, err
	}
	return proto.Clone(state).(*publishedv1.PublishedObjectState), nil
}

func importTombstone(tombstone *publishedv1.PublishedTombstone) error {
	if tombstone == nil {
		return invalidArtifact("tombstone record is required")
	}
	if tombstone.GetTenantId() == "" {
		return invalidArtifact("tombstone tenant id is required")
	}
	if tombstone.GetTransactionId() == "" {
		return invalidArtifact("tombstone transaction id is required")
	}
	if tombstone.GetTombstonedAtIndex() == 0 {
		return invalidArtifact("tombstone index is required")
	}
	if tombstone.GetTombstonedAt() == nil {
		return invalidArtifact("tombstone timestamp is required")
	}
	return nil
}

func validatePublishedLocation(location *publishedv1.PublishedLocation, documentName string) error {
	if location == nil {
		return invalidArtifact("document %q location is required", documentName)
	}
	if location.GetFormatVersion() != CurrentLocationFormatVersion {
		return fmt.Errorf("%w: location version %d", ErrUnsupportedFormatVersion, location.GetFormatVersion())
	}
	if location.GetBlockId() == "" {
		return invalidArtifact("document %q location block id is required", documentName)
	}
	if location.GetBackendObjectKey() == "" {
		return invalidArtifact("document %q has no backend object key", documentName)
	}
	return nil
}

func validateObjectRef(ref *publishedv1.ObjectRef) error {
	if ref == nil {
		return invalidArtifact("object ref is required")
	}
	if ref.GetKind() == publishedv1.ObjectKind_OBJECT_KIND_UNSPECIFIED {
		return invalidArtifact("object ref kind is required")
	}
	if ref.GetObjectKey() == "" {
		return invalidArtifact("object ref key is required")
	}
	if len(ref.GetSha256()) != sha256.Size {
		return invalidArtifact("object ref checksum is %d bytes", len(ref.GetSha256()))
	}
	return nil
}

func fixed32(data []byte, field string) ([32]byte, error) {
	var out [32]byte
	if len(data) != len(out) {
		return out, invalidArtifact("%s is %d bytes", field, len(data))
	}
	copy(out[:], data)
	return out, nil
}

func optionalFixed16(data []byte, field string) ([16]byte, error) {
	var out [16]byte
	if len(data) == 0 {
		return out, nil
	}
	if len(data) != len(out) {
		return out, invalidArtifact("%s is %d bytes", field, len(data))
	}
	copy(out[:], data)
	return out, nil
}

func invalidArtifact(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidArtifact, fmt.Sprintf(format, args...))
}
