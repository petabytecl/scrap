package peer

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"go.etcd.io/raft/v3/raftpb"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	scrapraft "github.com/petabytecl/scrap/internal/raft"
)

const reconnectBackoff = 100 * time.Millisecond

const senderBufferSize = 4096

type RaftRouter interface {
	RouteRaftMessage(ctx context.Context, shardID uint64, msg raftpb.Message) error
}

type RaftRouterFunc func(ctx context.Context, shardID uint64, msg raftpb.Message) error

func (f RaftRouterFunc) RouteRaftMessage(ctx context.Context, shardID uint64, msg raftpb.Message) error {
	return f(ctx, shardID, msg)
}

type outbound struct {
	shardID uint64
	to      uint64
	snap    bool
	data    []byte
}

// sendReport receives per-message delivery outcomes so dropped raft traffic —
// snapshots above all — is reported back to the raft state machine instead of
// silently stranding a follower in StateSnapshot (#462).
type sendReport func(msg outbound, delivered bool)

type peerSender struct {
	addr   string
	ch     chan outbound
	conn   *grpc.ClientConn
	cancel context.CancelFunc
	done   chan struct{}
	logger *slog.Logger
	report sendReport

	drops          atomic.Uint64
	streamFailures atomic.Uint64
}

func newPeerSender(addr string, conn *grpc.ClientConn, logger *slog.Logger, report sendReport) *peerSender {
	ctx, cancel := context.WithCancel(context.Background())
	ps := &peerSender{
		addr:   addr,
		ch:     make(chan outbound, senderBufferSize),
		conn:   conn,
		cancel: cancel,
		done:   make(chan struct{}),
		logger: logger,
		report: report,
	}
	go ps.run(ctx)
	return ps
}

func (ps *peerSender) send(msg outbound) {
	select {
	case ps.ch <- msg:
	default:
		ps.report(msg, false)
		count := ps.drops.Add(1)
		if shouldLogPowerOfTwoCount(count) {
			ps.logger.Warn("peer transport: raft outbound buffer full, dropping message",
				"peer_addr", ps.addr,
				"dropped_total", count,
			)
		}
	}
}

func (ps *peerSender) stop() {
	ps.cancel()
	<-ps.done
}

func (ps *peerSender) run(ctx context.Context) {
	defer close(ps.done)

	for {
		stream, err := ps.openStream(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			ps.logStreamFailure(ctx, "open", err)
			ps.drain(ctx)
			if !ps.backoff(ctx) {
				return
			}
			continue
		}

		go ps.observeStream(ctx, stream)
		if !ps.sendLoop(ctx, stream) {
			return
		}
		// The stream broke mid-send; back off before redialing so a peer that
		// rejects every stream does not drive a message-rate reconnect loop.
		if !ps.backoff(ctx) {
			return
		}
	}
}

func (ps *peerSender) backoff(ctx context.Context) bool {
	select {
	case <-time.After(reconnectBackoff):
		return true
	case <-ctx.Done():
		return false
	}
}

// observeStream surfaces the server's terminal status for the stream — e.g.
// an authorization denial that would otherwise leave a permanently mute raft
// link with no sender-side evidence. Receiving until error also releases the
// abandoned stream's resources, per the gRPC client stream contract.
func (ps *peerSender) observeStream(ctx context.Context, stream grpc.BidiStreamingClient[scrapv1.ForwardRaftStreamRequest, scrapv1.ForwardRaftStreamResponse]) {
	for {
		if _, err := stream.Recv(); err != nil {
			if ctx.Err() == nil {
				ps.logStreamFailure(ctx, "recv", err)
			}
			return
		}
	}
}

func (ps *peerSender) logStreamFailure(ctx context.Context, op string, err error) {
	count := ps.streamFailures.Add(1)
	if !shouldLogPowerOfTwoCount(count) {
		return
	}
	ps.logger.WarnContext(ctx, "peer transport: raft stream failed",
		"peer_addr", ps.addr,
		"op", op,
		"failures_total", count,
		"err", err,
	)
}

func (ps *peerSender) openStream(ctx context.Context) (grpc.BidiStreamingClient[scrapv1.ForwardRaftStreamRequest, scrapv1.ForwardRaftStreamResponse], error) {
	client := scrapv1.NewPeerServiceClient(ps.conn)
	stream, err := client.ForwardRaftStream(ctx)
	if err != nil {
		return nil, fmt.Errorf("peer transport: open stream to %s: %w", ps.addr, err)
	}
	return stream, nil
}

func (ps *peerSender) sendLoop(ctx context.Context, stream grpc.BidiStreamingClient[scrapv1.ForwardRaftStreamRequest, scrapv1.ForwardRaftStreamResponse]) bool {
	for {
		select {
		case msg, ok := <-ps.ch:
			if !ok {
				return false
			}
			if err := stream.Send(&scrapv1.ForwardRaftStreamRequest{
				ShardId: msg.shardID,
				Message: msg.data,
			}); err != nil {
				ps.report(msg, false)
				if ctx.Err() == nil {
					ps.logStreamFailure(ctx, "send", err)
					return true
				}
				return false
			}
			ps.report(msg, true)
		case <-ctx.Done():
			return false
		}
	}
}

