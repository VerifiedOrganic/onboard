# Concepts — how to think about onboard

Read this once and the rest of the docs stop looking like a pile of unrelated features.
It's the mental model, not the reference; no field tables, just the five ideas that make
everything else make sense.

## The situation it's built for

Understanding a codebase used to be gated by *typing speed* — someone wrote the code at
human pace, so a human could keep up reading it. That's over. Code now arrives from agents,
TDD loops, and harnesses faster than any one person's mental model can form. You end up
*responsible* for code you never wrote and don't yet understand — and the tests passing
tells you it does *something*, not that it does the right thing, or that the pieces fit
together the way anyone intended.

`onboard` is built for that gap. Everything below is in service of one bar: you should be
able to redraw the architecture from memory, predict what breaks when you change something,
and trace a flow end to end on a whiteboard — *without* the tool.

## Idea 1 — Two halves: skills teach, tools know

This is the core split, and it's worth getting straight early.

- **Skills** are the *playbooks* — how to teach a codebase. They're Markdown, embedded in
  the binary, and they encode the method: go top-down, map before code, trust the tests as a
  spec but verify against the code, and name the negative space. `codebase-walkthrough` is
  the headliner; the others handle diagrams, blast radius, a risk register, and guide upkeep.
- **Tools** are the *facts* — the structural truth a playbook teaches *from*. `recon`,
  `trace_flow`, `impact`, and the rest read your actual code and answer with file names and
  line numbers, not vibes.

A skill without tools is a smart teacher working from guesswork. Tools without a skill are a
pile of facts with no lesson plan. Together: a teacher with a verified source. The
`/onboard` tour is simply a skill *driving* the tools, paced as a wizard.

## Idea 2 — The code graph, and why edges are rumours

The tools that answer "who calls this?" and "where does this flow go?" are backed by a
**code graph**: every function/method/type is a node, every call is an edge. It's built by a
**pure-Go tree-sitter engine** that reads ~200 languages.

Here's the honest part, because onboard refuses to pretend otherwise. The edges are
**syntactic** — resolved by *name and lexical scope*, not by a type checker. If file A calls
`Save()` and exactly one `Save()` is in scope, that's an edge. If three different `Save()`s
are in scope, onboard *doesn't guess* — it leaves the edge unresolved rather than invent a
false one.

So: **treat an edge as a very strong rumour, not a sworn affidavit.** Dynamic dispatch,
reflection, and interface calls can hide edges the syntactic pass can't see.

For **Go**, you can upgrade. Pass `precise: true` and onboard runs a type-checked analysis
(VTA over the SSA call graph) that resolves interface dispatch and marks those edges
**proven**. For **Rust Cargo projects**, the same flag uses `rust-analyzer` call hierarchy
when the binary is available, enriching the graph while preserving the zero-setup
tree-sitter fallback when it is not. These paths are opt-in because they ask language
tooling to understand your project — slower, but useful when an edge is load-bearing for a
decision.

> Why not type-check everything, everywhere? Because that breaks [Idea 5](#idea-5--the-one-rule-that-explains-everything).
> Honest-and-everywhere beats precise-but-only-sometimes for a tool whose job is first
> contact with *any* codebase.

## Idea 3 — Honesty is a feature, not a disclaimer

You'll notice onboard hedges on purpose, and it's not timidity — it's the design. A tool you
consult about code you don't understand is *worse than useless* if it's confidently wrong,
because you have no way to catch it.

So the rules are baked in:

- Edges are labelled **likely** (syntactic) vs **proven/semantic** (precision-backed).
- `dead_code` returns **leads, not verdicts** — and tells you what could be hiding a caller
  (reflection, framework registration, external importers) before you go deleting things.
- "Could not determine the test runner" beats a confident wrong answer, every time.

When a tool tells you what it *can't* see, that's the feature working.

## Idea 4 — The durable guide, and the negative space

Two ideas that round out the picture:

- **The guide cache.** A walkthrough can be written down once as a durable, git-SHA-tagged
  Markdown guide that lives *inside* `.git` (so it's never accidentally committed) and
  **delta-updates** as the code changes — re-reading only the sections fed by changed files
  instead of rescanning the world. Onboarding becomes a thing you do once and *maintain*,
  not repeat. See [guide-cache.md](guide-cache.md).
- **The negative space.** The highest-value thing onboard surfaces is what *isn't* there:
  untested paths, unhandled errors, integration seams where two components quietly disagree,
  and silent assumptions a fast build baked in. In AI-generated code especially, the bugs
  cluster at the contracts between pieces — so onboard goes looking there on purpose.

## Idea 5 — The one rule that explains everything

`onboard` is a **single static, CGo-free, cross-compilable binary.** That's the rule the
whole design bends around:

- It's why the tree-sitter engine is **pure Go** (CGo would break static cross-compilation).
- It's why ~200 grammars are **embedded** — a ~32 MB binary is a fair price for "works on any
  codebase you point it at, offline, no install dance."
- It's why the graph is **syntactic by default** with Go precision as an *opt-in* — universal
  honesty first, deep precision where the toolchain allows.
- It's why the skills are `//go:embed`'d: one source of truth that the MCP server *and* the
  per-agent installer both read, so the teaching content can never fork.

When a choice in onboard looks strange, check it against this rule. It's almost always the
answer.

## The cast, in one place

The skills (the playbooks):

| Skill | What it owns |
|-------|--------------|
| `codebase-walkthrough` | The top-down tour; the first-time cached guide; the interactive HTML map |
| `architecture-cartographer` | Committable diagram-as-code (Mermaid: C4 / flow / ERD / deps) |
| `dependency-impact-analyzer` | The blast radius of one change ("what breaks if I touch X") |
| `test-gap-and-risk-auditor` | A standing, whole-repo risk register |
| `guide-maintainer` | The git-SHA delta loop that keeps a cached guide current |

The tools (the facts) are catalogued in [mcp-tools.md](mcp-tools.md); the engine behind them
is dissected in [code-graph.md](code-graph.md). Ready to use it? [getting-started.md](getting-started.md).
