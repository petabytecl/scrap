package block

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type BlockInfo struct {
	BlockID uint64
	BlkPath string
	IdxPath string
}

func BlockFilePath(dir string, blockID uint64) string {
	return filepath.Join(dir, fmt.Sprintf("%016x.blk", blockID))
}

func IdxFilePath(dir string, blockID uint64) string {
	return filepath.Join(dir, fmt.Sprintf("%016x.idx", blockID))
}

func ListSealedBlocks(dir string, openBlockID uint64) ([]BlockInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("block: read dir: %w", err)
	}

	var blocks []BlockInfo
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".blk") {
			continue
		}
		if strings.HasSuffix(name, ".quarantine") {
			continue
		}

		hexPart := strings.TrimSuffix(name, ".blk")
		id, err := strconv.ParseUint(hexPart, 16, 64)
		if err != nil {
			continue
		}
		if id == openBlockID {
			continue
		}

		blocks = append(blocks, BlockInfo{
			BlockID: id,
			BlkPath: filepath.Join(dir, name),
			IdxPath: IdxFilePath(dir, id),
		})
	}

	sort.Slice(blocks, func(i, j int) bool {
		return blocks[i].BlockID < blocks[j].BlockID
	})

	return blocks, nil
}
