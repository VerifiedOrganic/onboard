# Getting started

This is the unhurried version of the README quickstart: the same path, but with what you
should *see* at each step and what to do when a step sulks. Total time, first run: a couple
of minutes, most of it spent waiting on `go build`.

The goal by the end: you type `/onboard` in your agent and get a real, guided tour of a
codebase — yours, or one you've been handed and don't yet trust.

## Step 0 — You'll need

- **Go 1.25 or newer** to build from source (the path below).
- **At least one MCP-capable agent** installed: Claude Code, Codex, Grok, Kimi CLI,
  opencode, Cursor, Copilot CLI, or Junie CLI. `onboard` wires itself into whichever ones it
  finds. (MCP = the Model Context Protocol — the open standard those agents use to talk to
  external tools. If your agent speaks MCP, onboard works with it.)

No Go? Grab a prebuilt binary from the project's releases (built with GoReleaser) and skip
straight to Step 2 — every command below is the same, you just won't run `go build`.

## Step 1 — Build the binary

```sh
go build -o onboard .
```

One file comes out: `onboard`, ~32 MB. It looks chunky for a CLI because it has ~200
tree-sitter grammars baked in — that's deliberate, and [concepts.md](concepts.md) explains
why. Size-conscious? `go build -tags grammar_set_core .` trims it to ~26 MB with a curated
language set.

## Step 2 — Wire it into your agents

```sh
./onboard init --dry-run
./onboard init
```

`init` scans for installed agents and, for each one it finds, does two things: drops the
embedded skill files into that agent's skills directory, and registers this binary as an
MCP server in that agent's config. You'll see a line per agent:

```
Scanning for installed agents...
  ✓ claude    config: merged          skills: 17 file(s)
  ✓ codex     config: appended        skills: 17 file(s)
  – cursor    not detected, skipping
```

> "17 file(s)" and "6 skills" are both right: there are **6 skill bundles**, made of 17
> files total (a `SKILL.md` plus reference docs each). `init` counts the files it wrote;
> `doctor` (next step) counts the 6 bundles. Same skills, different unit. Only the agents
> you actually have installed show up — don't worry about the ones that say "not detected."
> On upgrades from older onboard releases, the line may also say it cleaned legacy skill
> dirs; that means old unprefixed onboard skills were replaced by the current `onboard-*`
> names.

Use `--dry-run` on `init` or `install` to preview config paths, skill paths, and planned
actions without writing files. `onboard uninstall --agent NAME` removes onboard's MCP entry
and embedded skill dirs if you need to roll an agent back.

Want just one, or one that isn't detected yet? Name it explicitly — this creates the dirs
it needs:

```sh
./onboard install --agent claude
```

> The supported agents genuinely disagree on config format (JSON here, TOML there, and opencode
> in a category of its own). The installer speaks all of them so you don't have to. The
> gory details live in [install.md](install.md).

## Step 3 — Confirm it took

```sh
./onboard doctor
```

`doctor` is read-only — it changes nothing, it just checks your work. Per agent it tells you
whether onboard is registered, whether the binary it points at still exists, and whether the
skills landed:

```
  ✓ claude    registered=true  bin=true  skills=6/6
      bin: /Users/you/code/onboard/onboard
  ✗ codex     registered=true  bin=false skills=6/6
      bin: /old/path/onboard
      ! configured binary not found: /old/path/onboard (re-run install to refresh the path)
```

A `✗` tells you exactly what to fix. The most common one is the second line above: you moved
or rebuilt the binary elsewhere, so the config points at a ghost. Re-run `install` and the
path updates.

## Step 4 — Take the tour

Restart your agent (so it picks up the new MCP server), then type:

```
/onboard
```

This kicks off the **guided tour** — a stepped walkthrough, not a wall of text. It opens by
asking which way you want to learn the repo:

- **Outside-in** — start at the edges (entry points, routes, CLI) and walk *inward* toward
  the core. Best for *"what happens when a request comes in?"*
- **Inside-out** — start at the load-bearing core (the most depended-on code) and expand
  *outward*. Best for *"what does everything here lean on, and where's the risk?"*

From there it goes one move at a time — orient, explore, surface the risky bits, check your
understanding — pausing so you can say `next`, go `deeper`, `jump` somewhere, or `switch
direction`. You're driving; it's navigating.

Not sure which onboard workflow you need? Type:

```
/onboard-skills
```

That shows the shipped `onboard-*` skill catalog with example prompts for diagrams,
blast-radius checks, risk audits, and guide maintenance.

> No `/onboard` command in your agent? Some clients don't surface MCP prompts. You can still
> ask the agent in plain language — *"walk me through this codebase"* — and it'll use
> onboard's tools and the `onboard-codebase-walkthrough` skill the same way.

## Step 5 — Poke at a tool directly

The tour orchestrates them for you, but you can call the tools à la carte. Ask your agent
things like:

- *"Run recon on this repo"* → the structural lay of the land: stack, entry points, tests,
  churn hotspots.
- *"What breaks if I change `FunctionName`?"* → `impact` — direct and transitive callers,
  and which of them are tests.
- *"Trace the flow from `main`"* → `trace_flow` — the call path, end to end.
- *"What did this branch change and what's the blast radius?"* → `explain_diff`.
- *"Find code nothing calls"* → `dead_code` — written-but-never-wired-in, ranked by how
  likely it's truly dead.

For Go repos, add *"use precise mode"* and the call graph gets upgraded from syntactic
guesses to type-checked facts. For Rust Cargo repos, precise mode uses `rust-analyzer` call
hierarchy when that binary is installed. It is slower because language tooling has to load
the project, but worth it when an edge matters. The full menu is in
[mcp-tools.md](mcp-tools.md).

## Running it headless

CI pipeline, a custom harness, or a hosted deployment — anything that isn't an interactive
agent. You run onboard as a *server* and have an **MCP client** call its tools:

```sh
./onboard serve                 # MCP over stdio
./onboard serve --http 127.0.0.1:8080    # MCP over Streamable HTTP at /mcp
```

One honest caveat: these start the server, they don't print a walkthrough by themselves.
`onboard` is the server; you still need *something that speaks MCP* on the other end (an
agent, or an MCP client library in your CI script) to actually call `recon`, `trace_flow`,
and friends. There's deliberately no standalone `onboard analyze` that dumps results to
your terminal — the analysis lives behind the MCP tools. Keep HTTP bound to loopback unless
you are deliberately putting it behind your own auth, TLS, and network controls; see
[trust.md](trust.md).

## When something's off

| Symptom | What's happening | Fix |
|---------|------------------|-----|
| `doctor` says `registered=false` | onboard isn't in that agent's config | `./onboard install --agent <name>` |
| `doctor` says `bin=false` | the config points at a binary that moved | re-run `install` to refresh the path |
| `init` says "No agents detected" | none of the supported agents are installed where expected | install an agent, or force one with `install --agent` |
| `/onboard` isn't there | your client doesn't surface MCP prompts | ask in plain language; the tools still work |
| The tour's edges look thin on a Go or Rust repo | syntactic graph missed dispatch/method calls | re-run the relevant tool with **precise mode** (`go` or `rust-analyzer` must be installed) |

## Where next

- Curious *why* it's shaped this way before you go deeper? → [concepts.md](concepts.md)
- Wiring it into something custom, or want the tool contracts? → [mcp-tools.md](mcp-tools.md)
- The full install matrix and every command? → [install.md](install.md)
