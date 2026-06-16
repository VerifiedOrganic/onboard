package server

// stacks lists a repository's deployable infrastructure units — the IaC
// analogue of the routes tool. For an application repo the deploy surface is
// its HTTP endpoints; for a Terraform/Terragrunt/OpenTofu repo it is the set of
// Terragrunt units and Terraform root modules: what gets planned and applied,
// from where, against which state.
//
// Like routes and schema, this is a PATTERN reader, not a full HCL evaluator:
// it resolves literal paths, ${get_repo_root()} and find_in_parent_folders(),
// and leaves other interpolations symbolic. Input VALUES are never returned —
// only names — because Terragrunt inputs routinely carry secrets material
// (Vault paths, tokens) that has no business in a tool transcript.

import (
	"context"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/VerifiedOrganic/onboard/internal/scan"
)

type stacksInput struct {
	Root string `json:"root,omitempty" jsonschema:"repo root; defaults to the working directory"`
}

type stacksOutput struct {
	Stacks    []scan.StackUnit `json:"stacks"`
	Total     int              `json:"total"`
	Truncated bool             `json:"truncated,omitempty"`
	Note      string           `json:"note,omitempty"`
}

func registerStacksTool(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "stacks",
		Description: "List a repo's deployable infrastructure units (Terraform/Terragrunt/OpenTofu): each Terragrunt unit and Terraform root module with its module source, include chain, inter-stack dependencies, state backend and key pattern, and input names. The IaC analogue of the routes tool — facts read from HCL, with unresolvable interpolations left symbolic. Input values are never returned (they can carry secrets).",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in stacksInput) (*mcp.CallToolResult, stacksOutput, error) {
		out, err := stacksExtract(in)
		return nil, out, err
	})
}

func stacksExtract(in stacksInput) (stacksOutput, error) {
	out := stacksOutput{}
	root, err := resolveRoot(in.Root)
	if err != nil {
		return out, err
	}
	result := scan.ExtractStacks(root)
	out.Stacks = result.Stacks
	out.Total = result.Total
	out.Truncated = result.Truncated
	out.Note = result.Note
	return out, nil
}