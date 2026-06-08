---
name: onboard-guide-maintainer
description: Runs the delta-update maintenance loop that reconciles an already-generated, git-SHA-tagged codebase guide with current HEAD, re-reading and rewriting only the sections fed by changed files instead of rescanning the whole repo via onboard:guide_read, onboard:guide_delta, and onboard:guide_write. Use when the user says "update the guide", "refresh the codebase guide", "sync the guide", "the guide is stale", "regenerate the guide after these changes", "bring the guide up to date", or asks to reconcile a cached guide with new commits. Requires git. Not first-time guide authoring (use onboard-codebase-walkthrough), not committed Mermaid diagrams (onboard-architecture-cartographer), not whole-repo risk auditing (onboard-test-gap-and-risk-auditor), and not one-target blast-radius analysis (onboard-dependency-impact-analyzer).
---

# Guide Maintainer

Keep an existing durable codebase guide current with the code, cheaply, by updating only what changed. This is the **maintenance loop**, not first-time authoring: the guide already exists, the user has shipped commits since it was written, and the job is to reconcile it with HEAD without re-reading the whole repo.

The discipline is a good code review's: touch the minimum. A delta update reads only the files that changed since the cached SHA, re-runs analysis only where those files reach, and rewrites only the guide sections they feed. Everything unchanged is preserved verbatim.

**Hard requirement: this needs git.** The guide is tagged with the HEAD SHA it was generated against, and the delta is a diff between that SHA and current HEAD. With no git history there is no SHA to diff from — say so and hand off to first-time authoring (`onboard-codebase-walkthrough`).

## When this is the right skill

Use it when the user says "update / refresh / sync the guide", "the guide is stale", "regenerate the guide after these changes", "bring the guide up to date", or otherwise asks to reconcile an already-cached guide with new code.

Do NOT use it to:
- **Author a guide from scratch** — that is `onboard-codebase-walkthrough` (cached-guide mode). If `guide_read` reports no guide exists, hand off there.
- **Draw committable Mermaid diagrams** — `onboard-architecture-cartographer`.
- **Audit the whole repo for risk** — `onboard-test-gap-and-risk-auditor`.
- **Compute the blast radius of one change as a standalone report** — `onboard-dependency-impact-analyzer`.

You own `guide_read` / `guide_delta` / `guide_write` and the delta loop between them. Nothing else.

## The loop

### Step 1 — Read the cached guide and check currency
Call `onboard:guide_read`. It reports whether a guide `exists`, its `cached_sha`, the `head_sha`, whether it is `current`, and the `body`. Branch:

- **No guide exists** (`exists: false`) → there is nothing to maintain. Tell the user no guide is cached and offer to generate one via `onboard-codebase-walkthrough`. Stop.
- **Result indicates this is not a git repo / there is no SHA to diff from** → delta is impossible. Tell the user delta updates require git, and offer a full regenerate via `onboard-codebase-walkthrough`. Stop.
- **`current: true`** (cached SHA == HEAD) → the guide already matches the code. **Do not rescan.** Load the `body` into the conversation, tell the user it is current (cite the `cached_sha`), and stop. Reuse beats rework.
- **`current: false`** → the guide is stale relative to HEAD. Keep the returned `body` in hand — it is the document you will patch, not replace — and continue to Step 2.

### Step 2 — Compute the delta
Call `onboard:guide_delta`. It returns `cached_sha`, `head_sha`, `current`, and `changed: [{status, path}]` — the files that moved between the cached SHA and HEAD. Statuses are raw git name-status: `A` added, `M` modified, `D` deleted, `R...` renamed (e.g. `R096`, shown as `old new`).

Read the change list before opening any file. It is your work order: it bounds both which files you read and which guide sections can possibly need editing. If `guide_delta` comes back `current: true` or reports no SHA-tagged guide, reconcile with Step 1 (normally Step 1 already caught these) and stop.

### Step 3 — Read only the changed subtree
Read the **added and modified** files — only those. Do not re-read unchanged files to "refresh context"; the unchanged guide sections are correct by construction. For **deleted** files, do not try to read them (they are gone); record them for Step 5. For **renames**, treat as a path change plus possible content change — read the new path.

If the change set is large (say >40 files), group by directory/subsystem and process the highest-signal groups first (entry points, routing, core domain logic, schema). Churn in lockfiles, generated code, or fixtures rarely moves a guide section. Full triage rules are in `references/section-map.md`.

### Step 4 — Re-run the MINIMAL analysis the changes warrant
Run only the analyses the delta justifies, and choose each call's scope by **what you trace or impact**, not by re-scanning everything:

- **Structure shifted** (new/removed top-level dir, new manifest or framework config, new entry point, moved packages) → `onboard:recon` to refresh stack / directory map / entry points. `recon` is a phase-1 structural scan keyed off the repo root — it does not read source beyond manifests, so it is cheap, but it is whole-repo by design; do not expect to point it at a subtree. A pure logic edit inside an existing file does **not** need recon at all.
- **A traced flow's functions were edited, or a new handler/route appeared** → `onboard:trace_flow` from that one entry symbol to re-walk that single flow. Scope comes from the entry you pass, so trace only the flows the delta actually touched — not every flow in the guide.
- **A widely-used symbol or a schema/contract changed** → `onboard:impact` on that symbol to see whether its blast radius reaches anything the Risks or Request Lifecycle sections describe. Use it to decide whether a downstream section needs a note — not to author a standalone impact report (that is `onboard-dependency-impact-analyzer`'s job).

Skip analyses nothing touched. If only test files changed, you likely only revisit the Behavioral Map. If only a README or comment changed, the guide may not move at all — say so and stop before writing.

**Honesty about the call graph:** `trace_flow` and `impact` edges are SYNTACTIC — matched by symbol name and lexical scope, not type-checked. Present any re-traced flow or recomputed blast radius as "likely" and name the ceiling where it bites: dynamic dispatch, reflection, interface / duck-typed / virtual dispatch, callbacks wired at runtime, and same-named symbols in different packages. Never imply the new trace is proven.

### Step 5 — Patch only the affected sections
Edit the guide body surgically. Preserve every section the change did not touch, byte for byte. Map changes to sections (full table and the section template in `references/section-map.md`):

| What changed | Section(s) to revisit |
|---|---|
| New/removed dir, manifest, framework config | Overview, Tech Stack, Architecture, Directory Map |
| New/removed entry point, route, handler | Key Entry Points, Request Lifecycle |
| Edited business logic on a traced flow | Request Lifecycle, Conventions |
| Schema / data-model / contract change | Architecture, Request Lifecycle, Risks & Negative Space |
| Test files added/removed/changed | Behavioral Map (incl. its untested-behavior list) |
| New error handling, new integration seam | Risks & Negative Space |
| New dev/build/test/lint/migrate command | Common Tasks |

For **deletions and renames**, actively prune: remove references to deleted files/symbols, repoint renamed paths, and delete now-empty bullets. A stale "look at `oldfile.go`" is worse than no pointer. If a deletion removed a behavior the Behavioral Map claimed, remove that claim.

Match the guide's existing headings and tone exactly; do not introduce new top-level sections during a delta. Keep it scannable in a couple of minutes — depth lives in the code, not the guide. Do not balloon a section just because you re-read its files.

### Step 6 — Restamp with `guide_write`, mode `delta`
Call `onboard:guide_write` with the **full updated body** and `mode: "delta"`. Contract details that bite if missed:

- **Do NOT hand-write the `<!-- walkthrough-cache ... -->` header in the body.** `guide_write` stamps it (new HEAD sha, branch, timestamp, mode) automatically. Start the body at the guide title (`# Codebase Guide: ...`). If the `body` you got from `guide_read` still carried that header, strip it before writing.
- Pass the **entire** guide, not just the changed sections — `guide_write` overwrites the file. Your edited-in-place body already contains the untouched sections.
- The restamp moves the cached SHA to current HEAD, so the next `guide_read` reports `current: true` until new commits land.

After writing, give the user a short changelog: which sections moved, why, and which files drove each edit. This keeps them able to sanity-check the update without re-reading the whole guide.

## Optional: sync the root CLAUDE.md
If the delta changed conventions, commands, stack, or structure, offer to reconcile a root `CLAUDE.md` so future agent sessions stay informed. **Preserve existing instructions** — read the current file, edit only the lines the delta affects, mark what changed, and never drop user-authored rules. If no `CLAUDE.md` exists, create one only if the user asks. Procedure in `references/section-map.md`.

## Principles
- **Touch the minimum.** Delta means delta: read changed files, run the analysis the change warrants, patch affected sections, preserve the rest verbatim.
- **Currency check before work.** If `guide_read` says `current`, reuse it and stop. Never rescan a guide that already matches HEAD.
- **Git is mandatory.** No SHA, no delta. Hand stale-but-gitless repos to first-time authoring.
- **Prune as diligently as you add.** Deletions and renames are the half of maintenance that rots a guide if ignored.
- **Stay syntactic-honest.** Re-traced flows and recomputed impact are "likely"; name the dispatch ceiling.
- **Never hand-write the cache header.** `guide_write` owns it; the body starts at the title.

## Reference
- `references/section-map.md` — the guide section template, full change→section mapping, deletion/rename pruning rules, large-delta triage, syntactic-edge phrasing, and the CLAUDE.md sync procedure.