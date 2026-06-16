package server

import (
	"context"
	"time"

	"github.com/VerifiedOrganic/onboard/internal/git"
	"github.com/VerifiedOrganic/onboard/internal/guide"
)

// GitPort is the git operations MCP tools require.
type GitPort interface {
	Available(root string) bool
	HeadSHA(root string) (string, error)
	Branch(root string) (string, error)
	DiffNameStatus(ctx context.Context, root, fromSHA string) ([]git.Change, error)
	Diff(ctx context.Context, root, base string) ([]git.FileDiff, error)
	History(ctx context.Context, root string, limit int) ([]git.FileStat, error)
	ValidateRef(root, ref string) error
	ArchiveTree(ctx context.Context, root, ref, dst string) error
	DefaultBase(root string) string
}

type gitPort struct{}

func (gitPort) Available(root string) bool          { return git.Available(root) }
func (gitPort) HeadSHA(root string) (string, error) { return git.HeadSHA(root) }
func (gitPort) Branch(root string) (string, error)  { return git.Branch(root) }
func (gitPort) DiffNameStatus(ctx context.Context, root, from string) ([]git.Change, error) {
	return git.DiffNameStatus(ctx, root, from)
}
func (gitPort) Diff(ctx context.Context, root, base string) ([]git.FileDiff, error) {
	return git.Diff(ctx, root, base)
}
func (gitPort) History(ctx context.Context, root string, limit int) ([]git.FileStat, error) {
	return git.History(ctx, root, limit)
}
func (gitPort) ValidateRef(root, ref string) error { return git.ValidateRef(root, ref) }
func (gitPort) ArchiveTree(ctx context.Context, root, ref, dst string) error {
	return git.ArchiveTree(ctx, root, ref, dst)
}
func (gitPort) DefaultBase(root string) string { return git.DefaultBase(root) }

// GuidePort reads and writes the SHA-tagged codebase guide cache.
type GuidePort interface {
	Read(root string) (guide.Guide, error)
	Write(root, body, mode string) (string, error)
}

type guidePort struct{}

func (guidePort) Read(root string) (guide.Guide, error) { return guide.Read(root) }
func (guidePort) Write(root, body, mode string) (string, error) {
	return guide.Write(root, body, mode, time.Now())
}
