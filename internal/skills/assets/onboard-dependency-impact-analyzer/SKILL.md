---
name: onboard-dependency-impact-analyzer
description: Computes the blast radius of one proposed code change before you edit — the direct callers, the full transitive caller set, the downstream callees the target itself relies on, and exactly which tests are at risk, then turns that into an actionable change plan with an honest fact-vs-assumption split. Use this skill for a single targeted change to one function, file, endpoint, schema, or field, whenever the user asks "what breaks if I change X", "what depends on this", "who calls this function", "is it safe to rename Y", "is it safe to delete Y", "what is the blast radius of changing Z", "what will this refactor affect", or "who uses this endpoint/field". Per-change impact only; for a whole-repo standing risk register use onboard-test-gap-and-risk-auditor, for durable diagrams use onboard-architecture-cartographer.
---

# Dependency Impact Analyzer

Before you change one thing, know what else moves. This skill takes a single proposed change — rename this function, alter this signature, delete this file, reshape this schema field, touch this endpoint — and computes its blast radius: who calls the target directly, who reaches it transitively, what the target itself depends on, and exactly which tests stand to break. The deliverable is not a list; it is a **change plan** the user can act on with their eyes open.

This is the skill that most justifies a real code-graph backend. Grep tells you where a name *appears*; the code graph tells you who actually *calls* it and how far the ripple travels. But that graph is syntactic — it resolves edges by name and lexical scope, not by type or runtime wiring — so the second half of this skill's job is naming the coupling the graph is blind to. **Map the ripple before you make the cut.**

Scope: one target, one change, before the edit. For a standing, whole-repository risk register (untested paths, fragile seams across the entire codebase) that is `onboard-test-gap-and-risk-auditor`; for durable committed diagrams-as-code that is `onboard-architecture-cartographer`. This skill is per-change and targeted — stay in that lane.

## Step 0 — Pin the target and the change

Get two things straight before touching any tool:

1. **The target** — the exact symbol, file, endpoint, or schema field in question. If the user named it loosely ("the auth check"), resolve it to a concrete symbol first. Prefer a qualified form (`file::name`) so you analyze the right thing and not a same-named neighbor. Terraform/Terragrunt targets are first-class: variables, outputs, locals, module calls, and resources index by their address (`var.nodes`, `output.api_endpoint`, `module.inventory`, `redfish_power.on`), so query e.g. `modules/inventory/variables.tf::var.nodes` — the blast radius covers module wiring, Terragrunt inputs/includes, and `.tftest.hcl` tests, with the IaC-specific blind spots listed in `references/hidden-coupling.md` item 10.
2. **The kind of change** — rename, signature/contract change, deletion, or behavior change. The blast-radius question differs for each: a rename breaks *references*; a signature change breaks *callers' call sites*; a deletion breaks *everyone downstream*; a behavior change breaks *assumptions* and may not surface as a broken call at all.

If you cannot locate the target, run `onboard:recon(root)` to map the codebase and surface candidate symbols. The change kind shapes every later step, so do not skip naming it.

## Step 1 — Compute the blast radius

Call `onboard:impact(symbol, root?)` on the resolved target. **If the target is Go, pass `precise=true`** — it enriches the graph with type-checked edges and resolves interface dispatch, so callers that reach the target through an interface (the dangerous, easy-to-miss ones) appear in the blast radius instead of being silently absent. It is slower and needs the `go` toolchain; the syntactic result is the fallback when either is missing. It returns:

- `matched_symbol` — the one definition it actually analyzed. **Always read this back.** `impact` matches by exact name, then exact qname, then qname-substring, and analyzes only the *first* match.
- `candidates` — present **only when the name was ambiguous** (more than one definition matched). If this is non-empty, the tool silently picked one and ignored the rest: stop, surface the ambiguity as the headline finding, and ask the user to disambiguate or re-run with a `file::name` qualifier. An unresolvable target is itself a risk.
- `direct_callers` — who calls the target directly. These are the call sites you edit (or re-verify) by hand.
- `transitive_callers` — the **full** reachable caller set (it *includes* the direct callers, then fans out). Group it by package/module so it reads as a ripple, not a flat dump.
- `at_risk_tests` — the subset of `transitive_callers` that live in test files. These are your verification targets; they overlap the transitive list, they are not additional to it.
- `impacted_count` — equals the size of `transitive_callers` (the headline blast-radius number), **not** direct-plus-transitive. State it as such.
- `note` / `provider` — the tool stamps its own syntactic caveat in `note`. If `provider` is `null`, there is **no call graph at all** (the repo had no indexable language tree): `impact` returns no callers and says so. In that case fall back to a whole-repo string search (Step 3) and tell the user the blast radius is grep-based, not graph-based.

State the numbers with their source: "`onboard:impact` reports N direct callers and a transitive set of M (impacted_count=M), of which K are tests." Treat every edge as **likely, not proven** (Step 3).

A target with **zero direct callers** is a signal, not a non-answer: either it is dead code (safe to delete — say so) or it is reached only dynamically, which means the graph is blind to its real callers — the most dangerous case. Flag it loudly.

## Step 2 — Understand how the target is reached and what it touches

