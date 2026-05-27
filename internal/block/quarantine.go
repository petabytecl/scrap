package block

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

const QuarantineSuffix = ".quarantine"

func Quarantine(blkPath string) error {
	idxPath := strings.TrimSuffix(blkPath, ".blk") + ".idx"

	hasIdx := false
	if _, err := os.Stat(idxPath); err == nil {
		hasIdx = true
		if err := os.Rename(idxPath, idxPath+QuarantineSuffix); err != nil {
			return fmt.Errorf("block: quarantine idx: %w", err)
		}
	}

	if err := os.Rename(blkPath, blkPath+QuarantineSuffix); err != nil {
		if hasIdx {
			_ = os.Rename(idxPath+QuarantineSuffix, idxPath)
		}
		return fmt.Errorf("block: quarantine blk: %w", err)
	}

	return nil
}

func Unquarantine(dir string, blockID uint64) error {
	blkQ := BlockFilePath(dir, blockID) + QuarantineSuffix
	if _, err := os.Stat(blkQ); err != nil {
		return fmt.Errorf("block: not quarantined: %w", err)
	}
	idxQ := IdxFilePath(dir, blockID) + QuarantineSuffix

	if err := os.Rename(blkQ, BlockFilePath(dir, blockID)); err != nil {
		return fmt.Errorf("block: unquarantine blk: %w", err)
	}
	if _, err := os.Stat(idxQ); err == nil {
		if err := os.Rename(idxQ, IdxFilePath(dir, blockID)); err != nil {
			return fmt.Errorf("block: unquarantine idx: %w", err)
		}
	}
	return nil
}

func ListQuarantined(dir string) ([]uint64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("block: list quarantined: %w", err)
	}

	var ids []uint64
	seen := make(map[uint64]bool)
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".blk"+QuarantineSuffix) {
			continue
		}
		hexPart := strings.TrimSuffix(name, ".blk"+QuarantineSuffix)
		id, err := strconv.ParseUint(hexPart, 16, 64)
		if err != nil {
			continue
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}

	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}
