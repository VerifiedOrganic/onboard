# Risk taxonomy

The checklists behind Phases 5 and 6. Walk these deliberately; each hit is a candidate register row.

## Table of contents
- Integration-seam catalog (Phase 5)
- Unhandled-error checklist (Phase 6)
- Silent-assumption checklist (Phase 6)
- Defend-the-design prompts (Phase 6)
- Language-specific tells

## Integration-seam catalog (Phase 5)
A seam is a contract between two pieces of code. For each present in the repo, verify both sides agree on **shape, field names, types, nullability, units, enum values, ordering, and the error contract**. Any disagreement is a finding with *both* `file:line` locations. A seam with no test crossing it (per the Phase 3 cross-check) is doubly flagged.

| Seam | The two sides | What silently disagrees |
|------|---------------|--------------------------|
| HTTP client ↔ server | request builder vs route handler | path/params, body shape, status codes, content-type |
| Producer ↔ consumer (queue/event) | publish payload vs handler | field names, units, schema version, ordering / at-least-once assumptions |
| ORM/model ↔ DB schema | model definition vs migration | column types, nullability, defaults, indexes vs query assumptions |
| Serialize ↔ deserialize | encoder vs decoder | optional fields, tag/JSON key casing, date/number formats |
| Frontend types ↔ backend types | TS interface / client model vs server DTO | nullable vs required, enum drift, camelCase vs snake_case |
| Config schema ↔ readers | the writer/default vs every `getenv`/config read | missing key, wrong type, unvalidated range, required-but-optional |
| Internal module API ↔ callers | exported signature vs call sites | argument order, returned-error contract, side-effect expectations |
| Third-party API ↔ wrapper | SDK/HTTP contract vs the wrapper's assumptions | pagination, rate limits, error shapes, eventual consistency |

**Heuristic:** the two sides of a seam are frequently authored in separate passes (or by separate agents), so drift here is the single most common class of fast-build bug.

## Unhandled-error checklist (Phase 6)
For each failure-capable call on a reachable path, ask: is the error path handled, and is it tested?
- Errors **swallowed**: empty `catch`, `except: pass`, `_ = err`, `.unwrap()`/`!` on fallible values, ignored returns.
- Errors **logged but not handled** — execution continues into a corrupt state.
- **Resource cleanup** missing on the error branch: unclosed files/connections, no `defer`/`finally`/`with`, leaked locks.
- **Partial failure** in a multi-step mutation with no rollback/compensation (write A succeeds, write B fails, no undo).
- **External calls** with no timeout, retry policy, or fallback (network, DB, third-party).
- **Boundary inputs**: empty collection, null/None, zero, negative, overflow, unexpected enum/default branch.
- Error branches that exist but are **unreachable in tests** — the code is there, nothing exercises it.

## Silent-assumption checklist (Phase 6)
A silent assumption is a condition the code *requires* but never checks or states. Each unfound enforcement is a finding (*assumed X, never enforced, breaks when not-X*).
- **Non-null / present**: input assumed non-null, map key assumed to exist, optional dereferenced.
- **Order / sortedness**: binary search or merge logic assuming sorted input; iteration order assumed stable.
- **Uniqueness / cardinality**: ID assumed unique, "exactly one" result from a query that can return many or none.
- **Concurrency**: shared state assumed single-threaded; read-modify-write with no lock; assumed idempotency under retry.
- **Time**: clock assumed monotonic; timezone/UTC assumed; TTL/expiry assumed fresh.
- **Environment**: env var assumed set, file/path assumed present, service assumed reachable, feature flag assumed in one state.
- **Numeric / units**: currency in cents vs dollars, ms vs s, no overflow, float used for money.
- **Trust**: external input assumed validated upstream; auth/authorization assumed checked by a caller.

## Defend-the-design prompts (Phase 6)
For each major structural choice, argue it as if in review. Where the answer is weak, that's a **Design risk** finding.
- **This boundary** — why is the line between these two modules *here*? Does responsibility actually split cleanly, or is logic duplicated / leaking across it?
- **This abstraction** — what does it earn? Is it a real seam, or indirection with one implementation that just adds a hop?
- **Sources of truth** — is any fact (a price, a status, a schema) defined in two places that can drift?
- **Test-shaped code** — was anything written only to satisfy a test, encoding the test's quirk rather than real behavior?
- **Coherence** — do these locally-reasonable choices add up to a system someone would design on purpose, or an accretion? Incoherence is itself a risk.

## Language-specific tells
Fast tells for swallowed errors and unchecked assumptions, by stack:
- **Go**: `_ = err`, `if err != nil { return }` with no wrap/context, missing `defer Close()`, ignored second return value, `panic` on a reachable path.
- **JS/TS**: empty `.catch()`, un-awaited promise, `as` casts that defeat the type checker, `!` non-null assertions, `any` at a boundary, `JSON.parse` with no try/catch.
- **Python**: bare `except:` / `except Exception: pass`, mutable default arguments, no context manager on resources, broad `getattr` defaults masking missing keys.
- **Rust**: `.unwrap()`/`.expect()` on a reachable path, `let _ =` on a `Result`, `unsafe` blocks.
- **Java/Kotlin**: caught-and-logged-only exceptions, `Optional.get()` without `isPresent`, nullable platform types from interop crossing a boundary.
- **Terraform/HCL**: providers or `required_version` without an upper bound, stateful resources (databases, buckets, KMS keys) missing `lifecycle { prevent_destroy = true }`, `for_each`/`count` keyed on reorderable values, modules with no `*.tftest.hcl` or harness coverage, variables never read inside their module (`onboard:dead_code` finds these), `try()`/`coalesce()` masking missing inputs, secrets fetched at apply time with no failure handling, policies in `policies/` not enforced by any CI job. Use `onboard:stacks` to enumerate the deploy surface and check each unit has a plan exercised in CI.
