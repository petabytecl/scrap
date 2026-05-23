package localstorage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/petabytecl/scrap/internal/api"
	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/backendupload"
	"github.com/petabytecl/scrap/internal/blockstore"
	"github.com/petabytecl/scrap/internal/cryptoenv"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	scrapv1 "github.com/petabytecl/scrap/internal/gen/scrap/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/operations"
	"github.com/petabytecl/scrap/internal/published"
	"github.com/petabytecl/scrap/internal/raftmeta"
	"github.com/petabytecl/scrap/internal/replication"
	"github.com/petabytecl/scrap/internal/safeconv"
	"github.com/petabytecl/scrap/internal/storageformat"
)

type Application struct {
	dir                      string
	blocks                   *blockstore.Store
	backendStore             backend.Store
	envelopeSource           backendupload.BlockEnvelopeSource
	envelopeTransit          cryptoenv.Transit
	operationStore           *operations.Store
	metadata                 *metastore.Store
	authority                *raftmeta.Authority
	prepare                  *prepareLog
	memberMu                 sync.Mutex
	memberState              localMemberState
	byteServingMu            sync.Mutex
	byteServingReady         bool
	byteServingErr           error
	byteServingCancel        context.CancelFunc
	byteServingDone          chan struct{}
	peerRepairSources        map[string]PeerRepairSource
	sealBlockAtBytes         uint64
	minUsableBytesAfterWrite uint64
	peerPrepareTargets       []replication.Target
	peerPreparePolicy        replication.Policy
	now                      func() time.Time
	writeFaults              writeFaultHooks
}

const (
	DefaultSealBlockAtBytes uint64 = 256 * 1024 * 1024
	backendReadChunkSize           = 1024 * 1024
)

type BackendUploadOnceResult struct {
	SealedBlockID       string
	SealedBlockIDs      []string
	Sealed              bool
	Upload              backendupload.RunResult
	MetadataPublished   bool
	MetadataPublication *published.SnapshotPublication
}

type PeerRepairSource interface {
	ReadReplica(context.Context, blockstore.ReplicaRef, io.Writer) error
}

type PeerRepairSourceFunc func(context.Context, blockstore.ReplicaRef, io.Writer) error

func (f PeerRepairSourceFunc) ReadReplica(ctx context.Context, replica blockstore.ReplicaRef, writer io.Writer) error {
	return f(ctx, replica, writer)
}

// writeFaultHooks makes local durability boundaries injectable for deterministic
// crash-recovery tests without process-level chaos.
type writeFaultHooks struct {
	afterBlockSync     func(blockstore.Record) error
	afterPrepareSync   func(metastore.Document) error
	afterMetadataApply func(metastore.Document) error
	beforeACK          func(metastore.Document) error
}

func Open(dir string) (*Application, error) {
	return OpenWithContext(context.Background(), dir)
}

func OpenWithContext(ctx context.Context, dir string) (*Application, error) {
	blocks, err := blockstore.Open(dir)
	if err != nil {
		return nil, err
	}
	_, metadataStatErr := os.Stat(filepath.Join(dir, "metadata"))
	if metadataStatErr != nil && !errors.Is(metadataStatErr, os.ErrNotExist) {
		_ = blocks.Close()
		return nil, metadataStatErr
	}
	metadata, err := metastore.Open(dir)
	if err != nil {
		_ = blocks.Close()
		return nil, err
	}
	authority, err := raftmeta.OpenAuthority(filepath.Join(dir, "raftmeta"), "local", metadata)
	if err != nil {
		_ = metadata.Close()
		_ = blocks.Close()
		return nil, err
	}
	prepare, err := openPrepareLog(dir)
	if err != nil {
		_ = authority.Close()
		_ = metadata.Close()
		_ = blocks.Close()
		return nil, err
	}
	if _, err := prepare.Recover(); err != nil {
		_ = prepare.Close()
		_ = authority.Close()
		_ = metadata.Close()
		_ = blocks.Close()
		return nil, err
	}
	memberState, err := readLocalMemberState(dir)
	if err != nil {
		_ = prepare.Close()
		_ = authority.Close()
		_ = metadata.Close()
		_ = blocks.Close()
		return nil, err
	}
	app := &Application{
		dir:              dir,
		blocks:           blocks,
		metadata:         metadata,
		authority:        authority,
		prepare:          prepare,
		memberState:      memberState,
		byteServingDone:  make(chan struct{}),
		sealBlockAtBytes: DefaultSealBlockAtBytes,
		now:              func() time.Time { return time.Now().UTC() },
	}
	app.startByteServingVerifier(ctx)
	return app, nil
}

func (a *Application) Close() error {
	if a == nil {
		return nil
	}
	if a.byteServingCancel != nil {
		a.byteServingCancel()
		<-a.byteServingDone
	}
	return errors.Join(a.prepare.Close(), a.authority.Close(), a.metadata.Close(), a.blocks.Close())
}

func (a *Application) BackendUploadProcessor(store backend.Store) backendupload.Processor {
	return backendupload.Processor{
		Uploader: backendupload.Uploader{
			Backend:  store,
			Source:   backendupload.LocalBlockSource{Blocks: a.blocks},
			Envelope: a.backendEnvelopeSource(),
			Index: backendupload.LocalBlockIndexSource{
				Documents: a.metadata,
				ShardID:   "local",
			},
		},
		Intents: a.metadata,
		Updater: a.authority,
		Now:     a.now,
	}
}

func (a *Application) SetEnvelopeTransit(transit cryptoenv.Transit, keyID string) {
	a.envelopeTransit = transit
	a.envelopeSource = backendupload.TransitBlockEnvelopeSource{
		Transit: transit,
		CellID:  localPublishedCellID,
		KeyID:   keyID,
	}
}

func (a *Application) SetBlockEnvelopeSource(source backendupload.BlockEnvelopeSource) {
	a.envelopeSource = source
}

func (a *Application) SetBackendStore(store backend.Store) {
	a.backendStore = store
}

func (a *Application) SetOperationStore(store *operations.Store) {
	a.operationStore = store
}

func (a *Application) SetSealBlockAtBytes(bytes uint64) {
	a.sealBlockAtBytes = bytes
}

func (a *Application) backendEnvelopeSource() backendupload.BlockEnvelopeSource {
	if a.envelopeSource != nil {
		return a.envelopeSource
	}
	return backendupload.LocalBlockEnvelopeSource{CellID: localPublishedCellID}
}

func (a *Application) SetPeerRepairSource(memberID string, source PeerRepairSource) {
	if a.peerRepairSources == nil {
		a.peerRepairSources = make(map[string]PeerRepairSource)
	}
	if source == nil {
		delete(a.peerRepairSources, memberID)
		return
	}
	a.peerRepairSources[memberID] = source
}

