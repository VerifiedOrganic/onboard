# The skill system

If the tools are *what onboard knows*, the skills are *how it teaches*. A skill is the
playbook an agent follows to walk a developer through a codebase — and onboard ships five of
them baked into the binary.

A *skill* is a directory bundle: a `SKILL.md` (YAML frontmatter + markdown body) plus an
optional `references/` folder of supporting docs. Skills are the *teaching* layer — they
tell an agent **how** to walk a developer through a codebase — while the MCP tools supply
the **facts**. (That split is [Idea 1 in concepts.md](concepts.md); this doc is the
mechanics behind it.)

## Single source of truth

All skills live under `internal/skills/assets/` and are compiled into the binary with
`//go:embed assets` (`internal/skills/skills.go`). The plain `assets` — *not* `all:assets` —
is deliberate: Go's default embed skips names beginning with `.` or `_`, so a stray
`.DS_Store` or editor swap file can never be silently baked into the binary. That one embed
feeds *every* consumer:

- the `list_skills` / `get_skill` MCP tools,
- the `onboard://skills/<name>` MCP resources,
- the `/onboard` and `/onboard-skills` MCP prompts,
- the per-agent installer, which writes the bundle into each agent's skills directory.

So the server and every installed agent always serve identical content. Editing a skill
means editing the asset; rebuilding the binary re-embeds it; `onboard install` propagates
it. (See the cautionary tale in [the frontmatter contract](#the-skillmd-frontmatter-contract).)

## The five skills

Public skill identifiers are namespaced with `onboard-`. Agent skill directories are
global and unscoped, so the prefix avoids collisions with user-installed skills and keeps
the suite grouped in skill pickers. `get_skill` still accepts the old unprefixed names as
compatibility aliases, but `list_skills`, resources, and installed native skills advertise
the `onboard-*` names.

The suite is partitioned so **no two descriptions compete** for the same trigger — each
owns one capability:

| Skill | Owns | Primary tools |
|-------|------|---------------|
| **`onboard-codebase-walkthrough`** | Teaching the codebase phase-by-phase; the first-time durable guide; the interactive clickable-HTML map. | `recon`, `trace_flow`, `render_map`, `guide_*` |
| **`onboard-architecture-cartographer`** | Durable, *committable* diagram-as-code (Mermaid C4 / flowchart / ERD / dependency graph). | `recon`, `impact`, `render_map` (mermaid) |
| **`onboard-guide-maintainer`** | The git-SHA delta loop that keeps an existing cached guide current with HEAD. | `guide_read/write/delta`, `recon` |
| **`onboard-dependency-impact-analyzer`** | The per-change blast radius of one target ("what breaks if I change X"). | `impact`, `trace_flow` |
| **`onboard-test-gap-and-risk-auditor`** | A standing, whole-repo risk register: untested paths, fragile seams, silent assumptions. | `recon`, `trace_flow`, `impact` |

The deliberate split: walkthrough **teaches**; cartographer **draws** durable diagrams
(interactive HTML stays with walkthrough); onboard-guide-maintainer runs the **delta loop**;
auditor produces a **standing whole-repo** register; analyzer computes **per-change**
blast radius. The auditor (whole-repo, standing) and the analyzer (one-target, per-change)
are kept distinct on purpose.

Each skill body stays thin and pushes detail into one-level-deep `references/*.md` files
(e.g. `onboard-codebase-walkthrough/references/{conversational,cached-guide,interactive-map,mcp-setup}.md`).
`get_skill` and the resource handler return the body **plus** all references, concatenated.

## How a bundle is rendered

`internal/skills/skills.go`:

- `List()` enumerates each subdirectory of `assets/` that contains a `SKILL.md`, parsing
  `name` + `description` from the frontmatter.
- `Get(name)` returns one bundle, accepting old unprefixed aliases for compatibility.
- `Catalog()` / `CatalogMarkdown()` return the discoverability catalog used by
  `/onboard-skills` and `onboard skills`.
- `Render()` returns `SKILL.md` followed by every reference file, each under a
  `# reference: <path>` delimiter.
- `Files()` returns the relative-path → content map the installer writes to disk.

## The SKILL.md frontmatter contract

Frontmatter has exactly two fields the loader cares about:

```yaml
---
name: onboard-codebase-walkthrough
description: Walk a developer top-down through an entire codebase …
---
```

- **`name`** — lowercase letters/numbers/hyphens, ≤64 chars, no path separators or `..`;
  shipped skills use the `onboard-` prefix (the installer refuses names that could escape
  the skills dir).
- **`description`** — non-empty, **single line**, ≤1024 chars, written in the third person
  with literal trigger phrases embedded (only name + description are pre-loaded for skill
  selection, so the triggers must live there).

### The parser is intentionally naive — and why that matters

`parseFrontmatter` (`internal/skills/skills.go`) is a deliberately line-based parser:
for the `description:` line it takes the **raw remainder of the line** and trims
whitespace. It avoids a YAML dependency because "the two fields we need are always
single-line."

That naivety has a sharp edge. The description must stay a **plain (unquoted) YAML
scalar**, which means it must not contain:

- `": "` (a colon-space) — a strict YAML parser reads it as a mapping separator;
- a trailing `:`;
- `" #"` — read as a comment.

This is not theoretical. A `": "` inside several descriptions (e.g. `…understand it:
architecture…`) once made Codex's *strict* YAML loader reject four skills with
`mapping values are not allowed in this context`, even though `onboard`'s own lenient
parser and Claude Code loaded them fine. The fix is to **reword** (an em dash reads well)
rather than to quote — quoting would satisfy strict YAML but leak the quote/escape
characters into the description that the naive parser hands to every agent.

`TestDescriptionsAreStrictYAMLSafe` (`internal/skills/skills_test.go`) guards this: it
fails if any embedded description contains `": "`, a trailing `:`, or `" #"`. Other
contract checks (`TestAllEmbeddedSkillsValid`) enforce non-empty, single-line, ≤1024-char
descriptions and that every skill renders.

## Adding a skill

1. Create `internal/skills/assets/onboard-<name>/SKILL.md` with valid frontmatter (mind
   the plain-scalar rule above) and a thin body. Use the same `onboard-<name>` value in
   frontmatter.
2. Add any `references/*.md`.
3. `go test ./internal/skills/...` — the contract tests will catch frontmatter mistakes.
4. Rebuild and `onboard install` to propagate to agents. The new skill automatically
   gains a `get_skill` entry **and** an `onboard://skills/<name>` resource — no server
   code changes needed.
