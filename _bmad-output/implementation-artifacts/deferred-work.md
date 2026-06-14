## Deferred from: code review of 1-6-fail-closed-on-missing-document-sha256-verification.md (2026-06-14)

- `VerifyBlock` can report clean when an index entry has `FrameCount == 0` and all-zero SHA-256. This appears pre-existing and outside the immediate Story 1.6 changed hunks, but it is a data-integrity hardening candidate for follow-up.
- Decide whether encrypted zero-SHA256 metadata should make `VerifyBlock` report `doc_sha256` corruption. Reason: separate scope: encrypted Block verification semantics need their own story.

## Deferred from: quick-dev scope split "fix open github issues" (2026-06-13)

- GitHub issue #438 — Validate Tier 3 evidence stack + stress/bundle phases end-to-end on CI. Blocked by #437 (Tier 3 `evidence-gate.yml` runs the full E2E suite before the stress/evidence-bundle phases). Deferred so #437 (flaky multi-member E2E suite) can be fixed first; resume #438 once a green E2E run is achievable. Remaining scope: confirm observability stack (loki/mimir/tempo/pyroscope/alloy/otel-collector/grafana/kube-state-metrics) reaches Ready on a hosted runner, validate the stress run + evidence-bundle generation (`manifest.json`, `gates.json`, `privacy-scan.json`, retention, privacy PASS), and consider readiness probes on monitoring deployments.

## Deferred from: code review of 1-4-transaction-scoped-document-discovery (2026-06-11)

- Rebuilding Store errors still map to `INTERNAL` [internal/server/server.go:570]: `storeapi.ErrRebuilding` from Shard read gates is not handled by central server Store error mapping, so public read/discovery paths can report `INTERNAL` instead of a transient unavailable-style status during projection rebuild. Deferred as pre-existing because Story 1.4 did not introduce `mapStoreError` or the Shard rebuilding sentinel.

## Deferred from: code review of 2-2-multi-shard-cell-startup-composition (2026-06-11)

- Peer replication sink buffers full replicated Documents before dispatch [internal/peer/server.go]: `replicateToSink` existed before Story 2.2 and buffers the full peer replication body before calling the sink. Deferred as pre-existing peer transport hardening outside the Story 2.2 composition boundary.
- Peer replication sink wraps status-bearing sink errors as `INTERNAL` [internal/peer/server.go]: `replicateToSink` existed before Story 2.2 and maps sink errors through a generic internal status. Deferred as pre-existing peer transport error-mapping hardening outside the Story 2.2 composition boundary.

## Deferred from: code review of 2-6-multi-shard-evidence-closure (2026-06-11)

- CI runner migration is outside Story 2.6 scope [.github/workflows/ci.yml:21]: the runner migration was committed before the Story 2.6 evidence implementation and needs separate CI evidence if it is kept. Deferred as pre-existing relative to this story review.
