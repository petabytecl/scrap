package shard_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/shard"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func TestWriteDocumentAckAfterPeerReplicationRaftApplyAndVisibility(t *testing.T) {
	ctx := context.Background()
	replicator := newWriteAckReplicator()
	replicator.BlockAfterPeerAppend()
	telemetry := &recordingWriteTelemetry{}
	cluster := openWriteAckCluster(t, replicator, telemetry)
	leader := cluster.waitForLeader(t)

	payload := []byte("replicated durable ack")
	done := make(chan shardWriteResult, 1)
	go func() {
		result, err := leader.WriteDocument(ctx, "tx-ack-order", "doc.xml", "text/xml", "", bytes.NewReader(payload))
		done <- shardWriteResult{result: result, err: err}
	}()

	replicator.WaitForPeerAppend(t)
	stagesBeforeRelease := telemetry.StageStarts()
	assertStageOrder(t, stagesBeforeRelease, []string{"block_append", "peer_replicate"})
	assertStageAbsent(t, stagesBeforeRelease, "raft_propose")
	assertStageAbsent(t, stagesBeforeRelease, "raft_apply")
	replicator.ReleasePeerAppends()

	write := waitForShardWriteResult(t, done)
	if write.err != nil {
		t.Fatalf("WriteDocument: %v", write.err)
	}

	meta, err := leader.HeadDocument(ctx, "tx-ack-order", "doc.xml")
	if err != nil {
		t.Fatalf("HeadDocument after ACK: %v", err)
	}
	if meta.Size != int64(len(payload)) {
		t.Fatalf("HeadDocument size = %d, want %d", meta.Size, len(payload))
	}
	if meta.SHA256 != write.result.SHA256 {
		t.Fatal("HeadDocument SHA256 should match WriteDocument result")
	}
	if !meta.CreatedAt.Equal(write.result.CreatedAt) {
		t.Fatalf("HeadDocument CreatedAt = %s, want %s", meta.CreatedAt, write.result.CreatedAt)
	}
	if got := replicator.SuccessCount(); got == 0 {
		t.Fatal("expected successful peer replication before ACK")
	}

	assertStageOrder(t, telemetry.StageStarts(), []string{"block_append", "peer_replicate", "raft_propose", "raft_apply"})
	assertClusterOpenlogsEmpty(t, cluster)
}

func TestWriteDocumentPeerDurabilityFailureDoesNotCommitOrAck(t *testing.T) {
	ctx := context.Background()
	replicator := newWriteAckReplicator()
	replicator.fail = true
	telemetry := &recordingWriteTelemetry{}
	cluster := openWriteAckCluster(t, replicator, telemetry)
	leader := cluster.waitForLeader(t)

	_, err := leader.WriteDocument(ctx, "tx-peer-fail", "doc.xml", "text/xml", "", bytes.NewReader([]byte("payload")))
	if !errors.Is(err, storeapi.ErrResourceExhausted) {
		t.Fatalf("WriteDocument error = %v, want ErrResourceExhausted", err)
	}
	if got := replicator.CallCount(); got == 0 {
		t.Fatal("expected attempted peer replication")
	}

	_, err = leader.HeadDocument(ctx, "tx-peer-fail", "doc.xml")
	if !isMissingDocumentOrTransaction(err) {
		t.Fatalf("HeadDocument after failed write = %v, want not found", err)
	}

	stages := telemetry.StageStarts()
	assertStageOrder(t, stages, []string{"block_append", "peer_replicate"})
	assertStageAbsent(t, stages, "raft_propose")
	assertStageAbsent(t, stages, "raft_apply")
}

func TestWriteDocumentPeerDurabilityQuorumAllowsOnePeerFailure(t *testing.T) {
	ctx := context.Background()
	replicator := newWriteAckReplicator()
	replicator.FailNext(1)
	cluster := openWriteAckCluster(t, replicator, &recordingWriteTelemetry{})
	leader := cluster.waitForLeader(t)

	payload := []byte("one peer may fail while quorum succeeds")
	if _, err := leader.WriteDocument(ctx, "tx-peer-quorum", "doc.xml", "text/xml", "", bytes.NewReader(payload)); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	if got := replicator.FailureCount(); got != 1 {
		t.Fatalf("replication failures = %d, want 1", got)
	}
	if got := replicator.SuccessCount(); got == 0 {
		t.Fatal("expected at least one successful peer replication for quorum")
	}
	assertReadDocumentContent(ctx, t, leader, "tx-peer-quorum", payload)
	assertClusterOpenlogsEmpty(t, cluster)
}

