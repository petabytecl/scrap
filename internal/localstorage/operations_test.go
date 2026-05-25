package localstorage

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/backend"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/storageapp"
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestRestoreTargetsCollectsDocumentsTransactionsAndBlocksWithoutDuplicates(t *testing.T) {
	app := openTestApplication(t)
	doc1 := testDocumentIdentity()
	doc2 := doc1
	doc2.DocumentName = "invoice-2.xml"
	stored1 := writeOperationTargetDocument(t, app, doc1, "restore target one")
	stored2 := writeOperationTargetDocument(t, app, doc2, "restore target two")

	blockIDs, documents, err := app.operationExecutor.restoreTargets(queuedOperation("restore-targets-1", "restore", []*adminv1.Target{
		documentTarget(doc1),
		transactionTarget(documentTransaction(doc1)),
		blockTarget("local", stored1.Location.BlockID),
	}))
	testutil.RequireNoErrorf(t, err, "collect restore targets")
	testutil.RequireTruef(t, blockIDs[stored1.Location.BlockID], "missing block id %q", stored1.Location.BlockID)
	testutil.RequireTruef(t, blockIDs[stored2.Location.BlockID], "missing block id %q", stored2.Location.BlockID)
	requireDocumentSet(t, documents, doc1, doc2)

	_, _, err = app.operationExecutor.restoreTargets(queuedOperation("restore-targets-remote", "restore", []*adminv1.Target{
		blockTarget("remote", stored1.Location.BlockID),
	}))
	if !errors.Is(err, metastore.ErrNotFound) {
		t.Fatalf("remote block target error = %v, want %v", err, metastore.ErrNotFound)
	}
}

func TestScrubTargetsCollectsTransactionsBlocksAndLocalScopesWithoutDuplicates(t *testing.T) {
	app := openTestApplication(t)
	doc1 := testDocumentIdentity()
	doc2 := doc1
	doc2.DocumentName = "scrub-2.xml"
	stored1 := writeOperationTargetDocument(t, app, doc1, "scrub target one")
	writeOperationTargetDocument(t, app, doc2, "scrub target two")

	targets, err := app.operationExecutor.scrubTargets(queuedOperation("scrub-targets-1", "scrub", []*adminv1.Target{
		documentTarget(doc1),
		transactionTarget(documentTransaction(doc1)),
		blockTarget("local", stored1.Location.BlockID),
		shardTarget("local"),
		storageMemberTarget("local"),
	}))
	testutil.RequireNoErrorf(t, err, "collect scrub targets")
	requireStoredDocumentSet(t, targets, doc1, doc2)

	allTargets, err := app.operationExecutor.scrubTargets(queuedOperation("scrub-targets-all", "scrub", nil))
	testutil.RequireNoErrorf(t, err, "collect all scrub targets")
	requireStoredDocumentSet(t, allTargets, doc1, doc2)

	_, err = app.operationExecutor.scrubTargets(queuedOperation("scrub-targets-remote", "scrub", []*adminv1.Target{
		shardTarget("remote"),
	}))
	if !errors.Is(err, metastore.ErrNotFound) {
		t.Fatalf("remote scrub scope error = %v, want %v", err, metastore.ErrNotFound)
	}
}

