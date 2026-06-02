# The code-intelligence engine

This is the heart of `onboard`, and the part most worth understanding before you change
anything: a pure-Go engine that turns a repository into a queryable call graph, with the
`recon` structural scan and `render_map` rendering built on top. It's the source of every
*fact* the skills teach from — and the reason onboard can be honest about what it doesn't
know, because this is where "syntactic rumour vs type-checked affidavit" actually lives.

The defining constraint is the [single static CGo-free binary](architecture.md#design-principles).
That rules out the mature CGo tree-sitter bindings, so the engine uses
[`gotreesitter`](https://github.com/odvcencio/gotreesitter) — a pure-Go tree-sitter
reimplementation (MIT, pinned `v0.19.1`) that embeds ~200 grammar blobs and ships an
S-expression query engine, all building under `CGO_ENABLED=0` to every target.

---

## recon

`recon` (`internal/server/tools_recon.go`) is a single `filepath.WalkDir` that reads only
file *names* (and one directory listing for `.github/workflows`). It never opens source
files, which keeps it instant and dependency-free.

Per file it runs five independent matches:

| Signal | How it's detected |
|--------|-------------------|
| **Stack** | filename → ecosystem via a manifest map: `go.mod`→Go, `package.json`→JS/TS (npm), `Cargo.toml`→Rust, `pyproject.toml`/`requirements.txt`/`setup.py`→Python, `pom.xml`→Java (Maven), `build.gradle`→Java/Kotlin (Gradle), `Gemfile`→Ruby, `composer.json`→PHP, `mix.exs`→Elixir, `pubspec.yaml`→Dart/Flutter, `Package.swift`→Swift. |
| **Frameworks** | config-file fingerprints: `next.config.*`→Next.js, `vite.config.*`→Vite, `nuxt.config.*`→Nuxt, `angular.json`→Angular, `svelte.config.*`→Svelte, `manage.py`→Django, `tailwind.config.*`→Tailwind. |
| **Entry points** | basename ∈ {`main`, `index`, `app`, `server`} and the path doesn't contain `test`. |
| **Test layout** | `*_test.go`, `*.spec.*`, `*.test.*`, or `test_*`; records the *directory*. |
| **Tooling** | `Dockerfile`/`docker-compose*`→Docker, `.env.example`, `.eslintrc*`→ESLint, `.golangci.y[a]ml`→golangci-lint, `Makefile`→Make; plus `.github/workflows/`→GitHub Actions. |

Pruned directories: `node_modules`, `vendor`, `dist`, `build`, `__pycache__`, `target`,
`venv`, `coverage`, `bin`, `obj`, and any dotdir except `.github`. The `dir_tree` is a
second walk keeping only paths ≤2 levels deep. All output slices are sorted. If no tests
are found, a `note` warns that the behavioral map will be thin.

When the root is a git work tree, recon also reports `hotspots`: the top-8 highest-churn
files from `git.History` (bounded to the last 1000 commits), formatted as
`path — N commits, M authors, last YYYY-MM-DD`. It is a quick pointer at where
understanding and risk concentrate — the dedicated `history` tool gives the full,
configurable view. Outside a git repo the field is simply omitted.

---

## The graph model

`internal/providers/provider.go` defines the model:

```go
type Symbol struct {
    QName string // file-relative qualified name, e.g. "internal/x/y.go::Foo"
    Name  string // the BARE identifier (agrees with the type checker's fn.Name())
    Kind  string // function, method, class, ...
    File  string // repo-relative path
    Line  int    // 1-based
    Lang  string
    Recv  string // method receiver type, e.g. "HTMLRenderer"; "" for non-methods
}

type Graph struct {
    Provider   string             // "builtin" or "null"
    Defs       map[string]*Symbol // QName -> symbol
    Forward    map[string][]string // caller QName -> callee QNames  (json:"-")
    Reverse    map[string][]string // callee QName -> caller QNames  (json:"-")
    Files      int
    Langs      []string
    Unresolved int                // references that could not be linked
    Note       string
}
```

`Forward`/`Reverse` are excluded from JSON (`json:"-"`) — only the `Defs` snapshot crosses
the wire. `Callees`/`Callers` return de-duplicated, sorted slices at read time.

## Building the graph (the Builtin provider)

`internal/providers/builtin.go`, `Builtin.Index(ctx, root)`:

1. **Walk + detect language.** `filepath.WalkDir` (same skip list as recon). Each file's
   language is detected by `grammars.DetectLanguage(path)` (exact filename → multi-dot
   suffix → extension). Files with no known grammar are skipped.
2. **Tag.** A tree-sitter *tagger* is built once per language (memoized) using the
   grammar's bundled tags query, or an inferred query for grammars that lack one. Tagging
   a file yields a flat `[]Tag`, each with a `Kind` (`definition.function`,
   `reference.call`, …), a `Name`, and byte ranges. `buildTagger` and the tag call are
   wrapped in `recover()` so one bad grammar can't abort the whole walk.
3. **Extract definitions** (first pass): every `definition.*` tag becomes a `Symbol`. Its
   QName is `file::Name`, made unique on collision by appending `#line`, then `.2`, `.3`
   (`uniqueQName`) — so two same-named defs in one file both survive. The definition's
   **full byte span** is recorded for the next step. For a Go method, the receiver type is
   parsed from the declaration header (`goReceiverType`, e.g. `func (h *HTMLRenderer)` →
   `HTMLRenderer`) and stored in `Recv`; `Symbol.Display()` then qualifies the name as
   `HTMLRenderer.Render` in trace/map output, while `Name` stays bare so it still matches
   the type checker during Go precise enrichment.
4. **Extract references** (second pass): every `reference.*` tag is attributed to its
   **innermost enclosing definition**. Because tree-sitter tags carry no "enclosing def"
   field, the engine computes it: among all definition spans that contain the reference's
   byte range, it picks the smallest. References outside any definition are attributed to
   `file::(top-level)`.
5. **Resolve references → edges.** For each reference `(callerQName, callerFile,
   calleeName)`, resolve at the narrowest scope where the name is unambiguous, widening
   only when a scope yields no single match:
   - **same-file** — if exactly one definition with that name exists in the caller's file,
     link to it;
   - else **same-directory (package)** — if exactly one definition with that name exists in
     the caller's directory, link to it. In Go a top-level name is unique per package, so a
     name that collides across the repo is usually unique within its own package; this tier
     recovers the many method/function calls (`Render`, `Name`, `Run`, …) a name-only global
     check would otherwise drop. The `len == 1` guard means two same-named methods in one
     package still stay unresolved — recovered recall, never a guess;
   - else **global** — if exactly one definition with that name exists across the repo,
     link to it;
   - else **leave unresolved** (counted in `Unresolved`).

   Self-edges are dropped. Resolved edges populate `Forward` and `Reverse`.

The crucial rule, stated in the code (`builtin.go`): *"resolve ONLY when exactly one
candidate exists, so an ambiguous name is left unresolved rather than mis-attributed."*
When two same-named symbols are in scope at the same tier, the reference is dropped, not
guessed. This is covered by `TestBuiltinSameFileNameClashLeftUnresolved`,
`TestBuiltinCrossFileAmbiguityLeftUnresolved`,
`TestBuiltinSamePackageMethodClashStaysUnresolved` (the directory tier must not weaken
precision), and `TestBuiltinSamePackageResolution` (the recall it recovers).

Method and interface-dispatch calls that remain unresolved after this are exactly what the
type-checked Go precision layer resolves; `trace_flow` / `impact` surface a hint pointing at
`precise:true` when a Go graph still has unresolved calls (`goPrecisionHint`).

## Providers and fallback

```go
type Provider interface {
    Name() string
    Index(ctx context.Context, root string) (*Graph, error)
}
```

- **`Builtin`** — the tree-sitter engine above. `Name() == "builtin"`.
- **`Null`** (`internal/providers/null.go`) — a regex-only fallback that extracts
  *definitions only* (per-extension patterns for Go, Python, JS/TS, Rust, Ruby, Java) and
  produces **no call edges**. `Name() == "null"`. Its `Note` says `trace_flow`/`impact`
  are unavailable.

The server's `indexGraph` (`internal/server/graph_index.go`) ties them together:

1. Return the in-memory cached `*Graph` for this root unless `refresh` is set.
2. Otherwise acquire a **per-root mutex** (so concurrent calls for the same repo don't
   double-index, while other repos proceed) and run `Builtin.IndexWithCache` against the
   on-disk per-file cache (see below).
3. **If `Builtin` matched zero files**, fall back to `Null` so callers still get a
   definition list. The graph reports `provider: "null"` and explains the degradation in
   a `Note`.
4. Cache the result in memory for the server's lifetime.

## Language gating

The full grammar set makes the stripped binary ~32 MB. Two independent knobs trim it:

| Knob | Scope | Effect |
|------|-------|--------|
| `-tags grammar_set_core` | compile time | Only the curated ~106-language "Core100" set is compiled in (~26 MB). |
| `GOTREESITTER_GRAMMAR_SET=go,python,…` | runtime | Only the listed languages are enabled this run. |

A language is indexed only if it passes **both** gates.

## Persistent incremental index

Indexing splits cleanly into two phases (`internal/providers/builtin.go`): **per-file
tagging** (`tagFile` — the expensive tree-sitter parse, isolated to one file) and
**global resolution** (`assembleGraph` — cheap map lookups linking names to defs). The
split is safe because a QName is `file::name`, so it is already globally unique per file
— a file can be tagged in complete isolation and its result cached.

`Builtin.IndexWithCache(ctx, root, cachePath)` exploits this:

- Each file's content is fingerprinted (FNV-64a). If the hash matches the on-disk cache,
  its definitions and references are **reused verbatim** — skipping the parse entirely.
- Changed and new files are re-tagged; deleted files simply drop out (only walked files
  carry forward).
- The **entire reference set is re-resolved every run**, so the graph is always correct
  even when a change elsewhere affects name resolution — resolution is cheap; parsing is
  what we avoid.
- The cache (`internal/providers/cache.go`) is written atomically (temp + rename). A
  missing, unreadable, or stale-`cacheVersion` cache is ignored (full rebuild); write
  failures are swallowed. **The cache is an optimization, never a correctness
  dependency.**

The server stores it at `<git-common-dir>/onboard-graph.json` — inside `.git`, so it is
never committed — and **only inside a git repo**; outside one, persistence is skipped
rather than litter an untracked working tree (`graphCachePath`). Effect: a warm index of
this repo drops from ~80 ms (cold, all files parsed) to ~3 ms (all files reused), with
an identical graph. `Index` (no cache) remains for callers that don't want persistence.

## trace_flow — BFS over callees

`internal/server/tools_graph.go`. After resolving `entry` to a symbol:

- Breadth-first over `Forward` to `depth` (default 4), tracking `visited` (expanded) and
  `seen` (ever-enqueued) sets; cycles are guarded by `visited`.
- Each emitted node carries its `qname`, `file`, `line`, `depth`, and direct `callees`.
- Hard cap of **250 nodes** (`maxTraceNodes`); hitting it sets `truncated`.
- `truncated` is *also* set when a depth-limited node has a callee that will never appear
  elsewhere in the output — but **not** merely because depth-limited nodes have callees
  (a false-positive fixed by `TestTraceFlowTruncationFalsePositiveFixed`).
- Under the `null` provider, the trace shows the entry symbol alone with an explanatory
  `note`.

## impact — reverse reachability

`internal/server/tools_graph.go`. After resolving `symbol`:

- **Direct callers** = `Reverse[symbol]`.
- **Transitive callers** = iterative DFS over `Reverse` from the direct callers, with a
  `seen` set for cycle protection — every symbol that can reach the target.
- **At-risk tests** = the transitive set filtered by `isTestFile` (`*_test.go`,
  `*.test.*`/`*.spec.*`, `test_*`, or a path under `/tests/` or `/__tests__/`).
- Under the `null` provider, `impact` returns early: blast radius can't be computed
  without call edges.
- The output `Note` is **always** present, restating that edges are syntactic and to
  treat the result as a strong lead, not a proof.

## render_map — deriving a package map

`internal/server/tools_map.go`. When no explicit `nodes` are given, `deriveMap`:

1. Maps every definition to its **directory** (`dirOf`).
2. Counts **cross-directory** call edges from `Forward` (same-directory edges are
   ignored), accumulating an edge count and a degree per directory.
3. Ranks directories by degree (desc, alphabetical tiebreak) and keeps the top **12**
   (`maxMapNodes`); more than that sets `truncated`.
4. Emits a node per kept directory (`N file(s), M call connection(s)`) and an edge per
   kept cross-directory pair.

If there are no cross-package call edges, it returns empty and a `note` says so. Output is
either a self-contained interactive **HTML** file (Mermaid 11 + svg-pan-zoom +
click-to-detail, IDs/labels sanitized against SVG injection) or committable **Mermaid**
`flowchart LR` source with a file-path legend. With `output_path`, the file is written and
the content is also returned inline.

## repo_map — ranking by centrality (PageRank)

`internal/server/tools_repomap.go` + `Graph.PageRank` (`internal/providers/pagerank.go`).
The graph tells you *what calls what*; `repo_map` answers *what matters most*. It runs
PageRank over the call graph — edges flow caller→callee, so a symbol called (directly or
transitively) by many important symbols accumulates a high score — and returns a
token-budgeted outline of the top symbols grouped by file. This is the orientation view
an agent should load first, modeled on aider's repo map.

- **Deterministic** — nodes are processed in sorted order, so scores are reproducible.
- **Dangling/teleport handling** — leaf symbols (no outgoing calls) and the random-restart
  mass are redistributed over the teleport vector, conserving total rank at 1.0.
- **Personalization** — `focus` concentrates the teleport distribution on a seed set (an
  exact QName, a file path → all its symbols, or a bare name), biasing the ranking toward
  an area without losing global structure.
- **Churn fusion** (`blendScore`) — in a git work tree the final score is
  `(1 − churn_weight)·centralityNorm + churn_weight·churnNorm` (default `churn_weight` 0.3).
  Centrality is PageRank normalized by its max; churn is the file's commit count from
  `git.History`, **log-normalized** (`log1p`) because commit counts are heavy-tailed. The
  effect: load-bearing code that *also changes often* rises first — the prime onboarding
  target. With no git history (or `churn_weight=0`) every churn term is zero, so the score
  is a constant multiple of pure centrality and the **ordering is unchanged** — which is
  exactly why a non-git repo (and the test fixtures) rank as if the fusion did not exist.
  Each symbol carries its file's `churn` (commit count) in the output.
- **Token budget** — symbols are included in rank order until the budget (`max_tokens`,
  default 1000) is exhausted, estimating ~4 chars per token; `truncated` reports a cut.
- Under the `null` provider there are no edges, so symbols are listed but not ranked, and
  the `note` says so.

---

## context_pack — retrieval without embeddings

`internal/server/tools_context.go`. Where `repo_map` is the *global* orientation view,
`context_pack` is the *local* one: given a seed symbol or file, it returns the bundle of
source you need to understand or change it — the offline, pure-Go answer to semantic
retrieval, with no embedding model or vector store.

- **Seed resolution** — an exact name/QName match wins; otherwise the seed is a
  path/substring, so a file path expands to every symbol defined in that file.
- **Neighborhood** — a breadth-first walk over **both** edge directions (callees = what the
  seed depends on, callers = who depends on it) records each definition's minimum hop
  `distance`. This is the key difference from the one-directional `trace_flow` (callees) and
  `impact` (callers): understanding or editing a symbol needs both sides. Bounded by
  `max_distance` (default 2) and a 200-node cap.
- **Relevance score** — `packDecay^distance · (1 + centralityW·centralityNorm +
  churnW·churnNorm)`: each hop halves relevance (distance dominates), and PageRank centrality
  + git churn break ties among equidistant symbols (so a load-bearing, actively-changing
  neighbor outranks a trivial one). Churn reuses `fileChurn`, shared with `repo_map`.
- **Snippet boundary** — the syntactic graph stores only a definition's *start* line, so the
  end is heuristic: the line before the next definition in the same file, capped at 40 lines.
  Exact for top-level functions; approximate for nested ones (the `note` discloses this). No
  re-parsing and no new cache state — it reuses the def lines already in the graph.
- **Token budget** — snippets are attached in relevance order until `max_tokens` (default
  4000) is spent, always keeping at least the seed; `truncated` reports a cut.
- Under the `null` provider there are no edges, so the pack is the seed's own definitions.

---

## Structured extractors

`tools_deps.go`, `tools_schema.go`, `tools_routes.go`, and the `sequenceDiagram` mode of
`trace_flow`. These sit beside the call graph but are a different epistemic class: they read
**facts** rather than infer edges, so they carry no "likely, not proven" caveat (with one
exception, `routes`, noted below).

- **`deps`** — parses dependency manifests (`go.mod` via `x/mod/modfile`, `package.json`,
  `requirements.txt`, `Cargo.toml`) into direct dependencies per manifest, optionally a
  Mermaid flowchart. A parsed `require` line *is* a dependency.
- **`schema`** — parses SQL DDL (`CREATE TABLE`) by capturing each balanced parenthesized
  table body, splitting it at paren-depth-0 commas (so `DECIMAL(10,2)` survives), and
  classifying each definition as a column or a primary/foreign-key constraint → entities,
  relationships, and a Mermaid `erDiagram`.
- **`routes`** — the one heuristic of the four: HTTP routing has no single grammar, so it
  pattern-matches registration calls across frameworks. It favors recall and scans source
  text (including comments), so it can over-match; the note says so.
- **`trace_flow` `format="mermaid"`** — renders a discovered trace as a `sequenceDiagram`
  (one message per in-trace edge, in breadth-first order — reach, not strict runtime order).

These exist to **ground the architecture-cartographer**: it can now build a dependency graph,
ERD, API map, or sequence diagram from extracted facts instead of asking the model to guess.

---

## Accuracy ceiling (read this)

The engine is deliberately, repeatedly honest about what it cannot know. The call graph is
**syntactic** — built from tree-sitter tags resolved by name and lexical scope, with no
type-checking or data-flow analysis. Concretely:

- **Dynamic dispatch, interfaces, reflection, higher-order/callback calls** produce edges
  the graph cannot see.
- **Same-named symbols** across scopes/modules are left *unresolved* rather than guessed,
  so real edges can be missing (never silently wrong).
- The `null` fallback has **no edges at all** — definitions only.

The code states this in the package doc (`provider.go`), in `Builtin.Index`'s `Note`,
and in `impact`'s always-present `Note`. Present syntactic results as **likely, not
proven** — but see the precision layer below, which upgrades specific edges to *proven*.

### The Go precision layer (opt-in, type-checked)

`internal/providers/goprecision.go`. When a caller passes `precise: true` to an edge tool
(`trace_flow`, `impact`, `repo_map`, `context_pack`) **and** the target is a Go module with
the `go` toolchain on PATH, `EnrichGo` upgrades the graph with **type-checked** edges:

- It loads the module with `golang.org/x/tools/go/packages`, builds SSA, and computes a
  call graph with **VTA over CHA** (`go/callgraph/vta` refined from `cha`) — far less
  interface-dispatch noise than CHA alone.
- Crucially it resolves **interface dispatch**, which the syntactic pass deliberately leaves
  unresolved (a call `s.Area()` where `Area` has several implementations is ambiguous by name
  alone). Those edges are the precision layer's headline win.
- Each precise edge is reconciled back to a syntactic QName by **(file, line, name)** — the
  SSA function and the tree-sitter def agree on the name identifier's position, and the name
  disambiguates the multiple defs the tagger can emit on one line. Edges are then merged into
  `Forward`/`Reverse` (so every traversal benefits transparently) and recorded in
  `Graph.ProvenEdges`, which flips `Graph.Precise` and lets each tool's note upgrade from
  "likely" to "proven" for Go edges.

It is a **strict, optional upgrade and never a hard dependency**: x/tools is pure Go (the
static binary is unaffected), and the runtime requirement — the `go` command plus a buildable
module — is capability-gated. Absent it, `EnrichGo` is a no-op and the graph stays exactly as
the syntactic pass built it. The work is bounded by a 90s context on `packages.Load` and is
panic-safe, so an analysis failure degrades silently to the syntactic graph.

**Known scope (v0):** precise results are computed per request and held only in the in-memory
graph cache (not persisted to the on-disk index), so the first precise query on a cold server
re-runs the analysis. SCIP ingestion and an LSP provider remain on the roadmap
([enhancements.md](enhancements.md) §2.1).
