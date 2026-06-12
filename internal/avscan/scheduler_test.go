package avscan

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

const schedulerTestSignatureVersion = "daily-2026.06.12:1"

func TestSchedulerRunOnceScansLeaderSealedBlocks(t *testing.T) {
	engine := &recordingEngine{}
	metrics := &recordingMetrics{}
	scheduler := NewScheduler(Config{
		ShardID:       7,
		BlockLister:   staticBlockLister{blocks: []Block{{BlockID: 1, SizeBytes: 10}, {BlockID: 2, SizeBytes: 20}}},
		LeaderChecker: staticLeaderChecker(true),
		Engine:        engine,
		Metrics:       metrics,
		Interval:      time.Hour,
	})

	if err := scheduler.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if got, want := engine.blockIDs(), []uint64{1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scanned Blocks = %v, want %v", got, want)
	}
	snapshot := scheduler.Snapshot()
	if snapshot.Status != StatusIdle {
		t.Fatalf("status = %q, want %q", snapshot.Status, StatusIdle)
	}
	if snapshot.LastScannedBlockID != 2 {
		t.Fatalf("last scanned Block = %d, want 2", snapshot.LastScannedBlockID)
	}
	if got, want := metrics.lagValues, []int{2, 1, 0}; !reflect.DeepEqual(got, want) {
		t.Fatalf("lag values = %v, want %v", got, want)
	}
}

func TestSchedulerSkipsWhenNotLeader(t *testing.T) {
	engine := &recordingEngine{}
	scheduler := NewScheduler(Config{
		ShardID:       7,
		BlockLister:   staticBlockLister{blocks: []Block{{BlockID: 1}}},
		LeaderChecker: staticLeaderChecker(false),
		Engine:        engine,
		Interval:      time.Hour,
	})

	if err := scheduler.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if got := len(engine.scanned); got != 0 {
		t.Fatalf("scanned Blocks = %d, want 0", got)
	}
	snapshot := scheduler.Snapshot()
	if snapshot.LastReason != ReasonNotLeader {
		t.Fatalf("last reason = %q, want %q", snapshot.LastReason, ReasonNotLeader)
	}
}

func TestSchedulerEngineUnavailableRecordsBoundedStatus(t *testing.T) {
	engine := engineFunc(func(context.Context, Block) (Result, error) {
		return Result{}, ErrEngineUnavailable
	})
	scheduler := NewScheduler(Config{
		ShardID:       7,
		BlockLister:   staticBlockLister{blocks: []Block{{BlockID: 1}}},
		LeaderChecker: staticLeaderChecker(true),
		Engine:        engine,
		Interval:      time.Hour,
	})

	err := scheduler.RunOnce(context.Background())
	if !errors.Is(err, ErrEngineUnavailable) {
		t.Fatalf("RunOnce error = %v, want ErrEngineUnavailable", err)
	}
	snapshot := scheduler.Snapshot()
	if snapshot.Status != StatusDegraded {
		t.Fatalf("status = %q, want %q", snapshot.Status, StatusDegraded)
	}
	if snapshot.LastReason != ReasonEngineUnavailable {
		t.Fatalf("last reason = %q, want %q", snapshot.LastReason, ReasonEngineUnavailable)
	}
	if snapshot.LagBlocks != 1 {
		t.Fatalf("lag Blocks = %d, want 1", snapshot.LagBlocks)
	}
}

func TestSchedulerNilEngineRecordsUnavailable(t *testing.T) {
	scheduler := NewScheduler(Config{
		ShardID:       7,
		BlockLister:   staticBlockLister{blocks: []Block{{BlockID: 1}}},
		LeaderChecker: staticLeaderChecker(true),
		Interval:      time.Hour,
	})

	err := scheduler.RunOnce(context.Background())
	if !errors.Is(err, ErrEngineUnavailable) {
		t.Fatalf("RunOnce error = %v, want ErrEngineUnavailable", err)
	}
	snapshot := scheduler.Snapshot()
	if snapshot.Status != StatusDegraded {
		t.Fatalf("status = %q, want %q", snapshot.Status, StatusDegraded)
	}
	if snapshot.LastReason != ReasonEngineUnavailable {
		t.Fatalf("last reason = %q, want %q", snapshot.LastReason, ReasonEngineUnavailable)
	}
}

