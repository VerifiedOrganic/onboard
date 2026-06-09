# Delta-update reference: change → section mapping

## Table of contents
- [The guide section template](#the-guide-section-template) — the sections you are patching
- [Change → section mapping](#change--section-mapping) — which edits touch which sections
- [Choosing the minimal analysis](#choosing-the-minimal-analysis) — when to call recon / trace_flow / impact
- [Deletions and renames](#deletions-and-renames) — pruning the half of maintenance that rots
- [Large-delta triage](#large-delta-triage) — when the change set is big
- [Syntactic-edge honesty](#syntactic-edge-honesty) — how to phrase re-traced flows
- [CLAUDE.md sync procedure](#claudemd-sync-procedure) — optional, preserve-existing

## The guide section template

The cached guide follows this structure. You are editing an existing instance of it in place — match its headings and tone exactly; do not introduce new top-level sections during a delta.

```markdown
# Codebase Guide: [Project Name]

## Overview
[2–3 sentences: what this does and for whom]

## Tech Stack
[table: layer | technology | version — detected, not assumed]

## Architecture
[pattern + a Mermaid or ASCII sketch of how components connect]

## Key Entry Points
[path → what enters here]

## Directory Map
[top-level dir → purpose; skip the obvious]

## Behavioral Map (from tests)
[what the system claims to do, by domain]
### Untested / uncovered behavior
[the gaps — explicitly listed]

## Request Lifecycle
[the 3 core end-to-end traces, file-level]

## Conventions
[naming, error handling, async, state — what to follow when editing]

## Risks & Negative Space
[unhandled errors, silent assumptions, fragile integration seams,
 architectural choices that look locally fine but don't cohere]

## Where to Look
[table: "I want to..." | "look at..."]

## Common Tasks
[detected dev/test/build/lint/migrate commands]
```

The `<!-- walkthrough-cache ... -->` header at the very top is owned by `guide_write` and is NOT part of the body you pass back. If the body you read still carried it, strip it from your working copy and let `guide_write` re-stamp it.

## Change → section mapping

| Changed file / signal | Sections to revisit |
|---|---|
| New/removed top-level directory | Architecture, Directory Map, Overview |
| New/changed manifest (`package.json`, `go.mod`, `pyproject.toml`, `Cargo.toml`, etc.) | Tech Stack, Common Tasks |
| New/changed framework config (`next.config.*`, `vite.config.*`, Django settings, etc.) | Tech Stack, Architecture |
| New/removed entry point (`main.*`, `index.*`, `server.*`, `cmd/`) | Key Entry Points, Architecture, Request Lifecycle |
| New/removed/changed route or handler | Key Entry Points, Request Lifecycle |
| Edited business logic on a flow the guide traces | Request Lifecycle, Conventions |
| Schema / migration / data-model / API-contract change | Architecture, Request Lifecycle, Risks & Negative Space |
| Test files added/removed/changed | Behavioral Map (and its Untested/uncovered list) |
| New error handling or a new integration seam between components | Risks & Negative Space, Conventions |
| New/changed dev/build/test/lint/migrate command | Common Tasks |
| File renamed or moved | Directory Map, Key Entry Points, Where to Look, plus any inline path references |
| File deleted | Every section that referenced it (prune — see below) |
| Terraform/Terragrunt: new/removed `terragrunt.hcl`, or `environments/**` tree change | Key Entry Points (stacks ARE the entry points), Architecture, Directory Map |
| Terraform/Terragrunt: module `variables.tf`/`outputs.tf` contract change, or `versions.tf`/lock-file change | Architecture, Request Lifecycle (the apply/value traces), Tech Stack |

If you cannot map a change to any section, that change does not move the guide. Say so rather than inventing an edit.

## Choosing the minimal analysis

Run the smallest set of `onboard:` analyses the delta justifies. Scope comes from *what you trace or impact*, not from re-scanning the repo:

- **`onboard:recon(root)`** — only when structure shifted: a new or removed top-level dir, a new/changed manifest or framework config, or a new entry point. It refreshes stack / directory map / entry points / tooling. It is a phase-1 structural scan that reads no source beyond manifests, so it is cheap — but it is keyed to the repo root and is whole-repo by design; do not expect to aim it at a subtree. A logic-only edit inside an existing file does NOT need recon.
- **`onboard:trace_flow(entry, depth?, root?)`** — when a traced flow's functions were edited, or a new handler/route/entry appeared. Re-walk that ONE flow from its entry symbol to refresh the matching Request Lifecycle trace. The entry you pass *is* the scope; do not re-trace flows the delta did not touch.
- **`onboard:impact(symbol, root?)`** — when a widely-used symbol or a schema/contract changed and you need to know whether the blast radius reaches what Risks or Request Lifecycle describe. It returns direct/transitive callers and at-risk tests. Use it to decide whether a downstream section needs a note — not to author a standalone impact report (that is the `onboard-dependency-impact-analyzer` skill's job).

When only test files changed, you typically revisit only the Behavioral Map. When only docs/comments changed, the guide may not move at all.

## Deletions and renames

This is the half of maintenance that silently rots a guide. Be as diligent pruning as adding.

- **Deleted file (`D`)**: search the guide body for the path and the symbols it defined. Remove every pointer in Where to Look, Key Entry Points, and Directory Map. If the Behavioral Map claimed a behavior that lived only in that file, remove the claim. If a Request Lifecycle trace passed through it, re-trace the surviving path or annotate the break.
- **Renamed file (`R...`, shown as `old new`)**: repoint every reference from the old path to the new one. Treat the content as possibly modified too — read the new file and apply normal modify rules.
- **Removed entry point**: drop it from Key Entry Points and from any Request Lifecycle trace that started there.

A guide that points at files that no longer exist is worse than one missing a section. When in doubt, remove the stale pointer.

## Large-delta triage

If the change set is large (roughly >40 files), do not read everything blindly:

1. Group `changed` by directory / subsystem.
2. Prioritize high-signal groups: entry points, routing, core domain logic, schema/migrations, public API surface.
3. Deprioritize low-signal churn: lockfiles, generated code, fixtures, vendored deps, formatting-only sweeps. These rarely move a guide section.
4. If a single subsystem was rewritten wholesale, consider telling the user that a full regenerate of that section (via the `onboard-codebase-walkthrough` skill) may be cleaner than a patch — but still keep the rest of the guide intact.

## Syntactic-edge honesty

`trace_flow` and `impact` edges are matched by symbol name and lexical scope — they are NOT type-checked. When you rewrite a Request Lifecycle trace or a Risks note from these results:

- Phrase callee/caller relationships as "likely" or "appears to."
- Name the ceiling explicitly where it bites: dynamic dispatch, reflection, interface / duck-typed / virtual dispatch, callbacks/handlers wired at runtime, and same-named symbols in different packages.
- Never imply the re-traced flow is proven. The guide should read as a sharp, honest map — not a false guarantee.

## CLAUDE.md sync procedure

Optional, and only offer it when the delta actually changed conventions, commands, stack, or structure.

1. Read the existing root `CLAUDE.md` if present.
2. **Preserve all existing instructions.** Edit only the lines the delta affects (e.g. a new test command, a changed lint tool, a moved package). Do not delete or rewrite user-authored rules.
3. Mark what changed (a short note or comment) so the user can review the diff.
4. Keep it under ~100 lines; it is a primer, not a duplicate of the guide.
5. If no `CLAUDE.md` exists, create one only if the user asks. Do not force one on every update.
