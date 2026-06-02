---
name: architecture-cartographer
description: Draws durable, committable diagram-as-code (Mermaid) of a codebase's architecture — C4 context/container/component diagrams, flowcharts for request and job flows, erDiagram for database schemas, and package/module dependency graphs. Use when someone says "draw a diagram", "draw an architecture diagram", "map the architecture", "give me a mermaid diagram", "generate a dependency graph", "draw the ERD", "diagram the database schema", or "diagram the request flow", or wants a static visual artifact they can commit and render on GitHub or in docs. Produces Mermaid source plus a legend mapping every node to a real file or directory; edges come from the onboard code graph and unconfirmable edges are drawn dashed and labeled inferred. NOT the interactive clickable HTML map or the teaching walkthrough (both codebase-walkthrough), NOT per-change blast radius (dependency-impact-analyzer), NOT a risk register (test-gap-and-risk-auditor), NOT the cached-guide update loop (guide-maintainer).
---

# Architecture Cartographer

Draw durable architecture diagrams **as code** — Mermaid the user commits to the repo and renders on GitHub, in docs, or in a wiki. The deliverable is diagram source plus a legend that maps every node to a real path. Not a picture, not a lecture.

The bar for "done": a reviewer who has never seen the repo can read the diagram and the legend, point at any node, and find the actual file or directory it stands for. Every edge is either drawn from the onboard code graph or explicitly marked as inferred. Nothing on the canvas is decorative.

## What this skill is and is NOT

- This skill **draws static diagram-as-code**. Output is committable text — Mermaid `flowchart`, `C4Context`/`C4Container`/`C4Component`, `erDiagram`, dependency graph — plus a legend. It lives in the repo and renders anywhere Mermaid renders.
- It is **NOT** the interactive clickable HTML map. Pan/zoom, detail panels, in-browser click-through belong to **codebase-walkthrough**'s interactive-map mode. If the user wants to *click around and explore*, route them there.
- It is **NOT** a phase-by-phase teaching walkthrough. If the user wants to *understand the whole codebase* conversationally, that is **codebase-walkthrough** too.
- It is **NOT** a per-change blast-radius report (**dependency-impact-analyzer**), a whole-repo risk register (**test-gap-and-risk-auditor**), or the cached-guide delta loop (**guide-maintainer**).

Litmus test: "help me understand X" → walkthrough. "draw / diagram / map X as an artifact I keep" → you are in the right place.

## Step 0 — Pin down altitude and scope

Before drawing, settle two things. Ask only if genuinely ambiguous; otherwise infer from the request and confirm in the delivery.

1. **Altitude** — how high are we standing? This picks the diagram type:

| User is asking about | Altitude | Diagram type |
|----------------------|----------|--------------|
| How the system sits among users + external systems | System context | C4 Context (`C4Context`) |
| The deployable/runnable pieces (services, DBs, queues, frontend) | Container | C4 Container (`C4Container`) |
| Inside one service: its major modules/classes | Component | C4 Component (`C4Component`) or `flowchart` |
| A request / job / event moving end to end | Flow | `flowchart` (or `sequenceDiagram`) |
| The database / persistence model | Schema | `erDiagram` |
| Which packages/modules import which | Dependencies | `flowchart` (dependency graph) |

See `references/diagram-catalog.md` for the syntax skeleton and a worked example of each.

2. **Scope** — the whole system, one service, or one flow? "Everything" at component altitude is unreadable. Narrow until it fits the node budget in Step 3.

## Step 1 — Recon the structure

Run **onboard:recon(root)** first, always. It returns `{stack, frameworks, entry_points, test_layout, tooling, dir_tree, file_count}` from manifests only — no deep source reads. Use it for:

- The real top-level package/directory boundaries — candidate nodes for container and dependency diagrams.
- Entry points — where flow diagrams start.
- Framework fingerprints that tell you what the external actors and containers actually are: a web framework implies an HTTP-client actor; an ORM implies a database container; a Stripe/SMTP SDK implies that external system.

`recon` gives you boundaries, not edges — it does not trace calls or imports. Treat `dir_tree` + `frameworks` as ground truth for the *nodes* of the coarse map; get the *edges* from Step 2. Do not invent structure from directory names alone.

## Step 2 — Get edges from the code graph

Edges are the part people get wrong, so source them, do not guess. For several diagram types onboard now extracts the structure as **facts** — prefer these grounded extractors over inferring from names or reading files by hand:

