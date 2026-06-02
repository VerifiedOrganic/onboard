# MCP tool reference

This is the exhaustive contract — every field of every tool. It's a reference, so it reads
like one; keep it open in a tab rather than front to back. If you're still forming the
mental model, [concepts.md](concepts.md) is the gentler door.

**Which tool when** (the short version):

| You're asking… | Reach for |
|----------------|-----------|
| "Where do I even start?" | `recon` (lay of the land), `repo_map` (the load-bearing core, ranked) |
| "Follow this from end to end" | `trace_flow` |
| "What breaks if I change this?" | `impact` |
| "Give me everything relevant to X" | `context_pack` |
| "What did this PR touch, and how far does it reach?" | `explain_diff` |
| "What did the build write but never wire in?" | `dead_code` |
| "Draw me the shape of it" | `render_map` |
| "What's the external/data/HTTP surface?" | `deps`, `schema`, `routes` |
| "Where do bugs and effort cluster?" | `history` |
| "Write the understanding down and keep it fresh" | `guide_read` / `guide_write` / `guide_delta` |

The `onboard` MCP server exposes **17 tools**, **1 resource family**, and **1 prompt**,
all registered in `server.New(version)` (`internal/server/server.go`). The server
identifies itself to clients as `onboard` with the stamped build `version`.

The 17 tools: `list_skills`, `get_skill` (skills); `recon` (structural scan); `repo_map`,
`trace_flow`, `impact`, `context_pack`, `history`, `dead_code` (code graph); `explain_diff`
(change-set review); `deps`, `schema`, `routes` (structured extractors); `render_map`
(diagrams); `guide_read`, `guide_write`, `guide_delta` (the cached guide).

Every tool handler uses the Go MCP SDK three-return pattern `(*mcp.CallToolResult, Out,
error)`: on success it returns `(nil, out, nil)` and the SDK serializes `out` as
structured content; on failure it returns a Go `error` that the SDK surfaces as
`IsError: true`. Tools never hand-build error results. Expected "degraded" states
(no git, no symbol match, thin graph) are **not** errors — they populate a `Note` field
and still return useful data.

Conventions shared by most tools:
- `root` (optional) — absolute path to the repo root. Defaults to the server's working
  directory.
- `refresh` (graph tools) — bypass the per-root cached graph and re-index.
- `precise` (graph tools: `trace_flow`, `impact`, `repo_map`, `context_pack`) — for Go
  modules, enrich the graph with type-checked edges (resolves interface dispatch); opt-in
  because it builds the program, and a silent no-op without the `go` toolchain.

---

## Skill tools

### `list_skills`
*`internal/server/tools_skills.go`* — List the onboarding skills embedded in this server.

**Input:** none.

**Output:**
| Field | JSON | Type | Notes |
|-------|------|------|-------|
| Skills | `skills` | `[]{name, description}` | one entry per embedded skill |

### `get_skill`
*`internal/server/tools_skills.go`* — Return the full content of an embedded skill (`SKILL.md` plus all reference files). The walkthrough workflow is loaded this way.

**Input:**
| Field | JSON | Type | Description |
|-------|------|------|-------------|
| Name | `name` | string | the skill name, e.g. `codebase-walkthrough` |

**Output:**
| Field | JSON | Type |
|-------|------|------|
| Name | `name` | string |
| Content | `content` | string — full rendered markdown (`SKILL.md` + references) |

---

## Structural scan

### `recon`
*`internal/server/tools_recon.go`* — Phase-1 reconnaissance: detect stack, frameworks, entry points, test layout, tooling, a pruned directory tree, and (in a git repo) the highest-churn hotspot files. A fast structural scan that **reads no source beyond manifest filenames**. Pure Go, no native deps.

**Input:**
| Field | JSON | Type | Description |
|-------|------|------|-------------|
| Root | `root` | string (opt) | repo root; defaults to the working dir |

