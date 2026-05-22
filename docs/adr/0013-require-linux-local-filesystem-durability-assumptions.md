# Require Linux local filesystem durability assumptions

Status: accepted

## Context

S.C.R.A.P. acknowledges writes before backend upload. The production ACK
contract depends on local block bytes, prepare/openlog records, Raft metadata
storage, snapshots, and local projections surviving crashes according to the
sync boundaries that the code and tests claim.

Those claims are not portable across every storage implementation that can look
like a filesystem. Network filesystems, object-store FUSE layers, mmap-heavy
designs, async IO libraries, and platform-specific storage shortcuts can have
different ordering, flush, rename, link, delete, and directory durability
semantics. If S.C.R.A.P. silently accepts those as equivalent to local Linux
storage, crash-recovery tests can prove the wrong thing.

## Decision

The v1 production storage-member safety claim assumes Linux local persistent
volumes with ext4/XFS-like file and directory durability semantics.

Production local storage code must use explicit Go file IO with reviewable
`fsync`/`fdatasync` and parent-directory sync boundaries. File creation,
rename, link, and delete operations that affect durable recovery state must
document and test the required sync sequence.

Generic network filesystems, object-store mounted filesystems, mmap-heavy
storage designs, async IO libraries, and FFI storage shortcuts are not v1
production defaults. They require a separate ADR, crash/recovery evidence, and
deployment-profile validation before they can be included in the production
durability claim.

Non-production filesystem profiles may exist, but they must be explicitly
named as non-production or risk-accepted profiles and must not imply production
write ACK readiness.

## Consequences

- Production deployments must choose storage classes whose filesystem
  semantics are part of the deployment readiness evidence.
- Crash/recovery tests can target a concrete local durability model instead of
  pretending all filesystems behave the same.
- The code must keep sync boundaries visible even when helper functions are
  introduced.
- Some convenient development or cloud-mounted filesystems remain outside the
  production safety claim until separately proven.
- Future storage substrate changes have a clear ADR and evidence bar.
