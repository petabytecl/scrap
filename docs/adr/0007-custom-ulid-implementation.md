# Custom ULID implementation

Status: Accepted

Date: 2026-05-26

## Context

V2 uses `github.com/oklog/ulid/v2` for a single call site: generating ephemeral
write IDs for prep files in the shard openlog (`internal/shard/shard.go`). The prep
file is created before a document write, deleted on commit, and replayed on crash
recovery. The ID never leaves the local shard directory.

Future phases will introduce more internally-generated IDs — repair job identifiers,
scrub task tokens, node registration records — that need cluster-wide uniqueness
without coordination, time-ordering for operational tooling, and compact
representation for Pebble keys and gRPC messages.

Pulling in a third-party library for one call site (and eventually several more)
adds supply-chain surface for code that is straightforward to implement and unlikely
to change once correct.

## Decision

**Format.** ULID (128-bit): 48-bit Unix millisecond timestamp + 80-bit random
payload, encoded as 26-character Crockford Base32 strings. Same spec as oklog/ulid,
implemented in `internal/ulid/` with zero external dependencies.

Considered UUIDv7 (RFC 9562). Rejected because: longer string representation (36
chars vs 26), fewer entropy bits (62 vs 80 after version/variant overhead), hex
encoding is less human-friendly than Crockford Base32, and the main UUIDv7 advantage
— ecosystem recognition by databases and ORMs — is irrelevant since V2 uses Pebble
with custom binary formats, not SQL.

**Monotonicity.** Within the same millisecond, the random portion increments (+1)
instead of generating fresh random bytes. This guarantees strict total ordering even
for IDs generated in rapid succession, which matters for sorted job queues and log
correlation.

Considered non-monotonic (stateless, fresh random per call). Rejected because future
use cases (repair job ordering, scrub scheduling) benefit from sub-millisecond
ordering without requiring a secondary timestamp field.

**Overflow.** If the 80-bit random portion overflows within a single millisecond
(requires 2^80 ≈ 1.2 × 10^24 IDs per ms — physically impossible), the generator
sleeps until the next millisecond. This makes `New()` infallible (`ULID`, not
`(ULID, error)`), eliminating error-handling noise at every call site.

Considered returning an error (oklog/ulid's approach). Rejected because adding
`if err != nil` at every ID generation site for an impossible scenario is pure
noise in a codebase that already has enough real error paths.

**API.** Package-level `New()` backed by a default monotonic generator with internal
mutex, plus `NewGenerator(opts...)` for tests that need a deterministic clock.
The ULID type is `[16]byte`, implements `fmt.Stringer`, `encoding.TextMarshaler`,
and `encoding.BinaryMarshaler`.

**Wire format.** String (26-char Crockford Base32) in protobuf messages and JSON,
consistent with existing `string`-typed ID fields (`transaction_id`,
`document_name`). Binary (16 bytes) in Pebble keys where compactness matters.

**Entropy source.** `crypto/rand` exclusively. No `math/rand` fallback.