func TestSchedulerPanicRecoveryRecordsBoundedStatus(t *testing.T) {
	engine := engineFunc(func(context.Context, Block) (Result, error) {
		panic("poison fixture leaked raw scanner payload")
	})
	scheduler := NewScheduler(Config{
		ShardID:       7,
		BlockLister:   staticBlockLister{blocks: []Block{{BlockID: 1}}},
		LeaderChecker: staticLeaderChecker(true),
		Engine:        engine,
		Interval:      time.Hour,
	})

	err := scheduler.RunOnce(context.Background())
	if !errors.Is(err, ErrScanPanic) {
		t.Fatalf("RunOnce error = %v, want ErrScanPanic", err)
	}
	snapshot := scheduler.Snapshot()
	if snapshot.LastReason != ReasonScanPanic {
		t.Fatalf("last reason = %q, want %q", snapshot.LastReason, ReasonScanPanic)
	}
}

func TestSchedulerRecoversBlockListerPanic(t *testing.T) {
	scheduler := NewScheduler(Config{
		ShardID:       7,
		BlockLister:   blockListerFunc(func(context.Context) ([]Block, error) { panic("raw block path leaked") }),
		LeaderChecker: staticLeaderChecker(true),
		Engine:        &recordingEngine{},
		Interval:      time.Hour,
	})

	err := scheduler.RunOnce(context.Background())
	if !errors.Is(err, ErrScanPanic) {
		t.Fatalf("RunOnce error = %v, want ErrScanPanic", err)
	}
	snapshot := scheduler.Snapshot()
	if snapshot.Status != StatusDegraded {
		t.Fatalf("status = %q, want %q", snapshot.Status, StatusDegraded)
	}
	if snapshot.LastReason != ReasonScanPanic {
		t.Fatalf("last reason = %q, want %q", snapshot.LastReason, ReasonScanPanic)
	}
}

func TestSchedulerDuplicateSchedulingIsIdempotent(t *testing.T) {
	engine := &recordingEngine{}
	metrics := &recordingMetrics{}
	scheduler := NewScheduler(Config{
		ShardID:       7,
		BlockLister:   staticBlockLister{blocks: []Block{{BlockID: 2}, {BlockID: 1}}},
		LeaderChecker: staticLeaderChecker(true),
		Engine:        engine,
		Metrics:       metrics,
		Interval:      time.Hour,
	})

	if err := scheduler.RunOnce(context.Background()); err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}
	if err := scheduler.RunOnce(context.Background()); err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	if got, want := engine.blockIDs(), []uint64{1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scanned Blocks = %v, want %v", got, want)
	}
	if metrics.duplicates != 2 {
		t.Fatalf("duplicates = %d, want 2", metrics.duplicates)
	}
}

func TestSchedulerResumesFromPersistedProgressAfterReconstruction(t *testing.T) {
	store := &memoryProgressStore{}
	firstEngine := &recordingEngine{}
	first := NewScheduler(Config{
		ShardID:                  7,
		BlockLister:              staticBlockLister{blocks: []Block{{BlockID: 1}, {BlockID: 2}}},
		LeaderChecker:            staticLeaderChecker(true),
		Engine:                   firstEngine,
		ProgressStore:            store,
		SignatureVersionProvider: staticSignatureVersion(schedulerTestSignatureVersion),
		Interval:                 time.Hour,
	})

	if err := first.RunOnce(context.Background()); err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}
	if got, want := firstEngine.blockIDs(), []uint64{1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first scanned Blocks = %v, want %v", got, want)
	}
	assertSavedProgress(t, store.lastSaved(), 2)

	secondEngine := &recordingEngine{}
	second := NewScheduler(Config{
		ShardID:                  7,
		BlockLister:              staticBlockLister{blocks: []Block{{BlockID: 1}, {BlockID: 2}, {BlockID: 3}}},
		LeaderChecker:            staticLeaderChecker(true),
		Engine:                   secondEngine,
		ProgressStore:            store,
		SignatureVersionProvider: staticSignatureVersion(schedulerTestSignatureVersion),
		Interval:                 time.Hour,
	})

	if err := second.RunOnce(context.Background()); err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	if got, want := secondEngine.blockIDs(), []uint64{3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second scanned Blocks = %v, want %v", got, want)
	}
	assertSavedProgress(t, store.lastSaved(), 3)
}