Two `onboard:trace_flow(entry, depth?, root?)` passes sharpen the plan (default depth is 4; it is breadth-first and de-duplicated, and truncates at 250 nodes):

- **From a key caller toward the target** — trace from an important entry point to show *how* execution arrives, so you can describe effects in terms of real flows ("this sits on the login hot path"), not just counts.
- **From the target outward** — trace from the target itself to list its callees: what the target depends on. A signature or behavior change can disturb these too (you change what you pass them, or when you call them). This is the downstream half of the blast radius the caller list alone misses.

Keep depth shallow (2–3) unless the user wants the deep chain — breadth-first reach informs a change plan; exhaustive paths rarely do. `trace_flow` also returns `matched_symbol` / `candidates`; apply the same disambiguation check as Step 1. (Pass `precise=true` here too on Go, for the same interface-dispatch reason.)

To assemble the edit itself, call `onboard:context_pack(seed=<target>)` — it returns a ranked, token-budgeted bundle of the target plus its call-neighborhood source (the callers you must re-verify and the callees you depend on) in one shot, so the change plan in Step 4 is grounded in the actual code, not just a list of names.

## Step 3 — Flag the hidden, non-syntactic coupling

This is the step that separates an honest answer from a misleading one. `onboard:impact` and `onboard:trace_flow` resolve edges by **name and lexical scope** — they are NOT type-checked, and the tool's own `note` says exactly this. So the reported blast radius is a **floor, not a ceiling**. Run the target against the checklist in `references/hidden-coupling.md`. The big ones:

- **Dynamic dispatch / interfaces** — a call through an abstraction lands on the interface, not your concrete method (or vice versa). For Go this is exactly what `precise=true` (Step 1) resolves, so if you ran precise, this blind spot is largely closed for Go code — say so; for every other language it remains.
- **Reflection, decorators, string-keyed registries, DI containers** — the "call" is a string literal or a runtime lookup, invisible to the graph.
- **Serialization & schema** — a renamed field is a *data-contract* change; consumers may live in other services, the frontend, or already-stored data.
- **Endpoints / external contracts** — routes, CLI flags, env vars have callers outside this repo entirely.
- **Temporal / ordering coupling** — code that works only because A runs before B. The change may break a *when*, not a *who* — no caller edge exists to find it.

The cheapest high-value move: grep the whole repo (config, YAML, templates included) for the target's **name as a string**, not just as a symbol. That one pass catches most registry/reflection/config wiring.

Watch the inverse too: `impact` resolves by name, so if the target's name is not unique, some reported callers may be **false positives** belonging to a same-named symbol. The `candidates` field from Step 1 is your tell — if it was populated, the same-name collision is real, so re-run against the qualified `file::name`.

## Step 4 — Output the change plan

Assemble the findings into a change plan using the template in `references/change-plan.md`. The required spine:

1. **Files to edit** — ordered, innermost contract first.
2. **Likely side effects** — behavioral, not just structural ("auth fails closed if this throws"), plus the hidden-coupling risks from Step 3.
3. **Verify with** — the *specific* `at_risk_tests` files and the commands to run, narrowest first (the unit tests on this path, then the integration suite, then the string-grep for dynamic references).
4. **Facts vs. assumptions** — the closing honesty pass. Facts are graph-backed and named with their source ("`onboard:impact`: 7 direct callers"). Assumptions need a human, a type-checker, or a test run to confirm ("these edges are syntactic; dynamic references will not appear; a green suite is the proof"). Never let an assumption wear a fact's clothes.

Label the change's severity so the user knows how much ceremony it needs — **Contained** (few callers, one module, tested), **Rippling** (crosses package boundaries or hits untested callers), or **Load-bearing** (public API / schema / endpoint, high fan-out, or thin coverage — treat a rename/delete as a small migration). Details in `references/change-plan.md`.

## Principles

- **Map the ripple before you make the cut.** The analysis exists so the user edits with foresight, not so you produce a report.
- **The graph is a floor, not a ceiling.** Syntactic edges are *likely*. Always name where the graph is probably blind (dynamic dispatch, reflection, config, schema, temporal order) rather than implying the count is complete.
- **Facts carry their source; assumptions are labeled as such.** "Likely 7 callers (`onboard:impact`)" is honest; "exactly 7 callers" is a claim the syntactic graph cannot back.
- **Read back `matched_symbol`; honor `candidates`.** The tool analyzes one definition and tells you when it had to choose. An ambiguous target is a finding, not a footnote.
- **Zero callers is an answer.** It means dead code or dynamic reach — decide which and say so.
- **One target, before the edit.** Whole-repo risk is `onboard-test-gap-and-risk-auditor`'s job; durable diagrams are `onboard-architecture-cartographer`'s. Stay in your lane.
- **Leave the user able to verify it themselves** — the plan ends with the exact tests and commands, so they do not need you to re-run the analysis next time.

## Reference files
- `references/change-plan.md` — the change-plan output template, the fact-vs-assumption split, severity tiers, and a worked example.
- `references/hidden-coupling.md` — the checklist of non-syntactic coupling the call graph cannot see, and how to hunt for each.