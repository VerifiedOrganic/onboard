# onboard

You inherited a codebase. Maybe a teammate wrote it and left. Maybe *you* wrote it,
six months and all your context ago. Maybe an agent generated forty files at 2am, they
all pass the tests, and you trust exactly none of them.

`onboard` reads the thing and walks you — or your agent — through it: the architecture,
the real end-to-end data-flow traces, and the load-bearing code nobody wrote down. It
ships as **one static Go binary** that is **both an MCP server and a CLI installer**.

> **MCP** is the Model Context Protocol — the open standard agents use to talk to outside
> tools. The whole point of onboard being an MCP server is that *every* agent speaks it, so
> one binary reaches all of them.

Any MCP-capable agent — Claude Code, Codex, Grok, opencode, Cursor — can launch it, and a
CI pipeline or custom harness can drive it over HTTP.

It's especially pointed at *fast* and *AI-generated* code — the kind that arrives faster
than any mental model can form. The walkthrough is as much a **verification** tool as a
learning one: it surfaces where an autonomous build drifted, duplicated logic, or wired
two components together in a way nobody intended.

## 60-second quickstart

```sh
# 1. build it (Go 1.25+)
go build -o onboard .

# 2. wire it into every agent you have installed
./onboard init

# 3. confirm it actually took (reads your configs, changes nothing)
./onboard doctor
```

Now **restart your agent** (so it picks up the new server) and type **`/onboard`**. You'll
get a guided, stepped tour of whatever repo you're sitting in — you pick the direction
(start from the entry points and walk *inward*, or from the load-bearing core and work
*outward*), and it paces itself one move at a time instead of dumping a wall of text.

Driving it from CI or your own harness instead of an interactive agent? Run it as a server
and point an **MCP client** at it:

```sh
./onboard serve                 # MCP over stdio (what agents launch)
./onboard serve --http :8080    # MCP over Streamable HTTP at /mcp
```

These start the server — they don't print a walkthrough on their own. `onboard` is the
*server*; something that speaks MCP (an agent, or your CI harness) is the client that calls
its tools. There's no standalone `onboard analyze` CLI by design.

New here? **[docs/getting-started.md](docs/getting-started.md)** is the unhurried version
of the above, with what-you-should-see at each step.

## What you actually get

Two halves of one idea — *how* to teach a codebase, and *the facts* to teach from:

- **Skills** — the teaching playbooks, embedded in the binary. `codebase-walkthrough` runs
  the top-down tour; four siblings cover diagrams, the per-change blast radius, a standing
  risk register, and keeping a cached guide fresh.
- **Tools** — 17 MCP tools that turn "where do I even start" into ranked, cited answers:
  `recon` (structural scan), `repo_map` (the load-bearing core, ranked), `trace_flow`
  (follow a flow end to end), `impact` (what breaks if I change this), `context_pack`
  (everything relevant to X in one shot), `dead_code` (written-but-never-wired-in),
  `explain_diff` (what this PR touched and its blast radius), plus `deps`, `schema`,
  `routes`, `history`, `render_map`, and a durable `guide` cache.

The tools are backed by a **pure-Go tree-sitter code graph** covering ~200 languages with
no CGo. Its call edges are *syntactic* — resolved by name and lexical scope, not
type-checked. Translation: treat an edge as a very strong rumour, not a sworn affidavit.
For Go, `precise: true` promotes the rumour to a fact (type-checked, interface dispatch and
all).

## The one rule that explains everything

`onboard` stays a **single static, CGo-free, cross-compilable binary**. That one
constraint is why the tree-sitter engine is pure Go, why ~200 grammars are baked in (the
stripped binary is ~32 MB — broad language coverage is the whole point of an onboarding
tool), and why the graph is honest about being syntactic rather than pretending to a
precision it can't guarantee everywhere. When a design choice looks odd, this rule is
usually the reason.

## Where to go next

The docs are arranged by what you're trying to do, not by what's easiest to write:

| You want to… | Read |
|--------------|------|
| **Use it** — install, verify, run your first walkthrough | [getting-started.md](docs/getting-started.md) · [install.md](docs/install.md) |
| **Understand it** — the mental model before the internals | [concepts.md](docs/concepts.md) |
| **Integrate it** — the tool, skill, and prompt contracts | [mcp-tools.md](docs/mcp-tools.md) · [skills.md](docs/skills.md) · [guide-cache.md](docs/guide-cache.md) |
| **Build on it** — hack on onboard's own Go internals | [architecture.md](docs/architecture.md) · [code-graph.md](docs/code-graph.md) · [development.md](docs/development.md) |
| **See why it's built this way** | [research-notes.md](docs/research-notes.md) · [enhancements.md](docs/enhancements.md) |

## Status

Built and tested: the embedded skills, the per-agent installer and `doctor`, the
code-graph engine (`recon`, `trace_flow`, `impact`, `repo_map`, `context_pack`,
`dead_code`, `explain_diff`, `render_map`), the guide cache, stdio **and** Streamable HTTP
transports, and the optional Go type-checked precision layer. CI runs test + vet +
golangci-lint + a cross-build matrix; releases ship via GoReleaser.

Requires Go 1.25 or newer to build. Author: recursive.
