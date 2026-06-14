# SCRAP Multi-Shard Routing Health Runbook

## Purpose

Use this runbook when Shard routing, leadership, peer scope, or per-Shard
diagnostics appear inconsistent.

## Owning Feature Epic or Release Gate

Epic 2 owns multi-Shard startup composition, deterministic routing, peer
authorization, admin diagnostics, and multi-Shard closure. FR-5, ADR 0026, and
FR-16 apply.

## Symptoms

- Public requests route to the wrong Shard.
- Peer requests are denied for Shard-scope mismatch.
- Admin status omits a configured Shard or reports unexpected leadership.
- Non-zero Shard evidence is missing for release review.

## Normal Path

```sh
scrapctl status --admin-url <admin-url> --output=json
scrapctl peers --admin-url <admin-url> --output=json
scrapctl leader --admin-url <admin-url> --output=json
scrapctl doctor --context <kube-context> --namespace scrap --output=json
```

For prod-like deployed proof, use the Tier 2 gate:

```sh
make tier2-e2e-up
```

## Failure Path

1. Confirm placement/config render and Cell identity.
2. Confirm admin status reports all configured Shards.
3. Confirm leader and peer status per Shard.
4. If wrong-Shard peer denial is absent, escalate as a security/release risk.
5. If routing is inconsistent, stop release closure and collect evidence.

## Rollback or Escalation

Rollback the deployment or placement config to a known-good version. Escalate to
the routing owner if deterministic slot mapping, Shard membership, or peer scope
cannot be proven.

## Expected Outputs

- Status reports configured Shards and per-Shard health.
- Peer and leader diagnostics are scoped to Shard authority.
- Tier 2 evidence covers at least two Shards including non-zero Shard IDs.

## Evidence Collection

Record status, peers, leader, doctor, Tier 2 command or artifact link, commit/ref,
environment, expected and actual outcomes, and redaction proof.

## Redaction Requirements

Do not paste sensitive peer addresses, auth claims, credential values, Document
payloads, Backend object names, unredacted log output, trace IDs, or request IDs.

## Authority Boundary

Do not infer Shard ownership from local files, hostnames, Backend objects,
cached peer addresses, network address, or certificate presence. Use the
authoritative routing and membership path.

## References

- `docs/adr/0026-multi-shard-release-boundary.md`
- `_bmad-output/implementation-artifacts/2-6-multi-shard-evidence-closure.md`
- `internal/scrapctl/run.go`
- `internal/admin/server.go`
