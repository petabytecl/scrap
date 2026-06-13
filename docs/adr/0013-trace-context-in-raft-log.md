# Trace context propagation through the Raft log

Status: Accepted

Date: 2026-05-28

## Context

ADR 0012 established the OpenTelemetry evidence plane. Traces currently cover the
client RPC and the **leader's** write-path stages (`scrap.write/*`), but they stop
at the leader. The deterministic state-machine apply runs on **every voter** in the
Raft goroutine — with no inbound request and no span — so "the Document was
committed and applied on all replicas," which is the core durability story, is
invisible to tracing. The asynchronous Backend upload (write-state-machine states
6–7) is likewise untraced.

A write crosses three trace-propagation boundaries, each needing a different
mechanism:

1. **Peer byte replication** (leader → followers): a synchronous gRPC call. Trace
   context rides in request metadata — solved by `otelgrpc` instrumentation, no
   format change.
2. **Raft log apply** (leader → all voters): there is **no inbound request** to
   carry context. The entry is applied later, in a different goroutine, on every
   voter. Context must travel *inside the committed bytes*.
3. **Backend upload** (per Block): **fan-in** (a Block aggregates many Documents,
   per CONTEXT.md) and **time-shifted** (minutes-to-hours after ACK, possibly on a
   new leader after failover, per ADR 0010). It has no single parent write and
   cannot share one trace with them without running for hours.

## Decision

### 1. Carry W3C trace context on the `RaftCommand` envelope

Two optional fields are added to the `RaftCommand` wrapper:

```
message RaftCommand {
  oneof command { /* fields 1-4 unchanged */ }
  string traceparent = 5;  // W3C; injected at Propose
  string tracestate  = 6;  // usually empty
}
```

The proposing leader injects the active span context; every voter extracts it once
in the apply loop and starts a child span with that remote parent. This makes the
state-machine apply observable on all replicas as one span group under the
originating trace (`apply/commit_document`, `apply/confirm_upload`, etc.).

The carrier lives on the **envelope**, not inside each command, so every command
type (`CommitDocument`, `SealBlock`, `ConfirmUpload`, `RequestConsistencyCheck`)
propagates uniformly through a single inject/extract path, and the domain messages
stay free of telemetry concerns.

This is additive and schema-evolution-safe: new optional scalar fields, the
`oneof` (fields 1–4) untouched. Per the Raft-log compatibility rule (CONTEXT.md),
readers accept entries with or without the fields; a log written by an older binary
applies unchanged (empty trace context → a root apply span).

### 2. Two linked traces joined by a deterministic block trace identity

The Document lifecycle is two traces:

- **`document.write`** — states 1–5, the synchronous ACK path (request →
  peer-replicate → raft propose → apply on all voters).
- **`block.upload`** — states 6–7, per Block (seal → backend put `.blk`/`.idx` →
  HEAD verify → `ConfirmUpload` → apply on all voters).

They are joined by a **deterministic** identity: the `block.upload` trace_id is a
stable function of `(cell_id, shard_id, block_id)`. Each document's apply span
emits one forward span link to that block trace, and `scrap.block_id` is set as an
attribute on both traces for reverse search.

Because Block IDs are monotonic and never reused within a Cell (CONTEXT.md), the
derived trace_id is unique within the Cell and is **recomputable by any leader with
no shared state** — so the link survives a leader change during upload (the exact
failover path ADR 0010 describes). The OTel specification states a trace_id SHOULD
(not MUST) be randomly generated; this is a deliberate, documented exception for a
synthetic correlation trace.

### 3. No live spans during replay

Trace context encountered during WAL replay, projection rebuild, or snapshot
restore must **not** produce live apply spans — the historical `traceparent` values
are stale and would pollute Tempo with ancient trace_ids. Apply-span emission is
gated to live committed entries only.

The replay cutoff is the **durably-applied index** (the loaded snapshot's index),
not `hardState.Commit`. The run loop persists the HardState before it calls Apply,
so a crash in that window leaves entries that are committed but not yet applied;
those are applied for the *first* time after restart and their trace context is
genuine, so they must emit live spans. Using the commit index as the cutoff would
suppress them and leave a gap in crash-recovery apply evidence. Only entries at or
below the durably-applied index — the ones known to have applied before the
restart — are suppressed as replay. The watermark is passed into the apply callback
by the Raft node (it is known before the run loop starts), so the apply path is
correct from its first invocation rather than depending on a value the state
machine stores after `Open` returns.

### 4. Identifier privacy is unchanged

Spans (and correlated logs) carry hashed `scrap.transaction.hash` /
`scrap.document.hash` by default (ADR 0012). Raw identifiers are emitted only behind
a fail-closed local-only override (`SCRAP_TELEMETRY_RAW_IDS`, refused when the Cell
is not local).

## Considered Options

- **Per-command trace fields vs. envelope.** Per-command is more explicit but
  duplicates inject/extract per type and risks trace context becoming durable
  per-Document metadata. The envelope is uniform and transient-by-nature. Chosen:
  envelope.
- **One trace (request → backend) vs. two linked traces.** A single trace has no
  valid parent for the fan-in upload (a Block holds many Documents), runs for
  hours, and exceeds the collector's tail-sampling decision window. Chosen: two
  linked traces.
- **In-memory seal→docs links vs. deterministic forward link.** An in-memory list
  of per-Document span contexts is lost on leader failover and must be capped for
  large Blocks. The deterministic forward link is bounded (one per span),
  failover-safe, and needs no durable storage. Chosen: deterministic link.

## Consequences

- The Raft log wire format gains two optional fields; `make proto` regenerates
  `gen/go`. Entries without the fields apply unchanged.
- ~55 bytes added per command (negligible against ~330-byte commands; Raft still
  carries metadata only).
- Each committed `CommitDocument` / `ConfirmUpload` produces an apply span per
  voter, increasing span volume. Head sampling stays AlwaysOn; the gateway
  tail-samples (100% in the evidence overlay, 10% + errors + slow in production).
- A new env knob `SCRAP_TELEMETRY_RAW_IDS` is added (fail-closed outside local
  Cells).
- The deterministic block trace_id is a synthetic-identity convention that querying
  tooling must understand (documented here and in the evidence query pack).