func (a *Application) RunBackendUploadOnce(ctx context.Context, store backend.Store) (BackendUploadOnceResult, error) {
	var result BackendUploadOnceResult
	sealedBlockID, sealed, err := a.SealCurrentBlockIfDue(ctx)
	if err != nil {
		return result, err
	}
	result.SealedBlockID = sealedBlockID
	result.Sealed = sealed
	if sealed {
		result.SealedBlockIDs = append(result.SealedBlockIDs, sealedBlockID)
	}
	recovered, err := a.SealPendingUploadBlocks(ctx)
	if err != nil {
		return result, err
	}
	result.SealedBlockIDs = append(result.SealedBlockIDs, recovered...)
	result.Upload, err = a.BackendUploadProcessor(store).RunOnce(ctx)
	if err != nil {
		return result, err
	}
	if a.shouldPublishMetadataCheckpoint(ctx, store, result.Upload) {
		mutable, ok := store.(backend.MutableStore)
		if !ok {
			return result, errors.New("localstorage: backend store does not support mutable metadata pointers")
		}
		publication, err := a.publishMetadataSnapshot(ctx, mutable)
		if err != nil {
			return result, err
		}
		result.MetadataPublished = true
		result.MetadataPublication = &publication
	}
	return result, err
}

func (a *Application) shouldPublishMetadataCheckpoint(ctx context.Context, store backend.Store, upload backendupload.RunResult) bool {
	if upload.Uploaded > 0 {
		return true
	}
	if upload.Scanned == 0 || upload.Deferred > 0 || upload.Failed > 0 {
		return false
	}
	if _, err := published.VerifyCurrentCheckpoint(ctx, store, localPublishedCellID); errors.Is(err, published.ErrCurrentPointerNotFound) {
		return true
	}
	return false
}

func (a *Application) SealPendingUploadBlocks(ctx context.Context) ([]string, error) {
	intents, err := a.metadata.ListUploadIntents()
	if err != nil {
		return nil, err
	}
	currentBlockID := a.blocks.CurrentBlockID()
	var sealed []string
	for _, intent := range intents {
		if intent.BlockID == "" || intent.BlockID == currentBlockID || !isUploadPending(intent.State) {
			continue
		}
		didSeal, err := a.blocks.SealBlock(ctx, intent.BlockID)
		if errors.Is(err, blockstore.ErrEmptyBlock) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if didSeal {
			sealed = append(sealed, intent.BlockID)
		}
	}
	sort.Strings(sealed)
	return sealed, nil
}

func isUploadPending(state metastore.UploadState) bool {
	return state == metastore.UploadStatePending || state == metastore.UploadStateFailed
}

func (a *Application) SealCurrentBlockIfDue(ctx context.Context) (string, bool, error) {
	if a.sealBlockAtBytes == 0 || a.blocks.CurrentBlockLength() < a.sealBlockAtBytes {
		return "", false, nil
	}
	blockID, err := a.blocks.SealCurrent(ctx)
	if errors.Is(err, blockstore.ErrEmptyBlock) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return blockID, true, nil
}

func (a *Application) startByteServingVerifier(ctx context.Context) {
	verifierCtx, cancel := context.WithCancel(ctx)
	a.byteServingCancel = cancel
	go a.runByteServingVerifier(verifierCtx)
}

func (a *Application) runByteServingVerifier(ctx context.Context) {
	err := a.verifyLocalRefsAndQueueRepairs(ctx)
	a.byteServingMu.Lock()
	if err == nil {
		a.byteServingReady = true
	} else {
		a.byteServingErr = err
	}
	a.byteServingMu.Unlock()
	close(a.byteServingDone)
}

func (a *Application) waitByteServingReady(ctx context.Context) error {
	select {
	case <-a.byteServingDone:
	case <-ctx.Done():
		return ctx.Err()
	}
	a.byteServingMu.Lock()
	defer a.byteServingMu.Unlock()
	if a.byteServingReady {
		return nil
	}
	if a.byteServingErr != nil {
		return a.byteServingErr
	}
	return errors.New("localstorage: byte-serving readiness did not complete")
}

// verifyLocalRefsAndQueueRepairs verifies committed hot local refs and records
// quarantined repair work for missing or corrupt bytes before this member serves
// local document bytes.
func (a *Application) verifyLocalRefsAndQueueRepairs(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	documents, err := a.metadata.ListDocuments(metastore.DocumentFilter{})
	if err != nil {
		return err
	}
	for _, document := range documents {
		if err := ctx.Err(); err != nil {
			return err
		}
		if document.RestoreState != metastore.RestoreStateHot || document.Availability != metastore.AvailabilityHot {
			continue
		}
		length := document.Length
		if err := a.blocks.VerifyRange(document.Location, 0, &length); err == nil {
			continue
		} else if !isIntegrityFailure(err) {
			return err
		}
		incidentID := integrityEvidenceID(document)
		if state, err := a.metadata.GetRepairState(document.Identity, incidentID); err == nil && state.Quarantined {
			continue
		} else if err != nil && !errors.Is(err, metastore.ErrNotFound) {
			return err
		}
		if err := a.recordDocumentRepairState(ctx, document, incidentID, true, a.now()); err != nil {
			return err
		}
	}
	return nil
}