func TestRepairTargetsFilterQuarantinedLocalStatesAndScopes(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	doc1 := testDocumentIdentity()
	doc2 := doc1
	doc2.DocumentName = "repair-2.xml"
	stored1 := writeOperationTargetDocument(t, app, doc1, "repair target one")
	stored2 := writeOperationTargetDocument(t, app, doc2, "repair target two")
	now := time.Unix(410, 0).UTC()
	testutil.RequireNoErrorf(t, app.recordDocumentRepairState(ctx, stored1, "incident-1", true, now), "record first repair state")
	testutil.RequireNoErrorf(t, app.recordDocumentRepairState(ctx, stored2, "incident-2", true, now), "record second repair state")
	testutil.RequireNoErrorf(t, app.recordDocumentRepairState(ctx, stored2, "resolved-incident", false, now), "record resolved repair state")
	testutil.RequireNoErrorf(t, app.recordRepairState(ctx, doc2, "peer/member-1/block/0/1", "peer-incident", true, now), "record peer repair state")

	repairs, err := app.operationExecutor.repairTargets(queuedOperation("repair-targets-1", "repair", []*adminv1.Target{
		documentTarget(doc1),
		blockTarget("local", stored1.Location.BlockID),
		shardTarget("local"),
		storageMemberTarget("local"),
	}))
	testutil.RequireNoErrorf(t, err, "collect repair targets")
	requireRepairIncidentSet(t, repairs, "incident-1", "incident-2")

	_, err = app.operationExecutor.repairTargets(queuedOperation("repair-targets-remote", "repair", []*adminv1.Target{
		blockTarget("remote", stored1.Location.BlockID),
	}))
	if !errors.Is(err, metastore.ErrNotFound) {
		t.Fatalf("remote repair block error = %v, want %v", err, metastore.ErrNotFound)
	}
}

func TestRunQueuedOperationsOnceSkipsUnsupportedAndHandlesMissingStoreOrContext(t *testing.T) {
	app := openTestApplication(t)
	if _, err := app.RunQueuedOperationsOnce(context.Background(), nil); err == nil {
		t.Fatal("run queued operations with nil store error = nil, want error")
	}
	if _, err := app.RecoverInterruptedOperations(context.Background(), nil); err == nil {
		t.Fatal("recover interrupted operations with nil store error = nil, want error")
	}

	store := openTestOperationStore(t)
	unsupported := queuedOperation("unsupported-op-1", "unsupported", nil)
	testutil.RequireNoErrorf(t, store.Put(unsupported), "put unsupported operation")
	result, err := app.RunQueuedOperationsOnce(context.Background(), store)
	testutil.RequireNoErrorf(t, err, "run unsupported queued operation")
	testutil.RequireEqualf(t, result.Scanned, 1, "scanned count")
	testutil.RequireEqualf(t, result.Skipped, 1, "skipped count")
	current, err := store.Get(unsupported.GetOperationId())
	testutil.RequireNoErrorf(t, err, "get unsupported operation")
	testutil.RequireEqualf(t, current.GetState(), adminv1.OperationState_OPERATION_STATE_QUEUED, "unsupported operation remains queued")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = app.RunQueuedOperationsOnce(ctx, store)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("run queued operations canceled error = %v, want %v", err, context.Canceled)
	}
	_, err = app.RecoverInterruptedOperations(ctx, store)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("recover interrupted operations canceled error = %v, want %v", err, context.Canceled)
	}
}