**Output:**
| Field | JSON | Type | Notes |
|-------|------|------|-------|
| Root | `root` | string | resolved absolute path |
| Stack | `stack` | `[]string` | ecosystems inferred from manifests (Go, Python, Rust, …) |
| Manifests | `manifests` | `[]string` | manifest files found (relative) |
| Frameworks | `frameworks` | `[]string` | Next.js, Vite, Django, Tailwind, … |
| EntryPoints | `entry_points` | `[]string` | files named `main`/`index`/`app`/`server` (non-test) |
| TestLayout | `test_layout` | `[]string` | directories containing test files |
| Tooling | `tooling` | `[]string` | Docker, Make, ESLint, golangci-lint, GitHub Actions, … |
| DirTree | `dir_tree` | `[]string` | top two directory levels, pruned |
| Hotspots | `hotspots` | `[]string` (opt) | top-8 highest-churn files (`path — N commits, M authors, last YYYY-MM-DD`); git only, omitted otherwise |
| FileCount | `file_count` | int | |
| Note | `note` | string (opt) | advisory when no tests are found |

Skips `node_modules`, `vendor`, `dist`, `build`, `__pycache__`, `target`, `venv`,
`coverage`, `bin`, `obj`, and dotdirs (except `.github`). See [code-graph.md](code-graph.md#recon)
for the full detection tables.

---

## History signals

### `history`
*`internal/server/tools_history.go`* — Surface git change-history signals: the files with the most churn, their additions/deletions, last-changed date, and distinct author count. High-churn, multi-author files are onboarding hotspots and prime risk-audit targets. Requires a git repository.

**Input:**
| Field | JSON | Type | Description |
|-------|------|------|-------------|
| Root | `root` | string (opt) | repo root |
| Limit | `limit` | int (opt) | max files to return, ranked by churn (default 20) |
| MaxCommits | `max_commits` | int (opt) | recent commits to scan (default 1000; negative = entire history) |

**Output:**
| Field | JSON | Type | Notes |
|-------|------|------|-------|
| Files | `files` | `[]{path, commits, additions, deletions, last_date, authors}` | ranked by churn (commit count) desc |
| TotalFiles | `total_files` | int | files with any history in the scanned range |
| Note | `note` | string (opt) | non-git advisory, or empty-history note |

Aggregated from `git log --no-merges --numstat`; merge commits are excluded, renames
follow to the destination path, and binary files count as zero lines. Degrades gracefully
(a `note`, no error) outside a git repo.

### `dead_code`
*`internal/server/tools_deadcode.go`* — Find callable definitions (functions and methods) with **no caller in the indexed graph** — a lead for code an autonomous build wrote but never wired in. Reads the same call graph as `trace_flow`/`impact`; an orphan is a def whose `Reverse` set is empty. Entry points (`main`, `init`), Go test/benchmark/fuzz/example functions, and test files are excluded.

**Input:**
| Field | JSON | Type | Description |
|-------|------|------|-------------|
| Root | `root` | string (opt) | repo root |
| Limit | `limit` | int (opt) | max orphans, highest-confidence first (default 50) |
| Precise | `precise` | bool (opt) | resolve Go interface-dispatch callers first so methods aren't falsely flagged |
| Refresh | `refresh` | bool (opt) | re-index instead of using the cached graph |

**Output:**
| Field | JSON | Type | Notes |
|-------|------|------|-------|
| Orphans | `orphans` | `[]{qname, symbol, file, line, kind, exported, confidence, reason}` | `symbol` is receiver-qualified for methods; ranked high → low |
| Scanned | `scanned_callables` | int | functions + methods considered |
| TotalFound | `total_found` | int | orphans before the limit |
| Truncated | `truncated` | bool | more orphans than the limit |
| Note | `note` | string | the leads-not-verdicts caveat (+ Go precision hint when applicable) |

**Confidence** reflects what the graph can and cannot see: `high` for an unexported function (nothing outside the repo can reach it), `medium` for an exported function (possible external importer) or an uncalled method *after* precise dispatch resolution, `low` for a method without precise (interface dispatch unresolved). The note is explicit that reflection, code generation, framework/DI registration, build-tagged files, and external importers can all hide a caller — **leads, not verdicts**.

### `explain_diff`
*`internal/server/tools_explaindiff.go`* — Scope onboarding to a change set: the files a branch/PR touched, the symbols inside the **changed lines**, and each one's blast radius. Changed lines come from `git diff --unified=0 <base>` (committed and uncommitted work since base, via `git.Diff`); symbols are attributed by line range (`touchedSymbols`: a symbol spans from its declaration to the next one's); impact comes from the same call graph as `impact`.

