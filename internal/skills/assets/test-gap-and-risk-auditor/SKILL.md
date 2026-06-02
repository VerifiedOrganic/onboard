---
name: test-gap-and-risk-auditor
description: Produces a standing, whole-codebase risk register of the negative space — reachable-but-untested code paths, unhandled error paths, fragile integration seams, and silent assumptions baked in by fast or AI-driven builds, each ranked by severity with a file:line location and a concrete mitigation. Use when someone asks "what's untested", "where is the risk", "find the fragile parts", "what are the coverage gaps", "audit the codebase for risk", "what could break", "where are the weak spots", "what did the AI build leave unsafe", or wants a whole-repo risk and coverage audit rather than the blast radius of one specific change. Drives the audit with onboard:recon, onboard:trace_flow, and onboard:impact, crossing the reachable call graph against the test set. Not a teaching walkthrough (that is codebase-walkthrough) and not a per-change impact analysis (that is dependency-impact-analyzer).
---

# Test-Gap and Risk Auditor

Produce a **standing risk register** for an entire codebase: a ranked list of where it is most likely to break, why, and what to do about it. The deliverable is a table of findings — each with a severity, a `file:line` location, a one-line reason it's risky, and a concrete mitigation — opened by a short narrative of the themes.

This audit hunts the **negative space**: code that runs but is never tested, errors that are never handled, integration seams where two sides quietly disagree, and assumptions a fast or AI-driven build baked in without ever stating them. Modern codebases arrive faster than anyone can vet them; this skill is the standing vetting pass.

**Scope discipline.** This is *whole-repo and standing* — "audit the codebase," "what's untested," "where's the risk." If the user instead asks "what breaks if I change *this one* function/file/endpoint/schema," that is the **dependency-impact-analyzer** skill's per-change blast radius — hand it off. If they want to be *taught* the codebase, that's **codebase-walkthrough**. If they want a committed diagram, that's **architecture-cartographer**. Stay in your lane: you *rank risk*, you do not re-teach the code or draw its maps.

## The honesty contract (read first)

`onboard:trace_flow` and `onboard:impact` return **syntactic** call edges — matched by name and lexical scope, not type-checked. They cannot see dynamic dispatch, interface/virtual calls, reflection, string-keyed dispatch tables, DI containers, or callbacks registered at runtime, and they can over-match on same-named symbols.

So every reachability and coverage claim is **"likely," never "proven."** A path you call "untested" might be exercised through an edge the graph can't see; a symbol you call "dead" might be invoked by reflection. State the ceiling explicitly in the register, and prefer false alarms a human can dismiss over silent misses. Never imply the graph proves anything it merely suggests.

**For Go, raise the ceiling.** Pass `precise=true` to `onboard:trace_flow` and `onboard:impact`: it resolves interface dispatch with the type checker, so reachability and blast-radius become type-checked (proven) for Go rather than merely syntactic. A "dead" or "untested" verdict you reach *with* precise is far stronger — note which findings were precise-backed.

## Workflow

Run these phases in order. Map before you read code — the call graph tells you *where* to look before you spend tokens reading.

### Phase 1 — Recon: find the test layout, entry points, and hotspots
Call `onboard:recon(root)`. Capture:
- **`test_layout`** — where tests live, the runner, the naming convention (`*_test.go`, `*.spec.ts`, `test_*.py`). This defines what "has a test" even means here.
- **`entry_points`** — the roots of the reachable call graph (HTTP handlers, CLI commands, `main`, job/queue consumers, the exported library API). Everything reachable from these is *live*; risk in live-but-untested code outranks risk in code nothing reaches.
- **`hotspots`** — in a git repo, recon returns the highest-churn files; cross-reference these against coverage in Phase 3, because churning + untested is the sharpest risk signal there is.
- **`frameworks` / `stack` / `tooling`** — tells you which seams and idioms to expect (ORM boundaries, serializers, RPC clients, error-handling conventions).

For the full churn picture call `onboard:history(root)` — commit count, author count, and recency per file. High churn (many commits, many authors) marks code that changes often and is understood by few; weight it up in the final ranking.

If `recon` reports **no test layout**, say so plainly and pivot: with no spec to cross against, *everything* reachable is effectively untested, so the audit leans entirely on error-path, seam, and assumption analysis (Phases 4–6) and the register's top line becomes "no automated tests detected."

### Phase 2 — Build the behavioral map from the tests
The test suite is the truest statement of *intended* behavior in a fast-built codebase. Read it selectively — names and assertions, not every line — and list **what the system claims to do, by domain**, noting for each behavior which symbols/files it touches. This is your **covered set**: the union of code the tests plausibly exercise. Keep it as a set of file/symbol references; it is the thing you subtract from the reachable set in Phase 3. Treat membership as an inference, not proof — a test that *references* a symbol does not prove it *exercises* the branch you care about.