func (a *Application) WriteDocument(ctx context.Context, init api.WriteDocumentInit, chunks api.ChunkReader) (api.WriteDocumentResult, error) {
	if err := a.authority.EnsureWriteReady(ctx); err != nil {
		return api.WriteDocumentResult{}, mapError(err)
	}
	if existing, err := a.metadata.HeadDocument(init.Identity); err == nil {
		body, err := drainChunks(chunks)
		if err != nil {
			return api.WriteDocumentResult{}, err
		}
		return a.replayExisting(ctx, init, existing, body)
	} else if !errors.Is(err, metastore.ErrNotFound) {
		return api.WriteDocumentResult{}, mapError(err)
	}
	if err := a.requireWriteAdmission(ctx, init.ExpectedLength); err != nil {
		return api.WriteDocumentResult{}, err
	}

	record, err := a.blocks.AppendValidated(ctx, &chunkReader{chunks: chunks}, func(record blockstore.Record) error {
		if init.ExpectedLength != nil && *init.ExpectedLength != record.StoredLength {
			return status.Errorf(codes.InvalidArgument, "expected length %d does not match written length %d", *init.ExpectedLength, record.StoredLength)
		}
		if len(init.ExpectedSHA256) > 0 && !bytes.Equal(init.ExpectedSHA256, record.LogicalSHA256[:]) {
			return status.Error(codes.InvalidArgument, "expected sha256 does not match written bytes")
		}
		return nil
	})
	if err != nil {
		return api.WriteDocumentResult{}, mapError(err)
	}
	if a.writeFaults.afterBlockSync != nil {
		if err := a.writeFaults.afterBlockSync(record); err != nil {
			return api.WriteDocumentResult{}, err
		}
	}

	now := a.now()
	document := metastore.Document{
		Identity:                    init.Identity,
		DocumentClass:               documentClassFromAPI(init.DocumentClass),
		PriorityClass:               priorityClassFromAPI(init.PriorityClass),
		ContentType:                 init.ContentType,
		HasContentType:              init.ContentType != "",
		Length:                      record.StoredLength,
		LogicalSHA256:               record.LogicalSHA256,
		StoredSHA256:                record.LogicalSHA256,
		DocumentIdentityFingerprint: documentIdentityFingerprint(init.Identity.TenantID, init.Identity.TransactionID, init.Identity.DocumentName),
		CreatedByService:            init.CreatedByService,
		WorkflowStage:               init.WorkflowStage,
		HasWorkflowStage:            init.WorkflowStage != "",
		CreatedAt:                   now,
		FinalizedAt:                 now,
		Availability:                metastore.AvailabilityHot,
		LifecycleState:              metastore.LifecycleStateActive,
		Tags:                        cloneTags(init.Tags),
		Location:                    record,
		ClientIdempotencyKey:        init.ClientIdempotencyKey,
		HasClientIdempotencyKey:     init.ClientIdempotencyKey != "",
	}
	if err := a.prepare.Append(document); err != nil {
		return api.WriteDocumentResult{}, err
	}
	if a.writeFaults.afterPrepareSync != nil {
		if err := a.writeFaults.afterPrepareSync(document); err != nil {
			return api.WriteDocumentResult{}, err
		}
	}
	replicationResult, err := a.preparePeerReplicas(ctx, document)
	if err != nil {
		return api.WriteDocumentResult{}, mapError(err)
	}
	document.Location.Replicas = replication.ReplicaRefs(replicationResult.Receipts)
	if err := a.authority.CommitDocument(ctx, document, commitDocumentCommandID(document), now); err != nil {
		return api.WriteDocumentResult{}, mapError(err)
	}
	if a.writeFaults.afterMetadataApply != nil {
		if err := a.writeFaults.afterMetadataApply(document); err != nil {
			return api.WriteDocumentResult{}, err
		}
	}
	uploadIntent := uploadIntentForDocument(document)
	if err := a.authority.RecordUploadIntent(ctx, uploadIntent, recordUploadIntentCommandID(uploadIntent), now); err != nil {
		return api.WriteDocumentResult{}, mapError(err)
	}
	if a.writeFaults.beforeACK != nil {
		if err := a.writeFaults.beforeACK(document); err != nil {
			return api.WriteDocumentResult{}, err
		}
	}
	desiredReplicaCount, err := safeconv.IntToUint32("desired replica count", replicationResult.DesiredReplicaCount)
	if err != nil {
		return api.WriteDocumentResult{}, err
	}
	achievedReplicaCount, err := safeconv.IntToUint32("achieved replica count", replicationResult.AchievedReplicaCount)
	if err != nil {
		return api.WriteDocumentResult{}, err
	}
	return api.WriteDocumentResult{
		Metadata:             documentToAPI(document),
		DesiredReplicaCount:  desiredReplicaCount,
		AchievedReplicaCount: achievedReplicaCount,
		RepairRequired:       replicationResult.RepairRequired,
		IdempotentReplay:     false,
	}, nil
}

func (a *Application) preparePeerReplicas(ctx context.Context, document metastore.Document) (replication.Result, error) {
	if len(a.peerPrepareTargets) == 0 {
		return replication.Result{
			DesiredReplicaCount:  1,
			RequiredReplicaCount: 1,
			AchievedReplicaCount: 1,
		}, nil
	}
	prepared := replication.PreparedDocumentFromMetadata(document)
	source := replication.ByteSourceFunc(func(ctx context.Context, requested replication.PreparedDocument, writer io.Writer) error {
		record := blockstore.Record{
			BlockID:       requested.BlockID,
			StoredOffset:  requested.StoredOffset,
			StoredLength:  requested.StoredLength,
			LogicalSHA256: requested.LogicalSHA256,
			Frames:        append([]blockstore.FrameRecord(nil), requested.Frames...),
		}
		return a.blocks.ReadRange(ctx, record, 0, nil, writer)
	})
	return replication.PrepareDocument(ctx, prepared, source, a.peerPrepareTargets, a.peerPreparePolicy)
}

func (a *Application) HeadDocument(ctx context.Context, req api.HeadDocumentRequest) (api.DocumentMetadata, error) {
	if err := a.authority.ReadFresh(ctx); err != nil {
		return api.DocumentMetadata{}, mapError(err)
	}
	document, err := a.metadata.HeadDocument(req.Identity)
	if err != nil {
		return api.DocumentMetadata{}, mapError(err)
	}
	return documentToAPI(document), nil
}

func (a *Application) ReadDocument(ctx context.Context, req api.ReadDocumentRequest, sender api.ReadDocumentSender) error {
	if err := a.authority.ReadFresh(ctx); err != nil {
		return mapError(err)
	}
	if err := a.waitByteServingReady(ctx); err != nil {
		return mapError(err)
	}
	document, err := a.metadata.HeadDocument(req.Identity)
	if err != nil {
		return mapError(err)
	}
	if document.Availability == metastore.AvailabilityCold {
		document, err = a.queueRestoreOnColdRead(ctx, document)
		if err != nil {
			return mapError(err)
		}
	}
	if err := unavailableReadStateError(document); err != nil {
		return err
	}
	offset := uint64(0)
	if req.Range != nil {
		offset = req.Range.Offset
	}
	if offset > document.Length {
		return mapError(blockstore.ErrInvalidRange)
	}
	readLength := document.Length - offset
	if req.Range != nil && req.Range.Length != nil {
		if *req.Range.Length > document.Length-offset {
			return mapError(blockstore.ErrInvalidRange)
		}
		readLength = *req.Range.Length
	}
	selectedRange := api.ReadRange{Offset: offset, Length: &readLength}
	if err := a.blocks.VerifyRange(document.Location, offset, &readLength); err != nil {
		return a.readDocumentFromBackend(ctx, document, selectedRange, sender, err)
	}
	if err := sender.SendMetadata(api.ReadDocumentMetadata{
		Metadata:      documentToAPI(document),
		SelectedRange: selectedRange,
		Source:        scrapv1.StorageSource_STORAGE_SOURCE_LOCAL,
	}); err != nil {
		return err
	}
	return mapError(a.blocks.ReadRange(ctx, document.Location, offset, &readLength, senderWriter{sender: sender}))
}

