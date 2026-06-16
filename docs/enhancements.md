# Enhancements & roadmap

Where onboard could go next — and where it stands against the neighbours. Every proposal is
weighed against onboard's [design ethos](architecture.md#design-principles): a **single
static CGo-free binary**, **honesty over false precision**, and **graceful degradation**.
Anything that would break the static-binary guarantee gets gated behind a runtime capability
check, never made a hard dependency.

> **Already shipped since this roadmap was written** (so don't propose them again): the Go
> type-checked precision layer, **same-package edge resolution** (recovers Go method/function
> calls the global-uniqueness check used to drop), **receiver-qualified method symbols**
> (`T.Method` in output), and three new tools/commands — **`dead_code`** (uncalled
> functions/methods), **`explain_diff`** (change-set + blast radius for a branch/PR), and
> **`onboard doctor`** (verify installs). Still open from the lists below: duplicate-logic
> detection, precise layers for TS/Python, and server-mediated delegation to an external
> code-graph MCP.

## Where onboard sits in the landscape

| System | Approach | Strength | Cost / dependency |
|--------|----------|----------|-------------------|
| **onboard** | Pure-Go tree-sitter syntactic call graph + skills, one static binary | Zero-setup, ~200 langs, offline, cross-agent, single artifact | Syntactic edges only ("likely, not proven") |
| **Serena MCP** | LSP-backed symbol intelligence as an MCP server | Semantic precision (go-to-def/refs/rename) across 40+ langs | Needs a language server installed per language |
| **Sourcegraph / SCIP** | Precise, persisted index (Protobuf), cross-repo nav | Type-checked, cross-repository, standardized format | Per-language indexers; infrastructure |
| **Aider repo map** | tree-sitter defs/refs → graph → **PageRank** → token-budgeted map | Surfaces the *most important* code for LLM context | (none notable; in-process) |
| **DeepWiki (Devin)** | Auto-generated wiki: architecture, module summaries, dependency & sequence diagrams, chat with line-level citations; ships an MCP server | Rich precomputed artifacts + Q&A | Hosted; LLM pipeline |
| **GitHub stack-graphs** | tree-sitter + graph-construction DSL, **file-incremental** name resolution, no build/CI/config | Precise resolution without a build step | Rust crate (`stack-graphs`); per-language rulesets |
| **Greptile / Bloop** | Embeddings + RAG over per-function chunks; natural-language search & chat | "Find the code that does X"; plain-English answers | Embedding model / vector DB |

**onboard's differentiator is deployment, not depth:** one binary, no language servers, no
indexer infra, no embedding model, works offline and in CI. The roadmap below keeps that
edge while closing the most valuable capability gaps — first with pure-Go additions that
reuse the graph we already build, then with *optional* precision/semantic layers.

---

## Tier 1 — High value, pure-Go, reuses the existing graph

### 1.1 Symbol-importance ranking + token-budgeted repo map (Aider-style PageRank) — ✅ shipped

> Implemented as the `repo_map` tool (`internal/server/tools_repomap.go`) backed by
> `Graph.PageRank` (`internal/providers/provider.go`). Deterministic PageRank over the
> call graph, personalized by `focus`, rendered to a token-budgeted outline. See
> [code-graph.md](code-graph.md#repo_map--ranking-by-centrality-pagerank).

**Gap.** `render_map` ranks *directories* by raw call-degree (`internal/server/tools_map.go`),
and `FindSymbols` matches by name only. Nothing surfaces "the most important symbols," and
there's no compact, token-budgeted overview an agent can load as orientation. Aider's
proven result: PageRank-ranked context beats naive file inclusion on edit accuracy, fit
into a `--map-tokens` budget (default 1k).

**Proposal.** Add a `repo_map` tool (or a `rank` mode to `render_map`) that runs PageRank
(or weighted in-degree as a v0) over the existing `Forward`/`Reverse` adjacency to rank
symbols/files by centrality, then emits a **token-budgeted** outline of the top entities
with signatures and locations. Support *personalization* — bias the ranking toward a
supplied set of focus files/symbols (Aider seeds PageRank toward files in the chat).

**Why it fits.** The graph already exists; PageRank is a few dozen lines of pure Go. This
is the single highest value-to-effort addition.

**Effort:** S. **Risk:** low.

### 1.2 Persistent, incremental graph index — ✅ shipped

> Implemented by splitting `Builtin.Index` into per-file tagging (`tagFile`) and global
> resolution (`assembleGraph`), plus `Builtin.IndexWithCache` backed by a content-hashed
> on-disk cache (`internal/providers/cache.go`) at `<git-common-dir>/onboard-graph.json`.
> Unchanged files reuse cached tags; the full reference set is re-resolved each run.
> Warm index of this repo: ~80 ms → ~3 ms. See
> [code-graph.md](code-graph.md#persistent-incremental-index).

**Shipped baseline.** The graph now has both a content-hashed on-disk file cache and a
bounded in-memory graph cache (32 entries / 30 minutes). `refresh` still intentionally
re-indexes the requested graph.

**Remaining proposal.** Make `refresh` itself file-incremental by driving the changed set
from `git diff` when available, falling back to mtime/hash. This would avoid repeated
whole-repo work on very large repos while keeping the pure-Go engine.

**Why it fits.** Pure-Go, reuses `internal/git` and the existing guide-cache storage
convention. Big win for large repos and repeated sessions.

**Effort:** M. **Risk:** medium (cache invalidation correctness).

### 1.3 Git-history signals: churn, recency, ownership, hotspots — ✅ shipped

> Implemented as `git.History` (`internal/git/git.go`) and the `history` MCP tool
> (`internal/server/tools_history.go`): per-file churn, additions/deletions, last-changed
> date, and distinct author count, ranked by commit count. **Now fused into the 1.1
> ranking** — `repo_map` blends churn into its PageRank score via a `churn_weight`
> (default 0.3; see `blendScore` in `internal/server/tools_repomap.go`) — **and into recon**,
> which surfaces the top-churn files as `hotspots`. Both degrade silently outside a git repo.

**Gap.** `internal/git` only does SHA/branch/diff. There's no notion of *which code changes
often, who owns it, or what's a hotspot* — signals DeepWiki and CodeScene-style tools use to
prioritize. The `onboard-test-gap-and-risk-auditor` skill would be far sharper with them.

**Proposal.** Extend `internal/git` (pure-Go, via the `git` CLI) with churn
(`git log --numstat`), recency, and ownership (`git shortlog`/blame summaries), exposed as a
`history` tool and folded into recon. Feed these into the 1.1 ranking (a high-churn,
high-fan-in symbol is a prime onboarding target) and the risk auditor.

**Why it fits.** Pure-Go, degrades gracefully without git, and directly strengthens an
existing skill.

**Effort:** M. **Risk:** low.

---

## Tier 2 — High value, optional dependencies (capability-gated)

These attack the **syntactic accuracy ceiling** without compromising the default static
binary. Each is a new `Provider` selected at runtime only when its prerequisite is present;
otherwise the engine falls back exactly as it does to `Null` today.

### 2.1 Pluggable precision providers — 🟡 Go layer shipped; SCIP deferred (upstream-blocked), LSP deferred (ethos)

> **Go precision layer shipped** as `EnrichGo` (`internal/providers/goprecision.go`), opt-in
> via `precise: true` on `trace_flow`/`impact`/`repo_map`/`context_pack`. It loads the module
> with `go/packages`, builds SSA, runs **VTA over CHA**, and merges type-checked edges
> (resolving interface dispatch) back into the graph by `(file, line, name)`, marking them
> proven so the honesty note upgrades. Capability-gated on the `go` toolchain + a module,
> panic-safe, 90s-bounded; the pure-Go syntactic graph stays the floor. Dogfooded on onboard:
> 24 dispatch edges newly resolved in ~0.65s. **SCIP ingestion and the LSP provider remain.**
> See [code-graph.md](code-graph.md#the-go-precision-layer-opt-in-type-checked).

**Gap.** Edges are syntactic; ambiguous names are (correctly) left unresolved, so real edges
are missing. Serena gets precision from LSP; Sourcegraph from SCIP; the design notes already
reserve a Go-only `x/tools/go/callgraph` layer.

**Proposal.** Generalize `providers.Provider` into a *chain*:
1. **Go precision layer** — ✅ *shipped* (see above): when a Go toolchain is present, enrich
   Go graphs with `golang.org/x/tools/go/callgraph` (type-checked, interface dispatch), gated
   behind a capability check because `go/packages` shells out to `go`.
2. **SCIP ingestion** — ⛔ *deferred (upstream-blocked)*: if a `*.scip` index exists (produced
   by `scip-go`, `scip-typescript`, `scip-clang`, rust-analyzer, …), consume it for
   type-checked, cross-file precision; optionally *emit* SCIP for Sourcegraph interop. Blocked
   as of this writing: the canonical bindings module
   `github.com/sourcegraph/scip/bindings/go/scip` misdeclares its own module path in go.mod
   (it reads `github.com/scip-code/scip/...`), so Go cannot resolve it, and the migrated
   `scip-code` org is an unverified source. Revisit once upstream fixes the path; the
   `EnrichGo` reconciliation pattern (positions → QNames, merge proven edges) is the template.
3. **Optional LSP provider (Serena-style)** — ⏸️ *deferred (ethos tension)*: when a language
   server is on `PATH`, use it for precise resolution. Deliberately not built: it requires an
   *installed language server per language* — the exact dependency profile onboard's
   positioning differentiates against — plus a sizable, fragile subsystem (subprocess
   lifecycle, JSON-RPC, initialize handshake, document sync, `callHierarchy`). The Go precision
   layer already captures the bulk of the precision win for Go with none of that cost. Build
   only behind a strong, explicit opt-in.

The default remains the pure-Go syntactic graph; precision is a strict upgrade when
available. Surface which provider answered (the output already carries `provider`).

**Why it fits.** Preserves the static binary as the floor; the honesty model already
distinguishes "likely" from "proven," so "proven when a precise backend is present" is a
natural extension.

**Effort:** L (per backend). **Risk:** medium; keep each strictly optional.

> A note on **stack graphs**: it's the ideal model (file-incremental, no build, tree-sitter
> native) but ships as a **Rust crate**, so embedding it would break the pure-Go binary. The
> pragmatic path to the same benefit is SCIP ingestion (2.1.2) plus a Go reimplementation of
> per-language scope rules where it pays off — not a Rust dependency.

### 2.2 Structured artifact extraction (diagrams as *facts*, not LLM guesses) — ✅ shipped

> Shipped as four extractors: `deps` (`tools_deps.go`, manifests → dependency graph),
> `schema` (`tools_schema.go`, SQL DDL → entities + `erDiagram`), `routes` (`tools_routes.go`,
> framework patterns → API surface), and a `sequenceDiagram` mode on `trace_flow`
> (`format="mermaid"`). All pure-Go; the first three read facts (no syntactic caveat), `routes`
> is a recall-oriented heuristic that says so. See
> [code-graph.md](code-graph.md#structured-extractors) and [mcp-tools.md](mcp-tools.md#structured-extractors).
> Follow-ups: ORM-model schema extraction (GORM/Prisma/SQLAlchemy), TOML deps via a real parser.

**Gap.** `onboard-architecture-cartographer` asks the LLM to draw ERDs and sequence diagrams without
structured input. DeepWiki extracts dependency graphs and sequence diagrams directly. onboard
can derive several of these deterministically.

**Proposal.** New extractors feeding `render_map`/the cartographer:
- **External-dependency graph** from manifests (`go.mod`, `package.json`, `Cargo.toml`, …) —
  pure-Go, no graph needed.
- **DB schema → ERD** from migrations / ORM models (e.g. SQL DDL, GORM/Prisma/SQLAlchemy
  models) → Mermaid `erDiagram`.
- **HTTP route / endpoint extraction** (router-registration patterns per framework) → an API
  surface map.
- **Sequence diagrams** derived from a `trace_flow` path → Mermaid `sequenceDiagram`.

**Why it fits.** Most are pure-Go pattern extraction; they turn the cartographer's output
from "LLM's best guess" into "grounded in the actual files."

**Effort:** M–L (incremental per extractor). **Risk:** low-medium.

### 2.3 Context-pack retrieval tool (RAG without embeddings) — ✅ shipped

> Implemented as the `context_pack` tool (`internal/server/tools_context.go`). Given a seed
> symbol or file it BFS-walks the call neighborhood (callers *and* callees) for proximity,
> scores `packDecay^distance · (1 + centralityW·centralityNorm + churnW·churnNorm)` (reusing
> PageRank and the shared `fileChurn` from 1.1/1.3), and emits a token-budgeted bundle of
> source snippets. Snippets are heuristic windows bounded by the next definition (no
> re-parsing). See [code-graph.md](code-graph.md#context_pack--retrieval-without-embeddings)
> and [mcp-tools.md](mcp-tools.md#context_pack).

**Gap.** onboard supplies facts but has no "given this question/symbol, hand me the relevant
code" retrieval — the core of DeepWiki/Bloop Q&A. Embeddings are the usual route but need a
model.

**Proposal.** A `context_pack` tool that, given a symbol or a set of seed files, returns a
ranked, token-budgeted bundle of the most relevant definitions/snippets using the **graph +
recon + history** (centrality, call-distance from the seed, churn) — retrieval-augmented
context without any embedding model. This is the offline, pure-Go 80% of semantic Q&A.

**Why it fits.** Pure-Go, offline, reuses Tier-1 ranking. A natural building block agents can
compose into Q&A.

**Effort:** M. **Risk:** low.

---

## Tier 3 — Bigger bets / ethos tension

### 3.1 Semantic (embedding) code search — opt-in only

**Gap.** "Find the code that *does* X" (Greptile/Bloop) is the biggest *functional* gap.
**Tension.** Embeddings require a model and (usually) a vector store — breaking the
offline/zero-dependency guarantee.
**Proposal.** Offer it strictly opt-in: a pluggable embedding provider (local model, or an
external API behind an explicit flag + key), persisting vectors alongside the graph index.
Greptile's lesson — translate code → natural language before embedding, and chunk
per-function — should guide the implementation. **Near-term pure alternative:** a structural
AST-search tool (ast-grep-style tree-sitter queries) that covers many "find code shaped like
X" needs with no model. Ship the structural search first.

**Effort:** L. **Risk:** high (ethos); must be clearly optional.

### 3.2 Multi-root / monorepo / workspace support

**Gap.** Indexing and the guide cache are single-root. Monorepos and multi-repo orgs are
common; SCIP's headline feature is cross-repo navigation.
**Proposal.** Allow multiple roots / a workspace manifest; index per-root and resolve edges
across roots where names match. Keep per-root caching.
**Effort:** M. **Risk:** medium.

### 3.3 Hosted-mode hardening

**Shipped baseline.** `onboard serve --http` has optional bearer-token auth
(`--http-token` / `ONBOARD_HTTP_TOKEN`), read/write/idle timeouts, a request body cap,
graceful shutdown, structured `slog` request logging, and basic `/metrics` counters.
**Remaining proposal.** Add hosted-mode deployment examples. Keep stdio/localhost
zero-config.
**Effort:** S–M. **Risk:** low.

### 3.4 Guide as a publishable artifact

**Gap.** The guide is a single in-`.git` Markdown file. DeepWiki produces a navigable site.
**Proposal.** Optional export of the guide (+ derived maps/diagrams) to a `docs/` static site
or a synced `CLAUDE.md`/`AGENTS.md`, and support multiple/sectioned guides.
**Effort:** M. **Risk:** low.

### 3.5 Deeper recon

**Gap.** recon is filename-only. **Proposal.** Detect monorepo tooling (nx, turbo, pnpm/yarn
workspaces, Bazel), IaC (Terraform, Kubernetes, Helm), more CI providers, and resolve lockfile
→ dependency versions. **Effort:** S. **Risk:** low.

---

## Suggested sequencing

1. **1.1 PageRank repo map** — biggest value/effort ratio; unlocks orientation + better
   `render_map` + powers 2.3.
2. **1.2 persistent incremental index** — makes everything fast on large/repeat repos.
3. **1.3 git-history signals** — sharpens the risk auditor and ranking.
4. **2.1 Go precision layer + SCIP ingestion** — start closing the accuracy ceiling, gated.
5. **2.2 structured extractors** + **2.3 context-pack** — richer, grounded artifacts and
   offline Q&A building blocks.
6. Tier 3 as demand dictates, semantic search last and strictly opt-in.

## Guardrails (do not regress)

- The **default build stays a single static CGo-free binary** with no runtime services. Every
  precision/semantic backend is optional and capability-gated, with the pure-Go path as the
  floor.
- Keep **honesty**: when a precise backend answers, say so; when falling back to syntactic or
  `Null`, keep saying "likely, not proven."
- Keep **graceful degradation**: no new feature may turn a missing dependency (git, a toolchain,
  a model) into a hard error where a `Note` + reduced result would do.

## Sources

- [Introducing stack graphs — GitHub Blog](https://github.blog/open-source/introducing-stack-graphs/)
- [Building a better repository map with tree-sitter — Aider](https://aider.chat/2023/10/22/repomap.html) · [Repository map docs](https://aider.chat/docs/repomap.html)
- [DeepWiki — Devin Docs](https://docs.devin.ai/work-with-devin/deepwiki)
- [Serena MCP — GitHub](https://github.com/oraios/serena)
- [SCIP — a better code indexing format than LSIF — Sourcegraph](https://sourcegraph.com/blog/announcing-scip) · [SCIP repo](https://github.com/sourcegraph/scip)
- [Codebases are uniquely hard to search semantically — Greptile](https://www.greptile.com/blog/semantic-codebase-search)
- [Bloop AI code search — deep dive](https://www.blog.brightcoding.dev/2025/09/29/ai-powered-code-search-and-chat-for-your-codebase-a-deep-dive-into-bloop/)
- [tree-sitter-graph — tree-sitter](https://github.com/tree-sitter/tree-sitter-graph/)