func TestSchedulerDoesNotPersistFrontierPastFailedLowerBlock(t *testing.T) {
	store := &memoryProgressStore{}
	engine := engineFunc(func(_ context.Context, block Block) (Result, error) {
		if block.BlockID == 2 {
			return Result{}, errors.New("scan failed")
		}
		return Result{Status: ResultClean, ScannedDocuments: 1}, nil
	})
	scheduler := NewScheduler(Config{
		ShardID:                  7,
		BlockLister:              staticBlockLister{blocks: []Block{{BlockID: 1}, {BlockID: 2}, {BlockID: 3}}},
		LeaderChecker:            staticLeaderChecker(true),
		Engine:                   engine,
		ProgressStore:            store,
		SignatureVersionProvider: staticSignatureVersion(schedulerTestSignatureVersion),
		Interval:                 time.Hour,
	})

	err := scheduler.RunOnce(context.Background())
	if err == nil {
		t.Fatal("RunOnce succeeded despite failed lower Block")
	}
	assertSavedProgress(t, store.lastSaved(), 1)

	retryEngine := &recordingEngine{}
	retry := NewScheduler(Config{
		ShardID:                  7,
		BlockLister:              staticBlockLister{blocks: []Block{{BlockID: 1}, {BlockID: 2}, {BlockID: 3}}},
		LeaderChecker:            staticLeaderChecker(true),
		Engine:                   retryEngine,
		ProgressStore:            store,
		SignatureVersionProvider: staticSignatureVersion(schedulerTestSignatureVersion),
		Interval:                 time.Hour,
	})
	if err := retry.RunOnce(context.Background()); err != nil {
		t.Fatalf("retry RunOnce: %v", err)
	}
	if got, want := retryEngine.blockIDs(), []uint64{2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("retry scanned Blocks = %v, want %v", got, want)
	}
}

func TestSchedulerSignatureVersionChangeResetsProgressFromBeginning(t *testing.T) {
	store := &memoryProgressStore{
		progress: Progress{
			LastScannedBlockID:          3,
			LastSignatureVersionScanned: "daily-2026.06.11:1",
		},
	}
	engine := &recordingEngine{}
	scheduler := NewScheduler(Config{
		ShardID:                  7,
		BlockLister:              staticBlockLister{blocks: []Block{{BlockID: 1}, {BlockID: 2}, {BlockID: 3}}},
		LeaderChecker:            staticLeaderChecker(true),
		Engine:                   engine,
		ProgressStore:            store,
		SignatureVersionProvider: staticSignatureVersion(schedulerTestSignatureVersion),
		Interval:                 time.Hour,
	})

	if err := scheduler.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if got, want := engine.blockIDs(), []uint64{1, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scanned Blocks after signature update = %v, want %v", got, want)
	}
	if len(store.saves) == 0 {
		t.Fatal("no progress saves recorded")
	}
	assertSavedProgress(t, store.saves[0], 0)
	assertSavedProgress(t, store.lastSaved(), 3)
}