func TestOpenlogRecoveryCompletedPrepRetryHasDeterministicTypedOutcome(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := openDurableAckShardInDir(t, dir)

	payload := []byte("committed before client observed ack")
	first, err := s.WriteDocument(ctx, "tx-completed-prep", "doc.xml", "text/xml", "", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	writeOpenlogPrepForTest(t, dir, "leftover-completed", &scrapv1.OpenlogEntry{
		TransactionId: "tx-completed-prep",
		DocumentName:  "doc.xml",
		BlockId:       1,
		StartOffset:   block.HeaderSize,
		ContentType:   "text/xml",
	})

	reopened := openDurableAckShardInDir(t, dir)
	defer func() { _ = reopened.Close() }()
	assertOpenlogEmpty(t, dir)

	retry, err := reopened.WriteDocument(ctx, "tx-completed-prep", "doc.xml", "text/xml", "", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("retry WriteDocument: %v", err)
	}
	if retry.SHA256 != first.SHA256 {
		t.Fatal("retry SHA256 should match original write result")
	}
	if retry.Size != first.Size {
		t.Fatalf("retry Size = %d, want original %d", retry.Size, first.Size)
	}
	if !retry.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("retry CreatedAt = %s, want original %s", retry.CreatedAt, first.CreatedAt)
	}

	assertReadDocumentContent(ctx, t, reopened, "tx-completed-prep", payload)
}

func TestOpenlogRecoveryPartialLocalWriteAllowsSingleRetryVisibility(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := openDurableAckShardInDir(t, dir)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	payload := []byte("partial local bytes")
	writePartialBlockForOpenlogRetry(t, dir, "tx-partial-prep", "doc.xml", payload)
	writeOpenlogPrepForTest(t, dir, "leftover-partial", &scrapv1.OpenlogEntry{
		TransactionId: "tx-partial-prep",
		DocumentName:  "doc.xml",
		BlockId:       1,
		StartOffset:   block.HeaderSize,
		ContentType:   "text/xml",
	})

	reopened := openDurableAckShardInDir(t, dir)
	defer func() { _ = reopened.Close() }()
	assertOpenlogEmpty(t, dir)
	assertBlockSize(t, filepath.Join(dir, "blocks"), 1, block.HeaderSize)

	if _, err := reopened.WriteDocument(ctx, "tx-partial-prep", "doc.xml", "text/xml", "", bytes.NewReader(payload)); err != nil {
		t.Fatalf("retry WriteDocument: %v", err)
	}

	assertReadDocumentContent(ctx, t, reopened, "tx-partial-prep", payload)
	docs, err := reopened.FindDocuments(ctx, "tx-partial-prep")
	if err != nil {
		t.Fatalf("FindDocuments: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("FindDocuments count = %d, want 1", len(docs))
	}
}

func TestOpenlogRecoveryMultiPeerPartialReplicaWriteAllowsSingleRetryVisibility(t *testing.T) {
	ctx := context.Background()
	dirs := []string{t.TempDir(), t.TempDir(), t.TempDir()}
	payload := []byte("partial local and peer bytes")
	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(dir, "blocks"), 0o750); err != nil {
			t.Fatalf("MkdirAll blocks: %v", err)
		}
		if err := os.MkdirAll(filepath.Join(dir, "openlog"), 0o750); err != nil {
			t.Fatalf("MkdirAll openlog: %v", err)
		}
		writePartialBlockForOpenlogRetry(t, dir, "tx-multi-partial", "doc.xml", payload)
		writeOpenlogPrepForTest(t, dir, "leftover-multi-partial", &scrapv1.OpenlogEntry{
			TransactionId: "tx-multi-partial",
			DocumentName:  "doc.xml",
			BlockId:       1,
			StartOffset:   block.HeaderSize,
			ContentType:   "text/xml",
		})
	}

	replicator := newWriteAckReplicator()
	cluster := openWriteAckClusterInDirs(t, dirs, replicator, &recordingWriteTelemetry{})
	leader := cluster.waitForLeader(t)
	for _, dir := range dirs {
		assertOpenlogEmpty(t, dir)
		assertBlockSize(t, filepath.Join(dir, "blocks"), 1, block.HeaderSize)
	}

	if _, err := leader.WriteDocument(ctx, "tx-multi-partial", "doc.xml", "text/xml", "", bytes.NewReader(payload)); err != nil {
		t.Fatalf("retry WriteDocument: %v", err)
	}
	assertReadDocumentContent(ctx, t, leader, "tx-multi-partial", payload)
	docs, err := leader.FindDocuments(ctx, "tx-multi-partial")
	if err != nil {
		t.Fatalf("FindDocuments: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("FindDocuments count = %d, want 1", len(docs))
	}
	assertClusterOpenlogsEmpty(t, cluster)
}

