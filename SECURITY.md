# Security Policy

## Reporting Vulnerabilities

Report suspected vulnerabilities privately through GitHub Private Vulnerability
Reporting:

https://github.com/petabytecl/scrap/security/advisories/new

This repository has private vulnerability reporting enabled. GitHub notifies
repository administrators and security managers for private reports according to
their GitHub security-alert notification settings.

Do not open public issues, pull requests, or discussions for undisclosed
vulnerabilities. If the advisory form is unavailable, open a public issue only
to ask for an alternate private contact path; do not include exploit details,
secrets, private data, crash dumps, or proof-of-concept code in that public
request.

## Supported Versions

S.C.R.A.P. has not reached a production release. Security fixes are accepted on
the default branch until a versioned release line exists.

After the first production release, this policy will list supported release
lines and their security maintenance windows.

## Scope

In scope:

- `scrapd` service binary and startup configuration gates
- public and admin gRPC APIs
- authentication and authorization policy enforcement
- storage formats, block verification, metadata persistence, and Raft metadata
- backend upload, restore, repair, and disaster-recovery workflows
- Kubernetes manifests shipped in this repository
- `scrapctl` operator workflows

Out of scope:

- cloud accounts, clusters, domains, CI runners, and other infrastructure
  operated outside this repository
- third-party services that are not configured or shipped by this repository
- social engineering, physical access, spam, denial-of-service against public
  infrastructure, or vulnerability reports based only on missing security
  headers for non-web endpoints

## Response Targets

Targets are measured from the first private report with enough detail to start
triage.

| Step | Target |
| --- | --- |
| Acknowledge report | 5 business days |
| Initial triage | 10 business days |
| Critical or high severity fix | 30 calendar days |
| Medium severity fix | 90 calendar days |

Low severity findings are scheduled according to maintenance priority and
release risk.

## Report Contents

Include enough information to reproduce and assess the issue:

- affected component, command, API, file format, or manifest
- expected security property and observed failure
- reproduction steps or proof of concept
- impact, required privileges, and whether data confidentiality, integrity, or
  availability is affected
- version, commit, deployment mode, and relevant configuration
- whether the report involves public data, private data, or generated test data

Do not include live secrets, production customer data, or unnecessary personal
data. Use synthetic data whenever possible.

## Current Security Notes

- Production `scrapd` startup fails closed unless gRPC TLS is enabled with
  server certificate, key, and client CA files. Local non-production storage can
  bypass mTLS for development and tests.
- Public and admin gRPC listeners require client certificates when TLS is
  enabled.
- Workload identity and tenant isolation are enforced in the gRPC authorization
  layer.
- Production write-ACK readiness remains gated by release evidence and owner
  signoff. Do not treat local or test-mode configuration as production-safe.
- Backend object stores, certificate issuers, Kubernetes RBAC, network policy
  enforcement, and provider retention settings remain deployment-owned controls
  unless this repository explicitly ships the configuration.

## Disclosure Process

The maintainers coordinate fixes in the private advisory until a patch or
mitigation is available. Public disclosure should wait for maintainer agreement
or for the applicable coordinated-disclosure deadline documented in the
advisory.