func TestOperationMetadataProgressAndAuditHelpers(t *testing.T) {
	doc := testDocumentIdentity()
	operation := queuedOperation("restore-meta-1", "restore", []*adminv1.Target{documentTarget(doc)})
	operation.Metadata = map[string]string{
		"custom":      "kept",
		"api_token":   "hidden",
		"db_password": "hidden",
	}
	metadata := restoreOperationMetadata(operation)
	testutil.RequireEqualf(t, metadata[operationLaneMetadata], operationLaneInteractive, "restore operation lane")
	testutil.RequireEqualf(t, metadata[backendLaneMetadata], string(backend.LaneRestore), "backend lane")
	testutil.RequireEqualf(t, operation.GetMetadata()[operationLaneMetadata], "", "source metadata was mutated")

	operation.Metadata = metadata
	progress, err := restoreOperationProgress(operation, restoreSummary{
		BlocksRestored:  1,
		BlocksSkipped:   2,
		BlocksPending:   3,
		DocumentsMarked: 4,
	}, "restore succeeded")
	testutil.RequireNoErrorf(t, err, "build restore progress")
	testutil.RequireEqualf(t, progress.GetWorkUnitsTotal(), uint64(6), "restore total units")
	testutil.RequireEqualf(t, progress.GetWorkUnitsCompleted(), uint64(3), "restore completed units")
	testutil.RequireEqualf(t, progress.GetCounters()["documents_marked"], "4", "documents marked counter")

	_, err = restoreOperationProgress(operation, restoreSummary{BlocksRestored: -1}, "bad progress")
	if err == nil {
		t.Fatal("negative restore work units error = nil, want error")
	}
	_, err = operationWorkUnits("overflow", math.MaxInt, math.MaxInt, 2)
	if err == nil {
		t.Fatal("overflow work units error = nil, want error")
	}

	restoreEvent := restoreCompletedAuditEvent(operation)
	testutil.RequireEqualf(t, restoreEvent.GetEventType(), operationEventRestoreComplete, "restore event type")
	testutil.RequireEqualf(t, restoreEvent.GetMetadata()["custom"], "kept", "audit metadata custom value")
	testutil.RequireEqualf(t, restoreEvent.GetMetadata()["api_token"], "", "audit metadata token redaction")
	testutil.RequireTruef(t, restoreEvent.GetTargets()[0] != operation.GetTargets()[0], "audit targets should be cloned")

	prewarm := queuedOperation("prewarm-meta-1", "prewarm", nil)
	testutil.RequireEqualf(t, defaultRestoreOperationLane(prewarm.GetOperationType()), operationLanePlannedPrewarm, "prewarm lane")
	testutil.RequireEqualf(t, restoreCompletedAuditEvent(prewarm).GetEventType(), operationEventPrewarmComplete, "prewarm event type")
	testutil.RequireEqualf(t, operationErrorPrefix("prewarm"), "PREWARM", "prewarm error prefix")
	testutil.RequireEqualf(t, operationErrorPrefix("rewrap"), "REWRAP", "rewrap error prefix")
	testutil.RequireEqualf(t, operationErrorPrefix("restore"), "RESTORE", "restore error prefix")
	testutil.RequireEqualf(t, adminDocumentIdentity(nil), identity.Document{}, "nil admin document identity")
	testutil.RequireNilf(t, cloneOperation(nil), "nil operation clone")
}