**Input:**
| Field | JSON | Type | Description |
|-------|------|------|-------------|
| Root | `root` | string (opt) | repo root |
| Base | `base` | string (opt) | ref to compare against; defaults to the merge-base with the default branch (`origin/main`, `main`, `master`) |
| Limit | `limit` | int (opt) | max changed symbols to detail, by blast radius (default 50) |
| Precise | `precise` | bool (opt) | type-checked Go edges so the radius includes interface-dispatch callers |
| Refresh | `refresh` | bool (opt) | re-index instead of using the cached graph |

**Output:**
| Field | JSON | Type | Notes |
|-------|------|------|-------|
| Base | `base` | string | the resolved base ref |
| ChangedFiles | `changed_files` | `[]{path, status, hunks}` | status A/M/D/R, hunk count |
| ChangedSymbols | `changed_symbols` | `[]{qname, symbol, file, line, kind, direct_callers, impacted_count}` | ranked by blast radius desc |
| AtRiskTests | `at_risk_tests` | `[]string` | tests in the union of transitive callers |
| ImpactedCount | `impacted_count` | int | size of the union of transitive callers across all changed symbols |
| Truncated | `truncated` | bool | more changed symbols than the limit |
| Note | `note` | string | line-attribution + edge caveats |

Degrades gracefully (a `note`, no error) outside a git repo, when no base branch can be detected (asks for an explicit `base`), or when there are no changes. Deletions and a file's preamble lines attribute to no symbol; the note says so.

---

## Code-graph tools

Both graph tools share a per-root indexed-graph cache and resolve their target symbol the
same way: `FindSymbols` matches by exact name/QName first, then by QName substring; the
first match is used and any others are returned in `candidates`. A symbol QName looks like
`internal/x/y.go::Foo`. See [code-graph.md](code-graph.md) for internals and accuracy
limits.