func (a *Application) readDocumentFromBackend(ctx context.Context, document metastore.Document, selectedRange api.ReadRange, sender api.ReadDocumentSender, localErr error) error {
	if a.backendStore == nil {
		return readIntegrityError(document, []string{"local"}, localErr)
	}
	intent, err := a.metadata.GetUploadIntent(document.Location.BlockID)
	if err != nil || intent.State != metastore.UploadStateUploaded {
		return readIntegrityError(document, []string{"local"}, localErr)
	}
	data, err := a.readVerifiedBackendRange(ctx, document, intent, selectedRange)
	if err != nil {
		if errors.Is(err, backend.ErrRestorePending) {
			return restorePendingError(document)
		}
		if cryptoenv.IsUnavailable(err) {
			return cryptoUnavailableError(document)
		}
		if isIntegrityFailure(err) {
			return readIntegrityError(document, []string{"local", "backend"}, err)
		}
		return mapError(err)
	}
	if isIntegrityFailure(localErr) {
		if err := a.recordDocumentRepairState(ctx, document, integrityEvidenceID(document), true, a.now()); err != nil {
			return err
		}
	}
	if err := sender.SendMetadata(api.ReadDocumentMetadata{
		Metadata:      documentToAPI(document),
		SelectedRange: selectedRange,
		Source:        scrapv1.StorageSource_STORAGE_SOURCE_BACKEND,
	}); err != nil {
		return err
	}
	for len(data) > 0 {
		n := backendReadChunkSize
		if n > len(data) {
			n = len(data)
		}
		if err := sender.SendChunk(data[:n]); err != nil {
			return err
		}
		data = data[n:]
	}
	return nil
}

func (a *Application) queueRestoreOnColdRead(ctx context.Context, document metastore.Document) (metastore.Document, error) {
	if a.operationStore == nil {
		return document, nil
	}
	now := a.now()
	operation := newRestoreOnReadOperation(document, now)
	stored, err := a.createOrLoadRestoreOnReadOperation(operation)
	if err != nil {
		return document, err
	}
	if restoreOnReadNeedsRequeue(stored) {
		stored = requeueRestoreOnReadOperation(stored, operation, now)
		if err := a.operationStore.Put(stored); err != nil {
			return document, err
		}
	}
	if !restoreOnReadIsQueuedOrRunning(stored) {
		return document, fmt.Errorf("localstorage: restore-on-read operation id %q is %s", stored.GetOperationId(), stored.GetState())
	}
	if err := a.operationStore.AppendAuditEvent(restoreQueuedAuditEvent(stored)); err != nil && !errors.Is(err, operations.ErrConflict) {
		return document, err
	}
	return a.markDocumentRestorePending(ctx, document, now)
}

func newRestoreOnReadOperation(document metastore.Document, now time.Time) *adminv1.Operation {
	operation := &adminv1.Operation{
		OperationId:         restoreOnReadOperationID(document),
		OperationType:       "restore",
		State:               adminv1.OperationState_OPERATION_STATE_QUEUED,
		RequestedByIdentity: "document-read",
		RequestedAt:         timestamppb.New(now),
		Targets:             []*adminv1.Target{documentOperationTarget(document.Identity)},
		Progress:            &adminv1.OperationProgress{Message: "queued by cold read"},
		Metadata: map[string]string{
			operationLaneMetadata:  operationLaneInteractive,
			backendLaneMetadata:    string(backend.LaneRestore),
			restoreTriggerMetadata: restoreTriggerRead,
		},
	}
	return operation
}

func (a *Application) createOrLoadRestoreOnReadOperation(operation *adminv1.Operation) (*adminv1.Operation, error) {
	if existing, err := a.operationStore.Get(operation.GetOperationId()); err == nil {
		return validateRestoreOnReadOperation(existing)
	} else if !errors.Is(err, operations.ErrNotFound) {
		return nil, err
	}
	created, err := a.operationStore.Create(operation)
	if err == nil {
		return created, nil
	}
	if !errors.Is(err, operations.ErrConflict) {
		return nil, err
	}
	existing, getErr := a.operationStore.Get(operation.GetOperationId())
	if getErr != nil {
		return nil, getErr
	}
	return validateRestoreOnReadOperation(existing)
}

func validateRestoreOnReadOperation(operation *adminv1.Operation) (*adminv1.Operation, error) {
	if operation.GetOperationType() != "restore" {
		return nil, fmt.Errorf("localstorage: restore-on-read operation id %q has type %q", operation.GetOperationId(), operation.GetOperationType())
	}
	return operation, nil
}

func restoreOnReadNeedsRequeue(operation *adminv1.Operation) bool {
	switch operation.GetState() {
	case adminv1.OperationState_OPERATION_STATE_SUCCEEDED,
		adminv1.OperationState_OPERATION_STATE_FAILED,
		adminv1.OperationState_OPERATION_STATE_CANCELED,
		adminv1.OperationState_OPERATION_STATE_EXPIRED:
		return true
	default:
		return false
	}
}

func restoreOnReadIsQueuedOrRunning(operation *adminv1.Operation) bool {
	switch operation.GetState() {
	case adminv1.OperationState_OPERATION_STATE_QUEUED,
		adminv1.OperationState_OPERATION_STATE_RUNNING:
		return true
	default:
		return false
	}
}

func requeueRestoreOnReadOperation(existing, desired *adminv1.Operation, now time.Time) *adminv1.Operation {
	operation := cloneOperation(existing)
	operation.State = adminv1.OperationState_OPERATION_STATE_QUEUED
	operation.StartedAt = nil
	operation.FinishedAt = nil
	operation.LastError = nil
	operation.Progress = &adminv1.OperationProgress{Message: "requeued by cold read"}
	if operation.GetRequestedAt() == nil {
		operation.RequestedAt = timestamppb.New(now)
	}
	if operation.GetRequestedByIdentity() == "" {
		operation.RequestedByIdentity = desired.GetRequestedByIdentity()
	}
	if len(operation.GetTargets()) == 0 {
		operation.Targets = cloneOperationTargets(desired.GetTargets())
	}
	operation.Metadata = cloneTags(operation.GetMetadata())
	if operation.Metadata == nil {
		operation.Metadata = make(map[string]string)
	}
	for key, value := range desired.GetMetadata() {
		if operation.Metadata[key] == "" {
			operation.Metadata[key] = value
		}
	}
	return operation
}

func (a *Application) markDocumentRestorePending(ctx context.Context, document metastore.Document, now time.Time) (metastore.Document, error) {
	if document.RestoreState != metastore.RestoreStateRestorePending {
		if err := a.authority.UpdateDocumentRestoreState(
			ctx,
			document.Identity,
			metastore.RestoreStateRestorePending,
			"cold read queued backend restore",
			stableCommandID("restore-on-read-state", document.Identity.TenantID, document.Identity.TransactionID, document.Identity.DocumentName, document.Location.BlockID),
			now,
		); err != nil {
			return document, err
		}
	}
	updated, err := a.metadata.HeadDocument(document.Identity)
	if err != nil {
		return document, err
	}
	return updated, nil
}

