---
stepsCompleted:
  - validate_prerequisites
  - extract_requirements
  - design_epics
  - create_stories
  - final_validation
inputDocuments:
  - _bmad-output/planning-artifacts/prds/prd-scrap-2026-06-07/prd.md
  - docs/adr/0019-production-security-boundary.md
  - docs/adr/0020-openbao-envelope-encryption-contract.md
  - docs/phase-4.5-security-implementation-slices.md
  - _bmad-output/project-context.md
---

# scrap - Epic Breakdown

## Overview

This document provides the complete epic and story breakdown for scrap,
decomposing the requirements from the Phase 4.5 PRD, accepted ADRs, and current
GitHub issue slices into implementable stories. GitHub Issues remain the
published execution tracker; BMAD stories must preserve existing issue numbers
where they map to #401 through #408.

## Requirements Inventory

### Functional Requirements

FR1: `scrapd` can run in explicit production, development, or test security
mode. Production mode fails startup when required cert/key/client-CA files,
role policy, peer identity policy, Transit configuration, or dangerous hook
policy is missing, invalid, or contradictory. Traceability: #401.

FR2: Public, peer, admin, and `scrapctl` paths can load and validate server
certificates, server keys, and client CA configuration. Production clients
validate server certificates and present client certificates. Traceability:
#402.

FR3: Authenticated principals are mapped into role sets for public Document
operations, peer operations, admin reads, and dangerous admin operations. Peer
RPCs verify matching Cell and Member identity before they can affect storage
state. Traceability: #403.

FR4: S.C.R.A.P. emits bounded audit events and rate-limit metrics for public,
peer, and admin security-sensitive operations, including repair, restore,
eviction, quarantine, pprof, and fault operations. Traceability: #404.

FR5: The OpenBao Transit boundary supports data-key generation, unwrap, rewrap,
readiness, outage, auth-denied, missing-key, and minimum-version failure
behavior, with a deterministic fake for unit and integration tests.
Traceability: #405.

FR6: Production writes are ACK'd only when payload encryption and durable
envelope metadata persistence both succeed. Reads decrypt encrypted Document
payloads, verify ciphertext storage integrity and plaintext Document integrity,
and fail closed when key material is unavailable. Traceability: #406.

FR7: Operators can trigger rewrap for encrypted Documents. Successful rewrap
updates envelope metadata through Raft, converges on all Members, remains
idempotent for already-updated envelopes, and emits bounded audit evidence.
Traceability: #407.

FR8: Prod-like and evidence workflows prove mTLS, authorization, audit,
rate-limit, encryption, crypto-outage, encrypted write/read/restore, and rewrap
behavior. Phase 5 entry remains blocked unless this gate is green.
Traceability: #408.

### NonFunctional Requirements

NFR1: Production security paths fail closed. Missing TLS, role policy, peer
identity, Transit configuration, unavailable key material, and unsafe hook
configuration must not degrade into insecure defaults.

NFR2: Public, peer, and admin surfaces require separate authentication,
authorization, rate-limit, audit, and network exposure treatment.

NFR3: Development/test mode must be explicit, visible in admin health,
`scrapctl status`, metrics, diagnostics, and evidence bundles, and must not
satisfy production readiness or Phase 5 entry checks.

NFR4: Audit events, metrics, traces, evidence bundles, screenshots, and CI
artifacts must not leak secrets, Document bytes, plaintext data keys,
wrapped-key ciphertext, raw Document identifiers, or high-cardinality values.

NFR5: Encryption must preserve existing integrity semantics: Frame CRC covers
stored ciphertext bytes, and Document SHA-256 verifies plaintext bytes before
they are returned.

NFR6: Backend upload remains outside the write ACK path; production ACK
readiness depends on local durability and envelope persistence, not Backend
upload or Backend object existence.

NFR7: Raft remains the metadata authority and Pebble Projection remains
rebuildable. Rewrap metadata changes converge through Raft authority.