**Precision (`precise: true`).** `trace_flow`, `impact`, `repo_map`, and `context_pack`
accept a `precise` flag. For a Go module with the `go` toolchain available, it enriches the
graph with **type-checked** edges (VTA over the SSA call graph), most importantly resolving
**interface dispatch** that the syntactic pass leaves unresolved. Those edges are *proven*,
so the tool's `note` upgrades accordingly. It is opt-in because it builds the program (slower
than the syntactic graph) and is cached separately per root; outside a Go module it is a
silent no-op. See [code-graph.md](code-graph.md#the-go-precision-layer-opt-in-type-checked).

### `trace_flow`
*`internal/server/tools_graph.go`* — Trace an execution flow from an entry symbol through its callees, breadth-first to a depth. Backed by a syntactic call graph.

**Input:**
| Field | JSON | Type | Description |
|-------|------|------|-------------|
| Root | `root` | string (opt) | repo root |
| Entry | `entry` | string | symbol to trace from: a function name, `file::name`, or a QName substring |
| Depth | `depth` | int (opt) | max call depth (default 4) |
| Format | `format` | string (opt) | `mermaid` to also return the trace as a `sequenceDiagram` |
| Precise | `precise` | bool (opt) | for Go modules, enrich with type-checked edges (resolves interface dispatch); slower, needs the `go` toolchain |
| Refresh | `refresh` | bool (opt) | re-index instead of using the cached graph |

**Output:**
| Field | JSON | Type | Notes |
|-------|------|------|-------|
| Entry | `entry` | string | echoes the input |
| Matched | `matched_symbol` | string (opt) | first matched symbol QName |
| Candidates | `candidates` | `[]string` (opt) | other matches when >1 |
| Nodes | `nodes` | `[]{qname, file, line, depth, callees}` | BFS traversal |
| Mermaid | `mermaid` | string (opt) | a `sequenceDiagram` of the trace, when `format="mermaid"` |
| Truncated | `truncated` | bool | hit the 250-node cap or depth-limited unseen callees |
| Provider | `provider` | string | `builtin` or `null` |
| Note | `note` | string (opt) | diagnostic for no match / null provider |

### `impact`
*`internal/server/tools_graph.go`* — Compute the blast radius of changing a symbol: direct callers, all transitive callers, and which of those are tests. Answers "what breaks if I change X" before editing.

**Input:**
| Field | JSON | Type | Description |
|-------|------|------|-------------|
| Root | `root` | string (opt) | repo root |
| Symbol | `symbol` | string | symbol whose blast radius to compute |
| Precise | `precise` | bool (opt) | for Go modules, enrich with type-checked edges so the blast radius includes dynamically-dispatched callers; slower, needs the `go` toolchain |
| Refresh | `refresh` | bool (opt) | re-index instead of using the cached graph |

**Output:**
| Field | JSON | Type | Notes |
|-------|------|------|-------|
| Symbol | `symbol` | string | echoes the input |
| Matched | `matched_symbol` | string (opt) | |
| Candidates | `candidates` | `[]string` (opt) | other matches when >1 |
| DirectCallers | `direct_callers` | `[]string` | immediate callers (QNames) |
| TransitiveCallers | `transitive_callers` | `[]string` | full reachable caller set |
| AtRiskTests | `at_risk_tests` | `[]string` | the subset that are test-file symbols |
| ImpactedCount | `impacted_count` | int | `len(transitive_callers)` |
| Provider | `provider` | string | `builtin` or `null` |
| Note | `note` | string | **always set** — the syntactic-edges caveat (or the type-checked upgrade when `precise` resolved Go edges), or a null-provider explanation |

### `repo_map`
*`internal/server/tools_repomap.go`* — Rank the codebase by call-graph centrality (PageRank), blended with git churn when available, and return a compact, token-budgeted map of the most important symbols — the heavily-relied-upon, actively-changing core. Load it first for orientation. Inspired by aider's repo map.

**Input:**
| Field | JSON | Type | Description |
|-------|------|------|-------------|
| Root | `root` | string (opt) | repo root |
| Focus | `focus` | `[]string` (opt) | symbols, repo-relative files, or QNames to bias the ranking toward (personalized PageRank) |
| MaxTokens | `max_tokens` | int (opt) | approximate token budget for the rendered map (default 1000) |
| ChurnWeight | `churn_weight` | float (opt) | how much git churn influences the ranking, 0..1 (default 0.3); 0 = pure centrality; ignored outside a git repo |
| Precise | `precise` | bool (opt) | for Go modules, enrich with type-checked edges before ranking; slower, needs the `go` toolchain |
| Refresh | `refresh` | bool (opt) | re-index instead of using the cached graph |

**Output:**
| Field | JSON | Type | Notes |
|-------|------|------|-------|
| Map | `map` | string | rendered outline: files, each with its top symbols (`:line kind name (callers: N, churn: M)`) |
| Symbols | `symbols` | `[]{qname, name, kind, file, line, callers, churn, score}` | the ranked symbols included, highest first |
| TotalSymbols | `total_symbols` | int | symbols in the graph |
| Included | `included` | int | symbols that fit the budget |
| Provider | `provider` | string | `builtin` or `null` |
| Truncated | `truncated` | bool | the budget cut some symbols |
| Note | `note` | string (opt) | ranking caveat (states whether churn was blended), or null-provider explanation |

Edges flow caller→callee, so a symbol called (directly or transitively) by many
important symbols accumulates a high score. `focus` concentrates PageRank's teleport
distribution on the seed set (an exact QName, a file path → all its symbols, or a bare
name), biasing the map toward an area.

**Churn fusion.** In a git work tree the final ranking score is
`(1 − churn_weight)·centralityNorm + churn_weight·churnNorm`, where centrality is PageRank
normalized by its max and churn is the file's commit count log-normalized by the max (commit
counts are heavy-tailed, so `log1p` keeps a single runaway file from flattening the signal).
A symbol that scores high on **both** axes — load-bearing *and* volatile — is the prime
onboarding target. With no git history (or `churn_weight=0`) every churn term is zero and the
score collapses to pure centrality, so the ordering is identical to a non-git repo. Under the
`null` provider, symbols are listed but not ranked by call importance.

### `context_pack`
*`internal/server/tools_context.go`* — Assemble a ranked, token-budgeted bundle of the source most relevant to a seed symbol or file — retrieval-augmented context with **no embedding model**. Relevance is call-graph proximity to the seed (callers *and* callees), refined by centrality and git churn.

**Input:**
| Field | JSON | Type | Description |
|-------|------|------|-------------|
| Root | `root` | string (opt) | repo root |
| Seed | `seed` | string | what to gather context around: a symbol name, a repo-relative file path, or a `file::name` QName |
| MaxTokens | `max_tokens` | int (opt) | approximate token budget for the bundled snippets (default 4000) |
| MaxDistance | `max_distance` | int (opt) | call-graph hops out from the seed to gather (default 2) |
| Precise | `precise` | bool (opt) | for Go modules, enrich with type-checked edges so the neighborhood follows real dispatch; slower, needs the `go` toolchain |
| Refresh | `refresh` | bool (opt) | re-index instead of using the cached graph |