func restoreOnReadOperationID(document metastore.Document) string {
	return stableCommandID(
		"restore-on-read-operation",
		document.Identity.TenantID,
		document.Identity.TransactionID,
		document.Identity.DocumentName,
		document.Location.BlockID,
	)
}

func documentOperationTarget(doc identity.Document) *adminv1.Target {
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

func restoreQueuedAuditEvent(operation *adminv1.Operation) *adminv1.AuditEvent {
	return &adminv1.AuditEvent{
		EventId:       stableCommandID(operationEventRestoreQueued, operation.GetOperationId()),
		EventType:     operationEventRestoreQueued,
		OperationId:   operation.GetOperationId(),
		OperationType: operation.GetOperationType(),
		ActorIdentity: operation.GetRequestedByIdentity(),
		OccurredAt:    operation.GetRequestedAt(),
		Targets:       cloneOperationTargets(operation.GetTargets()),
		Metadata:      sanitizeOperationAuditMetadata(operation.GetMetadata()),
	}
}

func (a *Application) readVerifiedBackendRange(ctx context.Context, document metastore.Document, intent metastore.UploadIntent, selectedRange api.ReadRange) ([]byte, error) {
	if selectedRange.Length == nil {
		return nil, blockstore.ErrInvalidRange
	}
	readLength := *selectedRange.Length
	if readLength == 0 {
		return nil, nil
	}
	if err := a.verifyBackendEnvelope(ctx, intent); err != nil {
		return nil, err
	}
	verifyStart, verifyEnd, err := verificationWindow(document.Location, selectedRange.Offset, readLength)
	if err != nil {
		return nil, err
	}
	fetchLength := verifyEnd - verifyStart
	var fetched bytes.Buffer
	if err := a.backendStore.ReadObjectRange(ctx, intent.BackendObjectKey, backend.Range{
		Offset: verifyStart,
		Length: &fetchLength,
	}, &fetched); err != nil {
		return nil, err
	}
	data := fetched.Bytes()
	if err := verifyFetchedBackendWindow(document.Location, selectedRange.Offset, readLength, verifyStart, data); err != nil {
		return nil, err
	}
	selectedStart := document.Location.StoredOffset + selectedRange.Offset
	start := selectedStart - verifyStart
	end := start + readLength
	if end > uint64(len(data)) {
		return nil, io.ErrUnexpectedEOF
	}
	return append([]byte(nil), data[start:end]...), nil
}

func (a *Application) verifyBackendEnvelope(ctx context.Context, intent metastore.UploadIntent) error {
	if intent.EnvelopeObjectKey == "" {
		return nil
	}
	var data bytes.Buffer
	if err := a.backendStore.ReadObjectRange(ctx, intent.EnvelopeObjectKey, backend.Range{}, &data); err != nil {
		return err
	}
	envelope, err := storageformat.UnmarshalEnvelopeRecord(data.Bytes())
	if err != nil {
		return fmt.Errorf("%w: backend envelope %s is invalid: %w", backend.ErrChecksumMismatch, intent.EnvelopeObjectKey, err)
	}
	if envelope.GetAeadAlgorithm() == "none" {
		return nil
	}
	if a.envelopeTransit == nil {
		return fmt.Errorf("%w: backend envelope %s requires key %s", cryptoenv.ErrUnavailable, intent.EnvelopeObjectKey, envelope.GetKeyId())
	}
	return cryptoenv.ValidateEnvelopeRecordForRestore(ctx, a.envelopeTransit, envelope)
}

func verificationWindow(record blockstore.Record, offset, length uint64) (uint64, uint64, error) {
	if offset+length < offset || offset+length > record.StoredLength {
		return 0, 0, blockstore.ErrInvalidRange
	}
	if len(record.Frames) == 0 {
		return record.StoredOffset, record.StoredOffset + record.StoredLength, nil
	}
	selectedStart := record.StoredOffset + offset
	selectedEnd := selectedStart + length
	var verifyStart uint64
	var verifyEnd uint64
	for _, frame := range record.Frames {
		frameStart := frame.SegmentOffset
		frameEnd := frame.SegmentOffset + frame.SegmentLength
		if frameEnd <= selectedStart || frameStart >= selectedEnd {
			continue
		}
		if verifyStart == 0 || frameStart < verifyStart {
			verifyStart = frameStart
		}
		if frameEnd > verifyEnd {
			verifyEnd = frameEnd
		}
	}
	if verifyStart == 0 || verifyEnd < selectedEnd {
		return 0, 0, io.ErrUnexpectedEOF
	}
	return verifyStart, verifyEnd, nil
}

func verifyFetchedBackendWindow(record blockstore.Record, offset, length, verifyStart uint64, data []byte) error {
	if len(record.Frames) == 0 {
		if uint64(len(data)) != record.StoredLength {
			return io.ErrUnexpectedEOF
		}
		got := sha256.Sum256(data)
		if got != record.LogicalSHA256 {
			return blockstore.ErrChecksumMismatch
		}
		return nil
	}
	selectedStart := record.StoredOffset + offset
	selectedEnd := selectedStart + length
	for _, frame := range record.Frames {
		frameStart := frame.SegmentOffset
		frameEnd := frame.SegmentOffset + frame.SegmentLength
		if frameEnd <= selectedStart || frameStart >= selectedEnd {
			continue
		}
		start := frameStart - verifyStart
		end := start + frame.SegmentLength
		if end > uint64(len(data)) {
			return io.ErrUnexpectedEOF
		}
		got := sha256.Sum256(data[start:end])
		if got != frame.SHA256 {
			return blockstore.ErrChecksumMismatch
		}
	}
	return nil
}

func (a *Application) FindDocuments(ctx context.Context, req api.FindDocumentsRequest) (api.FindDocumentsResult, error) {
	if err := a.authority.ReadFresh(ctx); err != nil {
		return api.FindDocumentsResult{}, mapError(err)
	}
	documents, err := a.metadata.FindDocuments(req.Transaction, documentFilterFromAPI(req.Filter))
	if err != nil {
		return api.FindDocumentsResult{}, mapError(err)
	}
	result := api.FindDocumentsResult{
		Documents: make([]api.DocumentMetadata, 0, len(documents)),
	}
	for _, document := range documents {
		result.Documents = append(result.Documents, documentToAPI(document))
	}
	return result, nil
}

func (a *Application) CompleteTransaction(ctx context.Context, req api.CompleteTransactionRequest) (api.TransactionState, error) {
	completedAt := a.now()
	transaction, err := a.authority.CompleteTransaction(ctx, req.Transaction, completedAt, cloneTags(req.Tags), completeTransactionCommandID(req.Transaction, completedAt, req.Tags))
	if err != nil {
		return api.TransactionState{}, mapError(err)
	}
	return transactionToAPI(transaction), nil
}

func (a *Application) GetTransaction(ctx context.Context, req api.GetTransactionRequest) (api.TransactionState, error) {
	if err := a.authority.ReadFresh(ctx); err != nil {
		return api.TransactionState{}, mapError(err)
	}
	transaction, err := a.metadata.GetTransaction(req.Transaction)
	if err != nil {
		return api.TransactionState{}, mapError(err)
	}
	return transactionToAPI(transaction), nil
}

func (a *Application) replayExisting(ctx context.Context, init api.WriteDocumentInit, existing metastore.Document, body drainedBody) (api.WriteDocumentResult, error) {
	if !existing.HasClientIdempotencyKey || existing.ClientIdempotencyKey == "" || existing.ClientIdempotencyKey != init.ClientIdempotencyKey {
		return api.WriteDocumentResult{}, mapError(metastore.ErrConflict)
	}
	if body.length != existing.Length || body.sha256 != existing.LogicalSHA256 {
		return api.WriteDocumentResult{}, mapError(metastore.ErrConflict)
	}
	if init.ExpectedLength != nil && *init.ExpectedLength != existing.Length {
		return api.WriteDocumentResult{}, mapError(metastore.ErrConflict)
	}
	if len(init.ExpectedSHA256) > 0 && !bytes.Equal(init.ExpectedSHA256, existing.LogicalSHA256[:]) {
		return api.WriteDocumentResult{}, mapError(metastore.ErrConflict)
	}
	intent := uploadIntentForDocument(existing)
	if _, err := a.metadata.GetUploadIntent(intent.BlockID); errors.Is(err, metastore.ErrNotFound) {
		if err := a.authority.RecordUploadIntent(ctx, intent, recordUploadIntentCommandID(intent), a.now()); err != nil {
			return api.WriteDocumentResult{}, mapError(err)
		}
	} else if err != nil {
		return api.WriteDocumentResult{}, mapError(err)
	}
	return api.WriteDocumentResult{
		Metadata:             documentToAPI(existing),
		DesiredReplicaCount:  1,
		AchievedReplicaCount: 1,
		IdempotentReplay:     true,
	}, nil
}

type chunkReader struct {
	chunks  api.ChunkReader
	pending []byte
}

func (r *chunkReader) Read(p []byte) (int, error) {
	for len(r.pending) == 0 {
		chunk, err := r.chunks.Recv()
		if err != nil {
			return 0, err
		}
		r.pending = chunk
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

type senderWriter struct {
	sender api.ReadDocumentSender
}

func (w senderWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if err := w.sender.SendChunk(append([]byte(nil), p...)); err != nil {
		return 0, err
	}
	return len(p), nil
}

type drainedBody struct {
	length uint64
	sha256 [32]byte
}

func drainChunks(chunks api.ChunkReader) (drainedBody, error) {
	hasher := sha256.New()
	var length uint64
	for {
		chunk, err := chunks.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				var sum [32]byte
				copy(sum[:], hasher.Sum(nil))
				return drainedBody{length: length, sha256: sum}, nil
			}
			return drainedBody{}, err
		}
		if len(chunk) == 0 {
			continue
		}
		length += uint64(len(chunk))
		if _, err := hasher.Write(chunk); err != nil {
			return drainedBody{}, err
		}
	}
}

func mapError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, metastore.ErrNotFound):
		return status.Error(codes.NotFound, "document or transaction not found")
	case errors.Is(err, metastore.ErrConflict):
		return status.Error(codes.AlreadyExists, "document already exists")
	case errors.Is(err, blockstore.ErrInvalidRange):
		return status.Error(codes.InvalidArgument, "read range is outside document bounds")
	case errors.Is(err, blockstore.ErrChecksumMismatch):
		return status.Error(codes.DataLoss, "stored document bytes failed checksum verification")
	case errors.Is(err, backend.ErrChecksumMismatch):
		return status.Error(codes.DataLoss, "backend document bytes failed checksum verification")
	case errors.Is(err, backend.ErrNotFound):
		return status.Error(codes.DataLoss, "backend document bytes are missing")
	case errors.Is(err, os.ErrNotExist):
		return status.Error(codes.DataLoss, "stored document bytes are missing")
	case errors.Is(err, raftmeta.ErrNotLeader):
		return status.Error(codes.FailedPrecondition, "local metadata authority is not leader")
	case errors.Is(err, raftmeta.ErrQuorumUnavailable):
		return status.Error(codes.Unavailable, "metadata quorum is unavailable")
	case errors.Is(err, raftmeta.ErrReadFreshnessUnavailable):
		return status.Error(codes.Unavailable, "metadata freshness cannot be proven")
	case errors.Is(err, raftmeta.ErrLeaseReadDisabled):
		return status.Error(codes.FailedPrecondition, "metadata lease reads are disabled")
	case errors.Is(err, replication.ErrInsufficientReplicas):
		return status.Error(codes.Unavailable, "required peer replicas could not be prepared")
	case errors.Is(err, replication.ErrMissingByteSource):
		return status.Error(codes.Internal, "peer prepare byte source is unavailable")
	case errors.Is(err, replication.ErrReceiptMismatch):
		return status.Error(codes.Internal, "peer prepare receipt did not match prepared document")
	case errors.Is(err, replication.ErrTransferMismatch):
		return status.Error(codes.DataLoss, "peer prepare transfer failed checksum verification")
	default:
		return err
	}
}

