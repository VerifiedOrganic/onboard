---
name: onboard-codebase-walkthrough
description: Walk a developer top-down through an entire codebase so they genuinely understand it — architecture, data flow, end-to-end request traces, and the risky negative space. Use whenever someone says "onboard me", "walk me through this codebase/repo", "help me understand this code", "explain how this works end to end", or "I built this with AI and now need to understand it". Produces a conversational walkthrough, a FIRST-TIME durable cached guide, or an interactive clickable HTML map (the user picks). Especially valuable for AI-generated code the user did not write by hand. For other intents route instead to onboard-architecture-cartographer (static committable Mermaid diagrams), onboard-guide-maintainer (updating/refreshing an existing cached guide as code changes), onboard-dependency-impact-analyzer (what breaks if I change X), onboard-test-gap-and-risk-auditor (a standing risk and coverage register), and onboard-infra-walkthrough (infrastructure-as-code repos — Terraform, Terragrunt, OpenTofu).
---

# Codebase Walkthrough

Take a developer top-down through a codebase until they can reason about it without help. The bar for "done" is that the user could redraw the architecture from memory, predict what breaks when they change something, and trace a flow end to end on a whiteboard.

This skill is built for a specific modern pain: code (often AI-generated, via harnesses and TDD loops) arrives faster than any single mental model can form. The walkthrough is both a *learning* tool and a *verification* tool — it surfaces where an autonomous build drifted, duplicated logic, or wired things together in ways nobody intended.

## Step 0 — Pick the output mode

Ask the user which output they want (unless they already said). The three modes share the same analysis engine but differ in delivery:

| Mode | Best for | Reference |
|------|----------|-----------|
| **Conversational** | Sitting down to learn, phase by phase, with pauses to absorb | `references/conversational.md` |
| **Cached guide** | A durable artifact that survives sessions and delta-updates as code changes | `references/cached-guide.md` |
| **Interactive map** | A clickable visual mental model (Mermaid diagram + detail panels) | `references/interactive-map.md` |

Default to **conversational** if the user is clearly in "teach me" mode and hasn't asked for a file. Read the matching reference file once a mode is chosen — it contains the output-specific instructions.

## Step 1 — Check for a structural backend (do this first, silently)

Before exploring by hand, check whether a code-graph MCP server is available. A real call graph beats inferring architecture from text — this matters most for AI-built code where you cannot trust that the structure is clean.

- **If you are running inside the `onboard` MCP server, its own tools are the backend** — prefer them: `onboard:recon` (Phase 1 structural scan), `onboard:repo_map` (Phase 1 ranked orientation — the most important symbols first), `onboard:deps` (Phase 1/3 external-dependency surface), `onboard:trace_flow` (Phase 4 end-to-end traces), `onboard:context_pack` (Phase 4 — pull the code most relevant to a symbol in one shot), `onboard:impact` (Phase 5 blast radius), `onboard:history` (Phase 5 churn hotspots), `onboard:render_map` (the interactive-map output mode), and `onboard:guide_read`/`onboard:guide_write`/`onboard:guide_delta` (the cached-guide output mode). `onboard`'s call edges are syntactic (name + lexical scope), so treat them as *likely*, not proven — for Go, pass `precise=true` to upgrade them to type-checked.
- Otherwise, look for tools whose names suggest call-graph / AST / code-graph capabilities (e.g. `find_callers`, `trace_call_graph`, `impact_analysis`, `semantic_code_search`).
- If present, use them for Phases 1, 4, and 5 below — they give exact caller/callee edges and impact analysis instead of guesses.
- If absent, fall back to glob/grep/read exploration. The skill works fully without an MCP; the MCP just makes structural claims trustworthy.
- If the codebase is large (>~200 files) and no backend exists, tell the user once that setting one up would sharpen the analysis, and point them to `references/mcp-setup.md`. Don't nag.

## Step 2 — The analysis engine (all modes run this)

Work in phases. **Do not dump code.** Go top-down — map first, code last. In conversational mode, pause between phases.

