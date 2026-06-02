# onboard — implementation decisions

This is the *why* archive — the original research and the calls that came out of it. It's
deliberately not kept in lockstep with the code, so read it for reasoning, not for current
fact. (If you want what onboard *is* today, you want [mcp-tools.md](mcp-tools.md) and
[concepts.md](concepts.md).)

Each section states the **DECISION**, the key rationale, and citations. Where research was
adversarially checked, the verdict here incorporates the corrected facts.

> **This is a point-in-time design-rationale archive, not the live API reference.** It
> records the decisions and reasoning as of initial implementation. The codebase has since
> grown — `repo_map`, `history`, `context_pack`, `deps`, `schema`, `routes`, `dead_code`,
> and `explain_diff` (**17 tools** now), the shipped Go precision layer, churn-blended
> ranking, same-package edge resolution, receiver-qualified methods, and the `doctor`
> command — and skills were later wired to use those tools. Where a section's tool lists or
> counts predate those additions, trust the code and the other docs
> ([mcp-tools.md](mcp-tools.md), [code-graph.md](code-graph.md),
> [enhancements.md](enhancements.md)) over this file. Kept for the *why*, not the
> *what-is-now*.

- Module path: `github.com/VerifiedOrganic/onboard`
- Go module target: `1.25` (CI builds with Go `1.26`)
- `main` is at the **repo root** (`main.go` calls `cmd.Execute()`)
- Version variable is `var version = "0.1.0"` in **package `cmd`** (a `var`, not a
  `const` — the `-X` linker stamp will land correctly)
- `serve` is a real Cobra subcommand (`onboard serve` runs the MCP server over stdio)
- Already depends on `github.com/modelcontextprotocol/go-sdk v1.6.1` and
  `github.com/spf13/cobra v1.10.2`

---

## 1. Code-graph engine

### DECISION

Adopt a **hybrid engine** with a pure-Go tree-sitter runtime as the universal
backbone, plus an optional Go-only semantic precision layer:

1. **Universal layer (all languages):** use the CGo-free pure-Go tree-sitter
   reimplementation **`github.com/odvcencio/gotreesitter` (MIT, v0.19.1)**. It
   embeds 206 grammar blobs and 116 hand-written Go external scanners, ships a
   full S-expression query engine, and compiles with `CGO_ENABLED=0` to every
   `GOOS/GOARCH` Go supports (including `wasip1`). Run tree-sitter **tags**
   queries per language to extract symbol **definitions**
   (`@definition.function/method/class/...`) and call **references**
   (`@reference.call`).

2. **Resolution layer (references → edges):** tree-sitter tags are **syntactic
   only** — they emit a flat list of named defs and refs that *you* must link.
   Build a name+scope resolver: index definitions by (qualified name, file,
   language), then link each `@reference.call` to a `@definition` by name within
   import/module scope. Store forward and reverse adjacency for impact analysis.
   Cross-language linking is name/heuristic-based; this is the accuracy ceiling.

3. **Optional Go precision layer:** when a Go toolchain is present at runtime,
   enrich Go-only graphs with `golang.org/x/tools/go/packages` +
   `golang.org/x/tools/go/callgraph` (CHA/RTA/VTA/static) for type-checked edges
   including interface dispatch. This is itself CGo-free, but `go/packages`
   **shells out to the `go` command**, so it needs the Go toolchain installed —
   keep it behind a runtime capability check, never a hard dependency of the
   static binary.

**Net:** single static binary, CGo-free, four-plus languages, syntactic call
graph everywhere, optional semantic precision for Go.

### Adversarial verdict (final)

The architecture and the central CGo-free claim **hold up** under adversarial
scrutiny and are adopted essentially as-is. Verified against primary sources:

- The library **is real and maintained** (created 2026-02-20, pushed 2026-05-30,
  ~494 stars, MIT, on pkg.go.dev at v0.19.1, Context7-indexed as
  `/odvcencio/gotreesitter`). The "young single-maintainer v0.x" risk is real and
  honestly disclosed.
- **CGo-free is genuinely true:** `go.mod` deps are only `golang.org/x/sync` +
  `gopkg.in/yaml.v3`; parser source imports only stdlib (no `import "C"`); README
  states "No CGo, no C toolchain... cross-compiles incl. wasip1." 116 hand-written
  Go external scanners.
