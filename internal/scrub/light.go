package scrub

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sort"
	"sync"
	"time"

	"github.com/petabytecl/scrap/internal/ulid"
)

const (
	defaultPeerResultTimeout      = 10 * time.Second
	defaultPeerResultPollInterval = 200 * time.Millisecond
)

type Proposer interface {
	ProposeConsistencyCheck(ctx context.Context, scrubID string) (Result, error)
}

type ConsistencyChecker interface {
	CheckConsistency(ctx context.Context, addr, scrubID string) (Result, error)
}

type LeaderChecker interface {
	IsLeader() bool
}

type Metrics interface {
	RecordRun(result string, durationSec float64)
}

type Rebuilder interface {
	RequestRebuild(ctx context.Context, addr, scrubID string) error
}

// SelfRebuilder triggers a projection rebuild on this Member. The light scrub
// uses it when the majority hash disagrees with the leader's own (#470): the
// leader is the divergent voter and must rebuild itself instead of rebuilding
// every healthy peer forever.
type SelfRebuilder interface {
	RequestSelfRebuild(ctx context.Context, scrubID string) error
}

type LightConfig struct {
	Proposer           Proposer
	ConsistencyChecker ConsistencyChecker
	LeaderChecker      LeaderChecker
	Metrics            Metrics
	Rebuilder          Rebuilder
	SelfRebuilder      SelfRebuilder
	Logger             *slog.Logger
	PeerAddrs          []string
	Interval           time.Duration
	Jitter             float64
	PeerResultTimeout  time.Duration
	PeerResultPoll     time.Duration
}

