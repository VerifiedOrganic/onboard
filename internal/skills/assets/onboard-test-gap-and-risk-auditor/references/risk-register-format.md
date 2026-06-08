# Risk register format

The register is the deliverable. Make it scannable, ranked, and actionable. Lead with the executive summary, then the table, then optional theme notes.

## Table of contents
- Severity rubric
- Confidence levels (the syntactic ceiling)
- Executive summary format
- The register table
- Worked example
- Anti-patterns to avoid

## Severity rubric
Assign severity from **blast radius × likelihood × damage-if-wrong**, not from gut feel.

| Severity | Meaning | Typical shape |
|----------|---------|---------------|
| **Critical** | A live, load-bearing path that is untested or unguarded AND mutates state, moves money, touches auth, or deletes/writes external data. A regression here is silent and expensive. | High `impacted_count`, zero `at_risk_tests`, on a write/auth path. |
| **High** | Reachable-but-untested logic on a real flow, or a seam where the two sides demonstrably disagree, or an unhandled error on a path that will be hit in normal operation. | Untested branch on a core flow; FE/BE type mismatch; swallowed exception on the happy path's failure mode. |
| **Medium** | Untested code that is reachable but peripheral, a plausible-but-unconfirmed assumption, or a design incoherence that bites only under specific conditions. | Untested read-only helper; assumed-sorted input; duplicated logic that could drift. |
| **Low** | Hygiene and latent fragility — narrow edge cases, defensive gaps with low likelihood, cosmetic seam mismatches. | Missing test for an unlikely nil; inconsistent error wrapping. |

If two findings tie, the one with the larger `impacted_count` ranks higher. Do not inflate severity to get attention; an honest Medium keeps the register trustworthy.

## Confidence levels (the syntactic ceiling)
Every row carries a confidence, because `trace_flow`/`impact` edges are syntactic — matched by name and lexical scope, not type-checked. They miss dynamic dispatch, interfaces/virtuals, reflection, string-keyed dispatch, DI, and runtime callbacks; they over-match same-named symbols.

- **Confirmed** — you read the code and verified it directly (e.g. you saw the `catch {}` that swallows the error).
- **Likely** — the call graph strongly implies it and nothing suggests a hidden edge.
- **Possible** — depends on an edge the graph may not see; could be a false alarm. Say *why* ("reaches via an interface `trace_flow` can't resolve").

When in doubt, label **Possible** and let the human dismiss it. A false alarm costs a glance; a silent miss costs an outage.

## Executive summary format
Three to five lines at the very top, before the table:
1. Overall posture in one sentence ("~40% of reachable write paths have no test crossing them").
2. The top 2–3 themes (e.g. "the queue-consumer seam and the billing path concentrate the risk").
3. The single scariest finding, named with its location.
4. One honesty line on the ceiling ("reachability is syntactic; N edges were unresolved — flagged Possible").

## The register table
One row per finding, **sorted Critical → Low**. Use this exact column set:

```markdown
| # | Severity | Location (file:line) | Category | Why it's risky | Suggested mitigation | Confidence |
|---|----------|----------------------|----------|----------------|----------------------|------------|
```

Column rules:
- **Location** — a real `file:line` (or `file:startLine` for a region). Never "somewhere in the auth module." If you genuinely can't pin a line, pin the symbol and say so.
- **Category** — one of: Untested live path · Load-bearing + thin coverage · Unhandled error path · Fragile seam · Silent assumption · Design risk.
- **Why it's risky** — one sentence, concrete. Name the failure, not the smell.
- **Suggested mitigation** — an action a developer can start today ("add a test asserting `chargeCard` returns an error when the gateway 5xxes"), not a platitude.
- **Confidence** — Confirmed / Likely / Possible, with the reason when it's Possible.

## Worked example

> **Posture:** 3 of 7 reachable write paths have no test crossing them; error handling on the payment-gateway client is the dominant theme. Scariest: `chargeCard` is load-bearing (impacted_count 14) with zero at-risk tests. Reachability is syntactic — 2 edges through the `Notifier` interface are unresolved and flagged Possible.

| # | Severity | Location (file:line) | Category | Why it's risky | Suggested mitigation | Confidence |
|---|----------|----------------------|----------|----------------|----------------------|------------|
| 1 | Critical | `billing/charge.go:142` | Load-bearing + thin coverage | `chargeCard` has 14 transitive callers and 0 at-risk tests; a regression silently mischarges. | Add tests for success, gateway-5xx, and timeout branches before any edit. | Likely |
| 2 | High | `api/handlers/order.go:88` | Unhandled error path | `json.Unmarshal` error is ignored; a malformed body proceeds with a zero-value order. | Return 400 on decode error; add a malformed-body test. | Confirmed |
| 3 | High | `worker/consume.go:31` ↔ `api/publish.go:64` | Fragile seam | Producer sends `amount_cents` (int), consumer reads `amount` (float) — silent unit/shape drift. | Share one struct or a schema; add a round-trip test across the seam. | Confirmed |
| 4 | Medium | `util/sort.go:12` | Silent assumption | `binarySearch` assumes a sorted slice; callers don't guarantee it. | Document the precondition and assert in debug, or sort defensively. | Likely |

## Anti-patterns to avoid
- **Unranked dumps.** A flat list of everything is noise; rank or it didn't ship.
- **Vague locations.** "Error handling is weak" with no `file:line` is unactionable.
- **False certainty.** Presenting a syntactic guess as proof. Use the confidence column.
- **Severity inflation.** Marking everything Critical destroys the signal the ranking exists to provide.
- **Mitigations that restate the problem.** "Improve coverage" is not a mitigation; "test the 5xx branch at line 142" is.