func TestSchedulerSignatureVersionChangeClearsProcessLocalDuplicates(t *testing.T) {
	store := &memoryProgressStore{}
	signatures := &mutableSignatureVersion{version: "daily-2026.06.11:1"}
	engine := &recordingEngine{}
	scheduler := NewScheduler(Config{
		ShardID:                  7,
		BlockLister:              staticBlockLister{blocks: []Block{{BlockID: 1}, {BlockID: 2}}},
		LeaderChecker:            staticLeaderChecker(true),
		Engine:                   engine,
		ProgressStore:            store,
		SignatureVersionProvider: signatures,
		Interval:                 time.Hour,
	})

	if err := scheduler.RunOnce(context.Background()); err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}
	signatures.version = schedulerTestSignatureVersion
	if err := scheduler.RunOnce(context.Background()); err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	if got, want := engine.blockIDs(), []uint64{1, 2, 1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scanned Blocks = %v, want %v", got, want)
	}
	assertSavedProgress(t, store.lastSaved(), 2)
}

func TestSchedulerMissingProgressActsAsZeroProgress(t *testing.T) {
	store := &memoryProgressStore{loadErr: ErrProgressNotFound}
	engine := &recordingEngine{}
	scheduler := NewScheduler(Config{
		ShardID:                  7,
		BlockLister:              staticBlockLister{blocks: []Block{{BlockID: 1}, {BlockID: 2}}},
		LeaderChecker:            staticLeaderChecker(true),
		Engine:                   engine,
		ProgressStore:            store,
		SignatureVersionProvider: staticSignatureVersion(schedulerTestSignatureVersion),
		Interval:                 time.Hour,
	})

	if err := scheduler.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if got, want := engine.blockIDs(), []uint64{1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scanned Blocks = %v, want %v", got, want)
	}
	assertSavedProgress(t, store.lastSaved(), 2)
}

func TestSchedulerNilSignatureProviderUsesProcessLocalProgress(t *testing.T) {
	store := &memoryProgressStore{}
	firstEngine := &recordingEngine{}
	first := NewScheduler(Config{
		ShardID:       7,
		BlockLister:   staticBlockLister{blocks: []Block{{BlockID: 1}, {BlockID: 2}}},
		LeaderChecker: staticLeaderChecker(true),
		Engine:        firstEngine,
		ProgressStore: store,
		Interval:      time.Hour,
	})
	if err := first.RunOnce(context.Background()); err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}
	if len(store.saves) != 0 {
		t.Fatalf("progress saves = %d, want 0 without signature provider", len(store.saves))
	}

	secondEngine := &recordingEngine{}
	second := NewScheduler(Config{
		ShardID:       7,
		BlockLister:   staticBlockLister{blocks: []Block{{BlockID: 1}, {BlockID: 2}}},
		LeaderChecker: staticLeaderChecker(true),
		Engine:        secondEngine,
		ProgressStore: store,
		Interval:      time.Hour,
	})
	if err := second.RunOnce(context.Background()); err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	if got, want := secondEngine.blockIDs(), []uint64{1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second scanned Blocks = %v, want %v", got, want)
	}
}

func TestSchedulerProgressSaveFailureIsObservable(t *testing.T) {
	store := &memoryProgressStore{
		progress: Progress{LastSignatureVersionScanned: schedulerTestSignatureVersion},
		saveErr:  errors.New("projection unavailable"),
	}
	engine := &recordingEngine{}
	scheduler := NewScheduler(Config{
		ShardID:                  7,
		BlockLister:              staticBlockLister{blocks: []Block{{BlockID: 1}}},
		LeaderChecker:            staticLeaderChecker(true),
		Engine:                   engine,
		ProgressStore:            store,
		SignatureVersionProvider: staticSignatureVersion(schedulerTestSignatureVersion),
		Interval:                 time.Hour,
	})

	err := scheduler.RunOnce(context.Background())
	if err == nil {
		t.Fatal("RunOnce succeeded despite progress save failure")
	}
	if got, want := engine.blockIDs(), []uint64{1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scanned Blocks = %v, want %v", got, want)
	}
	snapshot := scheduler.Snapshot()
	if snapshot.LastReason != ReasonProgressFailed {
		t.Fatalf("last reason = %q, want %q", snapshot.LastReason, ReasonProgressFailed)
	}
}

