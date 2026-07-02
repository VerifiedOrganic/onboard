package server

import (
	"context"
	"time"

	"github.com/VerifiedOrganic/onboard/internal/git"
	"github.com/VerifiedOrganic/onboard/internal/graph"
	"github.com/VerifiedOrganic/onboard/internal/guide"
	"github.com/VerifiedOrganic/onboard/internal/providers"
)

// GraphIndexer is the server's consumer-shaped port onto the indexing service.
type GraphIndexer interface {
	Index(ctx context.Context, root string, refresh, precise bool) (*providers.Graph, error)
}

var _ GraphIndexer = (*graph.Service)(nil)

// GitPort is the git operations MCP tools require.
type GitPort interface {
	Available(ctx context.Context, root string) bool
	HeadSHA(ctx context.Context, root string) (string, error)
	Branch(ctx context.Context, root string) (string, error)
	DiffNameStatus(ctx context.Context, root, fromSHA string) ([]git.Change, error)
	Diff(ctx context.Context, root, base string) ([]git.FileDiff, error)
	History(ctx context.Context, root string, limit int) ([]git.FileStat, error)
	ValidateRef(ctx context.Context, root, ref string) error
	ArchiveTree(ctx context.Context, root, ref, dst string) error
	DefaultBase(ctx context.Context, root string) string
}

type gitPort struct{}

func (gitPort) Available(ctx context.Context, root string) bool { return git.Available(ctx, root) }
func (gitPort) HeadSHA(ctx context.Context, root string) (string, error) {
	return git.HeadSHA(ctx, root)
}
func (gitPort) Branch(ctx context.Context, root string) (string, error) { return git.Branch(ctx, root) }
func (gitPort) DiffNameStatus(ctx context.Context, root, from string) ([]git.Change, error) {
	return git.DiffNameStatus(ctx, root, from)
}
func (gitPort) Diff(ctx context.Context, root, base string) ([]git.FileDiff, error) {
	return git.Diff(ctx, root, base)
}
func (gitPort) History(ctx context.Context, root string, limit int) ([]git.FileStat, error) {
	return git.History(ctx, root, limit)
}
func (gitPort) ValidateRef(ctx context.Context, root, ref string) error {
	return git.ValidateRef(ctx, root, ref)
}
func (gitPort) ArchiveTree(ctx context.Context, root, ref, dst string) error {
	return git.ArchiveTree(ctx, root, ref, dst)
}
func (gitPort) DefaultBase(ctx context.Context, root string) string {
	return git.DefaultBase(ctx, root)
}

// GuidePort reads and writes the SHA-tagged codebase guide cache.
type GuidePort interface {
	Read(ctx context.Context, root string) (guide.Guide, error)
	Write(ctx context.Context, root, body, mode string) (string, error)
}

type guidePort struct{}

func (guidePort) Read(ctx context.Context, root string) (guide.Guide, error) {
	return guide.Read(ctx, root)
}
func (guidePort) Write(ctx context.Context, root, body, mode string) (string, error) {
	return guide.Write(ctx, root, body, mode, time.Now())
}
