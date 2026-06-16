# Trust and Security

`onboard` is a local code-reading tool. It indexes repositories, writes native skill files
for supported agents, and can run as an MCP server. This page is the trust boundary.

## Default posture

- **Local by default.** Agent installs launch `onboard serve` over stdio. No network listener
  is opened unless you pass `--http`.
- **No telemetry.** The binary does not phone home or send repository contents to a hosted
  onboard service.
- **No model calls.** The MCP tools return facts to the calling agent. Any model/network
  traffic comes from that agent or MCP client, not from onboard's analysis engine.
- **Syntactic analysis first.** The code graph is built locally with embedded tree-sitter
  grammars. Optional Go/Rust precise mode may invoke local toolchains (`go` or
  `rust-analyzer`) if present.

## What It Reads

- Source files under the requested repo root, excluding dependency/build directories.
- Git metadata for repository status, history, diffs, merge bases, and cache placement.
- Agent config files during `install`, `init`, `uninstall`, and `doctor`.
- Embedded skill assets compiled into the binary.

## What It Writes

- Agent skill directories during `install` / `init`, and removes embedded onboard skill
  directories during `uninstall`.
- Agent MCP config entries for the `onboard` server during `install` / `init`, and removes
  only that entry during `uninstall`.
- JSON backups named `<config>.onboard-bak` when a config is unparseable.
- A graph cache and guide cache inside the repo's common `.git` directory when available.
- User-requested output files for tools that explicitly accept an `output_path`, such as
  `render_map`; these writes are constrained to the resolved repo root.

The installer merges known `onboard` fields instead of re-marshaling whole configs. A rerun
is idempotent when current and returns `refreshed` only when the existing onboard entry
points at a stale binary path or stale owned fields.

## HTTP Mode

Use stdio for interactive agents. Use HTTP only for a local harness or a deliberately
managed deployment:

```sh
onboard serve --http 127.0.0.1:8080
onboard serve --http 127.0.0.1:8080 --http-token "$ONBOARD_HTTP_TOKEN"
```

Do not bind a shared host directly to the internet. The MCP endpoint can read source code
and perform explicit tool writes, so hosted/shared deployments should provide their own
authentication, TLS, network allowlisting, process isolation, and request/session limits.
The built-in HTTP mode has read-header/read/write/idle timeouts, graceful shutdown, a
configurable request body cap (`--http-max-body-mb`, default 10), optional bearer-token
auth via `--http-token` or `ONBOARD_HTTP_TOKEN`, structured request logs to stderr, and
basic counters at `/metrics` (guarded by the same bearer token when configured).

## Caches

Guide and graph caches are stored under the repo's common git directory, so they are not
committed. Outside a git repo, persistent graph caching is skipped rather than writing
project-local metadata. The in-memory graph cache is bounded by entry count and idle age.

## Reporting

Please report security issues privately. Include the onboard version, platform, command,
agent/client involved, and a minimal reproduction when possible.
