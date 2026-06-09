# IaC risk checklist (Phase 5, expanded)

Work through every section; report findings as `severity — file:line — what — why it matters — mitigation`. Severity: **High** = can lose data or apply the wrong thing silently; **Medium** = drift/maintenance trap; **Low** = hygiene.

## 1. Dead declarations (use onboard:dead_code)

| Finding | Severity | Why |
|---|---|---|
| `variable` never read inside its module | Medium | Every caller setting it is doing nothing; usually a leftover from a refactor that will mislead the next editor |
| `local` with no reference | Low | Noise, safe to delete |
| `output` with no in-repo consumer | Low–Medium | May feed remote state, CI, or humans — verify before deleting; if nothing consumes it anywhere, it is API surface that must be maintained for no one |

## 2. Version discipline (use onboard:deps)

- Providers declared without a version constraint → High (any upgrade can land silently).
- `required_version` missing or unbounded above (`>= 1.x` with no `< 2.0`) → Medium.
- Lock file present at the root but missing in modules CI runs independently → Medium.
- Declared constraint and locked version disagree in spirit (e.g. `~> 0.6` but locked at a version CI never re-validates) → Low, note it.
- Terraform vs OpenTofu ambiguity (both lock-file names present, or docs say one and CI runs the other) → High for onboarding correctness.

## 3. Hidden coupling (grep patterns)

The syntactic graph cannot see these; grep and report found / ruled out:

- `TF_VAR_` — CI-injected variables (pipelines, Makefiles, docs).
- `data "vault_` / `data "aws_ssm_parameter"` / `data "google_secret_manager` — apply-time secret fetches; an apply can fail or change behavior with zero diff in the repo.
- `read_terragrunt_config(` — config indirection the include-chain map does not show.
- `terraform_remote_state` — cross-stack (or cross-repo) state readers; renaming an output breaks them invisibly.
- `provider "` with `alias` — multi-region/multi-account wiring that module composition hides.
- `generate "` blocks — code that exists only after terragrunt runs; the repo on disk is not the code that applies.

## 4. Lifecycle hazards (read the resources)

- Stateful resources (databases, buckets with data, KMS keys, volumes) without `lifecycle { prevent_destroy = true }` → High.
- `for_each`/`count` keyed on list order or on a value derived from a mutable map → Medium-High (reorders destroy/recreate).
- Conditional platform routers (`count = var.platform == "x" ? 1 : 0`) → Medium; flag that flipping the variable destroys one platform's resources while creating the other's.
- `create_before_destroy` absent where replacement causes outage (eips, certs, LB targets) → Medium.

## 5. State & identity

- Two units that could resolve to the same state key (templated keys with optional path segments) → High.
- State bucket/container without versioning or with shared write access across env classes → Medium (verify outside the repo; flag as unverifiable if you cannot).
- Identity-from-path conventions (cluster id derived from directory path): renaming/moving a directory re-keys state → say this out loud in the walkthrough.

## 6. Test & policy gaps

- Modules with no `*.tftest.hcl` and no harness coverage — list them all, worst (most-depended-on, per onboard:repo_map rank) first.
- Policies in `policies/` not referenced by any CI job — enforcement theater.
- CI that plans but never applies from a clean runner (apply-only-from-laptop smell) — check pipeline configs.