### Phase 1 — Reconnaissance (cheap, parallel, no full reads)
Gather signals with glob/grep, not by reading every file:
- **Manifests** → `package.json`, `go.mod`, `Cargo.toml`, `pyproject.toml`, `pom.xml`, `Gemfile`, `composer.json`, etc.
- **Framework fingerprints** → `next.config.*`, `vite.config.*`, Django settings, FastAPI/Flask entry, Rails config.
- **Entry points** → `main.*`, `index.*`, `app.*`, `server.*`, `cmd/`.
- **Directory tree** → top 2 levels, ignoring `node_modules`, `vendor`, `dist`, `build`, `.git`, `__pycache__`.
- **Test structure** → `tests/`, `__tests__/`, `*.spec.*`, `*_test.go`, jest/vitest/pytest configs.
- **Tooling** → linters, `Dockerfile`, `docker-compose*`, CI workflows, `.env.example`.
- **Ranked orientation (with onboard)** → call `onboard:repo_map` for a token-budgeted list of the most central symbols (PageRank, blended with git churn) — the heavily-relied-upon, actively-changing core to read first — and `onboard:deps` for the external-dependency surface. This turns "where do I even start" into a ranked answer instead of a directory guess.

### Phase 2 — Behavior from the tests (the spec)
For AI-built / TDD codebases this is the truest description of intent you have. Read the test suite and produce a **behavioral map**: everything the system claims to do, organized by domain. Crucially, **flag every major behavior that has NO test coverage** — that negative space is where understanding and risk both concentrate. If there is no test suite, say so and lean harder on Phase 4.

### Phase 3 — Architecture & conventions
From recon + tests, establish:
- **Tech stack**: languages/versions, frameworks, datastores/ORMs, build tools.
- **Architecture pattern**: monolith / monorepo / microservices / serverless; API style (REST/GraphQL/gRPC/tRPC).
- **Directory → purpose** map (skip the obvious; explain only what carries meaning).
- **Conventions**: naming, error-handling style, async patterns, state management, DI vs direct imports. Trust the *code* over config when they disagree.

### Phase 4 — End-to-end traces (the core of real understanding)
Pick the 3 most important flows (use the MCP call graph if available; otherwise trace by hand). For each, follow it from entry point → validation → business logic → persistence → response, naming concrete files and functions. This is where the user's durable mental model actually forms — one complete trace teaches more than any abstract overview. Watch for: logic written only to satisfy a test, duplicated responsibility, and tangled boundaries between components. With onboard, run `onboard:trace_flow(entry)` to lay the path, then `onboard:context_pack(seed)` on each pivotal symbol to pull its call-neighborhood source (callers + callees, ranked by proximity/centrality/churn) in one shot instead of opening files one by one.

### Phase 5 — The negative space (what to worry about)
Make the hidden risks explicit, because this is what autonomous builds conceal:
- **Churn hotspots (with onboard)** → call `onboard:history` for the files with the most commits and authors. High-churn, central code is where bugs and onboarding effort both concentrate — point the user there first.
- Untested code paths and unhandled error paths.
- Silent assumptions the build baked in that the spec never dictated.
- **Integration seams** — in multi-agent / fast AI builds, bugs cluster at the contracts between components. Surface every integration point and flag where the two sides disagree.
- For each significant architectural choice, justify it as if in review ("why this boundary, why this abstraction, what was the alternative"). Locally-reasonable choices that don't add up to a coherent whole show up here.

### Phase 6 — Check understanding
End by asking the user ~5 questions they should be able to answer if they truly understand the system (trace this flow, what breaks if you change X, where does Y live). Don't volunteer the answers unless they're wrong. This enforces the "explain it without the agent" bar.

## Principles

- **Map before code.** A request for understanding is a request for a mental model, not a code reference.
- **Don't read everything.** Recon is glob/grep; read selectively only to resolve ambiguity.
- **Trust the tests as spec, but verify against the code** — passing tests say nothing about internal quality.
- **Name the negative space.** What *isn't* there (coverage, error handling, justified decisions) is the highest-value thing you surface.
- **Flag unknowns instead of guessing.** "Could not determine the test runner" beats a confident wrong answer.
- **Keep the user honest.** The goal is their independent competence, not dependence on the next walkthrough.

## Reference files
- `references/conversational.md` — phase-by-phase in-chat delivery
- `references/cached-guide.md` — SHA-tagged cached markdown guide + delta-update workflow
- `references/interactive-map.md` — self-contained clickable HTML map (Mermaid + detail panels)
- `references/mcp-setup.md` — installing a code-graph MCP backend for structural analysis