- `x/tools` callgraph cha/rta/vta/static all exist and are pure Go; `go/packages`
  does shell out to the `go` command — so "CGo-free but **not toolchain-free**" is
  the correct caveat.
- Tags are syntactic-only and name resolution is on us — correctly stated as the
  accuracy ceiling.

**Is it CGo-free?** **Yes.** Builds with `CGO_ENABLED=0` to all targets.

**Fallback if not / if `gotreesitter` proves unusable at our target grammars:**
The only material defect found in the original research was a **fabricated code
sketch** (wrong API names). Use the **real API** instead (below). If the specific
Go/TS/JS/Py/Rust grammars fail validation at our pinned version, the ranked
fallbacks are:

1. `malivvan/tree-sitter` (wazero + WASM, genuinely CGo-free) — but v0.0.1,
   pre-release, only C/C++ grammars exposed today. Validates the path; immature.
2. Official `tree-sitter/go-tree-sitter` or `smacker/go-tree-sitter` (CGo) — most
   mature grammars, but **break `CGO_ENABLED=0`** and trivial cross-compile;
   require a C cross-toolchain per target (or `purego` runtime `.so` loading).
3. Pure-Go per-language parsers (`go/ast`+`go/types` for Go is excellent; no
   comparable pure-Go parser for TS/JS/Py/Rust) — high maintenance; only sane for
   the Go-specific precision layer.
4. Shell out to `universal-ctags`/tree-sitter CLI — defeats the
   single-static-binary goal and gives weak/no call edges.

### Real API (corrected from the fabricated sketch)

The original sketch invented `ts.NewQuery(lang, src)`, `q.Matches(...)`,
`m.CaptureName`, `m.Row`, and `m.EnclosingDefQName`. None of those exist. Use the
verified API:

**Query path** — note source comes **first**, then language:

```go
q, err := gotreesitter.NewQuery(tagsSCM, lang) // (source string, lang *Language)
cur := q.Exec(tree.RootNode(), lang, src)      // returns *QueryCursor
for {
    m, ok := cur.NextMatch() // *QueryMatch / *QueryCapture; NOT q.Matches(...)
    if !ok {
        break
    }
    // ...
}
```

**Preferred tags path** — simpler and idiomatic; use the grammar-bundled query
via `entry.TagsQuery`, do **not** hand-roll a `tagsByLang` map:

```go
entry  := grammars.DetectLanguage(path)
lang   := entry.Language()
tagger, _ := gotreesitter.NewTagger(lang, entry.TagsQuery) // use the SHIPPED query
tags := tagger.Tag(src)
// each tag exposes ONLY: tag.Kind ("definition.function", "reference.call", ...),
// tag.Name, tag.NameRange (with .StartPoint.Row/Column). There is NO field
// linking a reference to its enclosing definition.
```

**Compute caller scope yourself.** Because `Tag` has no enclosing-definition
field (the sketch's `EnclosingDefQName` is invented), the resolver must derive it:
sort definition tags by byte range and, for each reference tag, find the innermost
definition whose range contains the reference's position (walk the node tree or do
an interval/containment check). The rest of the resolver
(`ResolveEdges`/`Impacted`: name+scope linking, forward/reverse adjacency,
reverse-reachability) is correct as pseudocode.

**Optional Go-precision layer** — import paths are correct as written
(`golang.org/x/tools/go/packages`, `.../go/callgraph/vta`, `ssautil`). Keep it
behind a runtime capability check because `packages.Load` invokes `go`.

### Languages supported

Go, TypeScript, JavaScript, Python, Rust, Java, C, C++, C#, Ruby, PHP, and ~195
more via `gotreesitter`'s 206 embedded grammars.

### Risks (carried forward)

- Young single-maintainer v0.x project; self-reports per-grammar quality tiers
  (full/partial/none) and "does not guarantee error-free trees on all inputs."
  **Validate the specific Go/TS/JS/Py/Rust grammars at the pinned version.**
- Tags are **syntactic only**: name-based linking cannot distinguish same-named
  symbols across scopes/modules, and mishandles interface/dynamic dispatch,
  overloads, higher-order/callback calls, and dynamic imports. **Hard accuracy
  ceiling** of any pure-tree-sitter call graph.
- Binary size: embedding grammar blobs is large (a writeup reports ~12 MB → ~32 MB
  for 11 languages). **Trim to only needed grammars** if size matters.