type writeAckReplicator struct {
	mu               sync.Mutex
	shards           map[string]*shard.Shard
	fail             bool
	failNext         int
	calls            int
	successes        int
	failures         int
	blockAfterAppend chan struct{}
	peerAppended     chan struct{}
	appendOnce       sync.Once
}

func newWriteAckReplicator() *writeAckReplicator {
	return &writeAckReplicator{
		shards:       make(map[string]*shard.Shard),
		peerAppended: make(chan struct{}),
	}
}

func (r *writeAckReplicator) Register(addr string, s *shard.Shard) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.shards[addr] = s
}

func (r *writeAckReplicator) BlockAfterPeerAppend() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.blockAfterAppend = make(chan struct{})
}

func (r *writeAckReplicator) ReleasePeerAppends() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.blockAfterAppend != nil {
		close(r.blockAfterAppend)
		r.blockAfterAppend = nil
	}
}

func (r *writeAckReplicator) WaitForPeerAppend(t *testing.T) {
	t.Helper()
	select {
	case <-r.peerAppended:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for peer replication append")
	}
}

func (r *writeAckReplicator) FailNext(count int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failNext = count
}

func (r *writeAckReplicator) CallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func (r *writeAckReplicator) SuccessCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.successes
}

func (r *writeAckReplicator) FailureCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.failures
}

func (r *writeAckReplicator) ReplicateDocument(
	ctx context.Context,
	addr string,
	init *scrapv1.ReplicateDocumentInit,
	chunks [][]byte,
) ([]byte, error) {
	r.mu.Lock()
	r.calls++
	target := r.shards[addr]
	fail := r.fail || r.failNext > 0
	if r.failNext > 0 {
		r.failNext--
	}
	blockAfterAppend := r.blockAfterAppend
	r.mu.Unlock()
	if fail {
		r.recordFailure()
		return nil, errors.New("replication unavailable")
	}
	if target == nil {
		r.recordFailure()
		return nil, fmt.Errorf("no shard registered for %s", addr)
	}
	sha, err := target.AppendReplicatedDocument(ctx, init, bytes.NewReader(bytes.Join(chunks, nil)))
	if err != nil {
		r.recordFailure()
		return nil, err
	}
	r.recordSuccess()
	if blockAfterAppend != nil {
		r.appendOnce.Do(func() { close(r.peerAppended) })
		select {
		case <-blockAfterAppend:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return sha, nil
}

func (r *writeAckReplicator) recordSuccess() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.successes++
}

func (r *writeAckReplicator) recordFailure() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failures++
}

type recordingWriteTelemetry struct {
	mu     sync.Mutex
	stages []string
}

func (r *recordingWriteTelemetry) StartStage(ctx context.Context, stage string) (context.Context, shard.WriteStageEnd) {
	r.mu.Lock()
	r.stages = append(r.stages, stage)
	r.mu.Unlock()
	return ctx, noopRecordedStage{}
}

func (r *recordingWriteTelemetry) StartSpan(ctx context.Context, _ string, _ ...trace.SpanStartOption) (context.Context, shard.WriteStageEnd) {
	return ctx, noopRecordedStage{}
}

func (r *recordingWriteTelemetry) StageStarts() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.stages...)
}

type noopRecordedStage struct{}

func (noopRecordedStage) End(error) {}

func openWriteAckCluster(t *testing.T, replicator *writeAckReplicator, telemetry *recordingWriteTelemetry) *shardCluster {
	t.Helper()
	return openWriteAckClusterInDirs(t, []string{t.TempDir(), t.TempDir(), t.TempDir()}, replicator, telemetry)
}

