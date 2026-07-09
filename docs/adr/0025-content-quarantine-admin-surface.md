# Content quarantine admin surface

Status: Accepted

Date: 2026-06-10

## Context

ADR 0008 accepted asynchronous Content Scanner and Content Quarantine as SCRAP
architecture. It also named a new gRPC AdminService on a separate admin port for
quarantine management operations: list, inspect, confirm, and release.

Since ADR 0008, SCRAP production security work established a concrete operator
surface:

- public client gRPC remains the Document API;
- peer gRPC remains the replication, Raft, scrub, rebuild, and Block transfer
  surface;
- `internal/admin` is the HTTP operator/control-plane surface; and
- `scrapctl` is the operator CLI that calls public/admin APIs and renders
  evidence.

ADR 0019 also made admin operations security-sensitive. Dangerous admin
operations require authentication, authorization, audit, rate limits, and
network exposure controls. It explicitly includes Content Quarantine release in
the dangerous-operation class.

The SCRAP master PRD and architecture reconciliation keep Content Scanner and
Content Quarantine in SCRAP scope unless ADR 0008 is explicitly superseded. The
open question is not whether Content Quarantine exists. The open question is
whether the original gRPC AdminService shape should still be introduced now
that SCRAP already has an HTTP admin surface and `scrapctl` operator path.

## Decision

ADR 0008 remains accepted for the scanner, quarantine, Raft command, Projection
state, scan status, and read behavior. This ADR amends only the admin surface
portion of ADR 0008.

SCRAP Content Quarantine management uses the existing admin HTTP surface in
`internal/admin`, with `scrapctl` commands as the supported operator UX. SCRAP does
not add a new gRPC AdminService for quarantine management.

The admin HTTP surface must support, either directly or through `scrapctl`,
these operations:

- list quarantined Documents by bounded filters;
- inspect one quarantined Document by `(transaction_id, document_name)`;
- confirm quarantine for a true positive;
- release quarantine for a false positive; and
- expose scanner/quarantine health and lag in operator status/evidence output.

Confirm and release remain authoritative lifecycle changes. They must propose
Raft metadata commands and converge through the Shard authority path. Admin
HTTP handlers and `scrapctl` are operator surfaces, not metadata authority.

Committed confirm/release apply handlers must be deterministic and replay-safe
(finding `H-06`): missing records after a prior release are idempotent no-ops
or tombstone hits during Raft replay; not-found rejection is allowed only in
pre-proposal validation, never as a panic-inducing apply failure.

The implementation still needs the ADR 0008 wire/storage work:

- `QuarantineDocument` Raft command;
- `scan_status` fields on metadata responses;
- a Pebble Projection prefix for Content Quarantine state;
- persisted scanner watermarks; and
- leader-owned scan scheduling.

Admin handlers must use the Phase 4.5 production security boundary:

- production mTLS and role authorization;
- `admin_operator` or `admin_break_glass` for confirm/release;
- bounded audit events for list, inspect, confirm, and release;
- per-surface rate limits;
- redacted logs, metrics, traces, and evidence; and
- no Document bytes in admin responses.

If a future release needs a gRPC AdminService, that will be a new wire-contract
decision with its own ADR, proto changes, security model, and evidence gates.

## Consequences

Positive:

- Content Scanner and Content Quarantine stay in SCRAP scope.
- SCRAP avoids adding a second admin protocol surface only for quarantine.
- Operator workflows stay consistent with existing `internal/admin` and
  `scrapctl` patterns.
- The dangerous-operation security and audit model from ADR 0019 applies
  directly to quarantine release.

Negative:

- ADR 0008 readers must know that the gRPC AdminService paragraph is amended by
  this later ADR.
- If a future admin gRPC surface is needed, SCRAP will need an additional design
  and migration story.

Implementation guidance:

- Add `internal/avscan` for scanner orchestration and engine boundaries.
- Keep Content Scanner separate from Deep Scrub.
- Keep Content Quarantine separate from Block Quarantine.
- Keep Raft as the authority for quarantine state.
- Keep `scrapctl` out of Shard internals; it should call admin/public APIs.
- Do not add Document bytes, scanner match payloads, raw identifiers, or
  signature details to logs, metrics, audit, traces, or public tracker output.
