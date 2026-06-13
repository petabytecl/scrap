# PRD closure policy

PRDs that are gated by production-readiness work stay open until their closing
evidence is current, linked, and reviewable.

## V2 major release closure

V2 has no intermediate releases. A closed phase, closed milestone, merged PR, or
closed implementation issue is progress evidence only. V2 release readiness
requires current linked evidence for every required feature, ADR gate, operator
document, and release gate.

Final V2 release `PASS` is allowed only when the release evidence matrix links
reviewable artifacts for:

- required P0 feature evidence across the accepted V2 FR and ADR gates;
- production security evidence, including current green CI and CodeQL for the
  tested release ref;
- Tier 2 prod-like evidence when closure policy requires the Cilium-backed Kind
  Cell gate;
- Tier 3 telemetry/evidence bundle output with privacy proof;
- production security rehearsal evidence;
- real S3/IAM production rehearsal evidence for Backend S3 claims, or an
  explicit accepted waiver that keeps the final release decision below `PASS`;
- redaction proof for public/tracker-safe artifacts.

The following blockers are non-waivable for a final V2 release `PASS`:

- missing required P0 feature evidence;
- missing production security evidence;
- missing Tier 2 or Tier 3 release evidence required by this policy;
- missing real S3/IAM proof for Backend S3 claims while issue `#429` remains
  open;
- missing redaction proof, or evidence that exposes credential values, private
  keys, generated certificate material, Document payloads, raw Backend keys, raw
  logs, trace IDs, request IDs, auth claims, data keys, or wrapped-key
  ciphertext;
- ownerless or mitigation-free release blockers.

Waivers must be explicit, dated, ownered, scoped, and linked from the release
matrix. A waiver can record risk acceptance or explain why a row remains
`CONCERNS` or `FAIL`; it cannot convert a non-waivable blocker into final
release `PASS`.

Local-only output, screenshots, stale artifacts, unlinked terminal snippets,
and copied logs are useful during development, but they are not final V2 release
evidence.

## Cilium-backed Tier 2 guard

PRDs #312 and #337 were gated on a green Tier 2 prod-like E2E run from the
Cilium-backed Kind Cell before closure. Apply the same rule to any future PRD
that explicitly depends on the Cilium-backed Tier 2 guard.

The closure comment for each PRD must include:

- the GitHub Actions run link for `.github/workflows/prodlike-e2e.yml`, or for
  the `ci` workflow when the dedicated workflow file has not reached the default
  branch yet;
- the commit SHA or branch ref tested by that run;
- the run result showing `make tier2-e2e-up` passed;
- the uploaded Tier 2 artifact name that contains the Kind diagnostics and E2E
  log.

Manual local Tier 2 output is useful while developing, but it is not enough to
close #312 or #337. The durable proof is a green GitHub Actions run that executes
the Tier 2 gate. Dedicated workflow files added on `v2` cannot be dispatched from
GitHub Actions until they exist on the default branch, so the `ci` workflow is an
acceptable temporary carrier when it runs the same `make tier2-e2e-up` gate.

## Tier 3 evidence path

Tier 3 evidence can be produced from GitHub Actions:

```sh
gh workflow run evidence-gate.yml --ref v2 -f scenario=throughput
```

The workflow uploads the evidence bundle directory and writes the selected bundle
path to `artifacts/tier3-bundle-path.txt`.

The equivalent local command path is:

```sh
make tier3-evidence-up STRESS_SCENARIO=throughput
```

The local command writes a timestamped bundle under `evidence/`.

## Production rehearsal path

Production security and encryption closure can use the production rehearsal
targets documented in `docs/production-rehearsal.md`.

Use `make production-rehearsal-security` for local proof that production mode
starts with real mTLS, policy files, audit/rate-limit gates, and real OpenBao
Transit. This target uses the filesystem Backend and proves the security and
Transit path only.

Use `make production-rehearsal` when the closure claim includes real S3/IAM
Backend behavior. The issue or pull request must link the generated report and
name the tested commit or branch. Do not paste tokens, private keys, generated
certificate material, Document payloads, raw Backend keys, or raw logs into
public tracker comments.
