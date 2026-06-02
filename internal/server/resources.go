package server

import (
	"context"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/VerifiedOrganic/onboard/internal/skills"
)

// registerSkillResources exposes each embedded skill as onboard://skills/<name> for
// clients that can read MCP resources (e.g. Claude Code). Clients that can't still
// reach the same content via the get_skill tool.
func registerSkillResources(s *mcp.Server) {
	all, err := skills.List()
	if err != nil {
		return
	}
	for _, sk := range all {
		uri := "onboard://skills/" + sk.Name
		s.AddResource(&mcp.Resource{
			URI:         uri,
			Name:        sk.Name,
			Description: sk.Description,
			MIMEType:    "text/markdown",
		}, func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			content, err := sk.Render()
			if err != nil {
				return nil, err
			}
			return &mcp.ReadResourceResult{
				Contents: []*mcp.ResourceContents{{
					URI:      uri,
					MIMEType: "text/markdown",
					Text:     content,
				}},
			}, nil
		})
	}
}
