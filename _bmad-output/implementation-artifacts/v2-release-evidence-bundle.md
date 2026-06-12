# V2 Release Evidence Bundle

Story: 6.4 - `scrapctl` Release Evidence Bundle
Status: implemented for story scope; final V2 release remains blocked by Stories 6.5-6.7 and issue `#429`
Updated: 2026-06-12T19:32:03-04:00

## Scope

Story 6.4 extends the existing `scrapctl evidence bundle` path so each generated
bundle is self-auditing. The bundle now writes:

- `manifest.json` with bundle-relative artifact paths, SHA-256 checksums,
  provenance, command descriptors, environment summary, privacy status, and
  required evidence-domain statuses.
- `privacy-scan.json` with machine-readable forbidden-shape scan status,
  pattern descriptions, artifact count, finding count, and findings without
  echoing matched secret text.
- `gates.json` with a `privacy_scan_passed` check that fails the bundle gate
  when forbidden material is detected.
- redacted trace evidence in `traces/scrapd.json` as a count summary rather
  than raw Tempo trace IDs.

This story does not add replacement product behavior for feature-owned
diagnostics. Missing or intentionally non-passive evidence is recorded in the
manifest as `CONCERNS` or `FAIL`.

## Changed Boundary List

| Boundary | Change |
| --- | --- |
| `internal/scrapctl/evidencebundle` | Added manifest generation, bundle-relative checksum inventory, privacy scan generation, privacy gate input, additional scanner/security/scrub metric snapshots, and redacted trace-summary output. |
| `internal/scrapctl` CLI tests | Assert `scrapctl evidence bundle` writes `manifest.json` and `privacy-scan.json` through the CLI path. |
| `docs/runbooks/v2-evidence-collection.md` | Documents the manifest/privacy scan outputs and failure handling. |
| `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md` | Updates Story 6.4, FR-13, FR-14, and FR-16 bundle status without changing final release gate status. |

## Command Evidence

| Command | Environment | Result | Notes |
| --- | --- | --- | --- |
| `go test ./internal/scrapctl ./internal/scrapctl/evidencebundle ./scripts` | Local package tests | PASS | Red tests first failed on missing `manifest.json`, missing `privacy-scan.json`, and sensitive stderr not failing the gate; final focused run passes. |

Final broad verification is recorded in the Story 6.4 Dev Agent Record.

## Bundle Artifact Contract

Runtime bundle path pattern:

```text
evidence/<scenario>-<yyyymmddThhmmssZ>-<short-sha>/
```

Required bundle files added by Story 6.4:

```text
manifest.json
privacy-scan.json
```

Manifest schema example:

```json
{
  "schema_version": "scrap.evidence.bundle/v1",
  "bundle_name": "throughput-20260529T120000Z-abc1234",
  "scenario": "throughput",
  "generated_at": "20260529T120000Z",
  "privacy_status": "PASS",
  "privacy_findings_count": 0,
  "provenance": {
    "git_sha": "abc1234567890",
    "git_sha_short": "abc1234",
    "git_dirty": false,
    "image": "localhost/scrapd:test",
    "replicas": 3
  },
  "environment": {
    "namespace": "scrap",
    "cluster": "kind-scrap-evidence"
  },
  "artifacts": [
    {
      "path": "gates.json",
      "size_bytes": 100,
      "sha256": "<sha256>",
      "content_type": "application/json"
    }
  ],
  "evidence": [
    {
      "area": "redaction_proof",
      "status": "PASS",
      "artifact": "privacy-scan.json",
      "reason": "privacy scan passed",
      "owner": "release owner"
    }
  ]
}
```

The manifest intentionally uses bundle-relative artifact paths. Local operator
stdout may print an absolute bundle path, but public tracker summaries should
link or quote only bundle-relative paths and sanitized artifact names.

## Missing-Evidence Semantics

| Area | Story 6.4 behavior |
| --- | --- |
| Peers | Manifest records `CONCERNS` because passive bundle collection does not yet invoke `scrapctl peers`; Story 6.5/Tier 2 should link deployed peer proof. |
| Fault workflows | Manifest records `CONCERNS`; destructive/non-passive fault drills must remain controlled non-production evidence. |
| Eviction/restore | Manifest records `CONCERNS` when `--eviction-plan-id` is absent and `PASS` when the plan evidence is captured. |
| Security/OpenBao | Existing security report and admin health gates remain authoritative. Missing report still fails the gate. |
| Privacy scan | Forbidden findings fail `privacy_scan_passed` and the overall gate. Findings record artifact path, pattern, line, and reason without echoing matched secret text. |

## Redaction Proof

The privacy scan covers generated bundle artifacts before `gates.json` and
`manifest.json` are finalized. It scans for shaped credentials, private-key
markers, raw Document/Transaction/trace/request identifier fields, Backend key
markers, auth-claim markers, host-absolute paths intended for public evidence,
Document payload markers, data-key markers, wrapped-key markers, and ciphertext
markers.

Trace evidence is stored as:

```json
{
  "query": "service.name=scrapd",
  "trace_count": 1,
  "redacted": true
}
```

Raw Tempo trace IDs are not written into `traces/scrapd.json`.

## Release Status

Story 6.4 scope is `PASS` when focused and broad gates pass. Final V2 release
status remains `FAIL` until:

- Story 6.5 links current Tier 2 and Tier 3 evidence gates.
- Story 6.6 links real S3/IAM production rehearsal evidence and issue `#429`.
- Story 6.7 applies final closure policy and gate decision.