func TestDocumentStorageAppConversionHelpers(t *testing.T) {
	testutil.RequireEqualf(t, documentClassFromStorageApp(storageapp.DocumentClassPermanent), metastore.DocumentClassPermanent, "permanent class")
	testutil.RequireEqualf(t, documentClassFromStorageApp(storageapp.DocumentClassEphemeral), metastore.DocumentClassEphemeral, "ephemeral class")
	testutil.RequireEqualf(t, documentClassFromStorageApp(storageapp.DocumentClassUnspecified), metastore.DocumentClass(0), "unspecified class")
	testutil.RequireEqualf(t, documentClassToStorageApp(metastore.DocumentClassPermanent), storageapp.DocumentClassPermanent, "permanent class to app")
	testutil.RequireEqualf(t, documentClassToStorageApp(metastore.DocumentClassEphemeral), storageapp.DocumentClassEphemeral, "ephemeral class to app")
	testutil.RequireEqualf(t, documentClassToStorageApp(0), storageapp.DocumentClassUnspecified, "unknown class to app")

	testutil.RequireEqualf(t, priorityClassFromStorageApp(storageapp.PriorityClassCriticalIngest), metastore.PriorityClassCriticalIngest, "critical priority")
	testutil.RequireEqualf(t, priorityClassFromStorageApp(storageapp.PriorityClassNormal), metastore.PriorityClassNormal, "normal priority")
	testutil.RequireEqualf(t, priorityClassFromStorageApp(storageapp.PriorityClassBulk), metastore.PriorityClassBulk, "bulk priority")
	testutil.RequireEqualf(t, priorityClassFromStorageApp(storageapp.PriorityClassUnspecified), metastore.PriorityClass(0), "unspecified priority")
	testutil.RequireEqualf(t, priorityClassToStorageApp(metastore.PriorityClassCriticalIngest), storageapp.PriorityClassCriticalIngest, "critical priority to app")
	testutil.RequireEqualf(t, priorityClassToStorageApp(metastore.PriorityClassNormal), storageapp.PriorityClassNormal, "normal priority to app")
	testutil.RequireEqualf(t, priorityClassToStorageApp(metastore.PriorityClassBulk), storageapp.PriorityClassBulk, "bulk priority to app")
	testutil.RequireEqualf(t, priorityClassToStorageApp(0), storageapp.PriorityClassUnspecified, "unknown priority to app")

	testutil.RequireEqualf(t, availabilityToStorageApp(metastore.AvailabilityHot), storageapp.DocumentAvailabilityHot, "hot availability")
	testutil.RequireEqualf(t, availabilityToStorageApp(metastore.AvailabilityCold), storageapp.DocumentAvailabilityCold, "cold availability")
	testutil.RequireEqualf(t, availabilityToStorageApp(metastore.AvailabilityRestorePending), storageapp.DocumentAvailabilityRestorePending, "restore pending availability")
	testutil.RequireEqualf(t, availabilityToStorageApp(metastore.AvailabilityCryptoUnavailable), storageapp.DocumentAvailabilityCryptoUnavailable, "crypto unavailable availability")
	testutil.RequireEqualf(t, availabilityToStorageApp(metastore.AvailabilityDegradedRepair), storageapp.DocumentAvailabilityDegradedRepair, "degraded availability")
	testutil.RequireEqualf(t, availabilityToStorageApp(0), storageapp.DocumentAvailabilityUnspecified, "unknown availability")

	testutil.RequireEqualf(t, lifecycleStateToStorageApp(metastore.LifecycleStateActive), storageapp.DocumentLifecycleStateActive, "active lifecycle")
	testutil.RequireEqualf(t, lifecycleStateToStorageApp(metastore.LifecycleStateTransactionCompleted), storageapp.DocumentLifecycleStateTransactionCompleted, "completed lifecycle")
	testutil.RequireEqualf(t, lifecycleStateToStorageApp(metastore.LifecycleStateTombstoned), storageapp.DocumentLifecycleStateTombstoned, "tombstoned lifecycle")
	testutil.RequireEqualf(t, lifecycleStateToStorageApp(metastore.LifecycleStatePendingReclamation), storageapp.DocumentLifecycleStatePendingReclamation, "pending lifecycle")
	testutil.RequireEqualf(t, lifecycleStateToStorageApp(0), storageapp.DocumentLifecycleStateUnspecified, "unknown lifecycle")

	testutil.RequireEqualf(t, transactionStateToStorageApp(metastore.TransactionStateOpen), storageapp.TransactionStateOpen, "open transaction")
	testutil.RequireEqualf(t, transactionStateToStorageApp(metastore.TransactionStateCompleted), storageapp.TransactionStateCompleted, "completed transaction")
	testutil.RequireEqualf(t, transactionStateToStorageApp(metastore.TransactionStateTimedOut), storageapp.TransactionStateTimedOut, "timed out transaction")
	testutil.RequireEqualf(t, transactionStateToStorageApp(0), storageapp.TransactionStateUnspecified, "unknown transaction")

	testutil.RequireEqualf(t, restoreStateText(metastore.RestoreStateHot), "hot", "hot restore state")
	testutil.RequireEqualf(t, restoreStateText(metastore.RestoreStateCold), "cold", "cold restore state")
	testutil.RequireEqualf(t, restoreStateText(metastore.RestoreStateRestorePending), "restore_pending", "pending restore state")
	testutil.RequireEqualf(t, restoreStateText(metastore.RestoreStateCryptoUnavailable), "crypto_unavailable", "crypto restore state")
	testutil.RequireEqualf(t, restoreStateText(0), "unknown", "unknown restore state")

	start := time.Unix(1, 0).UTC()
	end := time.Unix(2, 0).UTC()
	tags := map[string]string{"tenant": "blue"}
	filter := documentFilterFromStorageApp(storageapp.DocumentFilter{
		DocumentNameExact:    "invoice.xml",
		HasDocumentNameExact: true,
		CreatedAt:            &storageapp.TimeRange{StartTime: &start, EndTime: &end},
		Tags:                 tags,
	})
	testutil.RequireEqualf(t, filter.DocumentNameExact, "invoice.xml", "filter exact name")
	testutil.RequireTruef(t, filter.CreatedAfter != &start, "created-after time was not cloned")
	testutil.RequireTruef(t, filter.CreatedBefore != &end, "created-before time was not cloned")
	filter.Tags["tenant"] = "mutated"
	testutil.RequireEqualf(t, tags["tenant"], "blue", "source filter tags should not be mutated")
}

