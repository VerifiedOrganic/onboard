package server

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/VerifiedOrganic/onboard/internal/skills"
)

// tourConductor is the stepped-wizard protocol layered over the walkthrough
// skill. It lives beside the server (not under skills/assets) on purpose: it is
// delivery behaviour, not skill content, so it must never surface in list_skills.
//
//go:embed assets/tour.md
var tourConductor string

// registerPrompt exposes /onboard as a slash command for clients that surface MCP
// prompts. It returns a guided *tour*: the conductor protocol (direction fork +
// stepping) followed by the unchanged onboard-codebase-walkthrough skill as the analysis
// engine, so the agent runs the walkthrough phase-by-phase as a wizard rather than
// dumping it all at once.
func registerPrompt(s *mcp.Server) {
	s.AddPrompt(&mcp.Prompt{
		Name:        "onboard",
		Description: "Guided tour of this codebase — a stepped wizard (architecture, data flow, end-to-end traces, the risky negative space). Choose inside-out or outside-in.",
		Arguments: []*mcp.PromptArgument{{
			Name:        "direction",
			Description: "outside-in (entry points → core) or inside-out (load-bearing core → outward). Omit to let the tour ask in Step 0.",
			Required:    false,
		}},
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		sk, err := skills.Get("onboard-codebase-walkthrough")
		if err != nil {
			return nil, err
		}
		engine, err := sk.Render()
		if err != nil {
			return nil, err
		}

		conductor := tourConductor
		if pre := normalizeDirection(req); pre != "" {
			conductor = fmt.Sprintf(
				"> Preselected direction: **%s** — skip the opening direction question, state the direction, and begin the Orient phase.\n\n%s",
				pre, conductor)
		}

		return &mcp.GetPromptResult{
			Description: "Guided codebase tour (stepped walkthrough)",
			Messages: []*mcp.PromptMessage{
				{Role: "user", Content: &mcp.TextContent{Text: conductor}},
				{Role: "user", Content: &mcp.TextContent{Text: engine}},
			},
		}, nil
	})

	s.AddPrompt(&mcp.Prompt{
		Name:        "onboard-skills",
		Description: "Show the onboard skill catalog, including the namespaced onboard-* skill names and example prompts for each workflow.",
	}, func(_ context.Context, _ *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		catalog, err := skills.CatalogMarkdown()
		if err != nil {
			return nil, err
		}
		text := "Show the user this catalog of onboard workflows. Keep it concise, and if they pick one, route to the named skill or call `get_skill` with that exact name.\n\n" + catalog
		return &mcp.GetPromptResult{
			Description: "Onboard skill catalog",
			Messages: []*mcp.PromptMessage{
				{Role: "user", Content: &mcp.TextContent{Text: text}},
			},
		}, nil
	})
}

// normalizeDirection reads the optional `direction` argument and canonicalizes it
// to "outside-in" or "inside-out". Anything unrecognized (or absent) returns "",
// which leaves Step 0 to ask the user.
func normalizeDirection(req *mcp.GetPromptRequest) string {
	if req == nil || req.Params == nil {
		return ""
	}
	v := strings.ToLower(strings.TrimSpace(req.Params.Arguments["direction"]))
	v = strings.NewReplacer("_", "-", " ", "-").Replace(v)
	switch v {
	case "outside-in", "outsidein", "top-down", "topdown", "out-in":
		return "outside-in"
	case "inside-out", "insideout", "bottom-up", "bottomup", "in-out":
		return "inside-out"
	default:
		return ""
	}
}
