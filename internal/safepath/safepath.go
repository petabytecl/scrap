package safepath

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func UnderDir(root, path string) (string, error) {
	if root == "" {
		return "", errors.New("storage root is required")
	}
	if path == "" {
		return "", errors.New("storage path is required")
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil {
		return "", err
	}
	if rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("storage path %q escapes root %q", pathAbs, rootAbs)
	}
	return pathAbs, nil
}
