# Production security boundary

Status: Accepted

Date: 2026-06-02

## Context

Phase 4 added operator-gated local eviction, Backend restore, repair fallback,
and richer admin evidence. Those controls can now change read availability for
Documents whose local Block data has been intentionally evicted. Phase 5
cold-only reads would make the Backend and administrative restore path even more
sensitive.

`SECURITY.md` already states the intended production posture: production
`scrapd` startup fails closed unless TLS is enabled, public and admin RPCs
require client certificates when TLS is enabled, and workload identity plus
tenant isolation are enforced by the gRPC authorization layer. The current V2
implementation has NetworkPolicy and prod-like evidence scaffolding, but those
are deployment defenses, not an application security boundary.

External systems research on 2026-05-31 recommended a production security
boundary before the next storage milestone: client, admin, and peer auth must be
defined separately; non-production escape hatches must be explicit; and admin
actions need audit evidence.

## Tracking

GitHub tracking issue: #399.

Implementation slices are published in
`docs/phase-4.5-security-implementation-slices.md`.

## Decision

Phase 4.5 is the production security bridge between Phase 4 partial local
eviction and Phase 5 cold-only reads. Phase 5 implementation must not begin
until Phase 4.5 has production-mode security gates and evidence.

S.C.R.A.P. has three application security surfaces:

- public client gRPC for `WriteDocument`, `ReadDocument`, `HeadDocument`, and
  `FindDocuments`;
- peer gRPC for Raft messages, byte replication, consistency checks, and
  `TransferBlock`; and
- admin HTTP or future admin gRPC for health, evidence, pprof, repair,
  eviction, restore, and dangerous fault workflows.

Production mode requires mTLS on every surface. Each listener has its own server
certificate and client CA configuration. Server-side TLS uses client certificate
verification equivalent to Go's `tls.RequireAndVerifyClientCert`. Clients
validate the server certificate and do not use testing-only authority overrides
in production.

Authentication is not authorization. After mTLS authenticates a caller,
S.C.R.A.P. maps the caller principal into a small role set:

- `document_writer`: may write Documents;
- `document_reader`: may read and list Document metadata;
- `peer_member`: may use peer replication, Raft, scrub, repair, and transfer
  RPCs for the configured Cell;
- `admin_reader`: may read health, status, and evidence;
- `admin_operator`: may execute repair, restore, and eviction operations; and
- `admin_break_glass`: may execute dangerous fault or diagnostic hooks that are
  disabled in ordinary production operation.

The first implementation may map roles from certificate SAN entries or SPIFFE
IDs. It does not need to solve multi-tenant policy yet. `tenant_id` remains
non-authoritative for storage identity until a later tenant-routing ADR.

Peer authorization also verifies Cell and Member identity. A peer certificate
alone is not enough to join a Shard or serve bytes. The authenticated peer must
match the configured `cell_id`, expected `member_hostname`, and durable
`member_id` relationship already described in `CONTEXT.md`. A mismatch makes the
Member non-serving and visible in admin health.

Admin authorization is separate from public client authorization. Dangerous
admin operations include eviction apply, repair, restore, Block quarantine,
Content Quarantine release, pprof profile capture, and any fault-injection
command. Dangerous operations require `admin_operator` or `admin_break_glass`
according to the operation. They must emit audit events with the authenticated
principal, role, operation, target Shard or Block when applicable, result, and a
low-cardinality reason. Audit events must not include Document bytes, secrets, or
unbounded user notes.

NetworkPolicy, Cilium policy, Kubernetes RBAC, and host access restrictions are
defense-in-depth controls. They do not replace application mTLS, authorization,
or audit checks.

Non-production escape hatches are allowed only behind an explicit security mode
such as `SCRAP_SECURITY_MODE=development` or `test`. Development mode may run
without mTLS for local tests and evidence clusters, but the mode must be visible
in admin health, `scrapctl status`, and evidence bundles. Development mode must
not satisfy production write-ACK readiness or Phase 5 entry checks.

Production startup fails closed when required security configuration is missing
or contradictory. This includes missing cert/key/client-CA files, invalid role
policy, peer identity policy gaps, enabled dangerous hooks without
`admin_break_glass`, and security mode values that do not match the deployment
profile.

Rate limits are part of the security boundary. Public, peer, and admin surfaces
need independent request budgets so a noisy caller or repeated unauthorized
operation cannot starve write, read, repair, or evidence work. Rate-limit
failures must be observable through metrics and audit events without leaking
secrets or certificate material.

Certificate reload is not part of the first Phase 4.5 contract. Restart-based
certificate rotation is acceptable for the initial implementation, provided
startup validation is fail-closed and rotation runbooks are captured before
production release.

## Consequences

Positive:

- Phase 5 starts from explicit auth, authorization, audit, and rate-limit
  contracts instead of treating network isolation as the security boundary.
- Peer byte transfer and repair cannot be authorized by address alone.
- Admin operations that can change read availability become attributable.
- Local and evidence clusters can stay ergonomic while remaining visibly
  non-production.

Negative:

- Every test and prod-like deployment needs an explicit security mode.
- `scrapctl` needs matching client-side TLS and authorization configuration.
- Existing admin HTTP handlers need authorization and audit plumbing before they
  can be considered production-safe.

Rejected alternatives:

- **NetworkPolicy-only security**: rejected because policy can be misapplied and
  cannot attribute application operations.
- **One shared admin/client role**: rejected because health/evidence reads and
  eviction/repair writes have different blast radius.
- **Implicit local-dev fallback in production**: rejected because missing
  certificates would silently weaken the production boundary.
