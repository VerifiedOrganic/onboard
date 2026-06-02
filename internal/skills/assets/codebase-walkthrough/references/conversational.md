# Conversational mode

Deliver the walkthrough live, in chat, one phase at a time. This is the "sit down and teach me" mode.

## Rules
- **Pause between phases.** After each phase, stop and let the user absorb. End with something like "Ready for the next part, or want to dig into anything here?" Do not run all six phases in one wall of text.
- **No code dumps.** Show file paths and function names; show code only when a specific snippet is the clearest way to make a point, and keep it short.
- **Use a sketch for Phase 3.** An ASCII or Mermaid block for the architecture is worth more than paragraphs. If the user wants it clickable, suggest switching to interactive-map mode.
- **Adapt depth to the user.** If they already know the domain, move fast through stack/conventions and spend the time on traces and negative space. If the AI took them into an unfamiliar language/framework, explain idioms as you go.

## Sequence
Run Phases 1–6 from SKILL.md in order. Suggested rhythm:

1. **Phase 1 + 3 together** as "the map" — what this is, entry points, major modules, how data flows. One sketch. No code.
2. **Phase 2** — behavior from the tests, with untested behavior flagged.
3. **Phase 4** — the 3 end-to-end traces, concrete and file-level. This is the longest beat.
4. **Phase 5** — the worry list and the "defend the design" pass.
5. **Phase 6** — the 5 comprehension questions. Wait for answers; correct only where wrong.

## Focusing a subsystem
If the user names a subsystem ("walk me through the payments flow"), scope all phases to that subsystem and its immediate dependencies rather than the whole repo. Still do recon first so the subsystem is placed in context.
