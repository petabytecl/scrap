package shard

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
)

func TestInjectExtractTraceContextRoundTrip(t *testing.T) {
	traceID, err := trace.TraceIDFromHex("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("trace id: %v", err)
	}
	spanID, err := trace.SpanIDFromHex("0123456789abcdef")
	if err != nil {
		t.Fatalf("span id: %v", err)
	}
	parent := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), parent)

	cmd := &scrapv1.RaftCommand{
		Command: &scrapv1.RaftCommand_CommitDoc{
			CommitDoc: &scrapv1.CommitDocument{TransactionId: "tx"},
		},
	}
	injectTraceContext(ctx, cmd)
	if cmd.GetTraceparent() == "" {
		t.Fatal("traceparent not injected into command")
	}

	// The command travels through the Raft log: it must survive a marshal/unmarshal
	// cycle AND keep its oneof command intact (additive-compat with ADR 0013).
	data, err := proto.Marshal(cmd)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	decoded := &scrapv1.RaftCommand{}
	if err := proto.Unmarshal(data, decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.GetCommitDoc().GetTransactionId() != "tx" {
		t.Fatal("oneof command lost across marshal")
	}

	got := trace.SpanContextFromContext(extractTraceContext(context.Background(), decoded))
	if got.TraceID() != traceID {
		t.Fatalf("trace id: got %s want %s", got.TraceID(), traceID)
	}
	if got.SpanID() != spanID {
		t.Fatalf("span id: got %s want %s", got.SpanID(), spanID)
	}
	if !got.IsRemote() {
		t.Fatal("extracted span context should be remote")
	}
	if !got.IsSampled() {
		t.Fatal("sampled flag should propagate")
	}
}

func TestExtractTraceContextEmptyIsRootSpan(t *testing.T) {
	// An entry written before ADR 0013 (or replayed from an older log) has no
	// trace context; extraction must yield no valid remote span so the apply span
	// starts as a root rather than attaching to a bogus parent.
	cmd := &scrapv1.RaftCommand{
		Command: &scrapv1.RaftCommand_SealBlock{
			SealBlock: &scrapv1.SealBlock{BlockId: 7},
		},
	}
	got := trace.SpanContextFromContext(extractTraceContext(context.Background(), cmd))
	if got.IsValid() {
		t.Fatalf("expected invalid (root) span context, got valid: %s", got.TraceID())
	}
}