- The Go-precision layer shells out to `go` — CGo-free but not toolchain-free;
  gate it.
- Pre-1.0 APIs may break — **pin versions** (`gotreesitter v0.19.1`).

**Confidence:** medium on the engine choice; high on the corrected API facts.

### Citations

- https://github.com/odvcencio/gotreesitter
- https://dev.to/thegdsks/parsing-11-languages-in-pure-go-without-cgo-how-i-replaced-regex-with-a-tree-sitter-runtime-g04
- https://github.com/malivvan/tree-sitter
- https://github.com/tree-sitter/go-tree-sitter
- https://pkg.go.dev/github.com/smacker/go-tree-sitter
- https://tree-sitter.github.io/tree-sitter/4-code-navigation.html
- https://pkg.go.dev/golang.org/x/tools/go/callgraph
- https://pkg.go.dev/golang.org/x/tools/go/callgraph/rta
- https://pkg.go.dev/golang.org/x/tools/go/callgraph/static
- https://pkg.go.dev/golang.org/x/tools/go/packages

---

## 2. Agent MCP-config matrix

### DECISION

Register the stdio server named **`onboard`** with `command = <abs binary path>`
and `args = ["serve"]` across all five agents. Four agents share a
`command`(string) + `args`(array) shape but differ in root key and file format;
**opencode is the outlier** that uses a single `command` **array** (binary + args
merged, no separate `args`). Three use JSON; **Codex and the locally-installed
Grok use TOML.** All five support MCP and all five have native filesystem skills
directories.

**Critical discrepancy (corrected per verification):** the locally installed
"Grok" is **xAI's Grok Build CLI v0.2.3** (TOML config at `~/.grok/config.toml`,
binary `~/.grok/bin/grok`) — **NOT** the superagent-ai npm `grok-cli` (which uses
JSON at `~/.grok/user-settings.json`). The installer must target the **TOML**
variant on this machine. Detect which by presence of `~/.grok/config.toml` vs
`user-settings.json`.

### Matrix

| Agent | Format | Root key | Server entry shape | Global config path | Project config path | Skills dir |
|---|---|---|---|---|---|---|
| **Claude Code** | JSON | `mcpServers` | `{"command": BIN, "args": ["serve"], "env": {}}` (optional `"type":"stdio"`) | `~/.claude.json` top-level `mcpServers` (also reads `~/.claude/mcp.json`) | `.mcp.json` top-level `mcpServers` | `~/.claude/skills/` |
| **Codex CLI** | TOML | `[mcp_servers.onboard]` | `command = BIN` (string), `args = ["serve"]` (array); optional `env`, `cwd`, `startup_timeout_sec`, `enabled` | `~/.codex/config.toml` (honors `CODEX_HOME`) | `.codex/config.toml` (trusted projects only) | `~/.codex/skills/` |
| **Grok (xAI Build CLI v0.2.3)** | TOML | `[mcp_servers.onboard]` | `command = BIN` (string), `args = ["serve"]` (array); optional `env`, `headers`, `enabled`, `startup_timeout_sec`, `tool_timeout_sec` | `~/.grok/config.toml` | `.grok/config.toml` (walks cwd→git root; project def **replaces**, not merges) | `~/.grok/skills/` |
| **opencode** | JSON | `mcp` (outlier) | `{"type":"local", "command":[BIN,"serve"], "enabled":true, "environment":{}}` | `~/.config/opencode/opencode.json` | `opencode.json[c]` in project root (highest precedence) | `~/.config/opencode/skills/` |
| **Cursor** | JSON | `mcpServers` | `{"command": BIN, "args": ["serve"], "env": {}}`; optional `envFile` (stdio only) | `~/.cursor/mcp.json` (**create — does not exist yet**) | `.cursor/mcp.json` | `~/.cursor/skills/` |

### Per-agent gotchas (verified)

- **Claude Code:** the global/user write target is a **top-level `mcpServers`**
  object in `~/.claude.json` (proven empirically — live file already has
  `context7`, `spindle` there; not literally printed in the docs as the user-scope
  shape). Local scope nests under `projects.<path>.mcpServers`. Avoid the reserved
  server name `workspace` (skipped at load); `onboard` is fine.
- **Codex / Grok:** snake_case table `[mcp_servers.<name>]` (NOT `mcpServers`).
  `command` is a string, `args` is an array. Codex's live `[mcp_servers.node_repl]`
  confirms the exact shape.
