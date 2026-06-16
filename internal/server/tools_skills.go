package server

import (
	"context"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/VerifiedOrganic/onboard/internal/skills"
)

type listSkillsInput struct{}

type skillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type listSkillsOutput struct {
	Skills []skillInfo `json:"skills"`
}

type getSkillInput struct {
	Name string `json:"name" jsonschema:"the skill name to load, e.g. onboard-codebase-walkthrough"`
}

type getSkillOutput struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

func registerSkillTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_skills",
		Description: "List the onboarding skills embedded in this server.",
	}, withToolLog("list_skills", func(_ context.Context, _ *mcp.CallToolRequest, _ listSkillsInput) (*mcp.CallToolResult, listSkillsOutput, error) {
		all, err := skills.List()
		if err != nil {
			return nil, listSkillsOutput{}, err
		}
		out := listSkillsOutput{}
		for _, sk := range all {
			out.Skills = append(out.Skills, skillInfo{Name: sk.Name, Description: sk.Description})
		}
		return nil, out, nil
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_skill",
		Description: "Return the full content of an embedded skill (SKILL.md plus all reference files). Call this to load the onboard-codebase-walkthrough workflow before onboarding a developer.",
	}, withToolLog("get_skill", func(_ context.Context, _ *mcp.CallToolRequest, in getSkillInput) (*mcp.CallToolResult, getSkillOutput, error) {
		sk, err := skills.Get(in.Name)
		if err != nil {
			return nil, getSkillOutput{}, err
		}
		content, err := sk.Render()
		if err != nil {
			return nil, getSkillOutput{}, err
		}
		return nil, getSkillOutput{Name: sk.Name, Content: content}, nil
	}))
}
