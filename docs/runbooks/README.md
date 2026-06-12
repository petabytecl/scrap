# V2 Operator Runbooks

These runbooks are the operator entry points for V2 failure-domain response.
They assume the operator has access to the target Cell, `scrapctl`, `kubectl`,
and the release evidence artifacts for the commit being investigated.

During incidents, preserve evidence while restoring service. Do not use Backend
object listings, local member files, audit records, or telemetry as storage
authority. Storage behavior follows committed Shard state and the documented
feature-specific authority path.

Repo-relative artifact paths such as `evidence/runbooks/...` are safe to cite
after redaction review. Do not paste host-absolute paths from local machines,
raw artifact contents, logs, credentials, generated key or certificate material,
or unredacted dependency output.

| Runbook | Owning source | Primary commands |
| --- | --- | --- |
| [Startup/security readiness](v2-startup-security-readiness.md) | Epic 4, FR-9, FR-16 | `scrapctl doctor`, `scrapctl status`, `make production-rehearsal-security` |
| [mTLS certificate rotation](v2-mtls-certificate-rotation.md) | Epic 4, production rehearsal docs | `make production-rehearsal-security`, `kubectl rollout status` |
| [OpenBao Transit dependency](v2-openbao-transit-dependency.md) | Epic 4, FR-10, FR-14 | `scrapctl openbao bootstrap`, `make production-rehearsal-security` |
| [Backend upload pressure](v2-backend-upload-pressure.md) | Epic 3, FR-6 | `scrapctl upload-pressure`, `scrapctl status` |
| [Restore failures](v2-restore-failures.md) | Epic 3, FR-7, FR-8 | `scrapctl status`, `scrapctl evidence bundle`, `make e2e-up` |
| [Eviction campaigns](v2-eviction-campaigns.md) | Epic 3, FR-7 | `scrapctl eviction plan`, `apply`, `status` |
| [Block Quarantine repair](v2-block-quarantine-repair.md) | Epic 1/2, Deep Scrub | `scrapctl status`, `scrapctl fault block corrupt` |
| [Content Quarantine response](v2-content-quarantine-response.md) | Epic 5, FR-11, FR-12 | `scrapctl quarantine list`, `inspect`, `confirm`, `release`, `evidence` |
| [Multi-Shard routing health](v2-multi-shard-routing-health.md) | Epic 2, FR-5 | `scrapctl peers`, `leader`, `status` |
| [Evidence collection](v2-evidence-collection.md) | Epic 6, FR-15, FR-16 | `scrapctl evidence bundle`, `make tier2-e2e-up`, `make tier3-evidence-up` |

Every runbook uses these sections: purpose, owning feature epic or release gate,
symptoms, normal path, failure path, rollback or escalation, expected outputs,
evidence collection, redaction requirements, authority-boundary note, and
references.
