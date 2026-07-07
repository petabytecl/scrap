package peer

// Send-outcome reporting for the raft transport (#462): every path that
// drops a raft message must report it, or a dropped MsgSnap strands the
// follower in StateSnapshot on the leader.

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"
)

type recordingReporter struct {
	mu            sync.Mutex
	unreachable   []uint64
	snapFailures  []uint64
	snapSuccesses []uint64
}

func (r *recordingReporter) ReportUnreachable(id uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.unreachable = append(r.unreachable, id)
}

func (r *recordingReporter) ReportSnapshotFailure(id uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.snapFailures = append(r.snapFailures, id)
}

func (r *recordingReporter) ReportSnapshotSuccess(id uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.snapSuccesses = append(r.snapSuccesses, id)
}

func (r *recordingReporter) snapshot() (unreachable, failures, successes []uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.unreachable), slices.Clone(r.snapFailures), slices.Clone(r.snapSuccesses)
}

func TestShardTransportRegistersStatusReporter(t *testing.T) {
	shared := NewSharedTransport(map[uint64]string{2: "localhost:1"})
	defer shared.Close()
	st := shared.ForShard(7, map[uint64]string{2: "localhost:1"})

	reporter := &recordingReporter{}
	st.SetStatusReporter(reporter)
	if got := shared.reporterFor(7); got != reporter {
		t.Fatalf("reporterFor(7) = %v, want the registered reporter", got)
	}
	if got := shared.reporterFor(8); got != nil {
		t.Fatalf("reporterFor(8) = %v, want nil for an unregistered shard", got)
	}
}

func TestPeerSenderBufferFullDropReportsSnapshotFailure(t *testing.T) {
	shared := NewSharedTransport(nil)
	defer shared.Close()
	reporter := &recordingReporter{}
	shared.setReporter(7, reporter)

	// An unbuffered channel with no consumer forces the buffer-full drop path.
	ps := &peerSender{
		addr:   "test-peer",
		ch:     make(chan outbound),
		logger: slog.New(slog.DiscardHandler),
		report: shared.reportSendResult,
	}
	ps.send(outbound{shardID: 7, to: 2, snap: true})

	unreachable, failures, successes := reporter.snapshot()
	if !slices.Equal(unreachable, []uint64{2}) || !slices.Equal(failures, []uint64{2}) {
		t.Fatalf("dropped MsgSnap reported unreachable=%v snapFailures=%v, want [2]/[2]", unreachable, failures)
	}
	if len(successes) != 0 {
		t.Fatalf("snapshot successes = %v, want none", successes)
	}
}

func TestPeerSenderDrainReportsEveryDroppedMessage(t *testing.T) {
	shared := NewSharedTransport(nil)
	defer shared.Close()
	reporter := &recordingReporter{}
	shared.setReporter(7, reporter)

	ps := &peerSender{
		addr:   "test-peer",
		ch:     make(chan outbound, 4),
		logger: slog.New(slog.DiscardHandler),
		report: shared.reportSendResult,
	}
	ps.ch <- outbound{shardID: 7, to: 2, snap: false}
	ps.ch <- outbound{shardID: 7, to: 3, snap: true}
	ps.drain(context.Background())

	unreachable, failures, _ := reporter.snapshot()
	if !slices.Equal(unreachable, []uint64{2, 3}) {
		t.Fatalf("drained unreachable reports = %v, want [2 3]", unreachable)
	}
	if !slices.Equal(failures, []uint64{3}) {
		t.Fatalf("drained snapshot failures = %v, want [3]", failures)
	}
}

// Close holds the transport mutex while waiting for sender goroutines to
// stop, and a stopping sender's failed stream.Send reports its outcome — so
// the reporter lookup must never contend with the transport mutex or
// shutdown deadlocks (Codex review on #481).
func TestReportSendResultDoesNotContendWithTransportMutex(t *testing.T) {
	shared := NewSharedTransport(nil)
	reporter := &recordingReporter{}
	shared.setReporter(7, reporter)

	shared.mu.Lock() // simulate Close holding the transport lock
	defer shared.mu.Unlock()

	done := make(chan struct{})
	go func() {
		shared.reportSendResult(outbound{shardID: 7, to: 2, snap: true}, false)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reportSendResult blocked on the transport mutex; a stopping sender's report would deadlock Close")
	}
}

func TestReportSendResultDeliveredSnapshotReportsSuccess(t *testing.T) {
	shared := NewSharedTransport(nil)
	defer shared.Close()
	reporter := &recordingReporter{}
	shared.setReporter(7, reporter)

	shared.reportSendResult(outbound{shardID: 7, to: 2, snap: true}, true)
	shared.reportSendResult(outbound{shardID: 7, to: 2, snap: false}, true)
	// No reporter for shard 9: must be a silent no-op.
	shared.reportSendResult(outbound{shardID: 9, to: 4, snap: true}, false)

	unreachable, failures, successes := reporter.snapshot()
	if !slices.Equal(successes, []uint64{2}) {
		t.Fatalf("snapshot successes = %v, want [2]", successes)
	}
	if len(unreachable) != 0 || len(failures) != 0 {
		t.Fatalf("delivered messages reported unreachable=%v failures=%v, want none", unreachable, failures)
	}
}