NFR8: Negative authorization, crypto outage, missing-key, corruption, duplicate
write, partial upload, peer loss, restart, timeout, and cancellation paths need
failure-path evidence before PRD closure.

NFR9: Prod-like closure evidence must be current, attributable, linked, and
reviewable; local output alone is insufficient when GitHub Actions, Tier gates,
CodeQL, or hosted CI proof is required.

### Additional Requirements

- ADR 0019 and ADR 0020 are accepted architecture authority for this phase.
- Phase 5 cold-only reads must not begin until Phase 4.5 production security
  gates and encryption evidence are green.
- Production mode requires mTLS on every public, peer, admin, and `scrapctl`
  surface.
- Authentication is not authorization; role mapping must distinguish
  `document_writer`, `document_reader`, `peer_member`, `admin_reader`,
  `admin_operator`, and `admin_break_glass`.
- Peer authorization verifies `cell_id`, expected `member_hostname`, and durable
  `member_id`; a certificate, hostname, or network address alone is not enough.
- NetworkPolicy, Cilium policy, Kubernetes RBAC, and host restrictions are
  defense-in-depth and do not replace application security checks.
- Dangerous admin operations include eviction apply, repair, restore, Block
  quarantine, Content Quarantine release, pprof profile capture, and
  fault-injection commands.
- Certificate reload is out of scope for the first Phase 4.5 contract;
  restart-based rotation is acceptable if production release runbooks are
  captured.
- Envelope encryption is per Document. Per-Block data keys, Transit convergent
  encryption, Transit encryption per Frame, plaintext fallback, metadata
  encryption, tenant-specific key policy, and direct Backend ciphertext
  streaming are out of scope.
- OpenBao policy should be least-privilege by operation: write path data-key
  generation, read path unwrap/decrypt, rewrap path rewrap, and health checks
  with no key material exposure.
- Phase 4.5 may hard-cut local development data; existing unencrypted Blocks do
  not need transparent compatibility unless a later migration issue requires
  it.

### UX Design Requirements

Not applicable. Phase 4.5 is a backend/security capability. Operator-visible
surfaces are covered as admin health, `scrapctl status`, audit events, metrics,
diagnostics, and evidence bundle requirements.

### FR Coverage Map

FR1: Epic 1 - explicit production/development/test security mode and startup
gates.

FR2: Epic 1 - mTLS credential loading and validation across public API, peer
API, admin API, and public/admin operations invoked through `scrapctl`.

FR3: Epic 1 - role authorization and authenticated Member identity checks.

FR4: Epic 1 - bounded audit logging and rate-limit behavior for
security-sensitive operations.

FR5: Epic 2 - OpenBao Transit boundary and deterministic fake Transit.

FR6: Epic 2 - encrypted new Document writes and decrypted reads through the
normal API.

FR7: Epic 2 - durable, idempotent rewrap workflow and evidence.

FR8: Epic 3 - prod-like security and encryption evidence gates sufficient to
block or unblock Phase 5.

## Epic List

### Epic 1: Production Security Boundary and Access Control

**Goal:** Operators can run a Cell only in an explicit production security mode,
with production mode refusing unsafe startup and enforcing mTLS, authenticated
Member identity, role authorization, bounded audit logging, and rate limits
across the public API, peer API, admin API, and public/admin operations invoked
through `scrapctl`.

**Scope:** Startup security-mode gates; mTLS credential loading; Member identity
extraction; authorization from Member identity; audit records with authenticated
actor/decision metadata; rate controls with identity-aware keys where
applicable.

**Out of Scope:** Transit, envelope encryption, encrypted Document writes/reads,
and rewrap.

**FRs covered:** FR1, FR2, FR3, FR4.

**GitHub Issues:** #401, #402, #403, #404.

**Acceptance Evidence:** Negative startup tests, mTLS identity proof,
authorization deny/allow matrix, audit emission checks, and rate-limit behavior.

