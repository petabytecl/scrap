# PRD closure policy

PRDs that are gated by production-readiness work stay open until their closing
evidence is current, linked, and reviewable.

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
