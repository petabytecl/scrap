package shard

// Raft apply handling: decode committed commands, emit apply telemetry, and
// dispatch state-machine mutations to the owning Shard collaborators.

import (
	"context"
	"errors"
	"fmt"
	"math"

	"go.etcd.io/raft/v3/raftpb"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/telemetry"
)

func (s *Shard) applyEntries(entries []raftpb.Entry, replayUntil uint64) error {
	for _, e := range entries {
		if e.Type != raftpb.EntryNormal || len(e.Data) == 0 {
			continue
		}

		cmd := &scrapv1.RaftCommand{}
		if err := proto.Unmarshal(e.Data, cmd); err != nil {
			return fmt.Errorf("shard: unmarshal raft command: %w", err)
		}

		// Entries at or below the replay watermark (the durably-applied index, passed
		// in by the Raft node) were applied before a restart; their trace context is
		// stale, so apply spans are suppressed. Entries above it — including ones
		// committed but not yet applied before a crash — emit live spans (ADR 0013 §3).
		live := e.Index > replayUntil
		if err := s.applyEntryTraced(cmd, e.Index, live); err != nil {
			return err
		}
	}
	return nil
}

// applyEntryTraced wraps applyEntryCommand in a per-voter apply span when the entry
// is a live commit (not startup replay) and the command is span-worthy. The span's
// parent is recovered from the command's W3C trace context, so it nests under the
// originating write trace on every replica. See ADR 0013.
func (s *Shard) applyEntryTraced(cmd *scrapv1.RaftCommand, entryIndex uint64, live bool) error {
	operation, attrs := applySpanInfo(cmd, s.identifierMode)
	if !live || operation == "" || s.writeTelemetry == nil {
		return s.applyEntryCommand(cmd, entryIndex)
	}
	opts := []trace.SpanStartOption{trace.WithAttributes(attrs...)}
	// A committed Document forward-links to its Block's upload trace, so a write can
	// be navigated to "where did these bytes go" in one click — and the link survives
	// leader failover because the block trace_id is deterministic (ADR 0013).
	if c, ok := cmd.Command.(*scrapv1.RaftCommand_CommitDoc); ok {
		opts = append(opts, trace.WithLinks(trace.Link{
			SpanContext: blockTraceContext(cellIDOrLocal(s.upload.CellID), s.shardID, c.CommitDoc.GetBlockId()),
			Attributes:  []attribute.KeyValue{attribute.String("scrap.link", "block.upload")},
		}))
	}
	ctx := extractTraceContext(context.Background(), cmd)
	ctx, applyEnd := s.writeTelemetry.StartSpan(ctx, "scrap.apply/"+operation, opts...)
	err := s.applyEntryCommand(cmd, entryIndex)
	// Logged on the apply span's context: the log line carries the same
	// trace_id/span_id, so Grafana can jump trace <-> logs for this apply (ADR 0013).
	if err != nil {
		s.logger.ErrorContext(ctx, "raft command apply failed", "op", operation, "index", entryIndex, "err", err)
	} else {
		s.logger.DebugContext(ctx, "applied raft command", "op", operation, "index", entryIndex)
	}
	applyEnd.End(err)
	return err
}

// applySpanInfo maps a RaftCommand to an apply-span operation name and attributes.
// Identity follows mode (hashed by default, ADR 0012); scrap.block_id is a
// low-cardinality attribute used to correlate the write trace with the block.upload
// trace (ADR 0013).
func applySpanInfo(cmd *scrapv1.RaftCommand, mode telemetry.IdentifierMode) (string, []attribute.KeyValue) {
	switch c := cmd.Command.(type) {
	case *scrapv1.RaftCommand_CommitDoc:
		attrs := telemetry.DocumentIdentityAttributes(
			c.CommitDoc.GetTransactionId(),
			c.CommitDoc.GetDocumentName(),
			mode,
		)
		attrs = append(attrs, blockIDAttribute(c.CommitDoc.GetBlockId()))
		return "commit_document", attrs
	case *scrapv1.RaftCommand_RewrapDoc:
		attrs := telemetry.DocumentIdentityAttributes(
			c.RewrapDoc.GetTransactionId(),
			c.RewrapDoc.GetDocumentName(),
			mode,
		)
		attrs = append(attrs, blockIDAttribute(c.RewrapDoc.GetBlockId()))
		return "rewrap_document", attrs
	case *scrapv1.RaftCommand_QuarantineDoc:
		attrs := telemetry.DocumentIdentityAttributes(
			c.QuarantineDoc.GetTransactionId(),
			c.QuarantineDoc.GetDocumentName(),
			mode,
		)
		attrs = append(attrs, blockIDAttribute(c.QuarantineDoc.GetBlockId()))
		return "quarantine_document", attrs
	case *scrapv1.RaftCommand_ConfirmQuarantine:
		attrs := telemetry.DocumentIdentityAttributes(
			c.ConfirmQuarantine.GetTransactionId(),
			c.ConfirmQuarantine.GetDocumentName(),
			mode,
		)
		return "confirm_quarantine", attrs
	case *scrapv1.RaftCommand_ReleaseQuarantine:
		attrs := telemetry.DocumentIdentityAttributes(
			c.ReleaseQuarantine.GetTransactionId(),
			c.ReleaseQuarantine.GetDocumentName(),
			mode,
		)
		return "release_quarantine", attrs
	case *scrapv1.RaftCommand_SealBlock:
		return "seal_block", []attribute.KeyValue{blockIDAttribute(c.SealBlock.GetBlockId())}
	case *scrapv1.RaftCommand_ConfirmUpload:
		return "confirm_upload", []attribute.KeyValue{blockIDAttribute(c.ConfirmUpload.GetBlockId())}
	case *scrapv1.RaftCommand_ConsistencyCheck:
		return "consistency_check", nil
	}
	return "", nil
}