**Output:**
| Field | JSON | Type | Notes |
|-------|------|------|-------|
| Seed | `seed` | string | echoes the input |
| Matched | `matched` | `[]string` (opt) | resolved seed QNames (a file seed → all its symbols) |
| Pack | `pack` | string | rendered bundle: per-snippet header (`// file:line qname (distance N, callers C, churn K)`) + source |
| Items | `items` | `[]{qname, name, kind, file, line, end_line, distance, callers, churn, score, snippet}` | included items, most relevant first |
| TotalCandidates | `total_candidates` | int | definitions in the neighborhood before the budget cut |
| Included | `included` | int | items that fit the budget |
| Provider | `provider` | string | `builtin` or `null` |
| Truncated | `truncated` | bool | the budget cut some items |
| Note | `note` | string (opt) | relevance + syntactic/heuristic caveats |

**Relevance.** A breadth-first walk from the seed over both edge directions assigns each
reachable definition its minimum hop `distance`. The score is
`packDecay^distance · (1 + centralityW·centralityNorm + churnW·churnNorm)` (defaults
`packDecay=0.5`, `centralityW=1.0`, `churnW=0.5`), so each hop halves relevance while
centrality and churn break ties among equidistant symbols. Snippets are **heuristic
windows**: from the definition's start line to the line before the next definition in the
same file, capped at 40 lines — accurate for top-level functions, approximate for nested
ones. Under the `null` provider there are no edges, so the pack contains only the seed's own
definitions.

---

## Map rendering

### `render_map`
*`internal/server/tools_map.go`* — Render a navigable map of the codebase. With explicit `nodes`/`edges` it renders exactly those; otherwise it derives a package-level dependency map from the code graph.

**Input:**
| Field | JSON | Type | Description |
|-------|------|------|-------------|
| Root | `root` | string (opt) | repo root |
| Topic | `topic` | string (opt) | diagram title, e.g. `Architecture` |
| Format | `format` | string (opt) | `html` (default) or `mermaid` |
| Nodes | `nodes` | `[]{id, label, description, files}` (opt) | explicit nodes; if omitted, derived from the graph |
| Edges | `edges` | `[]{from, to, label}` (opt) | explicit edges; ignored unless `nodes` given |
| OutputPath | `output_path` | string (opt) | absolute path to write the file; if empty, content is only returned |
| Refresh | `refresh` | bool (opt) | re-index (only when deriving) |

**Output:**
| Field | JSON | Type | Notes |
|-------|------|------|-------|
| Format | `format` | string | `html` or `mermaid` |
| Content | `content` | string | rendered HTML or Mermaid source (always returned inline) |
| Path | `path` | string (opt) | set if `output_path` was provided |
| NodeCount | `node_count` | int | |
| Derived | `derived` | bool | true when nodes were auto-derived |
| Truncated | `truncated` | bool | true when the derived graph was capped at 12 nodes |
| Note | `note` | string (opt) | advisory for an empty/thin graph |

- **`html`** → a self-contained interactive file: Mermaid 11 + svg-pan-zoom, dark theme,
  click-a-node-for-detail panel. Node IDs/labels are sanitized to prevent SVG injection.
- **`mermaid`** → committable `flowchart LR` diagram-as-code with a file-path legend.

When deriving, directories are ranked by call-degree and capped at 12 nodes; only
cross-directory call edges are drawn.

---

## Structured extractors

