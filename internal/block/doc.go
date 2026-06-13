// Package block implements the on-disk append-only block file format:
// CRC-protected frames, the .blk writer and reader, sealed-block listing,
// on-disk index files, integrity verification, and quarantine of corrupt blocks.
package block
