package peer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"go.etcd.io/raft/v3/raftpb"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	grpcpeer "google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/audit"
	"github.com/petabytecl/scrap/internal/security"
)

const (
	wrongShardPeerAddressFixture     = "203.0.113.42:9443"
	wrongShardCertMaterialFixture    = "-----BEGIN CERTIFICATE-----secret-cert-material-----END CERTIFICATE-----"
	wrongShardTransactionFixture     = "tx-secret-route"
	wrongShardDocumentFixture        = "invoice-secret.xml"
	wrongShardLocalPathFixture       = "/tmp/secret-local-path"
	wrongShardBackendKeyFixture      = "backend-key-secret"
	wrongShardDependencyErrorFixture = "dependency detail secret"
)

func TestPeerServerAuditsAndRateLimitsPeerOperations(t *testing.T) {
	expected := peerAuthExpectedIdentity()
	authz := security.NewStaticAuthorizer()
	sink := audit.NewMemorySink()
	limiter := security.NewRateLimiter(security.RateLimitPolicy{
		Surfaces: []security.RateLimitSurfacePolicy{
			{Surface: security.RateLimitSurfacePeer, Limit: 1, Window: time.Minute},
		},
	})
	srv := NewServer(t.TempDir(), WithAuthorizer(authz, expected), WithAuditSink(sink), WithRateLimiter(limiter))
	defer func() { _ = srv.Close() }()
	router := &recordingRaftRouter{}
	srv.SetRaftRouter(router)
	ctx := peerAuthContext(security.NewRoleSet(security.RolePeerMember), security.PeerIdentityConfig{
		CellID:         "cell-a",
		MemberHostname: "scrapd-1",
		MemberID:       "member-b",
	})

	if _, err := srv.ForwardRaft(ctx, &scrapv1.ForwardRaftRequest{Message: marshalRaftMessage(t)}); err != nil {
		t.Fatalf("ForwardRaft first call: %v", err)
	}
	_, err := srv.ForwardRaft(ctx, &scrapv1.ForwardRaftRequest{Message: mustMarshalRaftForRateLimit(t)})
	if !errors.Is(err, security.ErrRateLimited) || status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("ForwardRaft second call = %v (%s), want rate limited", err, status.Code(err))
	}
	if router.calls != 1 {
		t.Fatalf("router calls = %d, want 1", router.calls)
	}
	events := sink.Events()
	if len(events) != 2 {
		t.Fatalf("audit events = %d, want 2: %+v", len(events), events)
	}
	if events[0].Operation != audit.OperationForwardRaft || events[1].Result != audit.ResultRateLimited {
		t.Fatalf("unexpected audit events: %+v", events)
	}
}

func TestPeerServerAuditsUnauthorizedReplicationShardWithoutAllowedEvent(t *testing.T) {
	expected := peerAuthExpectedIdentity()
	authz := security.NewStaticAuthorizer()
	sink := audit.NewMemorySink()
	srv := NewServer(
		t.TempDir(),
		WithAuthorizer(authz, expected),
		WithAuditSink(sink),
		WithReplicationSink(&recordingReplicationSink{}),
		WithAuthorizedShards(7),
	)
	defer func() { _ = srv.Close() }()

	stream := &replicateDocumentStream{
		ctx: peerAuthContext(security.NewRoleSet(security.RolePeerMember), security.PeerIdentityConfig{
			CellID:         "cell-a",
			MemberHostname: "scrapd-1",
			MemberID:       "member-b",
		}),
		requests: []*scrapv1.ReplicateDocumentRequest{{
			Part: &scrapv1.ReplicateDocumentRequest_Init{
				Init: validReplicateDocumentInit(func(init *scrapv1.ReplicateDocumentInit) {
					init.ShardId = 8
					init.BlockId = 1
				}),
			},
		}},
	}

	err := srv.ReplicateDocument(stream)
	if !errors.Is(err, security.ErrPermissionDenied) || status.Code(err) != codes.PermissionDenied {
		t.Fatalf("ReplicateDocument wrong Shard = %v (%s), want permission denied", err, status.Code(err))
	}
	events := sink.Events()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1: %+v", len(events), events)
	}
	if events[0].Operation != audit.OperationReplicateDocument || events[0].Result != audit.ResultDenied {
		t.Fatalf("unexpected audit event: %+v", events[0])
	}
}

