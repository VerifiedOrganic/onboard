# Hidden coupling: the blast radius the call graph can't see

`onboard:impact` and `onboard:trace_flow` resolve edges from symbol name and lexical scope (a tree-sitter index over ~200 languages, linked syntactically). That makes them fast and language-agnostic, but it means the reported blast radius is a *floor, not a ceiling*. The real risk in a change — especially in AI-built code, which wires things together in ways nobody designed on purpose — often lives in coupling the syntactic graph never traversed. This file is the checklist you run by hand against the target before you trust the number.

## Contents
- [Why the graph under-counts](#why-the-graph-under-counts)
- [Why the graph over-counts](#why-the-graph-over-counts)
- [The hidden-coupling checklist](#the-hidden-coupling-checklist)
- [How to hunt for each](#how-to-hunt-for-each)
- [Reporting it](#reporting-it)

## Why the graph under-counts

A static name-and-scope graph sees `a()` calling `b()` when the token `b` appears in `a`'s body. It does NOT see a call that goes through a variable, a string, a config file, a network boundary, or a build step. So a symbol can show "2 callers" in the graph and have 20 in reality.

## Why the graph over-counts

The same name-resolution heuristic can ATTRIBUTE callers wrongly. `onboard:impact` matches the query to the first definition (exact name, then exact qname, then qname substring) and resolves caller edges by name — so if two unrelated functions are both named `handle`, one's callers can show up as the other's. The tool surfaces this as a non-empty `candidates` field: treat that as a red flag and re-run against the qualified `file::name`. Same-named symbols are the classic false positive — always check whether the target name is unique.

## The hidden-coupling checklist

Run through these for every blast-radius query. Each is a class of edge the graph likely missed:

1. **Dynamic dispatch / polymorphism** — the target is a method on an interface/base class; callers invoke it through the abstraction, so the edge points at the interface, not your concrete override (or vice versa). Renaming one implementation silently diverges from the contract.
2. **Reflection / metaprogramming** — `getattr`, `method_missing`, `Reflect`, decorators that register by name, ORM field magic. The name appears only as a string or is synthesized at runtime.
3. **String-keyed registries & DI containers** — route tables (`"GET /users" -> handler`), event buses (`emit("user.created")`), plugin maps, dependency-injection bindings, feature-flag lookups. The wire is a string literal the graph reads as data, not a call.
4. **Serialization & schema coupling** — the target is a field/type that gets JSON-serialized, written to a DB column, put on a queue, or shipped in an API response. Consumers may be in *other repos, other services, the frontend, or already-stored data*. Renaming a field is a data-contract change, not a code change.
5. **Endpoints & external contracts** — an HTTP route, GraphQL resolver, gRPC method, CLI flag, or env var. Callers are clients you can't see from this codebase. The graph stops at the framework boundary.
6. **Config / declarative wiring** — YAML/TOML/JSON that names a class, function, or migration; framework conventions (file-based routing, auto-registered handlers) where the "call" is the filename or a decorator.
7. **Temporal / ordering coupling** — code that works only because A runs before B (init order, migrations, cache warming, event sequence). Changing the target may not break a *caller* but may break an *assumption about when it runs*. Invisible to any caller graph.
8. **Shared mutable state** — globals, singletons, module-level caches, the DB itself. Two functions couple through the state they both touch with no call edge between them.
9. **Tests that mock the target** — a mock keyed by name or path won't move when you rename, and the test keeps passing while production breaks. At-risk tests that *mock* the target are a false sense of safety, not coverage.
10. **Terraform/Terragrunt-specific edges** — when the target is a variable, output, module, or resource, the graph covers in-repo wiring (var/local/module/output references, Terragrunt includes, dependency blocks, inputs), but NOT: `TF_VAR_*` environment injection from CI, `-var`/`-var-file` flags in pipeline definitions, `terraform_remote_state` readers in other stacks *or other repos*, outputs consumed by CI scripts or humans, `read_terragrunt_config` indirection, `generate`-block code that exists only at run time, and provider-alias wiring. Renaming an output is a cross-repo data-contract change, exactly like renaming a serialized field. Grep for the bare name, for `TF_VAR_<name>`, and check pipeline files before trusting the number.

## How to hunt for each

You don't need to chase all nine every time — scope to the target's nature:

- **Always:** grep the whole repo (including config, templates, and `*.json`/`*.yaml`) for the target's *name as a string*, not just as a symbol. This catches registries, reflection, and config wiring in one pass — and it is your only blast radius at all when `onboard:impact` reports `provider: null`.
- **If it's a method/interface member:** ask whether it implements or overrides something; `onboard:trace_flow` from the *interface* or from known call sites can reveal the abstraction layer.
- **If it's a field/type/struct:** assume serialization until proven otherwise. Check for marshalling tags, DB schema, API docs, and frontend usage.
- **If it's a route/handler/CLI/env var:** treat external callers as existing-and-unknown. The blast radius extends past the repo; say so.
- **If it's near init/migration/startup:** check ordering assumptions explicitly.

## Reporting it

Fold the findings into the plan's "Hidden / non-syntactic coupling" section and the assumptions column. Be specific and honest:

- Good: "`onboard:impact` found 4 callers (fact). The name also appears as a string in `routes.yaml:42` and `events.ts:registerHandler('order.paid', ...)` — likely a dynamic call the graph can't trace (assumption to confirm)."
- Bad: silently reporting "4 callers" and letting the user believe that's the whole blast radius.

The single most valuable sentence you can write is the honest one: "The graph shows N; here are the specific places it is probably blind, so the true number is N-or-more until a grep and a test run confirm it."