Deterministic extractors that read **facts** from manifests, DDL, and route registrations —
no call graph, no inference, so they carry no "likely, not proven" caveat. They feed the
architecture-cartographer, which otherwise asks the model to guess these. See
[code-graph.md](code-graph.md#structured-extractors).

### `deps`
*`internal/server/tools_deps.go`* — External dependency graph from manifests.

**Input:** `root` (opt); `format` (opt) — `mermaid` to also return a dependency flowchart.

**Output:** `manifests` (`[]{manifest, ecosystem, module, direct: [{name, version, dev}], indirect}`), `total_direct`, `mermaid` (opt), `truncated`, `note`. Parses `go.mod` (via `x/mod/modfile`), `package.json`, `requirements.txt`, and `Cargo.toml` (focused readers, not full TOML); versions are the declared constraint, not the resolved lockfile version.

### `schema`
*`internal/server/tools_schema.go`* — Database schema from SQL DDL.

**Input:** `root` (opt).

**Output:** `entities` (`[]{name, file, columns: [{name, type, pk, fk}]}`), `relationships` (`[]{from, to, column}`), `mermaid` (an `erDiagram`), `note`. Scans `.sql` files for `CREATE TABLE`, capturing columns, primary/foreign keys, and FK relationships (dangling FKs to unknown tables are dropped). A focused DDL reader, not a full SQL parser.

### `routes`
*`internal/server/tools_routes.go`* — HTTP API surface from framework patterns.

**Input:** `root` (opt).

**Output:** `routes` (`[]{method, path, file, line}`), `total`, `truncated`, `note`. Matches registration patterns across Go (chi/gin/echo/gorilla/net-http), Express, Flask, and FastAPI. A **recall-oriented heuristic**, not a parser: it can miss bespoke routing and occasionally over-match (it scans source text, including comments). `ANY` = the pattern (e.g. `net/http` `HandleFunc`) does not pin a method.

---

## Guide tools

The durable guide is a `codebase-walkthrough.md` cache stored **inside `.git`** (so it is
never committed) and tagged with the HEAD SHA. See [guide-cache.md](guide-cache.md) for
the on-disk format and lifecycle.

### `guide_read`
*`internal/server/tools_guide.go`* — Read the cached guide and report whether it is current with HEAD. Call before regenerating: if current, reuse the body instead of rescanning.

**Output:** `path`, `exists`, `current`, `cached_sha`, `head_sha`, `branch`, `mode`
(`full`/`delta`), `generated` (timestamp), `body` (full markdown), `note` (opt).

### `guide_write`
*`internal/server/tools_guide.go`* — Write (or overwrite) the guide. A machine-readable cache header (HEAD sha, branch, timestamp, mode) is prepended automatically.

**Input:** `root` (opt), `body` (required markdown), `mode` (`full` default, or `delta`).
**Output:** `path`, `sha` (HEAD at write time), `note` (opt — set when not a git repo).

### `guide_delta`
*`internal/server/tools_guide.go`* — Compute what changed since the cached guide's SHA, so an update can touch only affected sections.

**Output:** `cached_sha`, `head_sha`, `current` (bool), `changed` (`[]{status, path}` from
`git diff --name-status`), `note` (opt — non-git, no cached guide, or already current).

> `guide_delta` is read-only. After producing an incremental update, the caller invokes
> `guide_write` with `mode: "delta"` to record the new SHA and mode.

---

## Resources

`registerSkillResources` (`internal/server/resources.go`) adds **one resource per
embedded skill**:

- **URI:** `onboard://skills/<name>` (e.g. `onboard://skills/codebase-walkthrough`)
- **MIME type:** `text/markdown`
- **Body:** the fully rendered skill (`SKILL.md` + reference files)

The exact set of URIs is whatever skills are embedded in the binary at build time.

## Prompt

`registerPrompt` (`internal/server/prompts.go`) adds exactly one prompt:

- **Name:** `onboard`
- **Arguments:** `direction` (optional) — `outside-in` (entry points → core) or
  `inside-out` (load-bearing core → outward). Synonyms like `top-down` / `bottom-up`
  are normalized; anything unrecognized (or absent) leaves the tour to ask up front.
- **Returns:** a guided **tour** — two user-role messages. The first is the
  *conductor protocol* (`internal/server/assets/tour.md`, embedded beside the server,
  not under `skills/assets`, so it never appears in `list_skills`): it adds the
  direction fork and paces the walkthrough as four named phases —
  **Orient → Explore → Risks → Wrap-up** — where Explore loops as many passes as the
  codebase warrants (no fixed step count), with per-move pausing and a phase checklist
  instead of an `N/total` indicator. The second is the **unchanged** rendered
  `codebase-walkthrough` skill as the analysis engine. A client that surfaces
  `/onboard` therefore runs the walkthrough as a paced wizard rather than receiving it
  all at once. When `direction` is supplied, the conductor is prefixed with a note to
  skip the opening direction question.