func TestPeerServerAuditsUnauthorizedStreamShardWithoutAllowedEvent(t *testing.T) {
	expected := peerAuthExpectedIdentity()
	authz := security.NewStaticAuthorizer()
	sink := audit.NewMemorySink()
	srv := NewServer(t.TempDir(), WithAuthorizer(authz, expected), WithAuditSink(sink), WithAuthorizedShards(7))
	defer func() { _ = srv.Close() }()

	router := &recordingRaftRouter{}
	srv.SetRaftRouter(router)
	stream := &forwardRaftStream{
		ctx: peerAuthContext(security.NewRoleSet(security.RolePeerMember), security.PeerIdentityConfig{
			CellID:         "cell-a",
			MemberHostname: "scrapd-1",
			MemberID:       "member-b",
		}),
		requests: []*scrapv1.ForwardRaftStreamRequest{{
			ShardId: 8,
			Message: marshalRaftMessage(t),
		}},
	}

	err := srv.ForwardRaftStream(stream)
	if !errors.Is(err, security.ErrPermissionDenied) || status.Code(err) != codes.PermissionDenied {
		t.Fatalf("ForwardRaftStream wrong Shard = %v (%s), want permission denied", err, status.Code(err))
	}
	if router.calls != 0 {
		t.Fatalf("router calls = %d, want 0", router.calls)
	}
	events := sink.Events()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1: %+v", len(events), events)
	}
	if events[0].Operation != audit.OperationForwardRaftStream || events[0].Result != audit.ResultDenied {
		t.Fatalf("unexpected audit event: %+v", events[0])
	}
}

func TestPeerServerAuditsWrongShardDenialsWithoutRawIdentifierLeaks(t *testing.T) {
	expected := peerAuthExpectedIdentity()
	caller := security.PeerIdentityConfig{
		CellID:         "cell-a",
		MemberHostname: "secret-peer-host",
		MemberID:       "member-secret-raw",
	}
	for _, tt := range wrongShardAuditCases(t) {
		t.Run(tt.name, func(t *testing.T) {
			authz := security.NewStaticAuthorizer()
			var log bytes.Buffer
			sink := newRecordingAuditLogSink(&log)
			reader := metric.NewManualReader()
			meterProvider := metric.NewMeterProvider(metric.WithReader(reader))
			t.Cleanup(func() { _ = meterProvider.Shutdown(context.Background()) })
			authMetrics, err := security.NewAuthorizationOTelMetrics(meterProvider.Meter("test"))
			if err != nil {
				t.Fatalf("NewAuthorizationOTelMetrics: %v", err)
			}
			srv := NewServer(
				t.TempDir(),
				WithAuthorizer(authz, expected),
				WithAuditSink(sink),
				WithAuthorizationObserver(authMetrics),
				WithAuthorizedShards(7),
			)
			defer func() { _ = srv.Close() }()
			if tt.configure != nil {
				tt.configure(srv)
			}

			err = tt.call(srv, wrongShardEvidenceContext(caller))
			metricEvidence := authorizationDeniedMetricEvidence(t, reader, tt.operation)
			assertWrongShardAuditDenial(t, tt, sink.Events(), err, caller, log.String(), metricEvidence)
			if tt.assertNoSideEffects != nil {
				tt.assertNoSideEffects(t)
			}
		})
	}
}

type wrongShardAuditCase struct {
	name                string
	operation           string
	target              string
	configure           func(*Server)
	call                func(*Server, context.Context) error
	assertNoSideEffects func(*testing.T)
}

