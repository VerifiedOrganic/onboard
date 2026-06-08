# MCP code-graph backend setup

A code-graph MCP server turns the codebase into a queryable knowledge graph (AST + call edges) so structural claims are *verified* rather than inferred from text. This matters most for AI-generated codebases, where you cannot assume the structure is clean, and for large repos where grep-and-read burns context without ever seeing cross-file relationships.

## When it's worth it
- Repo larger than ~200 files, or deeply interconnected.
- AI-built code you need to trust the structure of (impact analysis: "changing X affects N functions across M files, K tests at risk").
- Frequent "who calls this / where is this used / how does data flow" questions.
- If the codebase is small and you can hold it in context, skip the MCP — the self-contained skill is enough.

## What these servers provide
Typically: AST extraction via Tree-sitter across many languages, semantic search (hybrid keyword + vector, so "handle user login" finds `authenticate_session`), forward/reverse call-graph traversal, dependency tracing, HTTP route mapping, and incremental re-indexing (only changed files re-parsed).

## Options (verify current install instructions at each project's repo before running anything)
- **code-graph-mcp** (`github.com/sdsrss/code-graph-mcp`) — Tree-sitter AST graph, ~10 languages, semantic search, call-graph traversal, HTTP route tracing, impact analysis, BLAKE3 Merkle incremental re-index. The most full-featured general option.
- **Code Pathfinder** (`codepathfinder.dev/mcp`) — Python-focused, 5-pass static analysis, call graphs, symbol/dependency/dataflow queries, Apache-2.0, runs locally.
- **Codebase-Memory** — Tree-sitter knowledge graphs; auto-detected by many agents; strongest on cross-file structural queries and dependency-chain traversal.
- Several others exist (ast-mcp-server, Code Grapher); the space moves fast — search for current leaders if these are stale.

## Wiring it into Claude Code
Most are configured as MCP servers in your client config (e.g. `.mcp.json` or the Claude Code MCP settings) with a `command`/`args` entry, then the agent auto-discovers the tools on restart. **Do not paste install commands from this file blindly** — fetch the chosen project's README for the exact, current command and config block, since flags and package names change.

## How the skill uses it once present
- **Phase 1/3** — pull the real module graph and route map instead of inferring from directory names.
- **Phase 4** — trace flows along verified caller→callee edges rather than by hand.
- **Phase 5** — run impact analysis to find fragile seams (a change touching many files/tests = a risky boundary).

## Privacy note
These run locally and index your code on your machine; nothing is sent externally. Still, confirm with the user before installing software on their system, and let them choose the server.