- **opencode:** merge binary + args into **one `command` array**; the field is
  **`environment`** (not `env`); `type:"local"` is required; root key is **`mcp`**.
- **Cursor:** if the top-level `mcpServers` key is missing/misspelled, Cursor
  silently ignores the file. `~/.cursor/` exists but `~/.cursor/mcp.json` must be
  created.

### Installer logic

Serialize **TOML** for Codex + Grok (`[mcp_servers.onboard]`, `command = BIN`,
`args = ["serve"]`); **JSON** for the other three. opencode merges into one array.
Claude Code/Cursor use `{"mcpServers":{"onboard":{...}}}`. For Grok, detect TOML vs
npm-JSON variant by config-file presence.

**Confidence:** high (verified against official docs and live local config files).

### Citations

- https://code.claude.com/docs/en/mcp
- https://developers.openai.com/codex/config-reference
- https://github.com/openai/codex/blob/main/docs/config.md
- https://opencode.ai/docs/mcp-servers/
- https://cursor.com/docs/mcp
- https://www.superagent.sh/blog/grok-cli-mcp-support
- Local: `~/.grok/README.md` (xAI Grok Build CLI v0.2.3, MCP section), `~/.grok/config.toml`,
  `~/.codex/config.toml`, `~/.claude.json`, `~/.claude/mcp.json`,
  `~/.config/opencode/opencode.json`

---

## 3. Go MCP SDK — test + HTTP patterns

### DECISION

Pin **`github.com/modelcontextprotocol/go-sdk v1.6.1`** (already in `go.mod`).
Define the `*mcp.Server` once; exercise it via **in-memory transports** in unit
tests and via the **Streamable HTTP handler** in production — the same server,
tools, resources, and prompts run over both transports.

### Verified API facts

- **In-memory transport:** `func NewInMemoryTransports() (*InMemoryTransport,
  *InMemoryTransport)` returns two transports prewired to each other (no
  stdio/exec/network).
- **Connect ordering:** `server.Connect(ctx, t1, nil)` **first**, then
  `client.Connect(ctx, t2, nil)` so the server is ready for the client's
  `initialize`. Signatures: `(*Server).Connect(ctx, Transport, *ServerSessionOptions)`
  and `(*Client).Connect(ctx, Transport, *ClientSessionOptions)`. Pass `nil` opts
  in tests.
- **Tool registration:** the generic **free function** (not a method)
  `func AddTool[In, Out any](s *Server, t *Tool, h ToolHandlerFor[In, Out])`, with
  `ToolHandlerFor[In, Out] = func(ctx, *CallToolRequest, In) (*CallToolResult, Out, error)`.
  The SDK auto-infers input **and** output JSON schemas from the In/Out structs
  (`json:"..."` for names, `jsonschema:"..."` for descriptions). Returning a
  non-zero `Out` auto-marshals into `StructuredContent` and validates against the
  inferred `OutputSchema`.
- **Invoke + read results:** client calls `cs.CallTool(ctx, &mcp.CallToolParams{Name,
  Arguments})` (`Arguments any`). `CallToolResult` has `Content []Content`,
  `StructuredContent any`, `IsError bool`. Read text via
  `res.Content[0].(*mcp.TextContent).Text`. **Structured output decodes to
  `map[string]any` on the client** — re-marshal/unmarshal to recover your `Out`
  struct. Always check `res.IsError` first. Resources/prompts work identically via
  `ListResources`/`ReadResource` and `ListPrompts`/`GetPrompt`.
- **Streamable HTTP:** `func NewStreamableHTTPHandler(getServer func(*http.Request)
  *Server, opts *StreamableHTTPOptions) *StreamableHTTPHandler`. It implements
  `http.Handler` (plus `Close() error`); the `getServer` callback runs per session
  (return a singleton or build per-tenant). `StreamableHTTPOptions`: `Stateless
  bool`, `JSONResponse bool`, `Logger *slog.Logger`, `EventStore`,
  `SessionTimeout time.Duration`, `DisableLocalhostProtection bool`,
  `CrossOriginProtection *http.CrossOriginProtection`. `nil` for defaults.
- **Sessions:** stateful mode tracks sessions by id, returns it in the
  `Mcp-Session-Id` response header on `initialize`; the client echoes it
  automatically. `Stateless: true` skips session tracking (serverless/load-balanced).