func readIntegrityError(document metastore.Document, attemptedSources []string, cause error) error {
	if !isIntegrityFailure(cause) {
		return mapError(cause)
	}
	st := status.New(codes.DataLoss, "document bytes failed integrity verification")
	withDetails, err := st.WithDetails(&scrapv1.IntegrityFailureDetail{
		Identity: &scrapv1.DocumentIdentity{
			TenantId:      document.Identity.TenantID,
			TransactionId: document.Identity.TransactionID,
			DocumentName:  document.Identity.DocumentName,
		},
		AttemptedSources: append([]string(nil), attemptedSources...),
		EvidenceId:       integrityEvidenceID(document),
	})
	if err != nil {
		return st.Err()
	}
	return withDetails.Err()
}

func unavailableReadStateError(document metastore.Document) error {
	switch document.Availability {
	case metastore.AvailabilityCold, metastore.AvailabilityRestorePending:
		return restorePendingError(document)
	case metastore.AvailabilityCryptoUnavailable:
		return cryptoUnavailableError(document)
	default:
		return nil
	}
}

func restorePendingError(document metastore.Document) error {
	st := status.New(codes.Unavailable, "document bytes require backend restore")
	withDetails, err := st.WithDetails(&scrapv1.RestorePendingDetail{
		Identity: &scrapv1.DocumentIdentity{
			TenantId:      document.Identity.TenantID,
			TransactionId: document.Identity.TransactionID,
			DocumentName:  document.Identity.DocumentName,
		},
		AffectedBlockIds: []string{document.Location.BlockID},
		RestoreState:     restoreStateText(document.RestoreState),
		RestoreQueued:    document.RestoreState == metastore.RestoreStateRestorePending,
		RetryHint: &scrapv1.RetryHint{
			Retryable: true,
			Reason:    "restore_pending",
		},
	})
	if err != nil {
		return st.Err()
	}
	return withDetails.Err()
}

