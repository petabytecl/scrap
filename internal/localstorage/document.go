package localstorage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/petabytecl/scrap/internal/appstatus"
	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/blockstore"
	"github.com/petabytecl/scrap/internal/cryptoenv"
	storagev1 "github.com/petabytecl/scrap/internal/gen/scrap/storage/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/observe"
	"github.com/petabytecl/scrap/internal/raftmeta"
	"github.com/petabytecl/scrap/internal/replication"
	"github.com/petabytecl/scrap/internal/safeconv"
	"github.com/petabytecl/scrap/internal/storageapp"
	"github.com/petabytecl/scrap/internal/storageformat"
)

const backendReadChunkSize = 1024 * 1024

var (
	ErrCorruptVerificationWindow = errors.New("localstorage: corrupt verification window")
	ErrWritePipelineInvariant    = errors.New("localstorage: write pipeline invariant violated")
)

func (a *verificationEngine) startByteServingVerifier(ctx context.Context) {
	verifierCtx, cancel := context.WithCancel(ctx)
	a.byteServingCancel = cancel
	go a.runByteServingVerifier(verifierCtx)
}

func (a *verificationEngine) runByteServingVerifier(ctx context.Context) {
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

func (a *verificationEngine) waitByteServingReady(ctx context.Context) error {
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
func (a *verificationEngine) verifyLocalRefsAndQueueRepairs(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	documents, err := a.metadata.ListDocuments(metastore.DocumentFilter{})
	if err != nil {
		return err
	}
	for _, document := range documents {
		if err := a.verifyLocalDocumentRefAndQueueRepair(ctx, document); err != nil {
			return err
		}
	}
	return nil
}

func (a *verificationEngine) verifyLocalDocumentRefAndQueueRepair(ctx context.Context, document metastore.Document) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !document.IsHot() {
		return nil
	}
	length := document.Length
	if err := a.blocks.VerifyRange(blockstoreRecord(document.Location), 0, &length); err == nil {
		return nil
	} else if !isIntegrityFailure(err) {
		return err
	}
	incidentID := integrityEvidenceID(document)
	if quarantined, err := a.repairStateIsQuarantined(document, incidentID); err != nil || quarantined {
		return err
	}
	return a.recordDocumentRepairState(ctx, document, incidentID, true, a.now())
}

func (a *verificationEngine) repairStateIsQuarantined(document metastore.Document, incidentID string) (bool, error) {
	state, err := a.metadata.GetRepairState(document.Identity, incidentID)
	if errors.Is(err, metastore.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return state.Quarantined, nil
}

func (a *transactionCoordinator) WriteDocument(ctx context.Context, init storageapp.WriteDocumentInit, chunks storageapp.ChunkReader) (storageapp.WriteDocumentResult, error) {
	if err := a.authority.EnsureWriteReady(ctx); err != nil {
		return storageapp.WriteDocumentResult{}, mapError(err)
	}
	if result, replayed, err := a.replayExistingDocument(ctx, init, chunks); err != nil || replayed {
		return result, err
	}
	if err := a.requireWriteAdmission(ctx, init.ExpectedLength); err != nil {
		return storageapp.WriteDocumentResult{}, err
	}

	record, err := a.appendValidatedDocument(ctx, init, chunks)
	if err != nil {
		return storageapp.WriteDocumentResult{}, err
	}

	now := a.now()
	document := newDocument(init, record, now)
	document, replicationResult, err := a.commitDocumentWrite(ctx, document, now)
	if err != nil {
		return storageapp.WriteDocumentResult{}, err
	}
	return writeDocumentResult(document, replicationResult)
}

func (a *transactionCoordinator) replayExistingDocument(ctx context.Context, init storageapp.WriteDocumentInit, chunks storageapp.ChunkReader) (storageapp.WriteDocumentResult, bool, error) {
	existing, err := a.metadata.HeadDocument(init.Identity)
	if errors.Is(err, metastore.ErrNotFound) {
		return storageapp.WriteDocumentResult{}, false, nil
	}
	if err != nil {
		return storageapp.WriteDocumentResult{}, false, mapError(err)
	}
	body, err := drainChunks(chunks)
	if err != nil {
		return storageapp.WriteDocumentResult{}, true, err
	}
	result, err := a.replayExisting(ctx, init, existing, body)
	return result, true, err
}

func (a *transactionCoordinator) appendValidatedDocument(ctx context.Context, init storageapp.WriteDocumentInit, chunks storageapp.ChunkReader) (blockstore.Record, error) {
	record, err := a.blocks.AppendValidated(ctx, &chunkReader{chunks: chunks}, func(record blockstore.Record) error {
		return validateWrittenRecord(init, record)
	})
	if err != nil {
		return blockstore.Record{}, mapError(err)
	}
	if a.writeFaults.afterBlockSync == nil {
		return record, nil
	}
	if err := a.writeFaults.afterBlockSync(record); err != nil {
		return blockstore.Record{}, err
	}
	return record, nil
}

func validateWrittenRecord(init storageapp.WriteDocumentInit, record blockstore.Record) error {
	if init.ExpectedLength != nil && *init.ExpectedLength != record.StoredLength {
		return appstatus.Errorf(appstatus.CodeInvalidArgument, "expected length %d does not match written length %d", *init.ExpectedLength, record.StoredLength)
	}
	if len(init.ExpectedSHA256) > 0 && !bytes.Equal(init.ExpectedSHA256, record.LogicalSHA256[:]) {
		return appstatus.New(appstatus.CodeInvalidArgument, "expected sha256 does not match written bytes")
	}
	return nil
}

func newDocument(init storageapp.WriteDocumentInit, record blockstore.Record, now time.Time) metastore.Document {
	return metastore.Document{
		Identity:                    init.Identity,
		DocumentClass:               documentClassFromStorageApp(init.DocumentClass),
		PriorityClass:               priorityClassFromStorageApp(init.PriorityClass),
		ContentType:                 init.ContentType,
		HasContentType:              init.ContentType != "",
		Length:                      record.StoredLength,
		LogicalSHA256:               record.LogicalSHA256,
		StoredSHA256:                record.StoredSHA256,
		DocumentIdentityFingerprint: documentIdentityFingerprint(init.Identity.TenantID, init.Identity.TransactionID, init.Identity.DocumentName),
		CreatedByService:            init.CreatedByService,
		WorkflowStage:               init.WorkflowStage,
		HasWorkflowStage:            init.WorkflowStage != "",
		CreatedAt:                   now,
		FinalizedAt:                 now,
		Availability:                metastore.AvailabilityHot,
		LifecycleState:              metastore.LifecycleStateActive,
		Tags:                        cloneTags(init.Tags),
		Location:                    metastoreLocation(record),
		ClientIdempotencyKey:        init.ClientIdempotencyKey,
		HasClientIdempotencyKey:     init.ClientIdempotencyKey != "",
	}
}

func (a *transactionCoordinator) commitDocumentWrite(ctx context.Context, document metastore.Document, now time.Time) (metastore.Document, replication.Result, error) {
	if err := a.prepare.Append(document); err != nil {
		return metastore.Document{}, replication.Result{}, err
	}
	if err := runDocumentWriteFault(a.writeFaults.afterPrepareSync, document); err != nil {
		return metastore.Document{}, replication.Result{}, err
	}
	replicationResult, err := a.preparePeerReplicas(ctx, document)
	if err != nil {
		return metastore.Document{}, replication.Result{}, mapError(err)
	}
	if err := validateWriteReplicationResult(replicationResult); err != nil {
		return metastore.Document{}, replication.Result{}, mapError(err)
	}
	document.Location.Replicas = replication.ReplicaRefs(replicationResult.Receipts)
	if err := a.authority.CommitDocument(ctx, document, commitDocumentCommandID(document), now); err != nil {
		return metastore.Document{}, replication.Result{}, mapError(err)
	}
	if err := runDocumentWriteFault(a.writeFaults.afterMetadataApply, document); err != nil {
		return metastore.Document{}, replication.Result{}, err
	}
	uploadIntent := uploadIntentForDocument(document)
	if err := a.authority.RecordUploadIntent(ctx, uploadIntent, recordUploadIntentCommandID(uploadIntent), now); err != nil {
		return metastore.Document{}, replication.Result{}, mapError(err)
	}
	if err := runDocumentWriteFault(a.writeFaults.beforeACK, document); err != nil {
		return metastore.Document{}, replication.Result{}, err
	}
	return document, replicationResult, nil
}

func validateWriteReplicationResult(result replication.Result) error {
	switch {
	case result.DesiredReplicaCount < 1:
		return fmt.Errorf("%w: desired replica count %d must include local replica", ErrWritePipelineInvariant, result.DesiredReplicaCount)
	case result.RequiredReplicaCount < 1:
		return fmt.Errorf("%w: required replica count %d must include local replica", ErrWritePipelineInvariant, result.RequiredReplicaCount)
	case result.RequiredReplicaCount > result.DesiredReplicaCount:
		return fmt.Errorf("%w: required replicas %d exceed desired replicas %d", ErrWritePipelineInvariant, result.RequiredReplicaCount, result.DesiredReplicaCount)
	case result.AchievedReplicaCount < 1:
		return fmt.Errorf("%w: achieved replica count %d must include local replica", ErrWritePipelineInvariant, result.AchievedReplicaCount)
	case result.AchievedReplicaCount < result.RequiredReplicaCount:
		return fmt.Errorf("%w: achieved replicas %d below required replicas %d", ErrWritePipelineInvariant, result.AchievedReplicaCount, result.RequiredReplicaCount)
	case result.AchievedReplicaCount > result.DesiredReplicaCount:
		return fmt.Errorf("%w: achieved replicas %d exceed desired replicas %d", ErrWritePipelineInvariant, result.AchievedReplicaCount, result.DesiredReplicaCount)
	case len(result.Receipts) != result.AchievedReplicaCount-1:
		return fmt.Errorf("%w: peer receipts %d do not match achieved peer replicas %d", ErrWritePipelineInvariant, len(result.Receipts), result.AchievedReplicaCount-1)
	default:
		return nil
	}
}

func runDocumentWriteFault(fault func(metastore.Document) error, document metastore.Document) error {
	if fault == nil {
		return nil
	}
	return fault(document)
}

func writeDocumentResult(document metastore.Document, replicationResult replication.Result) (storageapp.WriteDocumentResult, error) {
	desiredReplicaCount, err := safeconv.IntToUint32("desired replica count", replicationResult.DesiredReplicaCount)
	if err != nil {
		return storageapp.WriteDocumentResult{}, err
	}
	achievedReplicaCount, err := safeconv.IntToUint32("achieved replica count", replicationResult.AchievedReplicaCount)
	if err != nil {
		return storageapp.WriteDocumentResult{}, err
	}
	return storageapp.WriteDocumentResult{
		Metadata:             documentToStorageApp(document),
		DesiredReplicaCount:  desiredReplicaCount,
		AchievedReplicaCount: achievedReplicaCount,
		RepairRequired:       replicationResult.RepairRequired,
		IdempotentReplay:     false,
	}, nil
}

func (a *transactionCoordinator) preparePeerReplicas(ctx context.Context, document metastore.Document) (replication.Result, error) {
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
			StoredSHA256:  requested.StoredSHA256,
			Frames:        append([]blockstore.FrameRecord(nil), requested.Frames...),
		}
		return a.blocks.ReadRange(ctx, record, 0, nil, writer)
	})
	return replication.PrepareDocument(ctx, prepared, source, a.peerPrepareTargets, a.peerPreparePolicy)
}