- **HTTP integration test:** front the handler with `httptest.NewServer(handler)`
  and point `mcp.StreamableClientTransport{Endpoint: ts.URL}` at it.
- **Stale-API gotcha:** the repo's `design/design.md` (quoted by some Context7
  snippets) shows `CallTool(ctx, *CallToolParams[json.RawMessage])` and a
  struct-literal `&mcp.StreamableHTTPHandler{Server: server}`. Those are design-era.
  The shipped v1.6.x API is the **function** `NewStreamableHTTPHandler(getServer,
  opts)` and non-generic `CallTool(ctx, *CallToolParams)`.

### In-process test pattern

```go
package server_test

import (
    "context"
    "encoding/json"
    "testing"

    "github.com/modelcontextprotocol/go-sdk/mcp"
)

type AddIn struct {
    A int `json:"a" jsonschema:"first addend"`
    B int `json:"b" jsonschema:"second addend"`
}
type AddOut struct {
    Sum int `json:"sum" jsonschema:"the sum"`
}

func add(ctx context.Context, req *mcp.CallToolRequest, in AddIn) (*mcp.CallToolResult, AddOut, error) {
    out := AddOut{Sum: in.A + in.B}
    return &mcp.CallToolResult{
        Content: []mcp.Content{&mcp.TextContent{Text: "ok"}},
    }, out, nil
}

func TestAddTool(t *testing.T) {
    ctx := context.Background()

    server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "v0.0.1"}, nil)
    mcp.AddTool(server, &mcp.Tool{Name: "add", Description: "add two ints"}, add)

    client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)

    // In-memory pipe; connect SERVER first, then client.
    t1, t2 := mcp.NewInMemoryTransports()
    ss, err := server.Connect(ctx, t1, nil)
    if err != nil {
        t.Fatal(err)
    }
    defer ss.Close()

    cs, err := client.Connect(ctx, t2, nil)
    if err != nil {
        t.Fatal(err)
    }
    defer cs.Close()

    res, err := cs.CallTool(ctx, &mcp.CallToolParams{
        Name:      "add",
        Arguments: map[string]any{"a": 2, "b": 3},
    })
    if err != nil {
        t.Fatal(err)
    }
    if res.IsError {
        t.Fatalf("tool error: %v", res.Content)
    }

    // Structured output arrives as map[string]any on the client; re-decode.
    var got AddOut
    raw, _ := json.Marshal(res.StructuredContent)
    if err := json.Unmarshal(raw, &got); err != nil {
        t.Fatal(err)
    }
    if got.Sum != 5 {
        t.Fatalf("want 5, got %d", got.Sum)
    }
}
```

### Streamable HTTP server pattern

```go
package main

import (
    "log"
    "net/http"

    "github.com/modelcontextprotocol/go-sdk/mcp"
)

func newServer() *mcp.Server {
    s := mcp.NewServer(&mcp.Implementation{Name: "onboard", Version: "v1.0.0"}, nil)
    mcp.AddTool(s, &mcp.Tool{Name: "add", Description: "add two ints"}, add) // same handler as tests
    return s
}

func main() {
    srv := newServer() // reuse one server for all sessions (stateless tools)
    handler := mcp.NewStreamableHTTPHandler(
        func(r *http.Request) *mcp.Server { return srv },
        nil, // or &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true}
    )

    mux := http.NewServeMux()
    mux.Handle("/mcp", handler)
    log.Fatal(http.ListenAndServe(":8080", mux))
}
```

The MCP client connects with `&mcp.StreamableClientTransport{Endpoint:
"http://host:8080/mcp"}` and the same `client.Connect(ctx, transport, nil)` flow.

**Confidence:** high (signatures verified against pkg.go.dev and repo source
`mcp/transport.go`, `mcp/streamable.go`, `mcp/protocol.go` for v1.6.x).

### Citations

- https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp
- https://github.com/modelcontextprotocol/go-sdk
- https://github.com/modelcontextprotocol/go-sdk/blob/main/docs/protocol.md
- https://github.com/modelcontextprotocol/go-sdk/blob/main/docs/quick_start.md

---

## 4. Skill suite

### DECISION

Ship **all four** new skills as separate single-capability skills that compose
with the existing `codebase-walkthrough` skill and reuse the Phase 1–6 engine plus
the `recon` / `trace_flow` / `impact` / `render_map` / `guide` tools, partitioned
so **no two descriptions compete**:

