package shard

import "math"

func uploadGenerationFromApplyIndex(entryIndex uint64, fallback int64) int64 {
	if entryIndex > 0 && entryIndex <= math.MaxInt64 {
		return int64(entryIndex)
	}
	return fallback
}
