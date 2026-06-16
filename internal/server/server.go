// Package server wires the embedded skills and orchestration tools into an MCP
// server. New() returns a server ready to Run over any transport.
package server

import (
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// New builds the onboard MCP server with all tools, resources, and prompts
// registered. Options configure root policy, logging, and other dependencies.
func New(version string, opts ...Option) *mcp.Server {
	Configure(opts...)
	s := mcp.NewServer(&mcp.Implementation{
		Name:    "onboard",
		Version: version,
	}, nil)

	registerSkillTools(s)
	registerReconTool(s)
	registerGuideTools(s)
	registerGraphTools(s)
	registerRepoMapTool(s)
	registerHistoryTool(s)
	registerDeadCodeTool(s)
	registerExplainDiffTool(s)
	registerContextPackTool(s)
	registerDepsTool(s)
	registerSchemaTool(s)
	registerRoutesTool(s)
	registerStacksTool(s)
	registerMapTool(s)
	registerSkillResources(s)
	registerPrompt(s)

	return s
}
