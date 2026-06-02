# Cached guide mode

Produce a durable markdown guide and cache it, so future sessions start at full speed instead of re-discovering the codebase. Inspired by the `/onboard` caching pattern: scan once, cache tagged with the git HEAD SHA, delta-update later.

## Cache location and tagging
Write the guide to `<git-common-dir>/codebase-walkthrough.md` (resolve with `git rev-parse --git-common-dir`; falls back to `.git/`). This keeps it out of the working tree so it isn't accidentally committed.

Tag the top of the file with a machine-readable header:

```markdown
<!-- walkthrough-cache
sha: <current HEAD sha, from `git rev-parse HEAD`>
branch: <from `git rev-parse --abbrev-ref HEAD`>
generated: <ISO timestamp>
mode: full | delta
-->
```

## Run logic
1. **No cache exists** → full scan. Run Phases 1–5 from SKILL.md, write the guide, stamp the header.
2. **Cache exists, SHA matches HEAD** → load it directly into the conversation. No rescan. Tell the user it's cached and current.
3. **Cache exists, SHA differs** → delta update. Run `git diff --name-status <cached-sha> HEAD`, read only added/modified files, note deletions, update the affected sections, restamp the header with the new SHA and `mode: delta`. Don't rescan unchanged subtrees.

If the repo has no git history, skip caching and just produce the guide inline, noting that delta-updates aren't available.

## Guide structure
Use this template:

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

Keep it scannable in a couple of minutes — depth belongs in the code, not the guide. Don't copy the README; this adds structural insight the README lacks.

## Optional companion: starter CLAUDE.md
If the user also wants future agent sessions to start informed, offer to write/enhance a `CLAUDE.md` at the repo root from the detected conventions (stack, code style, test/build commands, structure). If one exists, read and enhance it — preserve existing instructions and mark what changed. Keep it under ~100 lines.
