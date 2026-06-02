# The durable guide cache

Onboarding the same codebase from scratch every session is a tax nobody should pay twice.
The guide cache is onboard's answer: generate the walkthrough **once**, then reuse it — and
when the code moves on, update only the parts the changes actually touched, rather than
re-reading the world.

It's a single Markdown file tagged with the git commit it describes. The commit SHA is the
entire staleness mechanism: same SHA, the guide is current; different SHA, `guide_delta`
tells you exactly which files moved so only those sections get rewritten. No mtimes, no
content hashes, no cache-invalidation folklore.

## Where it lives

`guide.Path(root)` (`internal/guide/guide.go`):

- **In a git repo:** `<git-common-dir>/codebase-walkthrough.md`. The common dir comes from
  `git rev-parse --git-common-dir`, which for a normal repo is `<root>/.git`. So the guide
  lives at `<root>/.git/codebase-walkthrough.md` — **inside `.git`, so it is never
  committed** (the index never touches `.git/`). Using the *common* dir (not `--git-dir`)
  keeps a single shared guide across linked worktrees.
- **Outside a git repo:** falls back to `<root>/.onboard/codebase-walkthrough.md`.

> The code-graph engine keeps a sibling cache, `<git-common-dir>/onboard-graph.json`, by
> the same convention (inside `.git`, never committed). Unlike the guide, it is written
> only inside a git repo — see [code-graph.md](code-graph.md#persistent-incremental-index).

## On-disk format

A machine-readable HTML-comment header, then the body:

```
<!-- walkthrough-cache
sha: <40-hex HEAD SHA, or empty when not a git repo>
branch: <branch name, or "HEAD" when detached, or empty>
generated: <RFC 3339 UTC timestamp>
mode: <full|delta>
-->

<guide body markdown>
```

The **SHA is the sole staleness signal** — there is no mtime, file count, or content hash.
`parse()` tolerates a missing/garbled header by treating the whole file as body with a
zero header.

## Lifecycle

```
guide_read ──► current? ──yes──► reuse body, done
     │              │
     │              no
     ▼              ▼
 (no guide)   guide_delta ──► changed files since cached SHA
     │              │
     ▼              ▼
 full scan    update only affected sections
     │              │
     ▼              ▼
 guide_write       guide_write
 mode: "full"      mode: "delta"
 (stamps HEAD SHA) (re-stamps HEAD SHA)
```

### `guide_read`
Returns `path`, `exists`, and — when git is available — `current` (cached SHA == HEAD
SHA), `cached_sha`, `head_sha`, `branch`, `mode`, `generated`, and the full `body`. A
missing file is **not** an error: it returns `exists: false`. If it isn't a git repo,
`current` stays false and a `note` explains why.

### `guide_write`
Persists `body` with a freshly stamped header. `mode` defaults to `full`; only `full` or
`delta` are accepted (anything else is rejected before any I/O). The HEAD SHA and branch
are read from git and silently left empty when git is unavailable — in which case the
output `note` warns that the guide isn't SHA-tagged and delta updates won't work. The file
is written `0o644`; its parent dir is created `0o700`.

### `guide_delta`
Read-only. It compares the cached header SHA to HEAD by **string equality** and, when they
differ, runs `git diff --name-status <cachedSHA>..HEAD` to list changed files
(`[]{status, path}`; for renames it reports the new path). It returns early with a `note`
when: not a git repo, no cached guide (or the guide has no SHA), or already current. It
does **not** write anything — the caller follows up with `guide_write mode: "delta"`.

## The git layer

`internal/git/git.go` wraps the `git` CLI via `git -C <root> …` (`run` helper):

| Function | Shells out to | Returns |
|----------|---------------|---------|
| `Available(root)` | `rev-parse --is-inside-work-tree` (after `LookPath("git")`) | bool; never errors |
| `CommonDir(root)` | `rev-parse --git-common-dir` | absolute common dir |
| `HeadSHA(root)` | `rev-parse HEAD` | full 40-char SHA |
| `Branch(root)` | `rev-parse --abbrev-ref HEAD` | branch name or `HEAD` (detached) |
| `DiffNameStatus(root, fromSHA)` | `diff --name-status <fromSHA>..HEAD` | `[]Change{Status, Path}` |
| `History(root, maxCommits)` | `log --no-merges --numstat` | `[]FileStat` (per-file churn/authors; powers the `history` tool) |

## Edge cases

| Situation | Behavior |
|-----------|----------|
| Not a git repo | Guide falls back to `.onboard/`; written without a SHA; `guide_delta` returns a "regenerate in full" note. |
| No prior guide | `guide_read` → `exists:false`; `guide_delta` → "generate a full guide first". |
| Guide written without git (no SHA) | Treated as not-current; `guide_delta` → "generate a full guide first". |
| Dirty working tree | **Not detected.** Delta compares committed SHAs only; uncommitted changes are invisible to the cache. |
| Cached SHA missing from repo (force-push, shallow clone) | `git diff` fails; `guide_delta` returns a hard error — caller should regenerate in full. |
| Empty repo (unborn HEAD) | `HeadSHA` fails → empty SHA written; delta says "generate a full guide first". |
| Invalid `mode` to `guide_write` | Rejected immediately with `unsupported guide mode %q`, before any I/O. |