func cryptoUnavailableError(document metastore.Document) error {
	st := status.New(codes.Unavailable, "document bytes require unavailable crypto material")
	withDetails, err := st.WithDetails(&scrapv1.CryptoUnavailableDetail{
		Identity: &scrapv1.DocumentIdentity{
			TenantId:      document.Identity.TenantID,
			TransactionId: document.Identity.TransactionID,
			DocumentName:  document.Identity.DocumentName,
		},
		KeyScope: "backend",
		RetryHint: &scrapv1.RetryHint{
			Retryable: true,
			Reason:    "crypto_unavailable",
		},
	})
	if err != nil {
		return st.Err()
	}
	return withDetails.Err()
}

func restoreStateText(state metastore.RestoreState) string {
	switch state {
	case metastore.RestoreStateHot:
		return "hot"
	case metastore.RestoreStateCold:
		return "cold"
	case metastore.RestoreStateRestorePending:
		return "restore_pending"
	case metastore.RestoreStateCryptoUnavailable:
		return "crypto_unavailable"
	default:
		return "unknown"
	}
}

func isIntegrityFailure(err error) bool {
	return errors.Is(err, blockstore.ErrChecksumMismatch) ||
		errors.Is(err, backend.ErrChecksumMismatch) ||
		errors.Is(err, backend.ErrNotFound) ||
		errors.Is(err, os.ErrNotExist) ||
		errors.Is(err, io.ErrUnexpectedEOF)
}

func integrityEvidenceID(document metastore.Document) string {
	return stableCommandID(
		"integrity-evidence",
		document.Location.BlockID,
		document.Identity.TenantID,
		document.Identity.TransactionID,
		document.Identity.DocumentName,
	)
}

func (a *Application) recordDocumentRepairState(ctx context.Context, document metastore.Document, incidentID string, quarantined bool, now time.Time) error {
	return a.recordRepairState(ctx, document.Identity, repairPhysicalRef(document), incidentID, quarantined, now)
}

func (a *Application) recordPeerRepairState(ctx context.Context, document metastore.Document, replica blockstore.ReplicaRef, quarantined bool, now time.Time) error {
	return a.recordRepairState(ctx, document.Identity, peerRepairPhysicalRef(replica), peerIntegrityEvidenceID(document, replica), quarantined, now)
}

func (a *Application) recordRepairState(ctx context.Context, doc identity.Document, physicalRef, incidentID string, quarantined bool, now time.Time) error {
	return a.authority.RecordRepairState(ctx, metastore.RepairState{
		Identity:    doc,
		PhysicalRef: physicalRef,
		IncidentID:  incidentID,
		Quarantined: quarantined,
		UpdatedAt:   now,
	}, stableCommandID("record-repair-state", incidentID, physicalRef, strconv.FormatBool(quarantined)), now)
}

func repairPhysicalRef(document metastore.Document) string {
	return fmt.Sprintf("local/%s/%d/%d", document.Location.BlockID, document.Location.StoredOffset, document.Location.StoredLength)
}

func peerRepairPhysicalRef(replica blockstore.ReplicaRef) string {
	return fmt.Sprintf("peer/%s/%s/%d/%d", replica.MemberID, replica.BlockID, replica.StoredOffset, replica.StoredLength)
}

func peerIntegrityEvidenceID(document metastore.Document, replica blockstore.ReplicaRef) string {
	return stableCommandID(
		"peer-integrity-evidence",
		document.Identity.TenantID,
		document.Identity.TransactionID,
		document.Identity.DocumentName,
		replica.MemberID,
		replica.BlockID,
		strconv.FormatUint(replica.StoredOffset, 10),
		strconv.FormatUint(replica.StoredLength, 10),
	)
}

func documentToAPI(document metastore.Document) api.DocumentMetadata {
	return api.DocumentMetadata{
		Identity:                    document.Identity,
		DocumentClass:               documentClassToAPI(document.DocumentClass),
		PriorityClass:               priorityClassToAPI(document.PriorityClass),
		ContentType:                 document.ContentType,
		HasContentType:              document.HasContentType,
		Length:                      document.Length,
		LogicalSHA256:               append([]byte(nil), document.LogicalSHA256[:]...),
		DocumentIdentityFingerprint: append([]byte(nil), document.DocumentIdentityFingerprint[:]...),
		CreatedByService:            document.CreatedByService,
		WorkflowStage:               document.WorkflowStage,
		HasWorkflowStage:            document.HasWorkflowStage,
		CreatedAt:                   document.CreatedAt,
		FinalizedAt:                 document.FinalizedAt,
		Availability:                availabilityToAPI(document.Availability),
		LifecycleState:              lifecycleStateToAPI(document.LifecycleState),
		Tags:                        cloneTags(document.Tags),
	}
}

func transactionToAPI(transaction metastore.Transaction) api.TransactionState {
	return api.TransactionState{
		Transaction:            transaction.Identity,
		State:                  transactionStateToAPI(transaction.State),
		DocumentCount:          transaction.DocumentCount,
		PermanentDocumentCount: transaction.PermanentDocumentCount,
		EphemeralDocumentCount: transaction.EphemeralDocumentCount,
		CreatedAt:              transaction.CreatedAt,
		CompletedAt:            cloneTime(transaction.CompletedAt),
		TimeoutAt:              cloneTime(transaction.TimeoutAt),
		Tags:                   cloneTags(transaction.Tags),
	}
}

func documentFilterFromAPI(filter api.DocumentFilter) metastore.DocumentFilter {
	out := metastore.DocumentFilter{
		DocumentNameExact:     filter.DocumentNameExact,
		HasDocumentNameExact:  filter.HasDocumentNameExact,
		DocumentNamePrefix:    filter.DocumentNamePrefix,
		HasDocumentNamePrefix: filter.HasDocumentNamePrefix,
		DocumentClass:         documentClassFromAPI(filter.DocumentClass),
		HasDocumentClass:      filter.HasDocumentClass,
		ContentType:           filter.ContentType,
		HasContentType:        filter.HasContentType,
		WorkflowStage:         filter.WorkflowStage,
		HasWorkflowStage:      filter.HasWorkflowStage,
		CreatedByService:      filter.CreatedByService,
		HasCreatedByService:   filter.HasCreatedByService,
		Tags:                  cloneTags(filter.Tags),
	}
	if filter.CreatedAt != nil {
		out.CreatedAfter = cloneTime(filter.CreatedAt.StartTime)
		out.CreatedBefore = cloneTime(filter.CreatedAt.EndTime)
	}
	return out
}

