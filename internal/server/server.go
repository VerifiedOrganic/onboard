// Package server wires the embedded skills and orchestration tools into an MCP
// server. New() returns a server ready to Run over any transport.
package server

import (
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// New builds the onboard MCP server with all tools, resources, and prompts
// registered. The same server is used for stdio (interactive agents) and, later,
// streamable HTTP (hosted / CI).
func New(version string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{
		Name:    "onboard",
		Version: version,
	}, nil)

	registerSkillTools(s)      // list_skills, get_skill — universal, work in every client
	registerReconTool(s)       // recon — Phase-1 structural scan
	registerGuideTools(s)      // guide_read/write/delta — durable SHA-tagged guide cache
	registerGraphTools(s)      // trace_flow, impact — code-graph queries
	registerRepoMapTool(s)     // repo_map — PageRank-ranked, token-budgeted orientation map
	registerHistoryTool(s)     // history — git churn/ownership hotspots
	registerDeadCodeTool(s)    // dead_code — uncalled functions/methods (orphans)
	registerExplainDiffTool(s) // explain_diff — change set + blast radius for a branch/PR
	registerContextPackTool(s) // context_pack — ranked, token-budgeted source bundle for a seed
	registerDepsTool(s)        // deps — external dependency graph from manifests
	registerSchemaTool(s)      // schema — SQL DDL → entities + ERD
	registerRoutesTool(s)      // routes — HTTP API surface from framework patterns
	registerMapTool(s)         // render_map — interactive HTML / static Mermaid map
	registerSkillResources(s)  // onboard://skills/* — for clients that read resources
	registerPrompt(s)          // /onboard prompts — for clients that surface prompts

	return s
}
