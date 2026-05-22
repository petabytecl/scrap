# S.C.R.A.P. Domain Context

S.C.R.A.P. stands for Strategic Cache Relay And Persistence.

The system is being designed as a storage gateway for a billing ETL platform. The platform handles very large numbers of relatively small immutable documents, such as XML and PDF files. Each ETL transaction creates roughly 2 to 7 documents, including ephemeral workflow artifacts and permanent definitive records.

The gateway should give the service fleet a transparent storage interface while hiding whether bytes are served from local hot storage, replicated peer storage, or an external object backend. For the service fleet, the gateway is the storage system. S3, GCS, Azure Blob, or filesystem storage are backend implementations behind the gateway.

Backend selection is deployment-level configuration. `tenant_id` is part of document identity, auditing, quotas, and fingerprints, but it does not route data to different durable backends in the current design.

The central physical abstraction is a block: an immutable byte container with an index mapping each logical document to `block_id + stored_offset + stored_length + checksums`.

The write path is leader-coordinated: bytes are prepared durably first, consensus metadata controls visibility, and backend upload intent is recorded after commit.

Shard consensus metadata is the authority for document visibility, physical byte references, encryption envelopes, restore state, and background work. Local in-memory indexes are read accelerators derived from durable shard metadata.

Backend encryption uses an envelope model: OpenBao Transit provides a deployment-scoped key-encryption key, and S.C.R.A.P. uses per-block data-encryption keys for backend block and index objects. Routine key rotation rewraps small per-block envelopes instead of re-encrypting block data.

Cold backend reads are explicit. `HeadDocument` can confirm existence from metadata, while `ReadDocument` returns a structured restore-pending or crypto-unavailable response when bytes cannot be served immediately.

Backend capacity is governed by deployment profiles. S.C.R.A.P. shapes backend writes and restores around provider-specific rate limits while preserving local replicated durability as the immediate source of truth.

Internal access is authorized by workload identity and service capabilities. Caller-supplied fields such as `tenant_id`, `priority_class`, and `created_by_service` are recorded and validated, but they are not the security principal.

Replica placement targets distinct Kubernetes storage nodes in v1. S.C.R.A.P. treats pod readiness as insufficient for storage safety; replicas must be caught up and byte-verified before they can serve reads or replace old members.

Administrative operations are exposed as typed, audited control-plane actions. Expensive or dangerous operations such as restore, drain, repair, tombstone, and capacity override are planned, idempotent, and tracked as durable jobs.

The core design discussion is captured in [Storage Gateway Design Notes](docs/storage-gateway-design-notes.md).
