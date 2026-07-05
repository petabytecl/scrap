package block

import (
	"errors"
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

		id, err := parseCanonicalBlockID(strings.TrimSuffix(name, ".blk"))
		if err != nil {
			return nil, fmt.Errorf("block: malformed block filename %q: %w", name, err)
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

// blockIDHexWidth is the fixed digit count of the %016x Block file names.
const blockIDHexWidth = 16

// parseCanonicalBlockID accepts only the fixed-width lowercase hex form that
// FilePath produces. Anything else in the Blocks directory is evidence of
// tampering or corruption and must fail the listing, not vanish from it.
func parseCanonicalBlockID(hexPart string) (uint64, error) {
	if len(hexPart) != blockIDHexWidth {
		return 0, fmt.Errorf("expected %d hex digits, got %d", blockIDHexWidth, len(hexPart))
	}
	if hexPart != strings.ToLower(hexPart) {
		return 0, errors.New("expected lowercase hex digits")
	}
	return strconv.ParseUint(hexPart, 16, 64)
}