### Phase 3 — Cross reachability against coverage → reachable-but-untested
For each entry point from Phase 1, call `onboard:trace_flow(entry, depth?, root?)` to get the breadth-first reachable callee set. Union these into the **reachable set**.

`reachable − covered = the untested live surface.` These are paths the system actually runs but no test pins down — the richest vein of risk. Rank within it by how central each path is (Phase 4) and how dangerous it is if wrong: mutations, money, auth, deletes, and external writes outrank pure reads.

Mark the ceiling honestly: note edges `trace_flow` couldn't follow (suspected dynamic dispatch / reflection), and any symbol you placed in `covered` by inference rather than by reading the test.

### Phase 4 — Find the load-bearing code with weak coverage
A path is far more dangerous when *many* things depend on it. Call `onboard:repo_map(root)` to rank symbols by call-graph centrality (PageRank, blended with churn) — the top of that list *is* the load-bearing core, surfaced directly instead of guessed. Then, for those high-centrality symbols (and the high-traffic ones surfaced in Phases 2–3), call `onboard:impact(symbol, root?)` and read `direct_callers`, `transitive_callers`, `impacted_count`, and `at_risk_tests`.

The killer finding is **high `impacted_count` + few or zero `at_risk_tests`**: load-bearing code with thin protection. A change there ripples widely yet nothing would catch the regression. These rise to the top of the register. (`at_risk_tests` is also syntactic — it counts *tests that reference this symbol*, an upper bound on real protection, not a guarantee of it.)

### Phase 5 — Enumerate integration seams; flag where the two sides disagree
Bugs cluster at **contracts between components** — exactly the joints a multi-agent or fast build wires up under-examined. Walk the seam taxonomy in `references/risk-taxonomy.md` and, for each seam present (HTTP client↔server, producer↔consumer, ORM↔schema, serialize↔deserialize, FE↔BE types, config↔reader), check whether the two sides agree on shape, nullability, units, enum values, and error contract. **Every disagreement is a finding** with both `file:line` locations. A seam that no test crosses (per the Phase 3 cross-check) is doubly flagged.

### Phase 6 — List silent assumptions, then "defend the design"
**Silent assumptions** are conditions the code requires but never checks or states — non-null inputs, sorted order, single-threaded access, a service always reachable, an env var always set, time monotonic, IDs unique. Hunt the unchecked preconditions and unhandled error paths (`references/risk-taxonomy.md` lists the catalog and the language-specific tells). Each becomes a finding: *assumed X, never enforced, breaks when not-X.*

Then run the **defend-the-design pass**: for each major structural choice, justify it as if in review — "why this boundary, why this abstraction, what was the alternative." Choices that are locally reasonable but don't cohere into a sound whole (duplicated responsibility, two sources of truth, logic written only to satisfy a test) surface here as design-risk findings.

### Phase 7 — Rank and emit the register
Assemble every finding into one **prioritized risk register**, highest severity first. Use the severity rubric and the exact table format in `references/risk-register-format.md`. Each row: **severity · location (`file:line`) · category · why it's risky · suggested mitigation · confidence (with the syntactic ceiling noted)**. Open with a 3–5 line executive summary — top themes, the single scariest finding, overall posture — so a busy reader gets the signal in ten seconds.

## What to flag, by category
- **Untested live path** — reachable from an entry point, absent from the covered set; weight by danger (writes/auth/money > reads).
- **Load-bearing + thin coverage** — high `impacted_count`, few `at_risk_tests`.
- **Unhandled error path** — a failure mode swallowed, ignored, or never reached (see taxonomy).
- **Fragile seam** — a contract where the two sides disagree, or that no test crosses.
- **Silent assumption** — an unchecked, unstated precondition.
- **Design risk** — a structurally incoherent choice that survives "defend the design."

## Principles
- **Map before you read.** Let `recon` + `trace_flow` + `impact` point you at the risky regions; don't read the whole tree.
- **Negative space is the product.** What *isn't* there — coverage, error handling, a stated assumption, a justified boundary — is the highest-value thing you surface.
- **Every claim is "likely."** Syntactic edges suggest; they don't prove. Surface the ceiling on every reachability and coverage claim.
- **Rank ruthlessly.** An unranked list of 40 risks is noise. Severity × centrality × danger, top finding first.
- **Standing, not per-change.** Audit the whole repo as it stands; per-change blast radius belongs to dependency-impact-analyzer — hand it off cleanly.
- **Actionable or it doesn't ship.** Every finding carries a concrete mitigation and a real `file:line`. "Add a test for the 4xx branch of `chargeCard` at billing.go:142" beats "improve coverage."
- **Keep the user independent.** Teach the *pattern* of each gap so they catch the next one, not just this instance.

## Reference files
- `references/risk-register-format.md` — severity rubric, confidence levels, and the exact register table + executive-summary format.
- `references/risk-taxonomy.md` — the integration-seam catalog, the unhandled-error and silent-assumption checklists, and the "defend the design" prompts, with language-specific tells.