- **`codebase-walkthrough` (existing):** teaches the codebase phase-by-phase;
  retains the interactive clickable-HTML map mode.
- **`architecture-cartographer` (new):** emits **durable diagrams-as-code**
  (Mermaid C4Context/C4Container/C4Component, erDiagram, classDiagram, flowchart)
  as committable artifacts. Triggers: diagram / map / ERD / dependency-graph
  requests. Runs Phase 1 recon + Phase 3 architecture; picks diagram type by
  altitude; queries the graph for **verified edges**; reduces to 5–12 legible
  nodes per diagram; emits diagram-as-code plus a file-path legend. Tools: `recon`,
  `impact`, `render_map` (static output, not interactive HTML). The
  keep-this-diagram sibling of walkthrough's interactive-map mode.
- **`guide-maintainer` (new):** refreshes a cached codebase guide via **git-SHA
  delta**, updating only sections touched by changed files. Triggers: update /
  refresh / sync the guide, or cached SHA ≠ HEAD. Steps: read cache header + stored
  SHA → `git rev-parse HEAD` (stop if equal) → `git diff --name-status <sha> HEAD`
  → read only added/modified files → re-run the minimal subset of Phases 1–5 on
  the changed subtree → patch affected sections → restamp header (mode `delta`) →
  optionally sync `CLAUDE.md` preserving existing instructions. Tools: `guide`,
  `recon`, `trace_flow`, `impact`. Owns the maintenance loop that `cached-guide.md`
  (lines 8–24) defines only for first generation.
- **`test-gap-and-risk-auditor` (new):** standing **whole-codebase** audit of
  negative space (untested paths, unhandled errors, fragile integration seams,
  silent AI-build assumptions) → ranked risk register. Triggers: what is untested /
  where is the risk / find fragile parts / pre-change coverage audit. Runs Phase 2
  behavioral map + Phase 5 negative space: cross the behavioral map against the
  call graph for reachable-but-untested paths, enumerate integration seams and flag
  disagreements, list baked-in assumptions, run the defend-the-design pass, emit a
  prioritized register. Tools: `recon`, `guide`, `trace_flow`, `impact`.
- **`dependency-impact-analyzer` (new):** computes **blast radius of one proposed
  change** (callers, dependents, at-risk tests downstream of a function / file /
  endpoint / schema). Triggers: what breaks if I change X / what depends on this /
  who calls this / safe to rename/delete Y. Steps: resolve target → reverse-traverse
  for direct + transitive callers → forward-traverse for dependencies → intersect
  with the test set for at-risk tests and uncovered paths → flag temporal coupling
  as hidden blast radius → emit a change plan (files to edit, side effects,
  verification commands, fact vs assumption). Tools: `impact` (core: reverse +
  forward traversal, test intersection), `trace_flow`, `recon`. This is the skill
  that most justifies the code-graph MCP backend.