**Defect Routing:** Failed behavior or evidence in this epic creates or reopens
defects against #401 through #404.

### Epic 2: Transit-Encrypted Document Write/Read Lifecycle

**Goal:** Authorized clients, under the production security boundary from Epic 1
or a test-scoped equivalent, can write and read encrypted Documents through the
normal API using the OpenBao Transit envelope contract, while operators can
durably rewrap envelope metadata without rewriting Block payload bytes.

**Scope:** Transit boundary plus fake implementation; vertical encrypted
write/read behavior; durable rewrap behavior.

**Out of Scope:** Production readiness proof from fake Transit, metadata
encryption, tenant-specific key policy, direct Backend ciphertext streaming, and
Block payload rewrites during rewrap.

**FRs covered:** FR5, FR6, FR7.

**GitHub Issues:** #405, #406, #407.

**Acceptance Evidence:** Transit fake contract tests, encrypted write/read
proof, no plaintext/key-material leaks in logs/metrics/audit/evidence, and
rewrap durability/recovery proof. Fake Transit is a test-only contract double
and cannot satisfy production readiness evidence.

**Defect Routing:** Failed behavior or evidence in this epic creates or reopens
defects against #405 through #407.

### Epic 3: Production Readiness Evidence and Release Gates

**Goal:** Operators can prove Phase 4.5 behavior through current prod-like
evidence, and Phase 5 remains blocked unless security and encryption evidence is
fresh, linked, repeatable, generated after the implementation commit/merge for
the relevant issue, and traceable to commands/configs/logs, ADR 0019, ADR 0020,
and issues #401-#408.

**Scope:** Prod-like config proof; CI artifacts; failure evidence; ADR 0019/0020
coverage; issue-by-issue closure mapping.

**Out of Scope:** New product behavior or inline fixes to Epic 1/Epic 2
behavior.

**FRs covered:** FR8.

**GitHub Issues:** #408.

**Acceptance Evidence:** Repeatable prod-like evidence sufficient to close #408.
This epic does not implement behavior; failed gates create or reopen defects
against Epic 1 or Epic 2 with failing evidence attached.

**Defect Routing:** Failed gates create or reopen defects against the owning
behavior epic and attach the failing evidence. Epic 3 does not absorb the fix.

## Epic 1: Production Security Boundary and Access Control

Operators can run a Cell only in an explicit production security mode, with
production mode refusing unsafe startup and enforcing mTLS, authenticated Member
identity, role authorization, bounded audit logging, and rate limits across the
public API, peer API, admin API, and public/admin operations invoked through
`scrapctl`.

### Story 1.1: Production Security Mode Startup Gates

As a platform operator,
I want `scrapd` to reject unsafe production security configuration at startup,
So that production Cells cannot accidentally run with development security
settings.

**Traceability:** FR1, NFR1, NFR3, #401.

**Acceptance Criteria:**

AC1:
**Given** `scrapd` is configured for production security mode
**When** required TLS, role policy, peer identity, or unsafe admin hook
configuration is missing, invalid, or contradictory
**Then** startup fails before the Cell can serve traffic
**And** the startup error names the missing or invalid configuration class
without logging secrets.

AC2:
**Given** `scrapd` is configured for development or test security mode
**When** the process starts successfully
**Then** admin health, `scrapctl status`, metrics, diagnostics, and evidence
bundles identify the non-production mode
**And** the mode does not satisfy production readiness or Phase 5 entry checks.

AC3:
**Given** a production readiness check runs against a Cell in development or
test security mode
**When** the check evaluates write ACK or Phase 5 readiness
**Then** readiness fails with a non-production-mode reason
**And** no production gate treats the mode as equivalent to production.

### Story 1.2: mTLS Credentials and Member Identity Extraction

