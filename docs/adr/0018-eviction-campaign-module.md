# Eviction Campaign module

Status: Accepted
Date: 2026-06-02

## Context

ADR 0016 introduced operator-gated Phase 4 eviction campaigns: dry-run plans,
apply attempts, plan status, sampled validation, and operator evidence. The first
implementation kept the campaign token cache, running-apply tracking, completed
apply cache, plan assembly, and apply result counting inside `internal/shard`.

That made `Shard` responsible for two different concerns. It still must own the
authoritative facts for a Block: committed Confirmed Upload Catalog state,
leadership, Backend restore availability, Local Block Lifecycle transitions,
Pebble Projection reads, and health/metric recording. But the operator-facing
campaign workflow is an in-memory lifecycle with its own invariants: TTL, stale
member checks, idempotent apply, running status, cached completed results, skip
counts, and final result status.

## Decision

`internal/eviction` owns the Eviction Campaign module.

The module owns:

- bounded plan assembly from Shard-supplied candidate facts;
- in-memory plan, running-apply, and completed-result lifecycle;
- plan status and stale/expired/member validation;
- generic apply result assembly, including per-Block counts, skip-count
  aggregation, final status, and cacheability.

`internal/shard` remains the adapter for authority and side effects. It supplies
Confirmed Upload Catalog metadata, Local Block Lifecycle classification,
leadership state, member identity, Backend restore/read behavior, local marker
and `.blk` transitions, health updates, and metrics.

Eviction Campaign state remains non-durable and TTL-bound, as decided by ADR
0016. This does not change Raft commands, the Pebble Projection format, Backend
object metadata, or the operator HTTP/`scrapctl` contract.
