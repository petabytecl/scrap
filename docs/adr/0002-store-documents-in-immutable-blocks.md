# Store documents in immutable blocks

Status: accepted

S.C.R.A.P. stores each immutable document as a byte range inside a sealed immutable block, referenced by `block_id + stored_offset + stored_length + checksums`. This avoids billions of tiny backend objects, gives the system control over backend object size and key fanout, and keeps efficient range reads possible through block indexes.

The tradeoff is that block, index, and envelope formats become long-lived compatibility contracts. New binaries must keep reading old formats, and writers use only the shard's active committed format until an explicit feature gate changes it.
