# Phase 4.5 security implementation slices

Status: Published

Source: ADR 0019 and ADR 0020

Parent tracking issue: #398

## Purpose

Bridge Phase 4 partial local eviction and Phase 5 cold-only reads by making the
production security and encryption boundaries explicit, testable, and
operator-visible.

## Published Issues

| Slice | Issue |
| ----- | ----- |
| 1. ADR 0019: production security boundary | #399 |
| 2. ADR 0020: OpenBao envelope encryption contract | #400 |
| 3. Add production security mode and startup gates | #401 |
| 4. Add mTLS credentials for public, peer, admin, and scrapctl paths | #402 |
| 5. Enforce role-based authorization and peer identity checks | #403 |
| 6. Add audit events and rate limits for security-sensitive operations | #404 |
| 7. Add OpenBao Transit client and deterministic fake Transit | #405 |
| 8. Encrypt new Document writes and decrypt reads | #406 |
| 9. Add durable rewrap workflow and evidence | #407 |
| 10. Add prod-like security and encryption evidence gates | #408 |

## Deferred Follow-ups

- Add `scrapctl` OpenBao bootstrap commands for local/prod-like operator
  workflows. The commands should initialize, unseal, mount Transit, and create
  the S.C.R.A.P. Transit key through the official OpenBao Go API client, then
  emit redacted evidence suitable for production rehearsal notes. Keep this
  separate from Testcontainers integration fixtures; it is operator tooling for
  production-mode rehearsal and explicit prod-like environments.

## Proposed Slices

### 1. ADR 0019: production security boundary (#399)

Type: AFK

Blocked by: None

What to build:
Publish the production security boundary decision for public client gRPC, peer
gRPC, and admin HTTP/future admin gRPC. Define mTLS, role mapping, admin audit,
rate limits, and explicit non-production escape hatches.

Acceptance criteria:

- ADR 0019 is accepted and linked from the tracking issue.
- The ADR separates authentication, authorization, NetworkPolicy, and audit
  responsibilities.
- The ADR defines which production gaps block Phase 5.

### 2. ADR 0020: OpenBao envelope encryption contract (#400)

Type: AFK

Blocked by: None

What to build:
Publish the OpenBao Transit envelope encryption decision for new Document
payloads. Define data-key granularity, envelope metadata, write/read outage
behavior, rewrap semantics, and fake Transit test behavior.

Acceptance criteria:

- ADR 0020 is accepted and linked from the tracking issue.
- The ADR states what is encrypted and what metadata remains plaintext.
- The ADR defines fail-closed write, read, and rewrap behavior.

### 3. Add production security mode and startup gates (#401)

Type: AFK

Blocked by: #399

What to build:
Add explicit security modes and production startup validation so `scrapd` cannot
accidentally run production workloads with development security settings.

Acceptance criteria:

- Production mode fails startup when required TLS, role policy, or peer identity
  config is missing.
- Development/test mode is explicit, visible in admin health and `scrapctl
  status`, and does not satisfy production readiness.
- Tests cover invalid, missing, and contradictory security configuration.

### 4. Add mTLS credentials for public, peer, admin, and scrapctl paths (#402)

Type: AFK

Blocked by: #399 and #401

What to build:
Wire mTLS server and client credentials for public gRPC, peer gRPC, admin HTTP
or future admin gRPC, and `scrapctl` operator calls.

Acceptance criteria:

- Each listener can load server cert, key, and client CA configuration.
- Clients validate server certificates and present client certificates in
  production mode.
- Production mode refuses insecure client or server credentials.
- Local development tests can still run with explicit development mode.

### 5. Enforce role-based authorization and peer identity checks (#403)

Type: AFK

Blocked by: #399 and #402

What to build:
Authorize public, peer, and admin operations from authenticated principals and
verify peer Cell/Member identity before peer RPCs can affect storage state.

Acceptance criteria:

- Public Document operations require document reader or writer roles.
- Peer RPCs require peer role plus matching Cell and Member identity.
- Admin read operations and dangerous admin operations require distinct roles.
- Unauthorized requests fail closed and do not perform side effects.

### 6. Add audit events and rate limits for security-sensitive operations (#404)

Type: AFK

Blocked by: #399 and #403

What to build:
Emit bounded audit events and apply independent request budgets for public,
peer, and admin surfaces, with special coverage for repair, restore, eviction,
quarantine, pprof, and fault operations.

Acceptance criteria:

- Audit events include principal, role, operation, target, result, and reason
  without logging secrets or Document bytes.
- Rate-limit failures are observable through metrics and audit events.
- Dangerous admin operations are denied or audited according to role.

### 7. Add OpenBao Transit client and deterministic fake Transit (#405)

Type: AFK

Blocked by: #400

What to build:
Introduce the Transit boundary used by the storage path, plus a deterministic
fake for unit and integration tests.

Acceptance criteria:

- The interface supports data-key generation, unwrap, rewrap, readiness, outage,
  auth-denied, missing-key, and minimum-version failure behavior.
- Production config validates Transit mount/key and credentials without logging
  secrets.
- Fake Transit tests prove fail-closed behavior without live OpenBao.

### 8. Encrypt new Document writes and decrypt reads (#406)

Type: AFK

Blocked by: #400 and #405

What to build:
Encrypt new Document payload Frames before writing Blocks and decrypt through
the normal read path while preserving CRC, SHA-256, Projection Resolution, and
Raft authority semantics.

Acceptance criteria:

- Production writes are not ACK'd unless payload encryption and envelope
  persistence both succeed.
- Reads fail closed with a typed crypto-unavailable error when key material is
  unavailable.
- CRC verifies ciphertext storage integrity and SHA-256 verifies plaintext
  Document integrity before bytes are returned.

### 9. Add durable rewrap workflow and evidence (#407)

Type: AFK

Blocked by: #400, #405, and #406

What to build:
Add an operator-triggered rewrap workflow that updates Document envelope
metadata through Raft without rewriting Block payload bytes.

Acceptance criteria:

- Rewrap is idempotent for already-updated envelopes.
- Rewrap records audit evidence without logging plaintext, data keys, or wrapped
  key ciphertext.
- Rewrap failures are visible in admin health/evidence and do not corrupt
  existing readable Documents.

### 10. Add prod-like security and encryption evidence gates (#408)

Type: AFK

Blocked by: #401, #402, #403, #404, #405, #406, and #407

What to build:
Extend prod-like and evidence workflows so production readiness can prove mTLS,
authorization, audit, rate-limit, encryption, crypto-outage, and rewrap behavior.

Acceptance criteria:

- Evidence bundles record security mode, TLS/authz gate results, audit samples,
  and encryption/rewrap outcomes without secrets.
- Negative tests prove unauthorized public, peer, and admin requests are denied.
- A fresh encrypted write/read/restore path passes in the prod-like Cell.
