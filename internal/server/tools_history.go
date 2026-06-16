package server

import (
	"context"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/VerifiedOrganic/onboard/internal/git"
)

// history surfaces git churn/ownership signals: which files change most often, how
// much, when they last changed, and how many authors have touched them. High-churn,
// multi-author files are onboarding hotspots and prime targets for the risk auditor.

type historyInput struct {
	Root       string `json:"root,omitempty" jsonschema:"repo root; defaults to the working directory"`
	Limit      int    `json:"limit,omitempty" jsonschema:"max files to return, ranked by churn (default 20)"`
	MaxCommits int    `json:"max_commits,omitempty" jsonschema:"how many recent commits to scan (default 1000; negative = entire history)"`
}

type historyOutput struct {
	Files      []git.FileStat `json:"files"`
	TotalFiles int            `json:"total_files"`
	Note       string         `json:"note,omitempty"`
}

func history(ctx context.Context, in historyInput) (historyOutput, error) {
	out := historyOutput{}
	root, err := resolveRoot(in.Root)
	if err != nil {
		return out, err
	}
	if !git.Available(root) {
		out.Note = "Not a git repository — no history signals available."
		return out, nil
	}
	// Omitted (0) → scan the last 1000 commits (bounds cost on huge repos); a negative
	// value means scan the entire history.
	maxCommits := in.MaxCommits
	switch {
	case maxCommits == 0:
		maxCommits = 1000
	case maxCommits < 0:
		maxCommits = 0 // git.History treats 0 as unbounded
	}

	files, err := git.History(ctx, root, maxCommits)
	if err != nil {
		return out, err
	}
	out.TotalFiles = len(files)

	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	if len(files) > limit {
		files = files[:limit]
	}
	out.Files = files
	if len(files) == 0 {
		out.Note = "No commit history found (empty repository or no commits yet)."
	}
	return out, nil
}

func registerHistoryTool(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "history",
		Description: "Surface git change-history signals: the files with the most churn (commit count), their additions/deletions, last-changed date, and distinct author count. High-churn, multi-author files are onboarding hotspots and prime risk-audit targets. Requires a git repository.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in historyInput) (*mcp.CallToolResult, historyOutput, error) {
		out, err := history(ctx, in)
		return nil, out, err
	})
}
