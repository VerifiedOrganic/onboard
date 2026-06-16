// Package pathutil provides shared path resolution and root-boundary checks.
package pathutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolveRoot returns the absolute directory to index. An empty root defaults to cwd.
func ResolveRoot(root string) (string, error) {
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve root: %w", err)
		}
		root = wd
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("root %q is not a directory", abs)
	}
	return abs, nil
}

// JoinUnderRoot joins rel to root and verifies the result stays within root.
func JoinUnderRoot(root, rel string) (string, error) {
	joined := filepath.Join(root, rel)
	abs, err := filepath.Abs(joined)
	if err != nil {
		return "", err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	relPath, err := filepath.Rel(rootAbs, abs)
	if err != nil {
		return "", err
	}
	if relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes repo root %q", rel, root)
	}
	return abs, nil
}
