package shard

import (
	"context"

	"go.opentelemetry.io/otel/propagation"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
)

// raftTraceContext is the propagator used to carry trace context through the Raft
// log. It is intentionally TraceContext-only (no baggage): a committed command
// should carry trace identity, not arbitrary baggage, and the result must not
// depend on global propagator init order. See ADR 0013.
var raftTraceContext = propagation.TraceContext{}

// raftCommandCarrier adapts a RaftCommand's W3C trace fields to a TextMapCarrier
// so the propagator can read and write them without knowing the proto shape.
type raftCommandCarrier struct{ cmd *scrapv1.RaftCommand }

func (c raftCommandCarrier) Get(key string) string {
	switch key {
	case "traceparent":
		return c.cmd.GetTraceparent()
	case "tracestate":
		return c.cmd.GetTracestate()
	default:
		return ""
	}
}

func (c raftCommandCarrier) Set(key, value string) {
	switch key {
	case "traceparent":
		c.cmd.Traceparent = value
	case "tracestate":
		c.cmd.Tracestate = value
	}
}

func (c raftCommandCarrier) Keys() []string {
	return []string{"traceparent", "tracestate"}
}

// injectTraceContext writes the active span context from ctx into cmd, so the
// committed entry carries trace identity to every voter's apply loop.
func injectTraceContext(ctx context.Context, cmd *scrapv1.RaftCommand) {
	if cmd == nil {
		return
	}
	raftTraceContext.Inject(ctx, raftCommandCarrier{cmd: cmd})
}

// extractTraceContext returns ctx carrying the remote span context recorded in
// cmd. When cmd has no trace context — an entry written before ADR 0013, or one
// replayed from an older log — the returned ctx has no remote span and any apply
// span derived from it becomes a root span rather than attaching to a bogus
// parent.
func extractTraceContext(ctx context.Context, cmd *scrapv1.RaftCommand) context.Context {
	if cmd == nil {
		return ctx
	}
	return raftTraceContext.Extract(ctx, raftCommandCarrier{cmd: cmd})
}
