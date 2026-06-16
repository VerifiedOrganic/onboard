package server

import (
	"context"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/VerifiedOrganic/onboard/internal/git"
)

// --- guide_read ---

type guideReadInput struct {
	Root string `json:"root,omitempty" jsonschema:"absolute path to the repo root; defaults to the working directory"`
}

type guideReadOutput struct {
	Path      string `json:"path"`
	Exists    bool   `json:"exists"`
	Current   bool   `json:"current"` // cache SHA matches HEAD
	CachedSHA string `json:"cached_sha"`
	HeadSHA   string `json:"head_sha"`
	Branch    string `json:"branch"`
	Mode      string `json:"mode"`
	Generated string `json:"generated"`
	Body      string `json:"body"`
	Note      string `json:"note,omitempty"`
}

// --- guide_write ---

type guideWriteInput struct {
	Root string `json:"root,omitempty" jsonschema:"absolute path to the repo root; defaults to the working directory"`
	Body string `json:"body" jsonschema:"the full markdown guide body; the SHA cache header is prepended automatically"`
	Mode string `json:"mode,omitempty" jsonschema:"full (a fresh scan) or delta (an incremental update)"`
}

type guideWriteOutput struct {
	Path string `json:"path"`
	SHA  string `json:"sha"`
	Note string `json:"note,omitempty"`
}

// --- guide_delta ---

type guideDeltaInput struct {
	Root string `json:"root,omitempty" jsonschema:"absolute path to the repo root; defaults to the working directory"`
}

type guideDeltaOutput struct {
	CachedSHA string       `json:"cached_sha"`
	HeadSHA   string       `json:"head_sha"`
	Current   bool         `json:"current"`
	Changed   []git.Change `json:"changed"`
	Note      string       `json:"note,omitempty"`
}

func registerGuideTools(rt *serverRuntime, s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "guide_read",
		Description: "Read the cached codebase guide and report whether it is current with HEAD. Call before regenerating: if current, reuse the body instead of rescanning.",
	}, withToolLog(rt, "guide_read", func(ctx context.Context, _ *mcp.CallToolRequest, in guideReadInput) (*mcp.CallToolResult, guideReadOutput, error) {
		root, err := resolveRoot(ctx, in.Root)
		if err != nil {
			return nil, guideReadOutput{}, err
		}
		deps := depsForContext(ctx)
		g, err := deps.Guide.Read(ctx, root)
		if err != nil {
			return nil, guideReadOutput{}, err
		}
		out := guideReadOutput{
			Path: g.Path, Exists: g.Exists, CachedSHA: g.Header.SHA,
			Branch: g.Header.Branch, Mode: g.Header.Mode,
			Generated: g.Header.Generated, Body: g.Body,
		}
		if deps.Git.Available(ctx, root) {
			out.HeadSHA, _ = deps.Git.HeadSHA(ctx, root)
			out.Current = g.Exists && out.CachedSHA != "" && out.CachedSHA == out.HeadSHA
		} else {
			out.Note = "Not a git repository — delta updates and currency checks are unavailable."
		}
		return nil, out, nil
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "guide_write",
		Description: "Write (or overwrite) the durable codebase guide. A machine-readable cache header (HEAD sha, branch, timestamp, mode) is prepended automatically. The guide lives inside .git so it is not committed.",
	}, withToolLog(rt, "guide_write", func(ctx context.Context, _ *mcp.CallToolRequest, in guideWriteInput) (*mcp.CallToolResult, guideWriteOutput, error) {
		root, err := resolveRoot(ctx, in.Root)
		if err != nil {
			return nil, guideWriteOutput{}, err
		}
		deps := depsForContext(ctx)
		mode := in.Mode
		if mode == "" {
			mode = "full"
		}
		path, err := deps.Guide.Write(ctx, root, in.Body, mode)
		if err != nil {
			return nil, guideWriteOutput{}, err
		}
		out := guideWriteOutput{Path: path}
		out.SHA, _ = deps.Git.HeadSHA(ctx, root)
		if out.SHA == "" {
			out.Note = "Not a git repository — guide written but not SHA-tagged; delta updates unavailable."
		}
		return nil, out, nil
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "guide_delta",
		Description: "Compute what changed since the cached guide's SHA: returns the cached vs HEAD SHA and the list of added/modified/deleted/renamed files, including old_path for renames, so an update can touch only affected sections.",
	}, withToolLog(rt, "guide_delta", func(ctx context.Context, _ *mcp.CallToolRequest, in guideDeltaInput) (*mcp.CallToolResult, guideDeltaOutput, error) {
		root, err := resolveRoot(ctx, in.Root)
		if err != nil {
			return nil, guideDeltaOutput{}, err
		}
		deps := depsForContext(ctx)
		out := guideDeltaOutput{}
		if !deps.Git.Available(ctx, root) {
			out.Note = "Not a git repository — cannot compute a delta; regenerate the guide in full."
			return nil, out, nil
		}
		out.HeadSHA, _ = deps.Git.HeadSHA(ctx, root)
		g, err := deps.Guide.Read(ctx, root)
		if err != nil {
			return nil, out, err
		}
		if !g.Exists || g.Header.SHA == "" {
			out.Note = "No SHA-tagged guide cached yet — generate a full guide first."
			return nil, out, nil
		}
		out.CachedSHA = g.Header.SHA
		out.Current = out.CachedSHA == out.HeadSHA
		if out.Current {
			out.Note = "Guide is already current with HEAD; no update needed."
			return nil, out, nil
		}
		changed, err := deps.Git.DiffNameStatus(ctx, root, out.CachedSHA)
		if err != nil {
			return nil, out, err
		}
		out.Changed = changed
		return nil, out, nil
	}))
}
