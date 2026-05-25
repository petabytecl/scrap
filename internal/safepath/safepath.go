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
	rootResolved, pathResolved, err := resolveUnderDirPaths(root, path)
	if err != nil {
		return "", err
	}
	escapes, err := pathEscapesRoot(rootResolved, pathResolved)
	if err != nil {
		return "", err
	}
	if escapes {
		return "", fmt.Errorf("storage path %q escapes root %q", pathResolved, rootResolved)
	}
	return pathResolved, nil
}

func resolveUnderDirPaths(root, path string) (string, string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", "", fmt.Errorf("resolve storage root: %w", err)
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return "", "", fmt.Errorf("resolve storage path: %w", err)
	}
	rootResolved, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", "", fmt.Errorf("resolve storage root symlinks: %w", err)
	}
	pathResolved, err := resolvePathSymlinks(pathAbs)
	if err != nil {
		return "", "", fmt.Errorf("resolve storage path symlinks: %w", err)
	}
	return rootResolved, pathResolved, nil
}

func pathEscapesRoot(root, path string) (bool, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false, fmt.Errorf("compare storage path to root: %w", err)
	}
	return rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(os.PathSeparator)), nil
}

func resolvePathSymlinks(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("resolve storage path %q symlinks: %w", path, err)
	}

	current := path
	missing := []string{}
	for {
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("resolve storage path ancestor %q symlinks: %w", current, err)
		}
		missing = append([]string{filepath.Base(current)}, missing...)
		resolvedParent, parentErr := filepath.EvalSymlinks(parent)
		if parentErr == nil {
			return filepath.Join(append([]string{resolvedParent}, missing...)...), nil
		}
		if !errors.Is(parentErr, os.ErrNotExist) {
			return "", fmt.Errorf("resolve storage path ancestor %q symlinks: %w", parent, parentErr)
		}
		current = parent
		err = parentErr
	}
}
