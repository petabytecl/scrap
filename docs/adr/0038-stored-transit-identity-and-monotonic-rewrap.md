# Stored Transit identity routing and monotonic Rewrap

Status: Accepted

Date: 2026-07-09

## Context

Findings `H-18` and `M-06` show reads ignore stored Transit mount/key identity
and Rewrap can downgrade key versions. ADR 0020 already stores mount/key on the
envelope; this ADR makes routing and monotonicity mandatory.

## Decision

1. **Envelope identity routing.** Unwrap and Rewrap use the envelope's stored
   Transit mount and key name through an allow-list. Current process config may
   add routes but must not strand existing Documents when the default mount/key
   changes.

2. **Production Transit readiness.** Transit outage or key health failure
   affects production readiness and write admission for encrypted Cells.

3. **Monotonic Rewrap.** Rewrap rejects targets below the Document's current key
   version at validation and apply. The provider must return the requested
   target version.

4. **TLS trust.** Production images ship a maintained CA trust bundle or require
   an explicit validated trust-bundle mount before HTTPS Backend/OpenBao use
   (`H-17`).
