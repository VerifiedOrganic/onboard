// Package pathutil provides shared path resolution and root-boundary checks.
package pathutil

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/VerifiedOrganic/onboard/internal/apperrors"
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
	realPath, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}
	info, err := os.Stat(realPath)
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: %q", apperrors.ErrRootNotDirectory, realPath)
	}
	return realPath, nil
}

// JoinUnderRoot joins rel to root and verifies the result stays within root.
func JoinUnderRoot(root, rel string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}
	target := rel
	if !filepath.IsAbs(target) {
		target = filepath.Join(rootReal, rel)
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	realTarget, err := resolveExistingPrefix(abs)
	if err != nil {
		return "", err
	}
	if !underRoot(rootReal, realTarget) {
		return "", fmt.Errorf("%w: %q escapes repo root %q", apperrors.ErrPathEscapesRoot, rel, root)
	}
	return realTarget, nil
}

func resolveExistingPrefix(abs string) (string, error) {
	existing := abs
	var suffix []string
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", fmt.Errorf("resolve path: %w", os.ErrNotExist)
		}
		suffix = append(suffix, filepath.Base(existing))
		existing = parent
	}
	realPath, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", err
	}
	for i := len(suffix) - 1; i >= 0; i-- {
		realPath = filepath.Join(realPath, suffix[i])
	}
	return realPath, nil
}
