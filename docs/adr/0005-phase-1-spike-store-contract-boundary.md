# Phase 1 spike-store contract boundary

Status: Accepted

Date: 2026-05-26

## Context

Phase 1 is a single-node spike-store milestone. It exists to prove the local
read/write path before Raft, replication, backend upload, TLS/auth, full idempotent
retry, and repair/quarantine workflow are added.

The risky failure mode is the one SCRAP is explicitly trying to avoid: disposable spike
code silently becoming production contract. Some Phase 1 pieces are intentionally
temporary, while other pieces write data or expose API semantics that future phases
must preserve.

## Decision

The following Phase 1 pieces are contract-grade:

- protobuf API and streaming shape
- Store interface behavior
- typed Store/domain errors and central gRPC mapping
- validation rules and size limits
- Block, Frame, and `.idx` binary formats
- Pebble value encoding
- SHA-256 representation boundary
- durability point for `WriteDocumentResponse`
- all-or-error read behavior

`internal/spike` is not contract-grade. It is replaceable Phase 2 scaffolding.
Phase 2 will replace the storage authority with Raft-backed metadata while preserving
the public API and binary storage contracts established in Phase 1.

`WriteDocumentRequest` uses an explicit `oneof` with `init` and `chunk_data`.
`ReadDocumentResponse` uses an explicit `oneof` with `meta` and `chunk_data`.

The API renders SHA-256 as lowercase 64-character hex. Store, Block, and Index code
store raw 32-byte digests.

Phase 1 visibility authority is Pebble. If Block or `.idx` bytes exist but Pebble did
not commit, the Document is invisible. In Phase 2+, Raft metadata becomes the
visibility authority and Pebble returns to being a rebuildable projection.

`WriteDocumentResponse` returns only after Block bytes, `.idx` entry, and Pebble entry
are locally durable, including directory fsync for newly created Block/Index files.
Infrastructure write, fsync, and Pebble failures fail closed.

`ReadDocument` verifies the whole Document before sending `meta` or `chunk_data`.
Corrupt reads return `DATA_LOSS` and send zero stream messages.

## Validation Contract

The API validates boundary text without trimming, normalization, or case folding.

| Field | Rule |
| --- | --- |
| `transaction_id` | required, max 256 bytes |
| `document_name` | required, max 512 bytes |
| `content_type` | required, max 255 bytes |
| `tenant_id` | optional, max 256 bytes |
| `idempotency_key` | optional, max 256 bytes |

NUL and other control characters are rejected. Zero-byte Documents are invalid.
Max client chunk size is 1 MiB. Max Document size is 128 MiB.

`tenant_id` is accepted and validated, but ignored for storage identity.
`idempotency_key` is accepted and validated, but not authoritative in Phase 1.
Duplicate `(transaction_id, document_name)` always returns `ALREADY_EXISTS`.

## gRPC Error Contract

Typed errors map centrally to:

- `ALREADY_EXISTS`
- `NOT_FOUND`
- `INVALID_ARGUMENT`
- `RESOURCE_EXHAUSTED`
- `DATA_LOSS`
- `UNAVAILABLE`

Substring-based error mapping is forbidden.

## Consequences

Phase 1 issues can be implemented in slices, but they must not weaken these contracts
to simplify spike code.

The binary formats must be tested as compatibility surfaces, not merely as helper
implementation details.

The Phase 2 Raft work can replace authority plumbing without changing client-visible
API behavior or rewriting Block/Frame/Index bytes already produced by Phase 1.