func documentClassFromAPI(value scrapv1.DocumentClass) metastore.DocumentClass {
	switch value {
	case scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT:
		return metastore.DocumentClassPermanent
	case scrapv1.DocumentClass_DOCUMENT_CLASS_EPHEMERAL:
		return metastore.DocumentClassEphemeral
	default:
		return 0
	}
}

func documentClassToAPI(value metastore.DocumentClass) scrapv1.DocumentClass {
	switch value {
	case metastore.DocumentClassPermanent:
		return scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT
	case metastore.DocumentClassEphemeral:
		return scrapv1.DocumentClass_DOCUMENT_CLASS_EPHEMERAL
	default:
		return scrapv1.DocumentClass_DOCUMENT_CLASS_UNSPECIFIED
	}
}

func priorityClassFromAPI(value scrapv1.PriorityClass) metastore.PriorityClass {
	switch value {
	case scrapv1.PriorityClass_PRIORITY_CLASS_CRITICAL_INGEST:
		return metastore.PriorityClassCriticalIngest
	case scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL:
		return metastore.PriorityClassNormal
	case scrapv1.PriorityClass_PRIORITY_CLASS_BULK:
		return metastore.PriorityClassBulk
	default:
		return 0
	}
}

func priorityClassToAPI(value metastore.PriorityClass) scrapv1.PriorityClass {
	switch value {
	case metastore.PriorityClassCriticalIngest:
		return scrapv1.PriorityClass_PRIORITY_CLASS_CRITICAL_INGEST
	case metastore.PriorityClassNormal:
		return scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL
	case metastore.PriorityClassBulk:
		return scrapv1.PriorityClass_PRIORITY_CLASS_BULK
	default:
		return scrapv1.PriorityClass_PRIORITY_CLASS_UNSPECIFIED
	}
}

func availabilityToAPI(value metastore.Availability) scrapv1.DocumentAvailability {
	switch value {
	case metastore.AvailabilityHot:
		return scrapv1.DocumentAvailability_DOCUMENT_AVAILABILITY_HOT
	case metastore.AvailabilityCold:
		return scrapv1.DocumentAvailability_DOCUMENT_AVAILABILITY_COLD
	case metastore.AvailabilityRestorePending:
		return scrapv1.DocumentAvailability_DOCUMENT_AVAILABILITY_RESTORE_PENDING
	case metastore.AvailabilityCryptoUnavailable:
		return scrapv1.DocumentAvailability_DOCUMENT_AVAILABILITY_CRYPTO_UNAVAILABLE
	case metastore.AvailabilityDegradedRepair:
		return scrapv1.DocumentAvailability_DOCUMENT_AVAILABILITY_DEGRADED_REPAIR
	default:
		return scrapv1.DocumentAvailability_DOCUMENT_AVAILABILITY_UNSPECIFIED
	}
}

func lifecycleStateToAPI(value metastore.LifecycleState) scrapv1.DocumentLifecycleState {
	switch value {
	case metastore.LifecycleStateActive:
		return scrapv1.DocumentLifecycleState_DOCUMENT_LIFECYCLE_STATE_ACTIVE
	case metastore.LifecycleStateTransactionCompleted:
		return scrapv1.DocumentLifecycleState_DOCUMENT_LIFECYCLE_STATE_TRANSACTION_COMPLETED
	case metastore.LifecycleStateTombstoned:
		return scrapv1.DocumentLifecycleState_DOCUMENT_LIFECYCLE_STATE_TOMBSTONED
	case metastore.LifecycleStatePendingReclamation:
		return scrapv1.DocumentLifecycleState_DOCUMENT_LIFECYCLE_STATE_PENDING_RECLAMATION
	default:
		return scrapv1.DocumentLifecycleState_DOCUMENT_LIFECYCLE_STATE_UNSPECIFIED
	}
}

func transactionStateToAPI(value metastore.TransactionStateKind) scrapv1.TransactionStateKind {
	switch value {
	case metastore.TransactionStateOpen:
		return scrapv1.TransactionStateKind_TRANSACTION_STATE_KIND_OPEN
	case metastore.TransactionStateCompleted:
		return scrapv1.TransactionStateKind_TRANSACTION_STATE_KIND_COMPLETED
	case metastore.TransactionStateTimedOut:
		return scrapv1.TransactionStateKind_TRANSACTION_STATE_KIND_TIMED_OUT
	default:
		return scrapv1.TransactionStateKind_TRANSACTION_STATE_KIND_UNSPECIFIED
	}
}

func documentIdentityFingerprint(parts ...string) [16]byte {
	hasher := sha256.New()
	for i, part := range parts {
		if i > 0 {
			_, _ = hasher.Write([]byte{0})
		}
		_, _ = hasher.Write([]byte(part))
	}
	var out [16]byte
	copy(out[:], hasher.Sum(nil))
	return out
}

func commitDocumentCommandID(document metastore.Document) string {
	return stableCommandID(
		"commit-document",
		document.Identity.TenantID,
		document.Identity.TransactionID,
		document.Identity.DocumentName,
		document.ClientIdempotencyKey,
	)
}

func completeTransactionCommandID(transaction identity.Transaction, completedAt time.Time, tags map[string]string) string {
	parts := []string{
		transaction.TenantID,
		transaction.TransactionID,
		completedAt.Format(time.RFC3339Nano),
	}
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts = append(parts, key, tags[key])
	}
	return stableCommandID("complete-transaction", parts...)
}

func uploadIntentForDocument(document metastore.Document) metastore.UploadIntent {
	blockID := document.Location.BlockID
	return metastore.UploadIntent{
		BlockID:           blockID,
		BackendObjectKey:  "blocks/" + blockID + ".blk",
		IndexObjectKey:    "blocks/" + blockID + ".idx",
		EnvelopeObjectKey: "blocks/" + blockID + ".env",
		State:             metastore.UploadStatePending,
	}
}

func recordUploadIntentCommandID(intent metastore.UploadIntent) string {
	return stableCommandID(
		"record-upload-intent",
		intent.BlockID,
		intent.BackendObjectKey,
		intent.IndexObjectKey,
		intent.EnvelopeObjectKey,
	)
}

func stableCommandID(prefix string, parts ...string) string {
	hasher := sha256.New()
	_, _ = hasher.Write([]byte(prefix))
	for _, part := range parts {
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write([]byte(part))
	}
	sum := hasher.Sum(nil)
	return prefix + ":" + hex.EncodeToString(sum[:16])
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

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