func TestSchedulerProgressSaveCancellationIsCancellation(t *testing.T) {
	store := &memoryProgressStore{
		progress: Progress{LastSignatureVersionScanned: schedulerTestSignatureVersion},
		saveErr:  context.Canceled,
	}
	scheduler := NewScheduler(Config{
		ShardID:                  7,
		BlockLister:              staticBlockLister{blocks: []Block{{BlockID: 1}}},
		LeaderChecker:            staticLeaderChecker(true),
		Engine:                   &recordingEngine{},
		ProgressStore:            store,
		SignatureVersionProvider: staticSignatureVersion(schedulerTestSignatureVersion),
		Interval:                 time.Hour,
	})

	err := scheduler.RunOnce(context.Background())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunOnce error = %v, want context.Canceled", err)
	}
	if got := scheduler.Snapshot().LastReason; got != ReasonCanceled {
		t.Fatalf("last reason = %q, want %q", got, ReasonCanceled)
	}
}

func TestSchedulerHigherPersistedFrontierRescansKnownBlocks(t *testing.T) {
	store := &memoryProgressStore{
		progress: Progress{
			LastScannedBlockID:          99,
			LastSignatureVersionScanned: schedulerTestSignatureVersion,
		},
	}
	engine := &recordingEngine{}
	scheduler := NewScheduler(Config{
		ShardID:                  7,
		BlockLister:              staticBlockLister{blocks: []Block{{BlockID: 1}, {BlockID: 2}}},
		LeaderChecker:            staticLeaderChecker(true),
		Engine:                   engine,
		ProgressStore:            store,
		SignatureVersionProvider: staticSignatureVersion(schedulerTestSignatureVersion),
		Interval:                 time.Hour,
	})

	if err := scheduler.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if got, want := engine.blockIDs(), []uint64{1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scanned Blocks = %v, want %v", got, want)
	}
	assertSavedProgress(t, store.lastSaved(), 2)
}

func TestSchedulerDoesNotPersistAcrossGapAboveFrontier(t *testing.T) {
	store := &memoryProgressStore{
		progress: Progress{
			LastScannedBlockID:          1,
			LastSignatureVersionScanned: schedulerTestSignatureVersion,
		},
	}
	engine := &recordingEngine{}
	scheduler := NewScheduler(Config{
		ShardID:                  7,
		BlockLister:              staticBlockLister{blocks: []Block{{BlockID: 3}}},
		LeaderChecker:            staticLeaderChecker(true),
		Engine:                   engine,
		ProgressStore:            store,
		SignatureVersionProvider: staticSignatureVersion(schedulerTestSignatureVersion),
		Interval:                 time.Hour,
	})

	if err := scheduler.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if got, want := engine.blockIDs(), []uint64{3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scanned Blocks = %v, want %v", got, want)
	}
	assertSavedProgress(t, store.lastSaved(), 1)
}

func TestSchedulerAdvancesFrontierThroughCompletedBlocksAfterGapFills(t *testing.T) {
	store := &memoryProgressStore{
		progress: Progress{
			LastScannedBlockID:          1,
			LastSignatureVersionScanned: schedulerTestSignatureVersion,
		},
	}
	lister := &mutableBlockLister{blocks: []Block{{BlockID: 3}}}
	scheduler := NewScheduler(Config{
		ShardID:                  7,
		BlockLister:              lister,
		LeaderChecker:            staticLeaderChecker(true),
		Engine:                   &recordingEngine{},
		ProgressStore:            store,
		SignatureVersionProvider: staticSignatureVersion(schedulerTestSignatureVersion),
		Interval:                 time.Hour,
	})

	if err := scheduler.RunOnce(context.Background()); err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}
	assertSavedProgress(t, store.lastSaved(), 1)

	lister.blocks = []Block{{BlockID: 2}, {BlockID: 3}}
	if err := scheduler.RunOnce(context.Background()); err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	assertSavedProgress(t, store.lastSaved(), 3)
}

