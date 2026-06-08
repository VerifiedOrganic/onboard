# Development

So you want to hack on onboard itself. Good — the codebase is small, the rules are few, and
the build is boring on purpose (one static binary, no C toolchain, no surprises). Here's how
to build it, prove it still works, and add a tool, skill, or agent without breaking the
single-binary promise.

## Prerequisites

- **Go 1.25+** (the module targets `go 1.25`; CI builds with the latest toolchain).
- `git` on `PATH` for the guide/delta features (everything else degrades gracefully
  without it).
- Optional: `golangci-lint` v2 for linting (`make tools` installs it).

## Build, test, lint

```sh
make build          # go build -trimpath -ldflags "..." -o onboard .
make install        # go install onto your PATH (GOBIN)
make skills         # build, then list embedded skills
go test ./...       # unit + integration tests (in-memory + httptest MCP transports)
go vet ./...
make lint           # golangci-lint run
```

Build-time knobs that trade binary size for language coverage:

```sh
go build -tags grammar_set_core .         # ~26 MB: curated Core100 grammar set
GOTREESITTER_GRAMMAR_SET=go,python,...     # runtime: restrict to specific languages
```

The binary is pure-Go and `CGO_ENABLED=0`, so it cross-compiles to
darwin/linux/windows × amd64/arm64 from a single runner with no C toolchain.

## Version stamping

`cmd/root.go` holds three package vars stamped at release time via `-ldflags -X`:

```
-X github.com/VerifiedOrganic/onboard/cmd.version=v1.2.3
-X github.com/VerifiedOrganic/onboard/cmd.commit=$(git rev-parse HEAD)
-X github.com/VerifiedOrganic/onboard/cmd.date=$(date -u +%FT%TZ)
```

`version` also identifies the MCP server to connecting clients. A dev build with a dirty
tree stamps something like `0.1.0-<sha>-dirty`.

## CI & release

- **`.github/workflows/ci.yml`** — `test` (race + coverage), `lint` (golangci-lint v2),
  and a cross-build `build` matrix (3 OS × 2 arch, `CGO_ENABLED=0`).
- **`.github/workflows/release.yml`** + **`.goreleaser.yaml`** — GoReleaser v2 builds the
  matrix on a `v*` tag, stamps the `-X` ldflags, and publishes archives + checksums.
- **`.golangci.yml`** — golangci-lint v2 config (standard linters + misspell, revive,
  bodyclose, gosec, …).

See [research-notes.md](research-notes.md) §5 for the rationale behind every CI/release
choice.

## Project layout

See [architecture.md](architecture.md#package-layout) for the package map. In short:
`cmd/` is the CLI, `internal/server/` is the MCP surface, `internal/providers/` is the
code-graph engine, `internal/skills/` holds the embedded skills, `internal/agents/` is the
installer, and `internal/guide/` + `internal/git/` are the guide cache.

## How to add things

### A new MCP tool
1. Add a `registerXTool(s *mcp.Server)` in a `internal/server/tools_x.go`, defining input
   and output structs with `json:` tags and `jsonschema:` descriptions (the SDK infers the
   schemas).
2. Register it via `mcp.AddTool(s, &mcp.Tool{Name, Description}, handler)`.
3. Call `registerXTool(s)` from `New` in `internal/server/server.go`.
4. Return `(nil, out, nil)` on success; return an `error` only for true failures —
   represent expected degraded states with a `Note` field.
5. Add tests using the in-memory transport pattern (see existing `*_test.go`).

### A new skill
See [skills.md](skills.md#adding-a-skill). Drop a namespaced bundle under
`internal/skills/assets/onboard-<name>/`; it auto-gains a `get_skill` entry and a resource.
Mind the plain-scalar frontmatter rule.

### A new agent target
Add an `Agent{Name, SkillsDir, ConfigPath, Shape}` to `Registry()` in
`internal/agents/agents.go`. If its config format matches an existing `Shape`
(`ShapeJSONMcpServers`, `ShapeJSONOpencode`, `ShapeTOMLMcpServers`) you're done; otherwise
add a new shape and a `registerX` writer in `internal/agents/agent_config.go`, and dispatch
it from `registerMCP` there.

## Testing notes

- Tools are exercised end-to-end over the SDK's **in-memory transports** (connect the
  server first, then the client); the HTTP path is tested with `httptest`.
- The graph engine has targeted regression tests for the tricky bits: ambiguous-name
  resolution (`resolve_test.go`) and the trace-truncation false positive
  (`graph_test.go`).
- The skill frontmatter contract is enforced by `skills_test.go` — including the strict-
  YAML safety check that prevents the colon-space pitfall described in
  [skills.md](skills.md#the-parser-is-intentionally-naive--and-why-that-matters).