func openWriteAckClusterInDirs(t *testing.T, dirs []string, replicator *writeAckReplicator, telemetry *recordingWriteTelemetry) *shardCluster {
	t.Helper()
	if len(dirs) != 3 {
		t.Fatalf("dirs count = %d, want 3", len(dirs))
	}
	transport := newShardTransport()
	peers := map[uint64]string{
		1: "localhost:9091",
		2: "localhost:9092",
		3: "localhost:9093",
	}

	cluster := &shardCluster{transport: transport}
	for i := range 3 {
		id := uint64(i + 1)
		s, err := shard.Open(shard.Config{
			DataDir:        dirs[i],
			ShardID:        0,
			RaftID:         id,
			Peers:          peers,
			TickInterval:   10 * time.Millisecond,
			Transport:      transport,
			Replicator:     replicator,
			WriteTelemetry: telemetry,
		})
		if err != nil {
			t.Fatalf("Open shard %d: %v", id, err)
		}
		cluster.shards = append(cluster.shards, s)
		transport.Register(id, s)
		replicator.Register(peers[id], s)
	}

	t.Cleanup(func() {
		for _, s := range cluster.shards {
			_ = s.Close()
		}
	})

	return cluster
}

type shardWriteResult struct {
	result storeapi.WriteResult
	err    error
}

func waitForShardWriteResult(t *testing.T, done <-chan shardWriteResult) shardWriteResult {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for shard WriteDocument")
		return shardWriteResult{}
	}
}

func openDurableAckShardInDir(t *testing.T, dir string) *shard.Shard {
	t.Helper()
	s, err := shard.Open(shard.Config{
		DataDir:      dir,
		ShardID:      0,
		RaftID:       1,
		Peers:        map[uint64]string{1: "localhost:9091"},
		TickInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	waitForLeader(t, s)
	return s
}

func writePartialBlockForOpenlogRetry(t *testing.T, dataDir, txID, docName string, payload []byte) {
	t.Helper()
	blocksDir := filepath.Join(dataDir, "blocks")
	writer, err := block.NewWriter(block.FilePath(blocksDir, 1), 0, 1)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if _, err := writer.AppendDocument(txID, docName, "text/xml", bytes.NewReader(payload)); err != nil {
		_ = writer.Close()
		t.Fatalf("AppendDocument: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close writer: %v", err)
	}
}

func assertStageOrder(t *testing.T, got, want []string) {
	t.Helper()
	next := 0
	for _, stage := range got {
		if next < len(want) && stage == want[next] {
			next++
		}
	}
	if next != len(want) {
		t.Fatalf("stage order %v does not contain ordered subsequence %v", got, want)
	}
}

func assertStageAbsent(t *testing.T, stages []string, forbidden string) {
	t.Helper()
	for _, stage := range stages {
		if stage == forbidden {
			t.Fatalf("stage %q unexpectedly present in %v", forbidden, stages)
		}
	}
}

func assertOpenlogEmpty(t *testing.T, dataDir string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		entries, err := os.ReadDir(filepath.Join(dataDir, "openlog"))
		if err != nil {
			t.Fatalf("ReadDir openlog: %v", err)
		}
		var prepFiles []string
		for _, entry := range entries {
			if !entry.IsDir() {
				prepFiles = append(prepFiles, entry.Name())
			}
		}
		if len(prepFiles) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("openlog entries should not remain after recovery/commit: %v", prepFiles)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func assertClusterOpenlogsEmpty(t *testing.T, cluster *shardCluster) {
	t.Helper()
	for _, s := range cluster.shards {
		assertOpenlogEmpty(t, s.DataDirForTest())
	}
}

func assertBlockSize(t *testing.T, blocksDir string, blockID uint64, want int64) {
	t.Helper()
	info, err := os.Stat(block.FilePath(blocksDir, blockID))
	if err != nil {
		t.Fatalf("Stat block %d: %v", blockID, err)
	}
	if info.Size() != want {
		t.Fatalf("block %d size = %d, want %d", blockID, info.Size(), want)
	}
}

func isMissingDocumentOrTransaction(err error) bool {
	return errors.Is(err, storeapi.ErrNotFound) || errors.Is(err, storeapi.ErrTxNotFound)
}