func TestSchedulerDeduplicatesBlockListerDuplicates(t *testing.T) {
	engine := &recordingEngine{}
	scheduler := NewScheduler(Config{
		ShardID:       7,
		BlockLister:   staticBlockLister{blocks: []Block{{BlockID: 1}, {BlockID: 1}, {BlockID: 2}}},
		LeaderChecker: staticLeaderChecker(true),
		Engine:        engine,
		Interval:      time.Hour,
	})

	if err := scheduler.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if got, want := engine.blockIDs(), []uint64{1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scanned Blocks = %v, want %v", got, want)
	}
}

func TestSchedulerContinuesAfterPoisonBlock(t *testing.T) {
	engine := &poisonOnceEngine{}
	scheduler := NewScheduler(Config{
		ShardID:       7,
		BlockLister:   staticBlockLister{blocks: []Block{{BlockID: 1}, {BlockID: 2}, {BlockID: 3}}},
		LeaderChecker: staticLeaderChecker(true),
		Engine:        engine,
		Interval:      time.Hour,
	})

	err := scheduler.RunOnce(context.Background())
	if !errors.Is(err, ErrScanPanic) {
		t.Fatalf("RunOnce error = %v, want ErrScanPanic", err)
	}
	if got, want := engine.scanned, []uint64{1, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first scanned Blocks = %v, want %v", got, want)
	}
	snapshot := scheduler.Snapshot()
	if snapshot.LagBlocks != 1 {
		t.Fatalf("lag Blocks = %d, want 1", snapshot.LagBlocks)
	}
	if snapshot.ScannedBlocks != 2 || snapshot.FailedBlocks != 1 {
		t.Fatalf("scanned/failed Blocks = %d/%d, want 2/1", snapshot.ScannedBlocks, snapshot.FailedBlocks)
	}

	if err := scheduler.RunOnce(context.Background()); err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	if got, want := engine.scanned, []uint64{1, 2, 3, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second scanned Blocks = %v, want %v", got, want)
	}
}

func TestSchedulerRunOnceSerializesConcurrentCalls(t *testing.T) {
	entered := make(chan uint64, 2)
	release := make(chan struct{})
	engine := engineFunc(func(_ context.Context, block Block) (Result, error) {
		entered <- block.BlockID
		<-release
		return Result{Status: ResultClean, ScannedDocuments: 1}, nil
	})
	scheduler := NewScheduler(Config{
		ShardID:       7,
		BlockLister:   staticBlockLister{blocks: []Block{{BlockID: 1}}},
		LeaderChecker: staticLeaderChecker(true),
		Engine:        engine,
		Interval:      time.Hour,
	})

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- scheduler.RunOnce(context.Background())
	}()
	if got := <-entered; got != 1 {
		t.Fatalf("first entered Block = %d, want 1", got)
	}

	secondDone := make(chan error, 1)
	go func() {
		secondDone <- scheduler.RunOnce(context.Background())
	}()
	select {
	case got := <-entered:
		t.Fatalf("second RunOnce scanned Block %d before first completed", got)
	case <-time.After(25 * time.Millisecond):
	}

	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	select {
	case got := <-entered:
		t.Fatalf("second RunOnce rescanned completed Block %d", got)
	default:
	}
}

func TestSchedulerMetricsPanicsDoNotEscape(t *testing.T) {
	scheduler := NewScheduler(Config{
		ShardID:       7,
		BlockLister:   staticBlockLister{blocks: []Block{{BlockID: 1}}},
		LeaderChecker: staticLeaderChecker(true),
		Engine:        &recordingEngine{},
		Metrics:       panicMetrics{},
		Interval:      time.Hour,
	})

	if err := scheduler.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
}