func wrongShardAuditCases(t *testing.T) []wrongShardAuditCase {
	t.Helper()
	forwardRouter := &recordingRaftRouter{}
	streamRouter := &recordingRaftRouter{}
	replicationSink := &recordingReplicationSink{
		err: errors.New(wrongShardBackendKeyFixture + ": " + wrongShardDependencyErrorFixture),
	}
	blockResolver := &recordingBlockDirResolver{dir: wrongShardLocalPathFixture, ok: true}
	blockStream := &transferBlockStream{}
	return []wrongShardAuditCase{
		{
			name:      "ForwardRaft",
			operation: audit.OperationForwardRaft,
			target:    audit.TargetPeer,
			configure: func(s *Server) { s.SetRaftRouter(forwardRouter) },
			call: func(s *Server, ctx context.Context) error {
				_, err := s.ForwardRaft(ctx, &scrapv1.ForwardRaftRequest{ShardId: 8, Message: marshalRaftMessage(t)})
				return err
			},
			assertNoSideEffects: func(t *testing.T) {
				t.Helper()
				if forwardRouter.calls != 0 {
					t.Fatalf("ForwardRaft routed wrong Shard %d time(s), want 0", forwardRouter.calls)
				}
			},
		},
		{
			name:      "ForwardRaftStream",
			operation: audit.OperationForwardRaftStream,
			target:    audit.TargetPeer,
			configure: func(s *Server) { s.SetRaftRouter(streamRouter) },
			call: func(s *Server, ctx context.Context) error {
				return s.ForwardRaftStream(&forwardRaftStream{
					ctx: ctx,
					requests: []*scrapv1.ForwardRaftStreamRequest{{
						ShardId: 8,
						Message: marshalRaftMessage(t),
					}},
				})
			},
			assertNoSideEffects: func(t *testing.T) {
				t.Helper()
				if streamRouter.calls != 0 {
					t.Fatalf("ForwardRaftStream routed wrong Shard %d time(s), want 0", streamRouter.calls)
				}
			},
		},
		{
			name:      "ReplicateDocument",
			operation: audit.OperationReplicateDocument,
			target:    audit.TargetDocument,
			configure: func(s *Server) { s.replicationSink = replicationSink },
			call: func(s *Server, ctx context.Context) error {
				return s.ReplicateDocument(&replicateDocumentStream{
					ctx: ctx,
					requests: []*scrapv1.ReplicateDocumentRequest{{
						Part: &scrapv1.ReplicateDocumentRequest_Init{
							Init: validReplicateDocumentInit(func(init *scrapv1.ReplicateDocumentInit) {
								init.ShardId = 8
								init.BlockId = 1
								init.TransactionId = wrongShardTransactionFixture
								init.DocumentName = wrongShardDocumentFixture
							}),
						},
					}},
				})
			},
			assertNoSideEffects: func(t *testing.T) {
				t.Helper()
				if replicationSink.calls != 0 {
					t.Fatalf("ReplicateDocument called sink for wrong Shard %d time(s), want 0", replicationSink.calls)
				}
			},
		},
		{
			name:      "TransferBlock",
			operation: audit.OperationTransferBlock,
			target:    audit.TargetBlock,
			configure: func(s *Server) { s.blockDirResolver = blockResolver },
			call: func(s *Server, ctx context.Context) error {
				blockStream.ctx = ctx
				return s.TransferBlock(&scrapv1.TransferBlockRequest{ShardId: 8, BlockId: 1}, blockStream)
			},
			assertNoSideEffects: func(t *testing.T) {
				t.Helper()
				if blockResolver.calls != 0 {
					t.Fatalf("TransferBlock resolved wrong Shard block dir %d time(s), want 0", blockResolver.calls)
				}
				if blockStream.sends != 0 {
					t.Fatalf("TransferBlock sent wrong Shard response %d time(s), want 0", blockStream.sends)
				}
			},
		},
	}
}

func assertWrongShardAuditDenial(t *testing.T, tt wrongShardAuditCase, events []audit.Event, err error, caller security.PeerIdentityConfig, logEvidence, metricEvidence string) {
	t.Helper()
	if !errors.Is(err, security.ErrPermissionDenied) || status.Code(err) != codes.PermissionDenied {
		t.Fatalf("%s wrong Shard = %v (%s), want permission denied", tt.name, err, status.Code(err))
	}
	assertWrongShardAuditEvent(t, tt, events)
	assertWrongShardEvidenceNoLeaks(t, tt, events[0], err, caller, logEvidence, metricEvidence)
}

func assertWrongShardAuditEvent(t *testing.T, tt wrongShardAuditCase, events []audit.Event) {
	t.Helper()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1: %+v", len(events), events)
	}
	if events[0].Surface != audit.SurfacePeer || events[0].Operation != tt.operation || events[0].Target != tt.target {
		t.Fatalf("unexpected audit classification: %+v", events[0])
	}
	if events[0].Result != audit.ResultDenied || events[0].Reason != audit.ReasonPermissionDenied {
		t.Fatalf("unexpected audit event: %+v", events[0])
	}
}

func assertWrongShardEvidenceNoLeaks(t *testing.T, tt wrongShardAuditCase, event audit.Event, err error, caller security.PeerIdentityConfig, logEvidence, metricEvidence string) {
	t.Helper()
	rendered := fmt.Sprintf("%+v %v %s %s", event, err, logEvidence, metricEvidence)
	for _, forbidden := range wrongShardAuditForbiddenValues(caller) {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("%s denial evidence leaked %q in %q", tt.name, forbidden, rendered)
		}
	}
}