As a platform operator,
I want public API, peer API, admin API, and `scrapctl`-invoked operations to use
validated mTLS credentials and authenticated Member identity,
So that transport identity is explicit before authorization decisions run.

**Traceability:** FR2, FR3, NFR2, #402, #403.

**Acceptance Criteria:**

AC1:
**Given** production security mode is enabled
**When** public API, peer API, admin API, or `scrapctl` client/server
credentials are missing or invalid
**Then** the affected listener or client refuses insecure credentials
**And** the failure does not fall back to plaintext or test-only authority.

AC2:
**Given** a peer API request presents authenticated transport credentials
**When** the request is accepted for authorization
**Then** the request context contains the authenticated Cell and Member identity
needed for policy evaluation
**And** identity extraction does not rely on hostname, network address, or
certificate presence alone.

AC3:
**Given** local development tests run with explicit development or test mode
**When** they use non-production credentials or test authority
**Then** the mode remains visible in diagnostics
**And** the same evidence cannot satisfy production readiness.

### Story 1.3: Role Authorization from Authenticated Member Identity

As a storage operator,
I want public, peer, and admin operations authorized from authenticated
principals and Member identity,
So that unauthorized requests fail closed before they can mutate storage state.

**Traceability:** FR3, NFR2, #403, ADR 0019.

**Acceptance Criteria:**

AC1:
**Given** an authenticated public API caller lacks the required Document reader
or writer role
**When** the caller invokes Document read, list, or write behavior
**Then** the request is denied before storage side effects occur
**And** the response does not leak policy internals or sensitive identifiers.

AC2:
**Given** an authenticated peer API caller has incomplete or mismatched
`cell_id`, `member_hostname`, or `member_id` identity
**When** it invokes Raft, replication, scrub, repair, or `TransferBlock`
behavior
**Then** the request is denied before it can affect Shard state or serve bytes
**And** the mismatch is visible in admin health or evidence.

AC3:
**Given** an authenticated admin caller has only read privileges
**When** it invokes repair, restore, eviction, quarantine, pprof, or fault
operations
**Then** the operation is denied before side effects occur
**And** dangerous operations require the appropriate operator or break-glass
role.

### Story 1.4: Bounded Audit Records for Security Decisions

As a platform operator,
I want security-sensitive operations to emit bounded audit records,
So that production security decisions are attributable without exposing secrets
or Document bytes.

**Traceability:** FR4, NFR4, #404, ADR 0019.

**Acceptance Criteria:**

AC1:
**Given** a public, peer, or admin security-sensitive operation is allowed or
denied
**When** audit logging is enabled
**Then** an audit record includes authenticated principal, role, operation,
target class, result, and low-cardinality reason
**And** it excludes secrets, Document bytes, raw Document identifiers, data
keys, wrapped-key ciphertext, and unbounded notes.

AC2:
**Given** dangerous admin operations such as eviction apply, repair, restore,
Block quarantine, Content Quarantine release, pprof profile capture, or
fault-injection commands are invoked
**When** the operation is allowed or denied
**Then** the audit record identifies the operation class and result
**And** it preserves bounded cardinality for metrics and evidence.

AC3:
**Given** evidence bundles or CI artifacts include audit samples
**When** the samples are generated
**Then** they demonstrate actor/decision metadata
**And** they contain no secrets, Document bytes, key material, or high-cardinality
identifiers.

### Story 1.5: Identity-Aware Rate Controls for Security Surfaces

As a platform operator,
I want independent rate controls on public, peer, and admin security surfaces,
So that noisy or unauthorized callers cannot starve write, read, repair, or
evidence work.

**Traceability:** FR4, NFR2, NFR4, #404, ADR 0019.

**Acceptance Criteria:**

AC1:
**Given** public, peer, and admin surfaces each receive requests
**When** configured request budgets are exceeded
**Then** the affected surface returns a rate-limit failure without starving the
other surfaces
**And** rate-limit decisions use bounded identity-aware keys where applicable.

