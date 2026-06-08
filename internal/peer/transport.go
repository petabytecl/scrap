package peer

import (
	"context"
	"fmt"
	"sync"
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
}

func newPeerSender(addr string, conn *grpc.ClientConn) *peerSender {
	ctx, cancel := context.WithCancel(context.Background())
	ps := &peerSender{
		addr:   addr,
		ch:     make(chan outbound, senderBufferSize),
		conn:   conn,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	go ps.run(ctx)
	return ps
}

func (ps *peerSender) send(msg outbound) {
	select {
	case ps.ch <- msg:
	default:
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
			ps.drain(ctx)
			select {
			case <-time.After(reconnectBackoff):
			case <-ctx.Done():
				return
			}
			continue
		}

		if !ps.sendLoop(ctx, stream) {
			return
		}
	}
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
				return ctx.Err() == nil
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

func NewSharedTransport(peers map[uint64]string, opts ...SharedTransportOption) *SharedTransport {
	t := &SharedTransport{
		peers:     peers,
		conns:     make(map[string]*grpc.ClientConn),
		senders:   make(map[string]*peerSender),
		transport: insecure.NewCredentials(),
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
		return nil
	}
	t.conns[addr] = conn

	sender := newPeerSender(addr, conn)
	t.senders[addr] = sender
	return sender
}

func (t *SharedTransport) enqueue(shardID uint64, addr string, msg raftpb.Message) {
	data, err := msg.Marshal()
	if err != nil {
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
