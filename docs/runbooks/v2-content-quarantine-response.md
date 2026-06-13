# V2 Content Quarantine Response Runbook

## Purpose

Use this runbook when the Content Scanner marks a Document as potentially
malicious and operators need to inspect, confirm, release, or collect evidence.

## Owning Feature Epic or Release Gate

Epic 5 owns Content Scanner, Content Quarantine, admin HTTP operations, and
`scrapctl quarantine` workflows. FR-11, FR-12, ADR 0025, and FR-16 apply.

## Symptoms

- `ReadDocument` fails with a bounded failed-precondition reason.
- `HeadDocument` or `FindDocuments` reports a quarantined scan status.
- `scrapctl quarantine list` reports pending Content Quarantine records.

## Normal Path

```sh
scrapctl quarantine list --admin-url <admin-url> --output=json
scrapctl quarantine inspect --admin-url <admin-url> \
  --transaction-id <redacted-transaction> \
  --document-name <redacted-document> --output=json
scrapctl quarantine evidence --admin-url <admin-url> \
  --evidence-path evidence/runbooks/quarantine-evidence.json \
  --output=json
```

Confirm a true positive:

```sh
scrapctl quarantine confirm --admin-url <admin-url> \
  --transaction-id <redacted-transaction> \
  --document-name <redacted-document> --output=json
```

Release a false positive only with the required break-glass authorization:

1. Record the private break-glass approval reference and authorized admin role.
2. Re-run `inspect` and confirm the redacted Transaction and Document
   placeholders match the approval.
3. Run release only if the approval, admin role, and inspected target match.

```sh
scrapctl quarantine release --admin-url <admin-url> \
  --transaction-id <redacted-transaction> \
  --document-name <redacted-document> --output=json
```

## Failure Path

1. If list or inspect fails, confirm admin auth, mTLS, rate limits, and Shard
   status.
2. If a quarantined read returns bytes, stop and escalate as a release-blocking
   security failure.
3. If confirm/release fails, preserve the evidence report and bounded admin
   response.

## Rollback or Escalation

Confirmed quarantine is permanent operationally unless a later approved process
changes the state. Release requires break-glass authority. Escalate scanner or
admin failures to the content-safety owner.

## Expected Outputs

- List and inspect return bounded quarantine metadata.
- Quarantined reads return no bytes.
- Confirm/release commands return operator-safe decisions.
- Evidence report records redaction checks and route coverage.

## Evidence Collection

Record command lines with placeholders, evidence artifact path, commit/ref,
environment, expected and actual outcomes, admin role used, private approval
reference, and redaction proof. Do not paste authorization claims or approval
contents into public artifacts.

## Redaction Requirements

Do not paste Document payloads, raw Document names, scanner rule payloads,
dependency output, credential values, unredacted log output, trace IDs, request
IDs, or auth claims.

## Authority Boundary

Content Quarantine is metadata-level Document gating through committed
quarantine state. It does not rename Block files and must not be confused with
Block Quarantine repair.

## References

- `CONTEXT.md`
- `docs/adr/0008-async-content-scanning-architecture.md`
- `docs/adr/0025-content-quarantine-admin-surface.md`
- `internal/scrapctl/quarantine.go`
- `_bmad-output/implementation-artifacts/5-6-scrapctl-quarantine-operator-workflow.md`
- `_bmad-output/implementation-artifacts/epic-5-content-safety-closure-evidence.md`