func writeOperationTargetDocument(t *testing.T, app *Application, doc identity.Document, data string) metastore.Document {
	t.Helper()
	_, err := app.WriteDocument(context.Background(), storageapp.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "coverage-test",
	}, newChunkReader([][]byte{[]byte(data)}))
	testutil.RequireNoErrorf(t, err, "write operation target document %s", doc.DocumentName)
	stored, err := app.metadata.HeadDocument(doc)
	testutil.RequireNoErrorf(t, err, "head operation target document %s", doc.DocumentName)
	return stored
}

func transactionTarget(tx identity.Transaction) *adminv1.Target {
	return &adminv1.Target{
		Target: &adminv1.Target_Transaction{
			Transaction: &adminv1.TransactionTarget{
				TenantId:      tx.TenantID,
				TransactionId: tx.TransactionID,
			},
		},
	}
}

func documentTransaction(doc identity.Document) identity.Transaction {
	return identity.Transaction{TenantID: doc.TenantID, TransactionID: doc.TransactionID}
}

func blockTarget(shardID, blockID string) *adminv1.Target {
	return &adminv1.Target{
		Target: &adminv1.Target_Block{
			Block: &adminv1.BlockTarget{
				ShardId: shardID,
				BlockId: blockID,
			},
		},
	}
}

func shardTarget(shardID string) *adminv1.Target {
	return &adminv1.Target{
		Target: &adminv1.Target_Shard{
			Shard: &adminv1.ShardTarget{ShardId: shardID},
		},
	}
}

func storageMemberTarget(memberID string) *adminv1.Target {
	return &adminv1.Target{
		Target: &adminv1.Target_StorageMember{
			StorageMember: &adminv1.StorageMemberTarget{StorageMemberId: memberID},
		},
	}
}

func requireDocumentSet(t *testing.T, got []identity.Document, want ...identity.Document) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("documents = %#v, want %#v", got, want)
	}
	seen := make(map[identity.Document]bool, len(got))
	for _, document := range got {
		seen[document] = true
	}
	for _, document := range want {
		testutil.RequireTruef(t, seen[document], "missing document %#v in %#v", document, got)
	}
}

func requireStoredDocumentSet(t *testing.T, got []metastore.Document, want ...identity.Document) {
	t.Helper()
	identities := make([]identity.Document, 0, len(got))
	for _, document := range got {
		identities = append(identities, document.Identity)
	}
	requireDocumentSet(t, identities, want...)
}

func requireRepairIncidentSet(t *testing.T, got []metastore.RepairState, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("repairs = %#v, want incidents %#v", got, want)
	}
	seen := make(map[string]bool, len(got))
	for _, state := range got {
		seen[state.IncidentID] = true
		if !state.Quarantined || !isLocalRepairState(state) {
			t.Fatalf("repair state = %#v, want quarantined local state", state)
		}
	}
	for _, incidentID := range want {
		testutil.RequireTruef(t, seen[incidentID], "missing incident %q in %#v", incidentID, got)
	}
}
