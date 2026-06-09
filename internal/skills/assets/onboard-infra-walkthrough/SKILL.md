---
name: onboard-infra-walkthrough
description: Walk a developer top-down through an infrastructure-as-code repository (Terraform, Terragrunt, OpenTofu) — deployable stacks, the module graph, state and environment layout, end-to-end apply traces, and IaC-specific risk such as unpinned providers and unread variables. Use whenever someone says "walk me through this Terraform repo", "onboard me onto our infrastructure", "explain our Terragrunt setup", "how do our environments work", or "what does this IaC repo deploy". For application codebases route to onboard-codebase-walkthrough instead; for committable diagrams of an IaC repo use onboard-architecture-cartographer; for "what breaks if I change this variable/module/output" use onboard-dependency-impact-analyzer.
---

# Infrastructure Walkthrough

Take a developer top-down through a Terraform/Terragrunt/OpenTofu repository until they can reason about it without help. The bar for "done" is that the user can name every deployable unit, redraw the module graph from memory, explain where state lives and how an environment differs from its siblings, and predict the blast radius of changing a variable or module output.

Infrastructure repos invert the usual onboarding problem: there is no "request flow" to trace — the unit of behavior is a **stack** (a Terragrunt unit or Terraform root module), and the architecture lives in module composition, include chains, and state layout rather than call stacks. The phases below mirror onboard-codebase-walkthrough but speak that language.

## Step 0 — Output mode

Same three modes as the codebase walkthrough (conversational / cached guide / interactive map); ask unless the user already said. The cached-guide and interactive-map mechanics are identical — reuse `onboard:guide_read`/`guide_write`/`guide_delta` and `onboard:render_map`. Default to conversational.

## Phase 1 — Reconnaissance

- `onboard:recon` — confirms the stack (Terraform/OpenTofu, Terragrunt), entry points (every `terragrunt.hcl` is a deployable unit), test layout (`*.tftest.hcl`, `tests/`), and tooling (Make targets, TFLint, OPA/Conftest policies, CI).
- `onboard:stacks` — the deploy surface in one call: each unit's module source, include chain, inter-stack dependencies, state backend, state-key pattern, and input names. This is the IaC analogue of an API-surface scan; lead with it.
- `onboard:deps` — providers with declared constraints AND lock-file pins (kind `provider` vs `locked`), plus any external (registry/git) module sources. Mismatched or missing pins surface here.

Establish immediately and say out loud: which binary (Terraform vs OpenTofu — check `required_version`, lock-file names, `.tofu` files), where state lives, and how many deployable units exist.

## Phase 2 — Behavior from tests and policies

The spec of an IaC repo is its tests *and its policies*:
- Native tests (`*.tftest.hcl`) and harnesses under `tests/` say what the modules claim to guarantee.
- OPA/Conftest rules under `policies/` (and tflint configs) are enforced invariants — read them; they encode the rules humans decided matter (pinned providers, no plaintext secrets, inventory shape).
- Flag every module with NO test and every policy that exists but is not wired into CI.

## Phase 3 — Architecture: environments × modules

Two orthogonal maps, both required:
- **Environment tree**: how `environments/` (or equivalent) is laid out, what each level of the Terragrunt include chain contributes (root → env → region → pod ...), and how a unit's filesystem path becomes its identity (cluster name, state key, IAM/Vault role names). `onboard:stacks` gives the include chains; read the root config once to explain the derivation.
- **Module graph**: `onboard:repo_map` ranks the load-bearing symbols (a router/orchestrator module's variables and the most-consumed outputs rise to the top); `onboard:render_map` draws the directory-level graph — for IaC repos its nodes are modules and stacks. Name the layering convention if one exists (primitives → platform modules → composition root).

## Phase 4 — End-to-end traces (the core)

Trace 2–3 flows the way an apply actually evaluates them, naming files and addresses:
1. **A deploy**: pick one stack from `onboard:stacks`; walk leaf `terragrunt.hcl` → include-chain merge (which level sets which input) → module source → the modules it instantiates → the providers that do the work. `onboard:trace_flow` from the stack symbol lays the path; `onboard:context_pack` on a pivotal module call pulls its variables/outputs neighborhood in one shot.
2. **A value**: follow one input (e.g. a CIDR or node map) from where a human writes it to the resource argument that consumes it — through inputs, variable, locals transforms, module argument, output. This teaches the variable-wiring discipline better than any overview.
3. **State and identity** (if Terragrunt): how `remote_state`/generate blocks produce backend config per unit, and what guarantees uniqueness of state keys.

## Phase 5 — The negative space

IaC-specific risk; check each and report findings with file:line:
- `onboard:dead_code` — variables never read inside their module (setting them does nothing), unreferenced locals, outputs nothing in-repo consumes (caveat remote-state/CI readers).
- Unpinned or drifting versions: `required_version` ranges, providers declared without constraints, lock files missing or inconsistent across modules (`onboard:deps` shows declared vs locked side by side).
- Untested modules (Phase 2 gap list) and stacks whose plan is never exercised in CI.
- Hidden coupling the graph cannot see: `TF_VAR_*` injected by CI, secrets fetched at apply time (Vault/SSM data sources), `read_terragrunt_config` indirection, remote-state readers in *other* repos, provider aliases. Grep for these; say which were found and which were ruled out.
- Lifecycle hazards: missing `prevent_destroy` on stateful resources, `count`/`for_each` keyed on values that can reorder, conditional module instantiation (platform routers) where both branches share names.
- `onboard:history` — churn concentrates in the modules that are still being figured out; point the user there.

## Phase 6 — Check understanding

~5 questions the user should now answer unaided, e.g.: Which file would you edit to add a node to cluster X, and which level of the include chain owns that input? Where is the state for stack Y and what derives its key? What happens to stack Z if module M's output O is renamed? Which binary and version does CI run? Which modules have no tests?

## Principles

- **Stacks before modules, modules before resources.** Altitude discipline is the whole game in IaC repos.
- **The graph is honest but partial**: onboard's HCL edges cover variable/local/module/output wiring and Terragrunt includes/dependencies; registry/git module internals, dynamic `for_each` keys, and `read_terragrunt_config` chains are not followed — say so when it matters.
- **Never print input values.** Inputs routinely carry secret material; reason about names and wiring, quote values only when the user pastes them first.
- **Plan beats prose.** When a claim is checkable with `terraform validate`/`plan` or `terragrunt run-all plan` and the user can run it safely, suggest it rather than asserting.

## Reference files
- `references/iac-risk-checklist.md` — the Phase 5 checklist expanded, with grep patterns and severity guidance
