# Interactive map mode

Generate a single self-contained HTML file with a clickable diagram that gives the user a navigable mental model — not a code reference. The goal: understand a flow or the architecture in under two minutes by clicking through nodes.

## Output
Write `walkthrough-<topic>.html` to the project root (or a path the user names). It must be fully self-contained via CDN deps — no build step, just open in a browser. Offer to open it.

## What the file contains
- A **clickable Mermaid diagram** — a `flowchart` for flows/architecture, an `erDiagram` for database schema. Choose based on what the user asked for.
- **5–12 nodes.** Fewer than 5 is too coarse to be useful; more than 12 stops being a map and becomes noise. If the scope needs more, split into multiple diagrams or narrow the topic.
- A **detail panel** per node: plain-English description, the relevant file path(s), and an optional short code snippet.
- **Pan and zoom**: scroll to zoom, drag to pan, auto-fit on load.
- Dark theme by default, readable contrast.

## Build approach
1. Run the analysis engine (Phases 1, 3, 4 from SKILL.md are the most relevant; Phase 5 risks can become flagged nodes). Use the MCP call graph if available for accurate edges.
2. Reduce findings to the 5–12 key concepts and the connections between them. Write node descriptions in plain English — what it does and why it matters, not a signature dump.
3. Emit the HTML using these CDN libraries:
   - **Mermaid 11** (diagram rendering)
   - A lightweight pan/zoom (e.g. `svg-pan-zoom`) or a small custom handler
   - Optionally a syntax highlighter (Shiki or highlight.js) for snippets
4. Wire each Mermaid node id to a detail object `{ title, description, files[], snippet? }`; on node click, populate the side panel.

## Diagram quality checklist
- Node labels are concepts a human would name, not raw file names.
- Edges represent real relationships (calls, data flow, foreign keys) — verified, not guessed. If unverified, say so in the node description.
- The diagram answers the question the user actually asked; if they said "how does auth work," the map is the auth flow, not the whole app.
- Every node's detail panel cites at least one real file path.

## Note
This is the closest mode to "see the system" rather than "read about it." If the user is a visual thinker or the codebase is large and interconnected, steer them here. For learning a single flow deeply with back-and-forth, conversational mode is better; for a durable reference, cached-guide mode is better.
