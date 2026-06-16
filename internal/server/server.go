// Package server wires the embedded skills and orchestration tools into an MCP
// server. New() returns a server ready to Run over any transport.
package server

import (
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// New builds the onboard MCP server with all tools, resources, and prompts
// registered. Options configure root policy, logging, and other dependencies.
func New(version string, opts ...Option) *mcp.Server {
	rt := newServerRuntime(opts...)
	s := mcp.NewServer(&mcp.Implementation{
		Name:    "onboard",
		Version: version,
	}, nil)

	registerSkillTools(rt, s)
	registerReconTool(rt, s)
	registerGuideTools(rt, s)
	registerGraphTools(rt, s)
	registerRepoMapTool(rt, s)
	registerHistoryTool(rt, s)
	registerDeadCodeTool(rt, s)
	registerExplainDiffTool(rt, s)
	registerContextPackTool(rt, s)
	registerDepsTool(rt, s)
	registerSchemaTool(rt, s)
	registerRoutesTool(rt, s)
	registerStacksTool(rt, s)
	registerMapTool(rt, s)
	registerSkillResources(s)
	registerPrompt(s)

	return s
}
