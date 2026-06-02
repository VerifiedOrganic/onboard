# Installation & agent integration

Five agents. Three config formats. One of them (looking at you, opencode) in a genre of
its own. This is the page that explains how `onboard install` keeps all of that straight so
you never have to hand-edit a TOML table at midnight.

> Just want it working? [getting-started.md](getting-started.md) is the happy path. This doc
> is the matrix behind it — read it when something's weird, or when you're wiring up an
> agent the installer doesn't know about.

`onboard` installs itself into a coding agent by doing **two writes** (`internal/agents/agents.go`,
`Install`):

1. **Skill files** — the embedded bundles are written into the agent's native skills
   directory (where one exists).
2. **MCP config** — an `onboard` server entry is registered in the agent's config,
   pointing at the absolute binary path with `args: ["serve"]`.

Both happen for every agent you target. The config registration reports back as
`merged` (new JSON key), `appended` (new TOML table), or `already-present` (idempotent
re-run). To confirm any of this actually landed, `onboard doctor` reads it all back without
touching a thing.

## CLI commands

```
onboard serve                 run the MCP server over stdio (what agents launch)
onboard serve --http :8080    run over Streamable HTTP at /mcp instead
onboard install --agent NAME  install into one agent (claude|codex|grok|opencode|cursor)
onboard install --all         install into every detected agent
onboard init                  convenience wrapper: detect agents and install into each
onboard doctor                verify each install; --agent NAME to check just one (read-only)
onboard skills                list the embedded skills
onboard -v                    print version (stamped commit + date when released)
```

- `doctor` is the inverse of `install`: for each detected agent it reports whether onboard is
  registered in the config, whether the configured binary still exists (a stale path after
  moving the binary is the usual breakage), and whether all skill files landed. It changes
  nothing and exits non-zero if a detected agent has a problem, so it doubles as a CI check.
- `install --agent X` always creates the needed dirs, even if X isn't detected yet
  (force-install).
- `install --all` and `init` only touch **detected** agents — those whose config or
  skills parent directory already exists — so they won't create `~/.cursor` for an agent
  you don't use.
- Plain `onboard install` with no flag is an error that asks you to pick `--agent` or
  `--all`.

After installing, **restart the agent** so it picks up the new MCP server and skills.

## The agent matrix

Five agents, three config shapes. The shapes genuinely differ — the installer encodes
each one (`Shape` in `agents.go`):

| Agent | Skills dir | Config file | Shape | Server entry |
|-------|-----------|-------------|-------|--------------|
| **Claude Code** | `~/.claude/skills/` | `~/.claude.json` | JSON `mcpServers` | `{"command": BIN, "args": ["serve"]}` |
| **Codex** | `~/.codex/skills/` | `~/.codex/config.toml` | TOML `mcp_servers` | `[mcp_servers.onboard]` `command`/`args` |
| **Grok** (xAI Build CLI) | `~/.grok/skills/` | `~/.grok/config.toml` | TOML `mcp_servers` | `[mcp_servers.onboard]` `command`/`args` |
| **opencode** | `~/.config/opencode/skills/` | `~/.config/opencode/opencode.json` | JSON `mcp` (outlier) | `{"type":"local", "command":[BIN,"serve"], "enabled":true, "environment":{}}` |
| **Cursor** | `~/.cursor/skills/` | `~/.cursor/mcp.json` | JSON `mcpServers` | `{"command": BIN, "args": ["serve"]}` |

Notable shape differences:
- **Codex / Grok** use a snake_case TOML table `[mcp_servers.<name>]`, *not* `mcpServers`.
  `command` is a string, `args` an array.
- **opencode** is the outlier: root key is `mcp` (not `mcpServers`), the binary and its
  args are merged into a **single `command` array**, and the env field is `environment`
  (not `env`), with `type: "local"` required.
- **Codex honors `CODEX_HOME`** — the installer resolves the codex paths against it when
  set.
- **Grok ships in two flavors.** The xAI Grok Build CLI uses TOML at
  `~/.grok/config.toml`; the npm `grok-cli` uses JSON at `~/.grok/user-settings.json`. The
  registry prefers TOML and only falls back to the JSON variant if the JSON file exists
  and the TOML one doesn't.

## Safety guarantees

The installer is written to never damage a config it doesn't understand
(`internal/agents/agents.go`):

- **Merge, don't clobber.** JSON installs preserve every other key and only add the
  `onboard` server. TOML installs **append** a table rather than re-marshaling, so your
  comments and key ordering survive.
- **Idempotent.** A second run detects the existing `onboard` entry and reports
  `already-present` (the TOML check uses a regex that ignores commented-out tables and
  matches both `[mcp_servers.onboard]` and the quoted-key form).
- **Backup on unparseable config.** If a JSON config can't be parsed, it's moved to
  `<path>.onboard-bak` (with a unique suffix so a second run can't overwrite the first
  backup) and a fresh object is written — the original is never silently lost.
- **Permission-preserving.** Existing config file permissions are kept (agent configs
  often hold tokens at `0600`); new files default to `0600`, not world-readable.
- **Path-escape guard.** Skill names containing `/`, `\`, or `..` are skipped so a name
  can't escape the skills directory.

## Manual / hosted setup

You don't have to use the installer. To wire onboard into any MCP client by hand, register
a stdio server that runs the absolute binary path with `serve` (e.g. for Claude Code, add
`{"command": "/abs/path/onboard", "args": ["serve"]}` under `mcpServers.onboard`). For a
hosted/shared deployment, run `onboard serve --http :8080` and point an HTTP MCP client at
`http://host:8080/mcp`.