AC2:
**Given** a rate-limit failure occurs
**When** metrics and audit evidence are emitted
**Then** the result, operation class, surface, and low-cardinality reason are
observable
**And** no secrets, certificate material, Document bytes, or raw Document
identifiers are exposed.

AC3:
**Given** production mode starts with missing or contradictory rate-limit
configuration for required security surfaces
**When** startup validation runs
**Then** production startup fails closed
**And** the error names the invalid configuration class.

## Epic 2: Transit-Encrypted Document Write/Read Lifecycle

Authorized clients, under the production security boundary from Epic 1 or a
test-scoped equivalent, can write and read encrypted Documents through the
normal API using the OpenBao Transit envelope contract, while operators can
durably rewrap envelope metadata without rewriting Block payload bytes.

### Story 2.1: OpenBao Transit Boundary and Test-Only Fake

As a storage engineer,
I want a production OpenBao Transit boundary and a deterministic test-only fake,
So that encryption, outage, and rewrap behavior can be implemented and tested
without coupling tests to live OpenBao.

**Traceability:** FR5, NFR1, NFR4, #405, ADR 0020.

**Acceptance Criteria:**

AC1:
**Given** production encryption configuration is loaded
**When** Transit mount, key, credential, readiness, or minimum-version
configuration is missing, invalid, or unauthorized
**Then** production startup or dependency readiness fails closed
**And** no secret or key material is logged.

AC2:
**Given** storage code calls the Transit boundary
**When** data-key generation, unwrap, rewrap, readiness, outage, auth-denied,
missing-key, and minimum-version behavior are exercised
**Then** each result is represented by a typed success or failure path
**And** callers can branch without parsing provider-specific error strings.

AC3:
**Given** tests use Fake Transit
**When** data-key, unwrap, rewrap, outage, auth-denied, missing-key, and
minimum-version cases are exercised
**Then** Fake Transit produces deterministic contract behavior
**And** Fake Transit is marked as test-only and cannot satisfy production
readiness evidence.

### Story 2.2: Encrypted Document Write and Read Path

As an authorized client,
I want new Document writes to be encrypted and readable through the normal API,
So that production storage protects payload bytes while preserving existing
integrity and read semantics.

**Traceability:** FR6, NFR1, NFR4, NFR5, NFR6, #406, ADR 0020.

**Acceptance Criteria:**

AC1:
**Given** production encryption is enabled and Transit can provide a data key
**When** an authorized client writes a new Document
**Then** Block Frame payload bytes are persisted as ciphertext
**And** the write is ACK'd only after encryption and durable envelope metadata
persistence both succeed.

AC2:
**Given** Transit is unavailable, sealed, unauthorized, missing the required
key, or returns incompatible envelope state
**When** an authorized client writes or reads an encrypted Document
**Then** the operation fails closed with a typed crypto-unavailable path
**And** production never falls back to plaintext Block payload bytes.

AC3:
**Given** an encrypted Document is read through the normal API
**When** stored bytes and envelope metadata are valid
**Then** Frame CRC verifies ciphertext storage integrity
**And** Document SHA-256 verifies plaintext bytes before they are returned.

AC4:
**Given** logs, metrics, audit records, traces, evidence bundles, screenshots,
or CI artifacts are produced by encrypted write/read tests
**When** they are inspected
**Then** they contain no plaintext Document bytes, plaintext data keys,
wrapped-key ciphertext, raw Document identifiers, or high-cardinality values.

### Story 2.3: Durable Envelope Rewrap Workflow

As a platform operator,
I want rewrap to update Document envelope metadata durably through Raft,
So that key-version metadata can rotate without rewriting Block payload bytes or
corrupting readable Documents.

**Traceability:** FR7, NFR4, NFR7, NFR8, #407, ADR 0020.

**Acceptance Criteria:**

