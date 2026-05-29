package block

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type Info struct {
	BlockID uint64
	BlkPath string
	IdxPath string
}

func FilePath(dir string, blockID uint64) string {
	return filepath.Join(dir, fmt.Sprintf("%016x.blk", blockID))
}

func IdxFilePath(dir string, blockID uint64) string {
	return filepath.Join(dir, fmt.Sprintf("%016x.idx", blockID))
}

func ListSealedBlocks(dir string, openBlockID uint64) ([]Info, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("block: read dir: %w", err)
	}

	var blocks []Info
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

		blocks = append(blocks, Info{
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