func (a *documentApplication) HeadDocument(ctx context.Context, req storageapp.HeadDocumentRequest) (storageapp.DocumentMetadata, error) {
	if err := a.authority.ReadFresh(ctx); err != nil {
		return storageapp.DocumentMetadata{}, mapError(err)
	}
	document, err := a.metadata.HeadDocument(req.Identity)
	if err != nil {
		return storageapp.DocumentMetadata{}, mapError(err)
	}
	return documentToStorageApp(document), nil
}

func (a *documentApplication) ReadDocument(ctx context.Context, req storageapp.ReadDocumentRequest, sender storageapp.ReadDocumentSender) error {
	document, err := a.readableDocument(ctx, req.Identity)
	if err != nil {
		return mapError(err)
	}
	if err := unavailableReadStateError(document); err != nil {
		return err
	}
	selectedRange, err := selectedDocumentRange(document.Length, req.Range)
	if err != nil {
		return mapError(blockstore.ErrInvalidRange)
	}
	readLength := *selectedRange.Length
	location := blockstoreRecord(document.Location)
	if err := a.blocks.VerifyRange(location, selectedRange.Offset, &readLength); err != nil {
		return a.readDocumentFromBackend(ctx, document, selectedRange, sender, err)
	}
	if err := sender.SendMetadata(storageapp.ReadDocumentMetadata{
		Metadata:      documentToStorageApp(document),
		SelectedRange: selectedRange,
		Source:        storageapp.StorageSourceLocal,
	}); err != nil {
		return err
	}
	return mapError(a.blocks.ReadRange(ctx, location, selectedRange.Offset, &readLength, senderWriter{sender: sender}))
}

