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
	data    []byte
}

type peerSender struct {
	addr   string
	ch     chan outbound
	conn   *grpc.ClientConn
	cancel context.CancelFunc
	done   chan struct{}
	logger *slog.Logger

	drops          atomic.Uint64
	streamFailures atomic.Uint64
}

func newPeerSender(addr string, conn *grpc.ClientConn, logger *slog.Logger) *peerSender {
	ctx, cancel := context.WithCancel(context.Background())
	ps := &peerSender{
		addr:   addr,
		ch:     make(chan outbound, senderBufferSize),
		conn:   conn,
		cancel: cancel,
		done:   make(chan struct{}),
		logger: logger,
	}
	go ps.run(ctx)
	return ps
}

func (ps *peerSender) send(msg outbound) {
	select {
	case ps.ch <- msg:
	default:
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
				if ctx.Err() == nil {
					ps.logStreamFailure(ctx, "send", err)
					return true
				}
				return false
			}
		case <-ctx.Done():
			return false
		}
	}
}

func (ps *peerSender) drain(ctx context.Context) {
	for {
		select {
		case <-ps.ch:
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
		transport: insecure.NewCredentials(),
		logger:    slog.Default(),
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
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

	sender := newPeerSender(addr, conn, t.logger)
	t.senders[addr] = sender
	return sender
}

func (t *SharedTransport) enqueue(shardID uint64, addr string, msg raftpb.Message) {
	data, err := msg.Marshal()
	if err != nil {
		t.logger.Warn("peer transport: marshal raft message failed",
			"scrap.shard_id", shardID,
			"err", err,
		)
		return
	}

	sender := t.getSender(addr)
	if sender == nil {
		return
	}
	sender.send(outbound{shardID: shardID, data: data})
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
