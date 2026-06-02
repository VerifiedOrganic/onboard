# onboard — documentation

`onboard` is a single static Go binary that is **both an MCP server and a CLI installer**.
It teaches an agent (or a human, headlessly) how a codebase actually works — architecture,
data flow, end-to-end traces, and the risky negative space — which is exactly what you want
when you've inherited code, forgotten code, or watched an agent generate code faster than
you could read it.

Two halves: embedded **skills** carry *how to teach* a codebase; an embedded **code-graph
engine** (exposed as MCP tools) supplies *the facts to teach from*.

## Start here

**New to onboard?** Don't start with the internals — that's the mistake the old version of
this index made. Start with the path that matches what you're trying to do:

| You want to… | Read, in order |
|--------------|----------------|
| **Use it** | [getting-started.md](getting-started.md) → [install.md](install.md) |
| **Understand it** | [concepts.md](concepts.md) — the five ideas that make everything else click |
| **Integrate it** | [mcp-tools.md](mcp-tools.md) → [skills.md](skills.md) → [guide-cache.md](guide-cache.md) |
| **Build on it** | [architecture.md](architecture.md) → [code-graph.md](code-graph.md) → [development.md](development.md) |
| **Know why** | [research-notes.md](research-notes.md) → [enhancements.md](enhancements.md) |

## Every doc

| Doc | What it covers |
|-----|----------------|
| [getting-started.md](getting-started.md) | The guided first run: build, install, `doctor`, the `/onboard` tour, your first tool call — with what to expect at each step. |
| [concepts.md](concepts.md) | The mental model: skills vs tools, why edges are rumours, honesty as a feature, the guide cache, and the one rule the whole design bends around. |
| [install.md](install.md) | Wiring the binary into five different agents — the config-shape matrix, idempotent merging, `doctor`, and the full command reference. |
| [mcp-tools.md](mcp-tools.md) | Reference for all 17 MCP tools plus the resources and prompt — exact input/output fields and behavior. |
| [skills.md](skills.md) | The embedded skill system: the five skills, one source of truth reaching every agent three ways, and the `SKILL.md` frontmatter contract. |
| [guide-cache.md](guide-cache.md) | The durable, git-SHA-tagged codebase guide: where it lives (inside `.git`), its header format, and the read / write / delta lifecycle. |
| [architecture.md](architecture.md) | The big picture: the dual server/CLI binary, component map, request lifecycle, and the design principles every other choice follows from. |
| [code-graph.md](code-graph.md) | The pure-Go tree-sitter engine: recon, definition/reference extraction, name+scope resolution, `trace_flow`, `impact`, `render_map`, providers, and the honest accuracy ceiling. |
| [development.md](development.md) | Build, test, lint, CI, release, the package layout, and how to add a new tool, skill, or agent. |
| [research-notes.md](research-notes.md) | The original research-to-decisions record. Rationale, not reference. |
| [enhancements.md](enhancements.md) | Competitive landscape and a prioritized roadmap of what to add next. |

## The one-minute version

- **What it is:** a binary you install once that any MCP-capable agent (Claude Code, Codex,
  Grok, opencode, Cursor) can launch as a server. Also runs headless / in CI over HTTP.
- **What it gives an agent:** the `codebase-walkthrough` skill (and four siblings) for *how
  to teach* a codebase, plus 17 tools — `recon`, `trace_flow`, `impact`, `dead_code`,
  `explain_diff`, `render_map`, a durable `guide` cache, and more — for *the facts* to teach
  from.
- **Why a binary and not just a skill file:** every agent has its own skill format, but they
  all speak MCP. One binary reaches all of them, and the skill content is the same
  `//go:embed`'d source of truth for both the server and the per-agent installer.
- **The defining constraint:** it stays a single static, CGo-free, cross-compilable
  artifact. That one rule explains the pure-Go tree-sitter engine, the embedded grammars,
  and the "syntactic, not type-checked" honesty of the call graph. ([concepts.md](concepts.md)
  unpacks why.)