func (a *documentApplication) readableDocument(ctx context.Context, documentIdentity identity.Document) (metastore.Document, error) {
	if err := a.authority.ReadFresh(ctx); err != nil {
		return metastore.Document{}, err
	}
	if err := a.verifier.waitByteServingReady(ctx); err != nil {
		return metastore.Document{}, err
	}
	document, err := a.metadata.HeadDocument(documentIdentity)
	if err != nil {
		return metastore.Document{}, err
	}
	return document, nil
}

func selectedDocumentRange(documentLength uint64, requested *storageapp.ReadRange) (storageapp.ReadRange, error) {
	offset := uint64(0)
	if requested != nil {
		offset = requested.Offset
	}
	if offset > documentLength {
		return storageapp.ReadRange{}, blockstore.ErrInvalidRange
	}
	readLength := documentLength - offset
	if requested != nil && requested.Length != nil {
		if *requested.Length > documentLength-offset {
			return storageapp.ReadRange{}, blockstore.ErrInvalidRange
		}
		readLength = *requested.Length
	}
	return storageapp.ReadRange{Offset: offset, Length: &readLength}, nil
}

func (a *documentApplication) readDocumentFromBackend(ctx context.Context, document metastore.Document, selectedRange storageapp.ReadRange, sender storageapp.ReadDocumentSender, localErr error) error {
	data, err := a.backendReadFallbackData(ctx, document, selectedRange, localErr)
	if err != nil {
		return err
	}
	if err := a.recordBackendFallbackRepair(ctx, document, localErr); err != nil {
		return err
	}
	if err := sender.SendMetadata(storageapp.ReadDocumentMetadata{
		Metadata:      documentToStorageApp(document),
		SelectedRange: selectedRange,
		Source:        storageapp.StorageSourceBackend,
	}); err != nil {
		return err
	}
	return sendReadChunks(sender, data)
}

func (a *documentApplication) backendReadFallbackData(ctx context.Context, document metastore.Document, selectedRange storageapp.ReadRange, localErr error) ([]byte, error) {
	if a.backendStore == nil {
		return nil, readIntegrityError(document, []string{"local"}, localErr)
	}
	intent, err := a.metadata.GetUploadIntent(document.Location.BlockID)
	if err != nil || intent.State != metastore.UploadStateUploaded {
		return nil, readIntegrityError(document, []string{"local"}, localErr)
	}
	data, err := a.verifier.readVerifiedBackendRange(ctx, document, intent, selectedRange)
	if err != nil {
		return nil, backendReadFallbackError(document, err)
	}
	return data, nil
}

