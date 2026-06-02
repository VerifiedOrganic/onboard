# Change plan output

The deliverable of this skill is a change plan: a single artifact the user can act on before they touch a line of code. It converts a blast-radius query into "here is exactly what to edit, what it will ripple into, and how to prove I didn't break it."

## Contents
- [Shape of the plan](#shape-of-the-plan)
- [Reading the impact output correctly](#reading-the-impact-output-correctly)
- [The fact-vs-assumption split](#the-fact-vs-assumption-split)
- [Severity tiers](#severity-tiers)
- [Worked example](#worked-example)
- [When the target can't be resolved](#when-the-target-cant-be-resolved)

## Shape of the plan

Emit these sections in order. Keep it scannable — this is a pre-flight checklist, not an essay.

```markdown
# Blast radius: <target> (<rename | signature change | delete | behavior change>)

## Target
<resolved symbol: matched_symbol from onboard:impact, what it is, what the change is>

## Direct callers (N) — edit or re-check each
<from onboard:impact direct_callers — path::caller, one line on what it does>

## Transitive reach (M = impacted_count)
<from onboard:impact transitive_callers — the FULL reachable set incl. the direct callers; group by package/module so it reads as a fan-out, not a flat list>

## Downstream the target depends on
<from onboard:trace_flow on the target — callees the change might also disturb (e.g. you change what you pass them)>

## Tests at risk (K)
<from onboard:impact at_risk_tests — the subset of the transitive set that lives in test files>

## Hidden / non-syntactic coupling
<see references/hidden-coupling.md — what the call graph cannot see>

## Change plan
1. Files to edit: <ordered list, innermost contract first>
2. Likely side effects: <behavioral, not just structural>
3. Verify with: <the specific test files + commands to run, narrowest first>

## Facts vs. assumptions
<the split — see below>
```

## Reading the impact output correctly

`onboard:impact` returns a precise shape; report it precisely:

- `matched_symbol` is the ONE definition it analyzed. Echo it so the user can confirm you analyzed the right thing.
- `candidates` appears ONLY when the name was ambiguous. Non-empty `candidates` means the tool picked the first match and ignored the rest — surface the ambiguity, do not bury it.
- `direct_callers` and `transitive_callers` are separate fields, but the transitive set is a superset: it already contains the direct callers and then fans outward. Do not add them together.
- `impacted_count` equals `len(transitive_callers)` — the headline blast-radius number, not direct-plus-transitive.
- `at_risk_tests` is the subset of `transitive_callers` whose files look like tests (`*_test.go`, `*.test.*`, `*.spec.*`, `test_*`, `tests/`, `__tests__/`). They overlap the transitive list; they are verification targets, not extra impact.
- If `provider` is `null`, there is NO call graph (no indexable language tree): callers come back empty and the `note` says so. Switch to a grep-based blast radius and label it as such.
- The `note` field carries the tool's own syntactic caveat. Carry it through to your assumptions column rather than restating it as fact.

## The fact-vs-assumption split

This is the section that earns trust. The code graph gives you *likely* edges from name + lexical scope; it does not type-check, does not see runtime wiring, and cannot tell two same-named symbols apart. So every plan ends with an explicit two-column honesty pass:

- **Facts (graph-backed):** "`onboard:impact` reports 7 direct callers across 4 files; the transitive set is 12 (impacted_count=12); 3 of those are tests." State the tool and the number.
- **Assumptions (needs a human/tool to confirm):** "These edges are syntactic — if `process()` is also defined elsewhere, some callers may be false positives. Dynamic dispatch, DI containers, reflection, and string-keyed registries will NOT appear above. A type-checker or test run is the proof."

Never let an assumption masquerade as a fact. "Likely 7 callers" is honest; "this changes exactly 7 callers" is a claim the syntactic graph cannot back.

## Severity tiers

Label the change so the user knows how much ceremony it deserves:

- **Contained** — few direct callers, all in one module, tests cover them. Edit with confidence.
- **Rippling** — transitive reach crosses package boundaries, or callers live in code with no at-risk tests. Edit, then run a wider suite.
- **Load-bearing** — high transitive count, public API / exported symbol / schema / endpoint, or thin test coverage on the callers. Treat a rename/delete as a small migration: stage it, consider a deprecation shim, expand tests first.

A symbol with zero direct callers is its own signal: either dead code (safe to delete, say so) or reached only dynamically (the graph is blind here — the most dangerous case, flag it loudly).

## Worked example

> "Is it safe to rename `validateToken`?"

1. `onboard:impact("validateToken")` → `matched_symbol` = `internal/auth/token.go::validateToken`, `candidates` empty (name is unique — good), 5 direct callers, transitive set 12 (impacted_count=12), 2 at-risk tests within that 12.
2. `onboard:trace_flow("internal/auth/token.go::validateToken")` → it calls `decodeJWT` and `lookupSession`; the rename doesn't disturb those, but they confirm it's on the auth hot path.
3. Hidden-coupling check: it is probably referenced by name in middleware registration and maybe in a route-guard config string — neither shows in the graph. Grep the repo for the literal string `validateToken`.

Plan: rename at the definition + 5 call sites (Contained-to-Rippling). Side effect: any string/reflective reference breaks silently. Verify: run the 2 auth tests, then grep for the old name in config/middleware, then the auth integration suite. Facts: 5 graph-backed call sites, transitive 12. Assumption: no dynamic references — confirmed only by the grep plus a green auth suite.

## When the target can't be resolved

If `onboard:impact` returns an empty `matched_symbol` (no symbol matched) or a non-empty `candidates` list (multiple same-named definitions), do not invent a blast radius. Run `onboard:recon` to locate candidates, ask the user which one they mean (or have them qualify as `file::name`), and report the ambiguity as the headline finding — an unresolvable or ambiguous target is itself a risk.
