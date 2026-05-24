# Storage Gateway Schema Evolution Example

Status: accepted example

Last updated: 2026-05-22

This example describes how S.C.R.A.P. rolls out an additive metadata field
without breaking old readers, old writers, old stored data, or in-flight
published metadata imports.

## Current Version Registry

The durable version numbers are intentionally small and explicit:

| Surface | Current version | Owner | Changelog |
| --- | --- | --- | --- |
| Authoritative metadata protobuf records | `metastore.CurrentSchemaVersion = 1` | `internal/metastore` | V1 is the initial authoritative document, transaction, upload intent, repair state, command receipt, Raft command, and Raft snapshot schema. |
| Metastore Pebble key schema | `metastore.PebbleKeySchemaV1 = 0x01` | `internal/metastore` | V1 prefixes every logical Pebble key with one schema byte before the existing logical key bytes. |
| Block, index, backend object, and envelope protobuf records | `storageformat.CurrentSchemaVersion = 1` | `internal/storageformat` | V1 is the initial durable block metadata and backend object schema. |
| Published metadata protobuf records | `published.CurrentSchemaVersion = 1` | `internal/published` | V1 is the initial current pointer, manifest, snapshot record, and tail record schema. |
| Published document location format | `published.CurrentLocationFormatVersion = 1` | `internal/published` | V1 describes block/index/envelope object references and frame ranges for imported document locations. |

Compatibility fixtures live in:

- `internal/compat/testdata/v1/` for durable protobuf records;
- `internal/metastore/testdata/pebble_key_schema_v1.txt` for Pebble key
  layout bytes.

Version changes must update this registry, the owning constant comment, and
the compatibility fixture set in the same pull request.

## Example Change

Add a future optional `compression_profile_id` to storage block indexes and
published metadata locations so readers can choose the right decompressor after
compression is introduced.

This is an additive optional field. It does not change the meaning of existing
fields and does not require a new top-level required schema version.

## Rollout Contract

### Phase 1: Reader Support

New binaries learn to read the optional field.

Acceptance criteria:

- old block indexes without `compression_profile_id` decode successfully;
- old published metadata without `compression_profile_id` imports successfully;
- missing `compression_profile_id` means the current uncompressed profile;
- unknown forward-compatible fields are preserved by decode/encode round trips;
- writers still omit the new field.

### Phase 2: Mixed Fleet

Deploy the reader-capable binary everywhere while writers still emit the old
shape.

Acceptance criteria:

- all storage members pass generated-code, compatibility, and replay tests;
- read-only import cells accept both old and new artifact shapes;
- rollback to the previous binary remains possible because no writer has
  emitted the new field yet.

### Phase 3: Feature-Gated Writes

Enable a shard-level feature gate that allows writers to set
`compression_profile_id` for newly sealed blocks.

Acceptance criteria:

- the shard records the active committed storage format before writing;
- only leaders with the feature gate enabled emit the new field;
- old binaries refuse leadership or write admission for shards whose committed
  format requires the new field;
- already written uncompressed blocks remain readable.

### Phase 4: Published Metadata

Publishers include `compression_profile_id` only after authoritative metadata
and block indexes contain it.

Acceptance criteria:

- importers without reader support reject manifests that declare a required
  compression feature;
- importers with reader support accept both old and new manifests;
- `current.pointer` is updated only after the new manifest and referenced
  artifacts have been written and checksum-verified.

## Required-Version Escalation

If a future change makes an existing field mean something different, removes a
field that old readers require, or requires readers to fail closed unless they
understand a new invariant, it is not an additive change.

That change requires:

- a new explicit format or schema version;
- tests that old readers reject the new required version;
- a feature gate that prevents old writers from writing into the new format;
- an operator-visible rollout and rollback plan;
- a DR drill proving old artifacts remain recoverable.

## Retention Compatibility

Billing documents have a seven-year retention requirement. S.C.R.A.P. treats
durable metadata compatibility as a retention contract, not only a rolling
upgrade convenience.

Any document written with schema version N must remain readable by software
that supports schema version N+5 or later throughout the retention window. New
readers may re-encode accepted old records into the current version during
snapshot restore or DR import, but only after validating the old record and its
referenced objects. If future software cannot carry direct reader support for
an old required version, the release must provide a tested migration/export
path before any writer emits the incompatible format.

During rolling upgrades:

- readers accept every supported version from V1 through current;
- writers emit only the shard's current committed version;
- old binaries must refuse leadership or write admission when the committed
  shard format requires fields they cannot understand;
- local Pebble projections may be rebuilt from authoritative metadata, but
  key-schema migrations still require compatibility fixtures and a rollback
  plan.

## Current Test Coverage

The current pre-production tests enforce the base compatibility contract:

- `make test-compat` reads initial v1 fixtures from
  `internal/compat/testdata/v1` for authoritative metadata, published
  metadata, block index, frame checksum, backend object refs, and envelope
  records, and verifies the V1 metastore Pebble key layout fixture in
  `internal/metastore/testdata/pebble_key_schema_v1.txt`;
- authoritative metadata rejects unsupported required schema versions;
- published metadata rejects unsupported required schema versions;
- storage-format records reject unsupported required schema versions;
- protobuf unknown fields survive decode/encode round trips for forward
  compatible additive fields.

Additive protobuf fields belong in the compatibility harness as unknown-field
round trips first. Required semantic changes need an explicit schema or format
version bump, a fail-closed test for old readers, and a feature gate before any
writer emits the new required shape.
