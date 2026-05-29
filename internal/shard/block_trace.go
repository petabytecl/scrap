package shard

import (
	"crypto/sha256"
	"encoding/binary"

	"go.opentelemetry.io/otel/trace"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
)

// blockTraceContext derives the deterministic SpanContext that roots a Block's
// upload trace (trace 2). It is a pure function of (cell, shard, block), so the
// write path and the upload processor — possibly a different leader after failover —
// compute the same trace_id with no shared state. Block IDs are monotonic and never
// reused within a Cell, so the id is unique within the Cell. The OTel spec says a
// trace_id SHOULD (not MUST) be random; this is a deliberate exception for a
// synthetic correlation trace. See ADR 0013.
func blockTraceContext(cellID string, shardID, blockID uint64) trace.SpanContext {
	var key [16]byte
	binary.BigEndian.PutUint64(key[0:8], shardID)
	binary.BigEndian.PutUint64(key[8:16], blockID)
	sum := sha256.Sum256(append([]byte(cellID+"\x00"), key[:]...))

	var traceID trace.TraceID
	copy(traceID[:], sum[0:16])
	var spanID trace.SpanID
	copy(spanID[:], sum[16:24])

	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
}

// uploadCommandBlockID returns the block ID carried by a seal or confirm command so
// proposeUploadCommand can stamp the matching block.upload trace context onto it.
func uploadCommandBlockID(cmd *scrapv1.RaftCommand) (uint64, bool) {
	switch c := cmd.Command.(type) {
	case *scrapv1.RaftCommand_SealBlock:
		return c.SealBlock.GetBlockId(), true
	case *scrapv1.RaftCommand_ConfirmUpload:
		return c.ConfirmUpload.GetBlockId(), true
	default:
		return 0, false
	}
}
