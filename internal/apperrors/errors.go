// Package apperrors defines sentinel errors shared across onboard packages.
package apperrors

import "errors"

var (
	// ErrNotGitRepository indicates git is unavailable or root is outside a work tree.
	ErrNotGitRepository = errors.New("not a git repository")

	// ErrInvalidGitRef indicates a git ref failed validation or does not resolve.
	ErrInvalidGitRef = errors.New("invalid git ref")

	// ErrPathEscapesRoot indicates a relative path leaves the repository root.
	ErrPathEscapesRoot = errors.New("path escapes repository root")

	// ErrRootNotAllowed indicates the requested root is outside configured allowlist.
	ErrRootNotAllowed = errors.New("root not in allowed set")

	// ErrRootNotDirectory indicates the resolved path exists but is not a directory.
	ErrRootNotDirectory = errors.New("root is not a directory")
)

// Is reports whether err matches target.
func Is(err, target error) bool { return errors.Is(err, target) }