// blockIDAttribute renders a block ID as a clamped int64 span attribute, matching
// the uint64->int64 guard used for shard_id (avoids overflow on absurd inputs).
func blockIDAttribute(blockID uint64) attribute.KeyValue {
	v := int64(math.MaxInt64)
	if blockID <= math.MaxInt64 {
		v = int64(blockID)
	}
	return attribute.Int64("scrap.block_id", v)
}

func (s *Shard) applyEntryCommand(cmd *scrapv1.RaftCommand, entryIndex uint64) error { //nolint:cyclop // exhaustive Raft command dispatch must stay fail-closed per ADR 0034
	switch c := cmd.Command.(type) {
	case *scrapv1.RaftCommand_CommitDoc:
		return s.applyCommitDocumentCommand(c.CommitDoc, entryIndex)
	case *scrapv1.RaftCommand_RewrapDoc:
		return s.applyRewrapDocumentEnvelopeCommand(c.RewrapDoc, entryIndex)
	case *scrapv1.RaftCommand_QuarantineDoc:
		return s.applyQuarantineDocumentCommand(c.QuarantineDoc)
	case *scrapv1.RaftCommand_ConfirmQuarantine:
		return s.applyConfirmQuarantineCommand(c.ConfirmQuarantine)
	case *scrapv1.RaftCommand_ReleaseQuarantine:
		return s.applyReleaseQuarantineCommand(c.ReleaseQuarantine)
	case *scrapv1.RaftCommand_ConsistencyCheck:
		s.scrubs.applyConsistencyCheck(c.ConsistencyCheck, entryIndex)
		return nil
	case *scrapv1.RaftCommand_SealBlock:
		return s.applySealBlock(c.SealBlock)
	case *scrapv1.RaftCommand_ConfirmUpload:
		return s.applyConfirmUpload(c.ConfirmUpload)
	case nil:
		return errors.New("shard: raft command is nil")
	default:
		// ADR 0034 / H-03: unknown oneof variants must fail closed, not advance
		// the applied watermark as silent success.
		return fmt.Errorf("shard: unsupported raft command type %T", c)
	}
}

func (s *Shard) applyCommitDocumentCommand(doc *scrapv1.CommitDocument, entryIndex uint64) error {
	applyErr := s.applyCommitDocument(doc, entryIndex)

	// ADR 0033 / H-02: wake only the waiter for this proposal_id. Historical
	// entries without proposal_id fall back to Document identity.
	key := commitProposalWaiterKey(doc)
	s.proposalMu.Lock()
	waiter, hasWaiter := s.proposals[key]
	if hasWaiter {
		waiter <- applyErr
		delete(s.proposals, key)
	}
	s.proposalMu.Unlock()

	if applyErr != nil && !hasWaiter {
		s.logger.Error("shard: commit document apply failed with no local waiter; replica diverged from committed state",
			"index", entryIndex, "block_id", doc.GetBlockId(), "err", applyErr)
	}
	return applyErr
}

func commitProposalWaiterKey(doc *scrapv1.CommitDocument) string {
	if id := doc.GetProposalId(); id != "" {
		return "commit-proposal\x00" + id
	}
	return doc.GetTransactionId() + "\x00" + doc.GetDocumentName()
}