func (ps *peerSender) drain(ctx context.Context) {
	for {
		select {
		case msg := <-ps.ch:
			ps.report(msg, false)
		case <-ctx.Done():
			return
		default:
			return
		}
	}
}

type SharedTransport struct {
	mu        sync.Mutex
	peers     map[uint64]string
	conns     map[string]*grpc.ClientConn
	senders   map[string]*peerSender
	transport credentials.TransportCredentials
	logger    *slog.Logger
	closed    bool

	// reporters has its own mutex: sender goroutines report send outcomes
	// while Close holds t.mu waiting for those same goroutines to stop, so
	// guarding reporters with t.mu would deadlock shutdown.
	reportersMu sync.Mutex
	reporters   map[uint64]scrapraft.StatusReporter
}

type SharedTransportOption func(*SharedTransport)

func WithSharedTransportCredentials(creds credentials.TransportCredentials) SharedTransportOption {
	return func(t *SharedTransport) {
		if creds != nil {
			t.transport = creds
		}
	}
}

func WithSharedTransportLogger(logger *slog.Logger) SharedTransportOption {
	return func(t *SharedTransport) {
		if logger != nil {
			t.logger = logger
		}
	}
}

func NewSharedTransport(peers map[uint64]string, opts ...SharedTransportOption) *SharedTransport {
	t := &SharedTransport{
		peers:     peers,
		conns:     make(map[string]*grpc.ClientConn),
		senders:   make(map[string]*peerSender),
		reporters: make(map[uint64]scrapraft.StatusReporter),
		transport: insecure.NewCredentials(),
		logger:    slog.Default(),
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

func (t *SharedTransport) setReporter(shardID uint64, r scrapraft.StatusReporter) {
	t.reportersMu.Lock()
	defer t.reportersMu.Unlock()
	t.reporters[shardID] = r
}

func (t *SharedTransport) reporterFor(shardID uint64) scrapraft.StatusReporter {
	t.reportersMu.Lock()
	defer t.reportersMu.Unlock()
	return t.reporters[shardID]
}

// reportSendResult feeds per-message delivery outcomes back to the shard's
// raft node. A dropped message marks the peer unreachable; a dropped MsgSnap
// additionally reports snapshot failure so the leader leaves StateSnapshot
// and retries instead of silently running one durable replica short (#462).
func (t *SharedTransport) reportSendResult(msg outbound, delivered bool) {
	r := t.reporterFor(msg.shardID)
	if r == nil {
		return
	}
	if delivered {
		if msg.snap {
			r.ReportSnapshotSuccess(msg.to)
		}
		return
	}
	r.ReportUnreachable(msg.to)
	if msg.snap {
		r.ReportSnapshotFailure(msg.to)
	}
}

func (t *SharedTransport) ForShard(shardID uint64, peers map[uint64]string) *ShardTransport {
	return &ShardTransport{
		shared:  t,
		shardID: shardID,
		peers:   peers,
	}
}

func (t *SharedTransport) getSender(addr string) *peerSender {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return nil
	}

	if sender, ok := t.senders[addr]; ok {
		return sender
	}

	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(t.transport.Clone()),
		grpc.WithConnectParams(grpc.ConnectParams{MinConnectTimeout: reconnectBackoff}),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		t.logger.Warn("peer transport: dial failed", "peer_addr", addr, "err", err)
		return nil
	}
	t.conns[addr] = conn

	sender := newPeerSender(addr, conn, t.logger, t.reportSendResult)
	t.senders[addr] = sender
	return sender
}

func (t *SharedTransport) enqueue(shardID uint64, addr string, msg raftpb.Message) {
	out := outbound{
		shardID: shardID,
		to:      msg.To,
		snap:    msg.Type == raftpb.MsgSnap,
	}
	data, err := msg.Marshal()
	if err != nil {
		t.logger.Warn("peer transport: marshal raft message failed",
			"scrap.shard_id", shardID,
			"err", err,
		)
		t.reportSendResult(out, false)
		return
	}
	out.data = data

	sender := t.getSender(addr)
	if sender == nil {
		t.reportSendResult(out, false)
		return
	}
	sender.send(out)
}

func (t *SharedTransport) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.closed = true
	for _, sender := range t.senders {
		sender.stop()
	}
	for _, conn := range t.conns {
		_ = conn.Close()
	}
}

type ShardTransport struct {
	shared  *SharedTransport
	shardID uint64
	peers   map[uint64]string
}

func (st *ShardTransport) Send(msgs []raftpb.Message) {
	for _, m := range msgs {
		addr, ok := st.peers[m.To]
		if !ok {
			continue
		}
		st.shared.enqueue(st.shardID, addr, m)
	}
}

// SetStatusReporter implements raft.ReporterSink: the shard's raft node
// registers itself so this transport can report send outcomes (#462).
func (st *ShardTransport) SetStatusReporter(r scrapraft.StatusReporter) {
	st.shared.setReporter(st.shardID, r)
}