func TestSchedulerStopCancelsAndWaitsForWorker(t *testing.T) {
	entered := make(chan struct{})
	engine := engineFunc(func(ctx context.Context, _ Block) (Result, error) {
		close(entered)
		<-ctx.Done()
		return Result{}, ctx.Err()
	})
	scheduler := NewScheduler(Config{
		ShardID:       7,
		BlockLister:   staticBlockLister{blocks: []Block{{BlockID: 1}}},
		LeaderChecker: staticLeaderChecker(true),
		Engine:        engine,
		Interval:      time.Hour,
	})

	scheduler.Start(context.Background())
	scheduler.Notify()
	<-entered

	stopped := make(chan struct{})
	go func() {
		scheduler.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not wait for worker cancellation")
	}
}

func TestSchedulerUsesPauseAndIOBudgetBeforeScan(t *testing.T) {
	pause := &recordingPause{}
	budget := &recordingBudget{}
	engine := &recordingEngine{}
	scheduler := NewScheduler(Config{
		ShardID:         7,
		BlockLister:     staticBlockLister{blocks: []Block{{BlockID: 1, SizeBytes: 32}}},
		LeaderChecker:   staticLeaderChecker(true),
		Engine:          engine,
		PauseController: pause,
		IOBudget:        budget,
		Interval:        time.Hour,
	})

	if err := scheduler.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if pause.waits != 1 {
		t.Fatalf("pause waits = %d, want 1", pause.waits)
	}
	if got, want := budget.bytes, []int64{32}; !reflect.DeepEqual(got, want) {
		t.Fatalf("budget bytes = %v, want %v", got, want)
	}
	if got, want := engine.blockIDs(), []uint64{1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scanned Blocks = %v, want %v", got, want)
	}
}

func TestSchedulerRunsOnInjectedTick(t *testing.T) {
	engine := &recordingEngine{scannedCh: make(chan Block, 1)}
	factory := &manualTickerFactory{ticker: newManualTicker()}
	scheduler := NewScheduler(Config{
		ShardID:       7,
		BlockLister:   staticBlockLister{blocks: []Block{{BlockID: 1}}},
		LeaderChecker: staticLeaderChecker(true),
		Engine:        engine,
		TickerFactory: factory,
		Interval:      time.Hour,
	})

	scheduler.Start(context.Background())
	defer scheduler.Stop()

	factory.ticker.ticks <- time.Now()

	select {
	case block := <-engine.scannedCh:
		if block.BlockID != 1 {
			t.Fatalf("scanned BlockID = %d, want 1", block.BlockID)
		}
	case <-time.After(time.Second):
		t.Fatal("scheduler did not run after manual tick")
	}
}

type staticBlockLister struct {
	blocks []Block
	err    error
}

func (l staticBlockLister) ListSealedBlocks(context.Context) ([]Block, error) {
	if l.err != nil {
		return nil, l.err
	}
	return append([]Block(nil), l.blocks...), nil
}

type blockListerFunc func(context.Context) ([]Block, error)

func (f blockListerFunc) ListSealedBlocks(ctx context.Context) ([]Block, error) {
	return f(ctx)
}

type mutableBlockLister struct {
	blocks []Block
}

func (l *mutableBlockLister) ListSealedBlocks(context.Context) ([]Block, error) {
	return append([]Block(nil), l.blocks...), nil
}

type staticLeaderChecker bool

func (l staticLeaderChecker) IsLeader() bool {
	return bool(l)
}

type engineFunc func(context.Context, Block) (Result, error)

func (f engineFunc) Scan(ctx context.Context, block Block) (Result, error) {
	return f(ctx, block)
}

type recordingEngine struct {
	scanned   []Block
	scannedCh chan Block
}

func (e *recordingEngine) Scan(_ context.Context, block Block) (Result, error) {
	e.scanned = append(e.scanned, block)
	if e.scannedCh != nil {
		e.scannedCh <- block
	}
	return Result{Status: ResultClean, ScannedDocuments: 1}, nil
}

func (e *recordingEngine) blockIDs() []uint64 {
	ids := make([]uint64, 0, len(e.scanned))
	for _, block := range e.scanned {
		ids = append(ids, block.BlockID)
	}
	return ids
}