- **External-dependency graph** — call **onboard:deps(format="mermaid", root=...)**. It parses the manifests (go.mod, package.json, requirements.txt, Cargo.toml) and returns the direct dependencies per manifest, optionally as a flowchart. These are facts, not inferred — use them for the "what third-party things does this pull in" diagram.
- **Internal package / module map** — call **onboard:render_map(topic, format="mermaid", root=...)** with `nodes`/`edges` omitted. It auto-derives a package-level dependency map from the code graph and returns Mermaid source directly. (deps = *external* libraries from manifests; render_map = *internal* package edges from the code graph — they answer different questions.)
- **erDiagram** — call **onboard:schema(root=...)** first. It parses SQL DDL (CREATE TABLE / migrations) into entities, columns, primary/foreign keys, and relationships, and returns a Mermaid `erDiagram` directly — cardinality comes from the actual foreign keys. Only fall back to reading model/migration files by hand when the schema lives in ORM models the DDL parser does not see (and say so).
- **API surface / route map** — call **onboard:routes(root=...)** to extract method+path+location across common frameworks. Treat it as recall-oriented (it is a cross-framework heuristic, not a parser — it can miss bespoke routing), but it grounds an endpoint map far better than reading routers by hand.
- **Component or flow diagram** — *you* decide what belongs on the canvas, then hand the explicit `nodes` and `edges` to **onboard:render_map(... format="mermaid")**, which renders the layout you authored (it does not invent your component boundaries). To find the edges for a flow, run **onboard:trace_flow(entry, depth?, root?)** from the entry symbol; it returns a breadth-first list of likely callees. Use that to lay out the flow, then collapse the long callee list into 5–12 conceptual nodes.
- **Sequence diagram** — call **onboard:trace_flow(entry, format="mermaid", root?)**, which renders the trace directly as a Mermaid `sequenceDiagram`. The ordering reflects breadth-first reach, not strict runtime sequence, so tidy it into the real call order before committing. For Go, add `precise=true` to resolve interface dispatch the syntactic trace misses.

**Facts vs. the code-graph ceiling (state it honestly).** The extractors differ in how much you can trust them:

- **`deps` and `schema` are facts** — parsed straight from manifests and DDL. Draw their edges solid and confident.
- **`routes` is a recall-oriented heuristic** — good for an endpoint map, but it can miss bespoke routing and occasionally over-match; note that.
- **`trace_flow` and `render_map` edges are syntactic** — matched by name and lexical scope, not type-checked. For Go, `precise=true` upgrades them to type-checked (proven, incl. interface dispatch); otherwise:

- Treat every syntactic edge as **likely**, not proven.
- Dynamic dispatch, interface/virtual calls, reflection, DI containers, and event buses can hide real edges the graph never saw, and can invent edges between same-named symbols that never actually call each other.
- The auto-derived dependency map is import-based, which is steadier than call resolution, but it is still syntactic — conditional, generated, or reflectively-loaded imports can be missed or spurious.
- If you draw an edge you could not confirm in the graph (e.g. "the queue worker eventually writes the DB," inferred from naming), **mark it inferred** — see Step 4.

Do not reach for `onboard:impact` here — per-change blast radius is **dependency-impact-analyzer**'s job, not cartography.

## Step 3 — Reduce to a legible diagram (5–12 nodes)

A diagram is a reduction, not a dump. Hold each diagram to **5–12 nodes**:

- Fewer than 5 is too coarse to be worth committing.
- More than 12 stops being a map and becomes noise. **Split it** — one diagram per altitude or per subsystem — rather than cramming. A repo usually wants a small *set* of diagrams (context, then container, then a flow or two), not one giant one.

Node rules:

- **Every node label is a concept a human would name** — "Auth Service", "Order Queue", "Postgres (orders)" — not a raw filename and not a class-signature dump.
- **Every node cites a real file or directory.** That mapping lives in the legend (Step 4), but you must be able to point to the path before you draw the node. No path, no node.
- Collapse incidental helpers into the concept they serve. The diagram shows boundaries and relationships, not every function.

## Step 4 — Emit diagram-as-code + legend

Deliver two things, ready to paste into the repo:

1. **The Mermaid source**, in a fenced ` ```mermaid ` block so it renders on GitHub and in most docs tools. If the user named an output file, also write it via **onboard:render_map(..., format="mermaid", output_path=...)**; otherwise emit it inline and offer to write it to a path (e.g. `docs/architecture/<topic>.md`).

2. **A legend** — a table mapping every node to its real path and a one-line description, plus a line declaring edge provenance:

```
| Node | Path | Notes |
|------|------|-------|
| Auth Service | internal/auth/ | session issue + verify |
| Postgres (orders) | (external) | orders + line_items tables |

Edges: solid = present in the code graph; dashed (`A -.-> B`) = inferred, not confirmed — <which ones and why>.
```

Use a dashed link (`A -.-> B`) for any inferred edge and call it out in the legend, so a committer knows exactly what was confirmed versus assumed.

3. **Where to put it.** Recommend committing the diagram into the repo (e.g. `docs/architecture/`, a `README` section, or an ADR) so it lives beside the code and renders in review. That permanence is the whole point of this skill versus the throwaway interactive map.

## Principles

- **Map before code.** A diagram request is a request for the shape of the system, not a code tour.
- **Choose altitude before syntax.** The wrong diagram type drawn beautifully is still the wrong diagram.
- **Reduce ruthlessly.** 5–12 named nodes. If it does not fit, split it; never cram.
- **Every node is a real path.** No node ships without a legend entry pointing at a file or directory.
- **Graph edges, honestly labeled.** Solid = present in the code graph; dashed = inferred. Never present a syntactic match as proof.
- **Diagram-as-code, meant to be committed.** Plain Mermaid the user owns and version-controls — not an interactive HTML toy (that is codebase-walkthrough's job).
- **Keep the user independent.** A good diagram plus legend lets them update it themselves the next time the code moves.

## Reference files

- `references/diagram-catalog.md` — syntax skeleton + worked example for each diagram type (C4 context/container/component, flowchart flow, erDiagram, dependency graph), and the altitude→type decision guide.