func wrongShardAuditForbiddenValues(caller security.PeerIdentityConfig) []string {
	return []string{
		caller.MemberHostname,
		caller.MemberID,
		security.PeerIdentityPrincipalID(caller),
		wrongShardPeerAddressFixture,
		wrongShardCertMaterialFixture,
		wrongShardTransactionFixture,
		wrongShardDocumentFixture,
		wrongShardLocalPathFixture,
		wrongShardBackendKeyFixture,
		wrongShardDependencyErrorFixture,
	}
}

func wrongShardEvidenceContext(caller security.PeerIdentityConfig) context.Context {
	ctx := peerAuthContext(security.NewRoleSet(security.RolePeerMember), caller)
	ctx = metadata.NewIncomingContext(ctx, metadata.Pairs("x-peer-certificate", wrongShardCertMaterialFixture))
	return grpcpeer.NewContext(ctx, &grpcpeer.Peer{Addr: staticAddr(wrongShardPeerAddressFixture)})
}

type staticAddr string

func (a staticAddr) Network() string {
	return "tcp"
}

func (a staticAddr) String() string {
	return string(a)
}

var _ net.Addr = staticAddr("")

type recordingAuditLogSink struct {
	memory *audit.MemorySink
	logger audit.Sink
}

func newRecordingAuditLogSink(log *bytes.Buffer) *recordingAuditLogSink {
	return &recordingAuditLogSink{
		memory: audit.NewMemorySink(),
		logger: audit.NewLoggerSink(slog.New(slog.NewJSONHandler(log, nil))),
	}
}

func (s *recordingAuditLogSink) Record(ctx context.Context, event audit.Event) error {
	if err := s.memory.Record(ctx, event); err != nil {
		return err
	}
	return s.logger.Record(ctx, event)
}

func (s *recordingAuditLogSink) Events() []audit.Event {
	return s.memory.Events()
}

func authorizationDeniedMetricEvidence(t *testing.T, reader *metric.ManualReader, operation string) string {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect authorization metrics: %v", err)
	}
	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != "scrap.security.authorization.denials" {
				continue
			}
			return authorizationDeniedMetricDataPoint(t, m, operation)
		}
	}
	t.Fatal("scrap.security.authorization.denials not found")
	return ""
}

func authorizationDeniedMetricDataPoint(t *testing.T, m metricdata.Metrics, operation string) string {
	t.Helper()
	sum, ok := m.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("authorization denials metric data = %T, want Sum[int64]", m.Data)
	}
	for _, dp := range sum.DataPoints {
		if !metricDataPointHasAttribute(dp, "scrap.operation", operation) {
			continue
		}
		assertMetricDataPointAttribute(t, dp, "scrap.surface", string(security.RateLimitSurfacePeer))
		assertMetricDataPointAttribute(t, dp, "scrap.reason", audit.ReasonPermissionDenied)
		assertMetricDataPointAttribute(t, dp, "scrap.authorization_status", security.AuthorizationStatusDenied)
		if dp.Value != 1 {
			t.Fatalf("authorization denial metric value = %d, want 1", dp.Value)
		}
		return renderMetricDataPoint(dp)
	}
	t.Fatalf("authorization denial metric missing operation %q", operation)
	return ""
}

func assertMetricDataPointAttribute(t *testing.T, dp metricdata.DataPoint[int64], key, want string) {
	t.Helper()
	if !metricDataPointHasAttribute(dp, key, want) {
		t.Fatalf("authorization metric attrs = %s, want %s=%s", renderMetricDataPoint(dp), key, want)
	}
}

func metricDataPointHasAttribute(dp metricdata.DataPoint[int64], key, want string) bool {
	for _, attr := range dp.Attributes.ToSlice() {
		if string(attr.Key) == key && attr.Value.AsString() == want {
			return true
		}
	}
	return false
}

func renderMetricDataPoint(dp metricdata.DataPoint[int64]) string {
	var b strings.Builder
	for _, attr := range dp.Attributes.ToSlice() {
		if b.Len() > 0 {
			b.WriteString(" ")
		}
		fmt.Fprintf(&b, "%s=%s", attr.Key, attr.Value.AsString())
	}
	return b.String()
}

func mustMarshalRaftForRateLimit(t *testing.T) []byte {
	t.Helper()
	data, err := (&raftpb.Message{}).Marshal()
	if err != nil {
		t.Fatalf("marshal raft message: %v", err)
	}
	return data
}