type poisonOnceEngine struct {
	scanned          []uint64
	blockOneAttempts int
}

func (e *poisonOnceEngine) Scan(_ context.Context, block Block) (Result, error) {
	e.scanned = append(e.scanned, block.BlockID)
	if block.BlockID == 1 {
		e.blockOneAttempts++
	}
	if block.BlockID == 1 && e.blockOneAttempts == 1 {
		return Result{}, ErrScanPanic
	}
	return Result{Status: ResultClean, ScannedDocuments: 1}, nil
}

type recordingMetrics struct {
	lagValues  []int
	duplicates int
}

func (m *recordingMetrics) RecordRun(uint64, string, string, time.Duration) {}

func (m *recordingMetrics) RecordBlock(uint64, string, string) {}

func (m *recordingMetrics) RecordFailure(uint64, string) {}

func (m *recordingMetrics) SetLag(_ uint64, blocks int) {
	m.lagValues = append(m.lagValues, blocks)
}

func (m *recordingMetrics) SetInFlight(uint64, int) {}

func (m *recordingMetrics) RecordDuplicate(uint64) {
	m.duplicates++
}

type panicMetrics struct{}

func (panicMetrics) RecordRun(uint64, string, string, time.Duration) { panic("metric panic") }

func (panicMetrics) RecordBlock(uint64, string, string) { panic("metric panic") }

func (panicMetrics) RecordFailure(uint64, string) { panic("metric panic") }

func (panicMetrics) SetLag(uint64, int) { panic("metric panic") }

func (panicMetrics) SetInFlight(uint64, int) { panic("metric panic") }

func (panicMetrics) RecordDuplicate(uint64) { panic("metric panic") }

type recordingPause struct {
	waits int
}

func (p *recordingPause) IsPaused() bool {
	return true
}

func (p *recordingPause) Wait(context.Context) error {
	p.waits++
	return nil
}

type recordingBudget struct {
	bytes []int64
}

func (b *recordingBudget) Wait(_ context.Context, bytes int64) error {
	b.bytes = append(b.bytes, bytes)
	return nil
}

type manualTickerFactory struct {
	ticker *manualTicker
}

func (f *manualTickerFactory) NewTicker(time.Duration) Ticker {
	return f.ticker
}

type manualTicker struct {
	ticks   chan time.Time
	stopped bool
}

func newManualTicker() *manualTicker {
	return &manualTicker{ticks: make(chan time.Time, 1)}
}

func (t *manualTicker) C() <-chan time.Time {
	return t.ticks
}

func (t *manualTicker) Stop() {
	t.stopped = true
}

type memoryProgressStore struct {
	progress Progress
	loadErr  error
	saveErr  error
	saves    []Progress
}

func (s *memoryProgressStore) LoadScannerProgress(context.Context) (Progress, error) {
	if s.loadErr != nil {
		return Progress{}, s.loadErr
	}
	return s.progress, nil
}

func (s *memoryProgressStore) SaveScannerProgress(_ context.Context, progress Progress) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.progress = progress
	s.saves = append(s.saves, progress)
	return nil
}

func (s *memoryProgressStore) lastSaved() Progress {
	if len(s.saves) == 0 {
		return s.progress
	}
	return s.saves[len(s.saves)-1]
}

type staticSignatureVersion string

func (v staticSignatureVersion) SignatureVersion(context.Context) (string, error) {
	return string(v), nil
}

type mutableSignatureVersion struct {
	version string
}

func (v *mutableSignatureVersion) SignatureVersion(context.Context) (string, error) {
	return v.version, nil
}

func assertSavedProgress(t *testing.T, got Progress, blockID uint64) {
	t.Helper()

	want := Progress{
		LastScannedBlockID:          blockID,
		LastSignatureVersionScanned: schedulerTestSignatureVersion,
	}
	if got != want {
		t.Fatalf("progress = %+v, want %+v", got, want)
	}
}