AC1:
**Given** an encrypted Document has envelope metadata eligible for rewrap
**When** an authorized operator triggers rewrap
**Then** S.C.R.A.P. records the successful envelope metadata update through
Raft authority
**And** all Members converge on the updated envelope metadata.

AC2:
**Given** a Document envelope already targets the requested key version
**When** rewrap runs again
**Then** the workflow completes idempotently
**And** it does not rewrite Block payload bytes.

AC3:
**Given** Transit outage, auth-denied, missing-key, crash, restart, timeout, or
cancellation occurs during rewrap
**When** the system recovers or the operator checks health/evidence
**Then** existing readable Documents remain uncorrupted
**And** the rewrap failure or resumable state is visible without exposing
plaintext, data keys, or wrapped-key ciphertext.

AC4:
**Given** rewrap audit evidence is generated
**When** it is inspected
**Then** it records principal, target key, old and new version markers, result,
and low-cardinality reason
**And** it excludes plaintext, data keys, wrapped-key ciphertext, and unbounded
notes.

## Epic 3: Production Readiness Evidence and Release Gates

Operators can prove Phase 4.5 behavior through current prod-like evidence, and
Phase 5 remains blocked unless security and encryption evidence is fresh,
linked, repeatable, generated after the implementation commit/merge for the
relevant issue, and traceable to commands/configs/logs, ADR 0019, ADR 0020, and
issues #401-#408.

### Story 3.1: Phase 4.5 Evidence Contract and Closure Map

As a platform operator,
I want a repeatable evidence contract for Phase 4.5,
So that every security and encryption requirement maps to current, linked,
reviewable proof before Phase 5 begins.

**Traceability:** FR8, NFR8, NFR9, #408, #398.

**Acceptance Criteria:**

AC1:
**Given** Phase 4.5 evidence is generated for #401 through #408
**When** the evidence bundle is produced
**Then** it records the command, config, commit or merge ref, run location,
artifact path, and result for each covered issue
**And** each entry links back to the relevant FR, ADR 0019 or ADR 0020
decision, and GitHub issue.

AC2:
**Given** evidence is older than the implementation commit/merge for the
relevant issue
**When** PRD or issue closure is evaluated
**Then** the evidence is rejected as stale
**And** closure remains blocked until fresh post-implementation evidence is
attached.

AC3:
**Given** a prod-like evidence run fails
**When** the failure is triaged
**Then** the failed gate creates or reopens a defect against the owning Epic 1
or Epic 2 behavior
**And** the failing evidence is attached to that defect.

### Story 3.2: Prod-Like Security and Encryption Gate Execution

As a platform operator,
I want prod-like workflows to execute the Phase 4.5 security and encryption
gate,
So that Phase 5 can be blocked or unblocked by evidence instead of assertion.

**Traceability:** FR8, NFR8, NFR9, #408, docs/prd-closure-policy.md.

**Acceptance Criteria:**

AC1:
**Given** Epics 1 and 2 behavior has landed
**When** the prod-like evidence workflow runs
**Then** it proves production security mode, mTLS identity, authorization
deny/allow behavior, audit emission, rate-limit behavior, Transit outage,
encrypted write/read/restore, and rewrap behavior
**And** the run uploads reviewable artifacts.

AC2:
**Given** unauthorized public, peer, or admin requests are exercised in the
prod-like Cell
**When** the evidence workflow evaluates negative authorization cases
**Then** each request is denied without side effects
**And** the evidence records the denial without secrets, Document bytes, raw
Document identifiers, or high-cardinality values.

AC3:
**Given** crypto outage, missing-key, auth-denied, corruption, restart, timeout,
or cancellation cases are exercised
**When** the evidence workflow evaluates encrypted write/read/restore and rewrap
behavior
**Then** failure modes fail closed and successful paths preserve CRC, SHA-256,
Raft authority, and Projection Resolution semantics
**And** Phase 5 remains blocked if any required proof is missing.