**Partitioning** (so descriptions don't collide): walkthrough **teaches**;
cartographer **draws** durable diagram-as-code (interactive HTML stays in
walkthrough); guide-maintainer runs the **delta-update loop**; auditor produces a
**standing** risk register; analyzer computes **per-change** blast radius. The
auditor (whole-codebase, standing) and analyzer (one-target, per-change) are
deliberately distinct.

**If forced to cut one:** drop `architecture-cartographer` first (most overlap with
interactive-map mode). The analyzer and auditor are the highest-value,
least-overlapping additions.

### SKILL.md authoring conventions (apply to all four)

- Frontmatter requires exactly two validated fields: **`name`** (≤64 chars,
  lowercase letters/numbers/hyphens, no XML tags, not reserved words `anthropic`/
  `claude`) and **`description`** (non-empty, ≤1024 chars, no XML tags, stating
  both **what it does and when to use it**). Claude Code/plugins also allow optional
  `allowed-tools` and `metadata`/`license`.
- Write descriptions in **third person** and embed **literal trigger phrases** —
  only name + description are pre-loaded for skill selection.
- Keep `SKILL.md` body **under 500 lines**; push detail into **one-level-deep**
  reference files linked directly from `SKILL.md` (no nested references); reference
  files >100 lines start with a table of contents.
- One capability per skill so descriptions don't collide. Fully qualify MCP tool
  names as **`ServerName:tool_name`** (e.g. `onboard:impact`). Build at least three
  evaluations per skill before extensive docs. The existing walkthrough already
  follows this thin-SKILL.md-plus-references pattern.
- **Naming:** prefer gerunds for consistency (noun phrases acceptable); pick **one**
  convention before publishing — the current proposal mixes styles.

### Open questions to resolve before publishing

1. Are `recon`/`trace_flow`/`impact`/`render_map`/`guide` real first-class MCP
   tools this server exposes, or capability shorthand? (Existing `SKILL.md`
   references only glob/grep/read plus an optional code-graph MCP.)
2. Pick one naming convention for the suite.
3. Confirm interactive HTML stays in walkthrough while cartographer owns static
   diagram-as-code (recommended).
4. Should `guide-maintainer` and `dependency-impact-analyzer` **hard-require** the
   code-graph MCP or degrade to grep? Blast radius via grep is unreliable on
   AI-built code — recommend hard-require.

**Confidence:** high.

### Citations

- https://platform.claude.com/docs/en/agents-and-tools/agent-skills/best-practices
- https://www.anthropic.com/engineering/equipping-agents-for-the-real-world-with-agent-skills
- https://github.com/mermaid-js/mermaid/blob/develop/docs/syntax/c4.md
- Local: `SKILL.md`, `cached-guide.md`, `mcp-setup.md`

---

## 5. CI / GoReleaser / golangci configs

### DECISION

Use a single-runner cross-build matrix that keeps the engine **pure-Go
(`CGO_ENABLED=0`)** so darwin/linux/windows on amd64/arm64 cross-compile trivially
from one Linux runner. Stamp the version into the **non-main** variable
`github.com/VerifiedOrganic/onboard/cmd.version` via `-ldflags -X` using the full
import path. (Verified: that variable is a package-level `var version` in package
`cmd`, so the stamp lands; `main` is at the repo root.)

### Verified version/tool choices

- **Go module target 1.25, CI toolchain 1.26.** Keeping the `go` directive at
  `1.25` matches the highest minimum required by current dependencies and lets
  golangci-lint v2.5.0 run; `setup-go` with `go-version: "1.26"` remains valid for
  CI build/test jobs.
- `CGO_ENABLED=0` cross-compiles all six targets from one Linux runner with no C
  toolchain. **`//go:embed` has no cgo implication** — embedded files are baked in
  at compile time and work identically in static builds.
- Version stamping: `-ldflags "-X
  github.com/VerifiedOrganic/onboard/cmd.version={{ .Version }}"` (full import
  path). Add `-s -w` to strip symbol/debug tables.
- **GoReleaser v2** (`version: 2`): archives use plural `formats: [tar.gz]`
  (singular `format` deprecated since v2.6); Windows uses `format_overrides`. Use
  `mod_timestamp: "{{ .CommitTimestamp }}"` + `flags: [-trimpath]` for reproducible
  builds.
- **`goreleaser-action@v7`** (`distribution: goreleaser`, `version: "~> v2"`,
  `args: release --clean`). Release job needs `permissions: contents: write`,
  `GITHUB_TOKEN`, and `fetch-depth: 0`. The action does **not** install Go — add
  `setup-go` yourself.
- **golangci-lint-action `v8`** (pinned for stability; v9 only bumps Node20→Node24)
  with golangci-lint binary **v2.5.0**. v8+ requires a separate `setup-go` step
  first and will not install Go.
- **golangci-lint v2 config** (`version: "2"`): `linters.default: standard` enables
  errcheck, govet, ineffassign, staticcheck, unused. In v2, **staticcheck subsumes
  gosimple and stylecheck** — do not list those separately. Formatters
  (gofmt/goimports/gci) live in a separate top-level `formatters:` section.

### CGo escape hatch (if the engine ever needs cgo)

Per the engine decision, keep it pure-Go (`gotreesitter`, and `modernc.org/sqlite`
over `mattn/go-sqlite3` if a DB is needed) so `CGO_ENABLED=0` holds. If cgo becomes
unavoidable: (1) Linux static still possible with `CGO_ENABLED=1` +
`-ldflags '-linkmode external -extldflags "-static"'` against musl (zig-cc or
musl-gcc); (2) macOS cgo binaries are always **partially dynamic** (cannot fully
static-link libSystem); (3) cross-compiling cgo needs per-target C toolchains —
use the `ghcr.io/goreleaser/goreleaser-cross` Docker image setting CC/CXX per
target, or split GoReleaser into per-OS jobs with `--split`/`partial` and merge.
**Do not abandon static builds** — switch toolchains.

### `.github/workflows/ci.yml`

```yaml
name: CI

on:
  push:
    branches: [main]
  pull_request:

permissions:
  contents: read

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.25"
      - run: go vet ./...
      - run: go test -race -coverprofile=coverage.out ./...

  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.26"
      - uses: golangci/golangci-lint-action@v8
        with:
          version: v2.5.0

  build:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        goos: [linux, darwin, windows]
        goarch: [amd64, arm64]
    env:
      CGO_ENABLED: "0"
      GOOS: ${{ matrix.goos }}
      GOARCH: ${{ matrix.goarch }}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.26"
      - run: go build -trimpath -ldflags "-s -w" ./...
```

### `.github/workflows/release.yml`

```yaml
name: Release

on:
  push:
    tags:
      - "v*"

permissions:
  contents: write

jobs:
  goreleaser:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - uses: actions/setup-go@v5
        with:
          go-version: "1.26"
      - uses: goreleaser/goreleaser-action@v7
        with:
          distribution: goreleaser
          version: "~> v2"
          args: release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

### `.goreleaser.yaml`

```yaml
version: 2

before:
  hooks:
    - go mod tidy

builds:
  - id: onboard
    main: .            # main is at the repo root (adjust to ./cmd/onboard if it moves)
    binary: onboard
    env:
      - CGO_ENABLED=0
    flags:
      - -trimpath
    mod_timestamp: "{{ .CommitTimestamp }}"
    ldflags:
      - -s -w
      - -X github.com/VerifiedOrganic/onboard/cmd.version={{ .Version }}
      - -X github.com/VerifiedOrganic/onboard/cmd.commit={{ .Commit }}
      - -X github.com/VerifiedOrganic/onboard/cmd.date={{ .CommitDate }}
    goos: [linux, darwin, windows]
    goarch: [amd64, arm64]

archives:
  - id: onboard
    formats: [tar.gz]
    name_template: >-
      {{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}
    format_overrides:
      - goos: windows
        formats: [zip]

checksum:
  name_template: "checksums.txt"

changelog:
  sort: asc
  filters:
    exclude:
      - "^docs:"
      - "^test:"
      - "^chore:"
```

> Note: only `version` is guaranteed to exist in package `cmd` today (`var version
> = "0.1.0"`). Add package-level `var commit string` / `var date string` to `cmd`
> if you want the extra `-X` stamps to land; otherwise drop those two ldflags.

### `.golangci.yml`

```yaml
version: "2"

linters:
  default: standard   # errcheck, govet, ineffassign, staticcheck, unused
  enable:
    - misspell
    - revive
    - bodyclose
    - gosec
    - unconvert
    - copyloopvar
  settings:
    gosec:
      excludes:
        - G104   # avoid duplicate "unhandled error" reports with errcheck

formatters:
  enable:
    - gofmt
    - goimports
  settings:
    goimports:
      local-prefixes:
        - github.com/VerifiedOrganic/onboard
```

> The extra linters and the goimports local-prefix are sensible defaults (inferred,
> not doc-mandated) — adjust to taste. In v2, do **not** list `gosimple`/`stylecheck`
> (subsumed by `staticcheck`).

### Pre-first-run checklist (all confirmed against the live repo)

1. `main` lives at the **repo root** → `.goreleaser.yaml` `main: .` is correct.
2. `var version` is a package-level `var` in package `cmd` → the `-X` stamp will
   land (it would silently no-op for a `const` or wrong package).
3. Engine stays pure-Go → `CGO_ENABLED=0` and the single-runner matrix hold.

**Confidence:** high.

### Citations

- https://go.dev/doc/devel/release
- https://go.dev/doc/go1.26
- https://goreleaser.com/customization/builds/go/
- https://goreleaser.com/customization/archive/
- https://goreleaser.com/ci/actions/
- https://github.com/goreleaser/goreleaser-action/releases
- https://golangci-lint.run/docs/configuration/file/
- https://golangci-lint.run/docs/linters/
- https://github.com/golangci/golangci-lint-action/releases
- https://ldez.github.io/blog/2025/03/23/golangci-lint-v2/
- https://medium.com/@bytebase/how-we-explored-the-best-practices-of-goreleaser-x-cgo-23ae3e2174e7
