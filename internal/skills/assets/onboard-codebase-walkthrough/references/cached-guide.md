# Cached Guide Mode

Produce a durable markdown guide through onboard's guide tools, so future sessions start
from the cached understanding instead of rediscovering the codebase.

## Tool Contract

Use the MCP tools when they are available:

- `onboard:guide_read(root?)` — check whether a cached guide exists and whether its SHA
  matches HEAD.
- `onboard:guide_write(root?, body, mode)` — write the full guide body; the tool stamps the
  cache header automatically.
- `onboard:guide_delta(root?)` — for an existing stale guide, list changed files since the
  cached SHA so a maintainer can update only affected sections.

Do not hand-write the `<!-- walkthrough-cache ... -->` header. `guide_write` owns it. The
body you pass starts at the guide title.

## Run Logic

1. Call `onboard:guide_read`.
2. If `exists: true` and `current: true`, load the returned `body` into the conversation
   and tell the user the guide is current. Do not rescan.
3. If no guide exists, run the full walkthrough phases from `SKILL.md`, assemble the guide
   body, and call `onboard:guide_write` with `mode: "full"`.
4. If a guide exists but is stale, prefer handing off to `onboard-guide-maintainer`; that
   skill owns the delta loop. If the user explicitly asked for a fresh guide, regenerate
   and call `onboard:guide_write` with `mode: "full"`.
5. If the repo has no git history, produce the guide inline and explain that SHA-tagged
   delta updates are unavailable.

## Guide Structure

Use this template:

```markdown
# Codebase Guide: [Project Name]

## Overview
[2-3 sentences: what this does and for whom]

## Tech Stack
[table: layer | technology | version — detected, not assumed]

## Architecture
[pattern + a Mermaid or ASCII sketch of how components connect]

## Key Entry Points
[path -> what enters here]

## Directory Map
[top-level dir -> purpose; skip the obvious]

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

Keep it scannable in a couple of minutes. Do not copy the README; this guide should add
structural insight the README lacks.

## Optional Companion

If the user also wants future agent sessions to start informed, offer to write or enhance a
root `CLAUDE.md` from detected conventions, commands, and structure. If one exists, preserve
existing instructions and edit only the parts this walkthrough established.