func backendReadFallbackError(document metastore.Document, err error) error {
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

func (a *documentApplication) recordBackendFallbackRepair(ctx context.Context, document metastore.Document, localErr error) error {
	if !isIntegrityFailure(localErr) {
		return nil
	}
	return a.recordDocumentRepairState(ctx, document, integrityEvidenceID(document), true, a.now())
}

func sendReadChunks(sender storageapp.ReadDocumentSender, data []byte) error {
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

func (a *verificationEngine) readVerifiedBackendRange(ctx context.Context, document metastore.Document, intent metastore.UploadIntent, selectedRange storageapp.ReadRange) ([]byte, error) {
	if selectedRange.Length == nil {
		return nil, blockstore.ErrInvalidRange
	}
	readLength := *selectedRange.Length
	if readLength == 0 {
		return nil, nil
	}
	envelope, err := a.openBackendEnvelope(ctx, intent)
	if err != nil {
		return nil, err
	}
	if envelope.encrypted() {
		return a.readVerifiedEncryptedBackendRange(ctx, document, intent, selectedRange, envelope)
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

func (a *verificationEngine) verifyBackendEnvelope(ctx context.Context, intent metastore.UploadIntent) error {
	_, err := a.openBackendEnvelope(ctx, intent)
	return err
}

type backendEnvelopeMaterial struct {
	record       *storagev1.EnvelopeRecord
	plaintextDEK []byte
}

type verifiedEncryptedBackendBlock struct {
	reader io.ReadCloser
	length uint64
	sha256 [32]byte
}

func (m backendEnvelopeMaterial) encrypted() bool {
	return m.record != nil && m.record.GetAeadAlgorithm() != "" && m.record.GetAeadAlgorithm() != "none"
}

func (a *verificationEngine) openBackendEnvelope(ctx context.Context, intent metastore.UploadIntent) (backendEnvelopeMaterial, error) {
	if intent.EnvelopeObjectKey == "" {
		return backendEnvelopeMaterial{}, nil
	}
	var data bytes.Buffer
	if err := a.backendStore.ReadObjectRange(ctx, intent.EnvelopeObjectKey, backend.Range{}, &data); err != nil {
		return backendEnvelopeMaterial{}, err
	}
	envelope, err := storageformat.UnmarshalEnvelopeRecord(data.Bytes())
	if err != nil {
		return backendEnvelopeMaterial{}, fmt.Errorf("%w: backend envelope %s is invalid: %w", backend.ErrChecksumMismatch, intent.EnvelopeObjectKey, err)
	}
	if envelope.GetAeadAlgorithm() == "none" {
		return backendEnvelopeMaterial{record: envelope}, nil
	}
	if a.envelopeTransit == nil {
		return backendEnvelopeMaterial{}, fmt.Errorf("%w: backend envelope %s requires key %s", cryptoenv.ErrUnavailable, intent.EnvelopeObjectKey, envelope.GetKeyId())
	}
	plaintextDEK, err := cryptoenv.UnwrapEnvelopeDataKey(ctx, a.envelopeTransit, envelope)
	if err != nil {
		return backendEnvelopeMaterial{}, err
	}
	return backendEnvelopeMaterial{record: envelope, plaintextDEK: plaintextDEK}, nil
}

func (a *verificationEngine) readVerifiedEncryptedBackendRange(ctx context.Context, document metastore.Document, intent metastore.UploadIntent, selectedRange storageapp.ReadRange, envelope backendEnvelopeMaterial) ([]byte, error) {
	index, err := a.readBackendBlockIndex(ctx, intent)
	if err != nil {
		return nil, err
	}
	selectedStart, selectedEnd, err := encryptedSelectedRange(document, selectedRange)
	if err != nil {
		return nil, err
	}
	frames, err := encryptedFrameWindow(index.GetFrames(), selectedStart, selectedEnd)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	for _, frame := range frames {
		plaintext, err := a.readVerifiedEncryptedBackendFrame(ctx, intent, envelope, frame)
		if err != nil {
			return nil, err
		}
		if err := writeSelectedFramePlaintext(&out, plaintext, frame, selectedStart, selectedEnd); err != nil {
			return nil, err
		}
	}
	outLength, err := safeconv.IntToUint64("encrypted backend read length", out.Len())
	if err != nil {
		return nil, err
	}
	if outLength != *selectedRange.Length {
		return nil, io.ErrUnexpectedEOF
	}
	return append([]byte(nil), out.Bytes()...), nil
}

func encryptedSelectedRange(document metastore.Document, selectedRange storageapp.ReadRange) (uint64, uint64, error) {
	if selectedRange.Length == nil {
		return 0, 0, blockstore.ErrInvalidRange
	}
	selectedStart := document.Location.StoredOffset + selectedRange.Offset
	selectedEnd := selectedStart + *selectedRange.Length
	if selectedEnd < selectedStart {
		return 0, 0, blockstore.ErrInvalidRange
	}
	return selectedStart, selectedEnd, nil
}

func writeSelectedFramePlaintext(out *bytes.Buffer, plaintext []byte, frame *storagev1.FrameChecksumRecord, selectedStart, selectedEnd uint64) error {
	frameStart := frame.GetPlaintextOffset()
	frameEnd := frameStart + frame.GetPlaintextLength()
	if frameEnd < frameStart {
		return blockstore.ErrInvalidRange
	}
	copyStart := maxUint64(selectedStart, frameStart)
	copyEnd := minUint64(selectedEnd, frameEnd)
	if copyStart >= copyEnd {
		return nil
	}
	start := copyStart - frameStart
	end := copyEnd - frameStart
	if end > uint64(len(plaintext)) {
		return io.ErrUnexpectedEOF
	}
	out.Write(plaintext[start:end])
	return nil
}

func (a *verificationEngine) readBackendBlockIndex(ctx context.Context, intent metastore.UploadIntent) (*storagev1.BlockIndex, error) {
	if intent.IndexObjectKey == "" {
		return nil, fmt.Errorf("%w: backend block index is required for encrypted block %s", backend.ErrChecksumMismatch, intent.BackendObjectKey)
	}
	var data bytes.Buffer
	if err := a.backendStore.ReadObjectRange(ctx, intent.IndexObjectKey, backend.Range{}, &data); err != nil {
		return nil, err
	}
	index, err := storageformat.UnmarshalBlockIndex(data.Bytes())
	if err != nil {
		return nil, fmt.Errorf("%w: backend index %s is invalid: %w", backend.ErrChecksumMismatch, intent.IndexObjectKey, err)
	}
	return index, nil
}

func encryptedFrameWindow(frames []*storagev1.FrameChecksumRecord, selectedStart, selectedEnd uint64) ([]*storagev1.FrameChecksumRecord, error) {
	var selected []*storagev1.FrameChecksumRecord
	coveredUntil := selectedStart
	for _, frame := range frames {
		frameStart := frame.GetPlaintextOffset()
		frameEnd := frameStart + frame.GetPlaintextLength()
		if frameEnd < frameStart {
			return nil, blockstore.ErrInvalidRange
		}
		if frameEnd <= selectedStart {
			continue
		}
		if frameStart >= selectedEnd {
			break
		}
		if frameStart > coveredUntil {
			return nil, io.ErrUnexpectedEOF
		}
		selected = append(selected, frame)
		if frameEnd > coveredUntil {
			coveredUntil = frameEnd
		}
		if coveredUntil >= selectedEnd {
			return selected, nil
		}
	}
	return nil, io.ErrUnexpectedEOF
}

func (a *verificationEngine) readVerifiedEncryptedBackendFrame(ctx context.Context, intent metastore.UploadIntent, envelope backendEnvelopeMaterial, frame *storagev1.FrameChecksumRecord) ([]byte, error) {
	if frame.GetEncryptionMode() != storagev1.EncryptionMode_ENCRYPTION_MODE_AES_256_GCM {
		return nil, fmt.Errorf("%w: encrypted backend frame %d has unsupported encryption mode %s", backend.ErrChecksumMismatch, frame.GetFrameIndex(), frame.GetEncryptionMode())
	}
	storedLength := frame.GetStoredLength()
	var ciphertext bytes.Buffer
	if err := a.backendStore.ReadObjectRange(ctx, intent.BackendObjectKey, backend.Range{
		Offset: frame.GetStoredOffset(),
		Length: &storedLength,
	}, &ciphertext); err != nil {
		return nil, err
	}
	if got := sha256.Sum256(ciphertext.Bytes()); !bytes.Equal(got[:], frame.GetStoredSha256()) {
		return nil, backend.ErrChecksumMismatch
	}
	plaintext, err := cryptoenv.OpenPayloadFrame(
		envelope.plaintextDEK,
		envelope.record,
		frame.GetFrameIndex(),
		frame.GetPlaintextOffset(),
		frame.GetPlaintextLength(),
		ciphertext.Bytes(),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: decrypt backend frame %d: %w", backend.ErrChecksumMismatch, frame.GetFrameIndex(), err)
	}
	if got := sha256.Sum256(plaintext); !bytes.Equal(got[:], frame.GetPlaintextSha256()) {
		return nil, blockstore.ErrChecksumMismatch
	}
	return plaintext, nil
}

func (a *verificationEngine) openVerifiedEncryptedBackendBlock(ctx context.Context, intent metastore.UploadIntent, envelope backendEnvelopeMaterial) (verifiedEncryptedBackendBlock, error) {
	index, err := a.readBackendBlockIndex(ctx, intent)
	if err != nil {
		return verifiedEncryptedBackendBlock{}, err
	}
	temp, err := a.createScratchFile("backend-restore", "encrypted-backend-block-*.blk")
	if err != nil {
		return verifiedEncryptedBackendBlock{}, err
	}
	tempPath := temp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = temp.Close()
			removeScratchFile(tempPath)
		}
	}()
	length, sum, err := a.writeVerifiedEncryptedBackendBlockFrames(ctx, temp, intent, envelope, index.GetFrames())
	if err != nil {
		return verifiedEncryptedBackendBlock{}, err
	}
	if err := temp.Sync(); err != nil {
		return verifiedEncryptedBackendBlock{}, err
	}
	if _, err := temp.Seek(0, io.SeekStart); err != nil {
		return verifiedEncryptedBackendBlock{}, err
	}
	committed = true
	return verifiedEncryptedBackendBlock{
		reader: removeOnCloseScratchFile{File: temp, path: tempPath},
		length: length,
		sha256: sum,
	}, nil
}

func (a *verificationEngine) writeVerifiedEncryptedBackendBlockFrames(
	ctx context.Context,
	writer io.Writer,
	intent metastore.UploadIntent,
	envelope backendEnvelopeMaterial,
	frames []*storagev1.FrameChecksumRecord,
) (uint64, [32]byte, error) {
	hasher := sha256.New()
	coveredUntil := uint64(0)
	for _, frame := range frames {
		nextCoveredUntil, err := a.writeVerifiedEncryptedBackendBlockFrame(ctx, writer, hasher, intent, envelope, frame, coveredUntil)
		if err != nil {
			return 0, [32]byte{}, err
		}
		coveredUntil = nextCoveredUntil
	}
	var sum [32]byte
	copy(sum[:], hasher.Sum(nil))
	return coveredUntil, sum, nil
}

func (a *verificationEngine) writeVerifiedEncryptedBackendBlockFrame(
	ctx context.Context,
	writer io.Writer,
	hasher hash.Hash,
	intent metastore.UploadIntent,
	envelope backendEnvelopeMaterial,
	frame *storagev1.FrameChecksumRecord,
	coveredUntil uint64,
) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if frame.GetPlaintextOffset() != coveredUntil {
		return 0, io.ErrUnexpectedEOF
	}
	plaintext, err := a.readVerifiedEncryptedBackendFrame(ctx, intent, envelope, frame)
	if err != nil {
		return 0, err
	}
	written, err := writer.Write(plaintext)
	if err != nil {
		return 0, err
	}
	if written != len(plaintext) {
		return 0, io.ErrShortWrite
	}
	_, _ = hasher.Write(plaintext)
	nextCoveredUntil := coveredUntil + frame.GetPlaintextLength()
	if nextCoveredUntil < coveredUntil {
		return 0, blockstore.ErrInvalidRange
	}
	return nextCoveredUntil, nil
}

type removeOnCloseScratchFile struct {
	*os.File
	path string
}

func (f removeOnCloseScratchFile) Close() error {
	closeErr := f.File.Close()
	removeScratchFile(f.path)
	return closeErr
}

func removeScratchFile(path string) {
	// #nosec G703 -- path comes directly from os.CreateTemp in the application scratch directory.
	_ = os.Remove(path)
}

func verificationWindow(record metastore.Location, offset, length uint64) (uint64, uint64, error) {
	if offset+length < offset || offset+length > record.StoredLength {
		return 0, 0, blockstore.ErrInvalidRange
	}
	if len(record.Frames) == 0 {
		return record.StoredOffset, record.StoredOffset + record.StoredLength, nil
	}
	selectedStart := record.StoredOffset + offset
	selectedEnd := selectedStart + length
	window := verificationFrameWindow{}
	for _, frame := range record.Frames {
		var err error
		window, err = includeVerificationFrame(frame, selectedStart, selectedEnd, window)
		if err != nil {
			return 0, 0, err
		}
	}
	if !window.initialized || window.start > selectedStart || window.end < selectedEnd {
		return 0, 0, io.ErrUnexpectedEOF
	}
	return window.start, window.end, nil
}

type verificationFrameWindow struct {
	start       uint64
	end         uint64
	initialized bool
}

func includeVerificationFrame(frame metastore.FrameRecord, selectedStart, selectedEnd uint64, window verificationFrameWindow) (verificationFrameWindow, error) {
	frameStart := frame.SegmentOffset
	frameEnd := frame.SegmentOffset + frame.SegmentLength
	if frameEnd < frameStart {
		return window, blockstore.ErrInvalidRange
	}
	if frameEnd <= selectedStart || frameStart >= selectedEnd {
		return window, nil
	}
	if !window.initialized || frameStart < window.start {
		window.start = frameStart
	}
	if frameEnd > window.end {
		window.end = frameEnd
	}
	window.initialized = true
	return window, nil
}

func verifyFetchedBackendWindow(record metastore.Location, offset, length, verifyStart uint64, data []byte) (err error) {
	defer func() {
		observe.RecordVerification(verificationOutcome(err))
	}()

	if len(record.Frames) == 0 {
		return verifyFetchedBackendDocument(record, verifyStart, data)
	}
	selectedStart := record.StoredOffset + offset
	selectedEnd := selectedStart + length
	for _, frame := range record.Frames {
		if err := verifyFetchedBackendFrame(frame, selectedStart, selectedEnd, verifyStart, data); err != nil {
			return err
		}
	}
	return nil
}

func verifyFetchedBackendDocument(record metastore.Location, verifyStart uint64, data []byte) error {
	if verifyStart != record.StoredOffset {
		return fmt.Errorf(
			"localstorage: verification window start %d does not match document start %d: %w",
			verifyStart,
			record.StoredOffset,
			ErrCorruptVerificationWindow,
		)
	}
	if uint64(len(data)) != record.StoredLength {
		return fmt.Errorf(
			"localstorage: verification window length %d does not match fetched data length %d: %w",
			record.StoredLength,
			len(data),
			ErrCorruptVerificationWindow,
		)
	}
	got := sha256.Sum256(data)
	if got != record.LogicalSHA256 {
		return blockstore.ErrChecksumMismatch
	}
	return nil
}

func verifyFetchedBackendFrame(frame metastore.FrameRecord, selectedStart, selectedEnd, verifyStart uint64, data []byte) error {
	frameStart := frame.SegmentOffset
	frameEnd := frame.SegmentOffset + frame.SegmentLength
	if frameEnd < frameStart {
		return blockstore.ErrInvalidRange
	}
	if frameEnd <= selectedStart || frameStart >= selectedEnd {
		return nil
	}
	start, end, err := verificationFrameDataRange(frameStart, frameEnd, frame.SegmentLength, verifyStart, uint64(len(data)))
	if err != nil {
		return err
	}
	got := sha256.Sum256(data[start:end])
	if got != frame.SHA256 {
		return blockstore.ErrChecksumMismatch
	}
	return nil
}

func verificationFrameDataRange(frameStart, frameEnd, frameLength, verifyStart, dataLength uint64) (uint64, uint64, error) {
	if frameStart < verifyStart {
		return 0, 0, fmt.Errorf(
			"localstorage: verification window start %d exceeds frame start %d: %w",
			verifyStart,
			frameStart,
			ErrCorruptVerificationWindow,
		)
	}
	start := frameStart - verifyStart
	if start > dataLength || frameLength > dataLength-start {
		return 0, 0, fmt.Errorf(
			"localstorage: verification window frame [%d,%d) exceeds fetched data length %d from start %d: %w",
			frameStart,
			frameEnd,
			dataLength,
			verifyStart,
			ErrCorruptVerificationWindow,
		)
	}
	return start, start + frameLength, nil
}

func maxUint64(left, right uint64) uint64 {
	if left > right {
		return left
	}
	return right
}

func minUint64(left, right uint64) uint64 {
	if left < right {
		return left
	}
	return right
}

func verificationOutcome(err error) string {
	if err == nil {
		return observe.VerificationOutcomeMatch
	}
	if errors.Is(err, blockstore.ErrChecksumMismatch) ||
		errors.Is(err, blockstore.ErrInvalidRange) ||
		errors.Is(err, io.ErrUnexpectedEOF) {
		return observe.VerificationOutcomeMismatch
	}
	if errors.Is(err, ErrCorruptVerificationWindow) {
		return observe.VerificationOutcomeError
	}
	return observe.VerificationOutcomeSkipped
}

func (a *documentApplication) FindDocuments(ctx context.Context, req storageapp.FindDocumentsRequest) (storageapp.FindDocumentsResult, error) {
	if err := a.authority.ReadFresh(ctx); err != nil {
		return storageapp.FindDocumentsResult{}, mapError(err)
	}
	documents, err := a.metadata.FindDocuments(req.Transaction, documentFilterFromStorageApp(req.Filter))
	if err != nil {
		return storageapp.FindDocumentsResult{}, mapError(err)
	}
	result := storageapp.FindDocumentsResult{
		Documents: make([]storageapp.DocumentMetadata, 0, len(documents)),
	}
	for _, document := range documents {
		result.Documents = append(result.Documents, documentToStorageApp(document))
	}
	return result, nil
}

func (a *transactionCoordinator) CompleteTransaction(ctx context.Context, req storageapp.CompleteTransactionRequest) (storageapp.TransactionState, error) {
	completedAt := a.now()
	transaction, err := a.authority.CompleteTransaction(ctx, req.Transaction, completedAt, cloneTags(req.Tags), completeTransactionCommandID(req.Transaction, req.Tags))
	if err != nil {
		return storageapp.TransactionState{}, mapError(err)
	}
	return transactionToStorageApp(transaction), nil
}

func (a *transactionCoordinator) GetTransaction(ctx context.Context, req storageapp.GetTransactionRequest) (storageapp.TransactionState, error) {
	if err := a.authority.ReadFresh(ctx); err != nil {
		return storageapp.TransactionState{}, mapError(err)
	}
	transaction, err := a.metadata.GetTransaction(req.Transaction)
	if err != nil {
		return storageapp.TransactionState{}, mapError(err)
	}
	return transactionToStorageApp(transaction), nil
}

func (a *transactionCoordinator) replayExisting(ctx context.Context, init storageapp.WriteDocumentInit, existing metastore.Document, body drainedBody) (storageapp.WriteDocumentResult, error) {
	if !validExistingReplay(init, existing, body) {
		return storageapp.WriteDocumentResult{}, mapError(metastore.ErrConflict)
	}
	intent := uploadIntentForDocument(existing)
	if err := a.ensureReplayUploadIntent(ctx, intent); err != nil {
		return storageapp.WriteDocumentResult{}, mapError(err)
	}
	return storageapp.WriteDocumentResult{
		Metadata:             documentToStorageApp(existing),
		DesiredReplicaCount:  1,
		AchievedReplicaCount: 1,
		IdempotentReplay:     true,
	}, nil
}

func validExistingReplay(init storageapp.WriteDocumentInit, existing metastore.Document, body drainedBody) bool {
	if !existing.HasClientIdempotencyKey || existing.ClientIdempotencyKey == "" || existing.ClientIdempotencyKey != init.ClientIdempotencyKey {
		return false
	}
	if body.length != existing.Length || body.sha256 != existing.LogicalSHA256 {
		return false
	}
	if init.ExpectedLength != nil && *init.ExpectedLength != existing.Length {
		return false
	}
	return len(init.ExpectedSHA256) == 0 || bytes.Equal(init.ExpectedSHA256, existing.LogicalSHA256[:])
}

func (a *transactionCoordinator) ensureReplayUploadIntent(ctx context.Context, intent metastore.UploadIntent) error {
	if _, err := a.metadata.GetUploadIntent(intent.BlockID); errors.Is(err, metastore.ErrNotFound) {
		return a.authority.RecordUploadIntent(ctx, intent, recordUploadIntentCommandID(intent), a.now())
	} else if err != nil {
		return err
	}
	return nil
}

type chunkReader struct {
	chunks  storageapp.ChunkReader
	pending []byte
}

var errEmptyWriteChunk = errors.New("localstorage: empty write chunk received")

func (r *chunkReader) Read(p []byte) (int, error) {
	for len(r.pending) == 0 {
		chunk, err := r.chunks.Recv()
		if err != nil {
			return 0, err
		}
		if len(chunk) == 0 {
			return 0, errEmptyWriteChunk
		}
		r.pending = chunk
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

type senderWriter struct {
	sender storageapp.ReadDocumentSender
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

func drainChunks(chunks storageapp.ChunkReader) (drainedBody, error) {
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
	if err == nil {
		return nil
	}
	for _, mapping := range applicationErrorMappings {
		if errors.Is(err, mapping.target) {
			return appstatus.Wrap(mapping.code, mapping.message, err)
		}
	}
	return err
}

func (h *HealthApplication) CheckReadiness(ctx context.Context) error {
	if h == nil || h.app == nil || h.app.authority == nil {
		return errors.New("localstorage: application is closed")
	}
	if err := h.app.authority.CheckHealth(); err != nil {
		return err
	}
	return h.app.authority.ReadFresh(ctx)
}

func (h *HealthApplication) CheckLiveness(ctx context.Context) error {
	if h == nil || h.app == nil || h.app.metadata == nil || h.app.blocks == nil || h.app.authority == nil {
		return errors.New("localstorage: application is closed")
	}
	return errors.Join(h.app.metadata.CheckReachable(), h.app.blocks.CheckOpen(ctx), h.app.authority.CheckHealth())
}

var applicationErrorMappings = []struct {
	target  error
	code    appstatus.Code
	message string
}{
	{target: metastore.ErrNotFound, code: appstatus.CodeNotFound, message: "document or transaction not found"},
	{target: metastore.ErrConflict, code: appstatus.CodeAlreadyExists, message: "document already exists"},
	{target: metastore.ErrTransactionClosed, code: appstatus.CodeFailedPrecondition, message: "transaction is closed"},
	{target: errEmptyWriteChunk, code: appstatus.CodeInvalidArgument, message: "write chunk must not be empty"},
	{target: blockstore.ErrInvalidRange, code: appstatus.CodeInvalidArgument, message: "read range is outside document bounds"},
	{target: blockstore.ErrChecksumMismatch, code: appstatus.CodeDataLoss, message: "stored document bytes failed checksum verification"},
	{target: backend.ErrChecksumMismatch, code: appstatus.CodeDataLoss, message: "backend document bytes failed checksum verification"},
	{target: backend.ErrNotFound, code: appstatus.CodeDataLoss, message: "backend document bytes are missing"},
	{target: ErrCorruptVerificationWindow, code: appstatus.CodeDataLoss, message: "backend verification window is corrupt"},
	{target: ErrWritePipelineInvariant, code: appstatus.CodeInternal, message: "write pipeline invariant failed"},
	{target: os.ErrNotExist, code: appstatus.CodeDataLoss, message: "stored document bytes are missing"},
	{target: raftmeta.ErrNotLeader, code: appstatus.CodeFailedPrecondition, message: "local metadata authority is not leader"},
	{target: raftmeta.ErrQuorumUnavailable, code: appstatus.CodeUnavailable, message: "metadata quorum is unavailable"},
	{target: raftmeta.ErrReadFreshnessUnavailable, code: appstatus.CodeUnavailable, message: "metadata freshness cannot be proven"},
	{target: raftmeta.ErrLeaseReadDisabled, code: appstatus.CodeFailedPrecondition, message: "metadata lease reads are disabled"},
	{target: replication.ErrInsufficientReplicas, code: appstatus.CodeUnavailable, message: "required peer replicas could not be prepared"},
	{target: replication.ErrMissingByteSource, code: appstatus.CodeInternal, message: "peer prepare byte source is unavailable"},
	{target: replication.ErrReceiptMismatch, code: appstatus.CodeInternal, message: "peer prepare receipt did not match prepared document"},
	{target: replication.ErrTransferMismatch, code: appstatus.CodeDataLoss, message: "peer prepare transfer failed checksum verification"},
}

func readIntegrityError(document metastore.Document, attemptedSources []string, cause error) error {
	if !isIntegrityFailure(cause) {
		return mapError(cause)
	}
	return appstatus.New(appstatus.CodeDataLoss, "document bytes failed integrity verification", appstatus.WithDetails(storageapp.IntegrityFailureDetail{
		Identity:         document.Identity,
		AttemptedSources: append([]string(nil), attemptedSources...),
		EvidenceID:       integrityEvidenceID(document),
	}))
}

func unavailableReadStateError(document metastore.Document) error {
	switch {
	case document.RequiresBackendRestore():
		return restorePendingError(document)
	case document.IsCryptoUnavailable():
		return cryptoUnavailableError(document)
	default:
		return nil
	}
}

func restorePendingError(document metastore.Document) error {
	return appstatus.New(appstatus.CodeUnavailable, "document bytes require backend restore", appstatus.WithDetails(storageapp.RestorePendingDetail{
		Identity:         document.Identity,
		AffectedBlockIDs: []string{document.Location.BlockID},
		RestoreState:     restoreStateText(document.RestoreState),
		RestoreQueued:    document.IsRestoreQueued(),
		RetryHint: storageapp.RetryHint{
			Retryable: true,
			Reason:    "restore_pending",
		},
	}))
}

func cryptoUnavailableError(document metastore.Document) error {
	return appstatus.New(appstatus.CodeUnavailable, "document bytes require unavailable crypto material", appstatus.WithDetails(storageapp.CryptoUnavailableDetail{
		Identity: document.Identity,
		KeyScope: "backend",
		RetryHint: storageapp.RetryHint{
			Retryable: true,
			Reason:    "crypto_unavailable",
		},
	}))
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
		errors.Is(err, ErrCorruptVerificationWindow) ||
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

func (a *Application) recordPeerRepairState(ctx context.Context, document metastore.Document, replica metastore.ReplicaRef, quarantined bool, now time.Time) error {
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

func peerRepairPhysicalRef(replica metastore.ReplicaRef) string {
	return fmt.Sprintf("peer/%s/%s/%d/%d", replica.MemberID, replica.BlockID, replica.StoredOffset, replica.StoredLength)
}

func peerIntegrityEvidenceID(document metastore.Document, replica metastore.ReplicaRef) string {
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

func documentToStorageApp(document metastore.Document) storageapp.DocumentMetadata {
	return storageapp.DocumentMetadata{
		Identity:                    document.Identity,
		DocumentClass:               documentClassToStorageApp(document.DocumentClass),
		PriorityClass:               priorityClassToStorageApp(document.PriorityClass),
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
		Availability:                availabilityToStorageApp(document.Availability),
		LifecycleState:              lifecycleStateToStorageApp(document.LifecycleState),
		Tags:                        cloneTags(document.Tags),
	}
}

func transactionToStorageApp(transaction metastore.Transaction) storageapp.TransactionState {
	return storageapp.TransactionState{
		Transaction:            transaction.Identity,
		State:                  transactionStateToStorageApp(transaction.State),
		DocumentCount:          transaction.DocumentCount,
		PermanentDocumentCount: transaction.PermanentDocumentCount,
		EphemeralDocumentCount: transaction.EphemeralDocumentCount,
		CreatedAt:              transaction.CreatedAt,
		CompletedAt:            cloneTime(transaction.CompletedAt),
		TimeoutAt:              cloneTime(transaction.TimeoutAt),
		Tags:                   cloneTags(transaction.Tags),
	}
}

func documentFilterFromStorageApp(filter storageapp.DocumentFilter) metastore.DocumentFilter {
	out := metastore.DocumentFilter{
		DocumentNameExact:     filter.DocumentNameExact,
		HasDocumentNameExact:  filter.HasDocumentNameExact,
		DocumentNamePrefix:    filter.DocumentNamePrefix,
		HasDocumentNamePrefix: filter.HasDocumentNamePrefix,
		DocumentClass:         documentClassFromStorageApp(filter.DocumentClass),
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

func documentClassFromStorageApp(value storageapp.DocumentClass) metastore.DocumentClass {
	switch value {
	case storageapp.DocumentClassPermanent:
		return metastore.DocumentClassPermanent
	case storageapp.DocumentClassEphemeral:
		return metastore.DocumentClassEphemeral
	default:
		return 0
	}
}

func documentClassToStorageApp(value metastore.DocumentClass) storageapp.DocumentClass {
	switch value {
	case metastore.DocumentClassPermanent:
		return storageapp.DocumentClassPermanent
	case metastore.DocumentClassEphemeral:
		return storageapp.DocumentClassEphemeral
	default:
		return storageapp.DocumentClassUnspecified
	}
}

func priorityClassFromStorageApp(value storageapp.PriorityClass) metastore.PriorityClass {
	switch value {
	case storageapp.PriorityClassCriticalIngest:
		return metastore.PriorityClassCriticalIngest
	case storageapp.PriorityClassNormal:
		return metastore.PriorityClassNormal
	case storageapp.PriorityClassBulk:
		return metastore.PriorityClassBulk
	default:
		return 0
	}
}

func priorityClassToStorageApp(value metastore.PriorityClass) storageapp.PriorityClass {
	switch value {
	case metastore.PriorityClassCriticalIngest:
		return storageapp.PriorityClassCriticalIngest
	case metastore.PriorityClassNormal:
		return storageapp.PriorityClassNormal
	case metastore.PriorityClassBulk:
		return storageapp.PriorityClassBulk
	default:
		return storageapp.PriorityClassUnspecified
	}
}

func availabilityToStorageApp(value metastore.Availability) storageapp.DocumentAvailability {
	switch value {
	case metastore.AvailabilityHot:
		return storageapp.DocumentAvailabilityHot
	case metastore.AvailabilityCold:
		return storageapp.DocumentAvailabilityCold
	case metastore.AvailabilityRestorePending:
		return storageapp.DocumentAvailabilityRestorePending
	case metastore.AvailabilityCryptoUnavailable:
		return storageapp.DocumentAvailabilityCryptoUnavailable
	case metastore.AvailabilityDegradedRepair:
		return storageapp.DocumentAvailabilityDegradedRepair
	default:
		return storageapp.DocumentAvailabilityUnspecified
	}
}

func lifecycleStateToStorageApp(value metastore.LifecycleState) storageapp.DocumentLifecycleState {
	switch value {
	case metastore.LifecycleStateActive:
		return storageapp.DocumentLifecycleStateActive
	case metastore.LifecycleStateTransactionCompleted:
		return storageapp.DocumentLifecycleStateTransactionCompleted
	case metastore.LifecycleStateTombstoned:
		return storageapp.DocumentLifecycleStateTombstoned
	case metastore.LifecycleStatePendingReclamation:
		return storageapp.DocumentLifecycleStatePendingReclamation
	default:
		return storageapp.DocumentLifecycleStateUnspecified
	}
}

func transactionStateToStorageApp(value metastore.TransactionStateKind) storageapp.TransactionStateKind {
	switch value {
	case metastore.TransactionStateOpen:
		return storageapp.TransactionStateOpen
	case metastore.TransactionStateCompleted:
		return storageapp.TransactionStateCompleted
	case metastore.TransactionStateTimedOut:
		return storageapp.TransactionStateTimedOut
	default:
		return storageapp.TransactionStateUnspecified
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

func completeTransactionCommandID(transaction identity.Transaction, tags map[string]string) string {
	parts := []string{
		transaction.TenantID,
		transaction.TransactionID,
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