type Light struct {
	cfg    LightConfig
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewLight(cfg LightConfig) *Light {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultLightScrubInterval
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.PeerResultTimeout <= 0 {
		cfg.PeerResultTimeout = defaultPeerResultTimeout
	}
	if cfg.PeerResultPoll <= 0 {
		cfg.PeerResultPoll = defaultPeerResultPollInterval
	}
	return &Light{cfg: cfg}
}

func (ls *Light) Start(ctx context.Context) {
	ctx, ls.cancel = context.WithCancel(ctx)
	ls.wg.Add(1)
	go ls.loop(ctx)
}

func (ls *Light) Stop() {
	if ls.cancel != nil {
		ls.cancel()
	}
	ls.wg.Wait()
}

func (ls *Light) loop(ctx context.Context) {
	defer ls.wg.Done()

	for {
		delay := ls.jitteredInterval()
		select {
		case <-time.After(delay):
			_ = ls.RunOnce(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (ls *Light) jitteredInterval() time.Duration {
	if ls.cfg.Jitter <= 0 {
		return ls.cfg.Interval
	}
	jitter := ls.cfg.Jitter * float64(ls.cfg.Interval)
	offset := (rand.Float64()*2 - 1) * jitter //nolint:gosec // jitter is not security-sensitive
	return ls.cfg.Interval + time.Duration(offset)
}

func (ls *Light) RunOnce(ctx context.Context) error {
	if !ls.cfg.LeaderChecker.IsLeader() {
		return nil
	}

	start := time.Now()
	scrubID := ulid.New().String()

	leaderResult, err := ls.cfg.Proposer.ProposeConsistencyCheck(ctx, scrubID)
	if err != nil {
		ls.cfg.Logger.WarnContext(ctx, "scrub: consistency check proposal failed", "scrub_id", scrubID, "err", err)
		ls.cfg.Metrics.RecordRun("error", time.Since(start).Seconds())
		return err
	}

	// A zero hash is impossible for a real SHA-256 and means the voter failed
	// to compute one. Comparing zero hashes would either mark every healthy
	// peer divergent (triggering projection rebuilds off a transient local
	// failure) or mask real divergence when two voters fail together.
	if leaderResult.SHA256 == [32]byte{} {
		ls.cfg.Metrics.RecordRun("error", time.Since(start).Seconds())
		return fmt.Errorf("scrub: leader projection hash missing for scrub %s", scrubID)
	}

	peerHashes, err := ls.collectPeerHashes(ctx, scrubID)
	if err != nil {
		ls.cfg.Metrics.RecordRun("error", time.Since(start).Seconds())
		return err
	}

	return ls.applyQuorumVerdict(ctx, scrubID, leaderResult.SHA256, peerHashes, start)
}

// applyQuorumVerdict majority-votes the leader and peer projection hashes and
// rebuilds the minority (#470). The pre-quorum behavior judged divergence
// solely against the leader's hash, so a corrupt leader flagged every healthy
// peer divergent and rebuilt them forever while itself never rebuilding. A
// tie (no strict majority among comparable voters) freezes: no rebuilds, a
// distinct degraded run result, and an error-level alert for the operator —
// rebuilding on a coin flip could propagate the corrupt copy.
func (ls *Light) applyQuorumVerdict(
	ctx context.Context,
	scrubID string,
	leaderHash [32]byte,
	peerHashes map[string][32]byte,
	start time.Time,
) error {
	majority, ok := majorityHash(leaderHash, peerHashes)
	if !ok {
		ls.cfg.Logger.ErrorContext(ctx, "scrub: projection hash quorum tie; refusing to rebuild",
			"scrub_id", scrubID, "voters", 1+len(peerHashes))
		ls.cfg.Metrics.RecordRun("quorum_tie", time.Since(start).Seconds())
		return fmt.Errorf("scrub: projection hash quorum tie for scrub %s across %d voter(s)", scrubID, 1+len(peerHashes))
	}

	var divergent []string
	for addr, hash := range peerHashes {
		if hash != majority {
			divergent = append(divergent, addr)
		}
	}
	sort.Strings(divergent)
	leaderDivergent := leaderHash != majority

	duration := time.Since(start).Seconds()
	if !leaderDivergent && len(divergent) == 0 {
		ls.cfg.Metrics.RecordRun("ok", duration)
		return nil
	}

	ls.cfg.Metrics.RecordRun("mismatch", duration)
	if leaderDivergent {
		ls.requestSelfRebuild(ctx, scrubID)
	}
	if ls.cfg.Rebuilder != nil {
		for _, addr := range divergent {
			if err := ls.cfg.Rebuilder.RequestRebuild(ctx, addr, scrubID); err != nil {
				ls.cfg.Logger.WarnContext(ctx, "scrub: rebuild request failed", "addr", addr, "scrub_id", scrubID, "err", err)
			}
		}
	}
	return nil
}

func (ls *Light) requestSelfRebuild(ctx context.Context, scrubID string) {
	if ls.cfg.SelfRebuilder == nil {
		ls.cfg.Logger.ErrorContext(ctx, "scrub: leader projection diverges from quorum but no self rebuilder is wired",
			"scrub_id", scrubID)
		return
	}
	if err := ls.cfg.SelfRebuilder.RequestSelfRebuild(ctx, scrubID); err != nil {
		ls.cfg.Logger.WarnContext(ctx, "scrub: self rebuild request failed", "scrub_id", scrubID, "err", err)
	}
}

// majorityHash returns the hash held by a strict majority of the comparable
// voters (leader + peers with a published hash), or ok=false on a tie.
func majorityHash(leaderHash [32]byte, peerHashes map[string][32]byte) ([32]byte, bool) {
	votes := map[[32]byte]int{leaderHash: 1}
	for _, hash := range peerHashes {
		votes[hash]++
	}
	total := 1 + len(peerHashes)
	for hash, n := range votes {
		if n > total/2 {
			return hash, true
		}
	}
	return [32]byte{}, false
}

func (ls *Light) collectPeerHashes(ctx context.Context, scrubID string) (map[string][32]byte, error) {
	hashes := make(map[string][32]byte, len(ls.cfg.PeerAddrs))
	for _, addr := range ls.cfg.PeerAddrs {
		peerResult, err := ls.checkPeerConsistency(ctx, addr, scrubID)
		if errors.Is(err, ErrConsistencyResultNotReady) {
			// The peer has not published its result for this scrub yet; abort so
			// the whole check retries next cycle rather than reporting against a
			// partial peer set.
			ls.cfg.Logger.WarnContext(ctx, "scrub: peer consistency result not ready", "addr", addr, "scrub_id", scrubID)
			return nil, err
		}
		if err != nil {
			// An unreachable or errored peer is inconclusive, not divergent: skip
			// it and keep evaluating the reachable peers instead of aborting the
			// entire run on one down voter.
			ls.cfg.Logger.WarnContext(ctx, "scrub: peer consistency check inconclusive", "addr", addr, "scrub_id", scrubID, "err", err)
			continue
		}
		if peerResult.SHA256 == [32]byte{} {
			return nil, fmt.Errorf("scrub: peer %s projection hash missing for scrub %s", addr, scrubID)
		}
		hashes[addr] = peerResult.SHA256
	}
	// If the Cell has peers but none produced a comparable result, the check is
	// inconclusive, not clean: reporting ok here would mask a total loss of
	// projection-divergence coverage. Fail so RunOnce records a degraded run and
	// retries next cycle.
	if len(ls.cfg.PeerAddrs) > 0 && len(hashes) == 0 {
		return nil, fmt.Errorf("scrub: no peer consistency result compared for scrub %s across %d peer(s)", scrubID, len(ls.cfg.PeerAddrs))
	}
	return hashes, nil
}

func (ls *Light) checkPeerConsistency(ctx context.Context, addr, scrubID string) (Result, error) {
	deadline := time.NewTimer(ls.cfg.PeerResultTimeout)
	defer deadline.Stop()

	ticker := time.NewTicker(ls.cfg.PeerResultPoll)
	defer ticker.Stop()

	var lastErr error
	for {
		result, err := ls.cfg.ConsistencyChecker.CheckConsistency(ctx, addr, scrubID)
		if err == nil {
			return result, nil
		}
		if !errors.Is(err, ErrConsistencyResultNotReady) {
			return Result{}, err
		}
		lastErr = err

		select {
		case <-ticker.C:
		case <-deadline.C:
			return Result{}, lastErr
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}
}
