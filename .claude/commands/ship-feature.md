---
description: Run the full disciplined pipeline for a feature — specifier, coder, cleaner, architect, hardener — in strict order.
argument-hint: <feature description>
---

Ship the following feature through the full pipeline, in this exact
order, with no steps skipped and no steps reordered:

```
specifier → coder → cleaner → architect → hardener
```

Feature: $ARGUMENTS

## Rules for this run

1. **Call each subagent explicitly, one at a time.** Do not let a
   subagent implicitly decide to act as the next one. You (the main
   agent) are responsible for invoking `@specifier`, then `@coder`,
   then `@cleaner`, then `@architect`, then `@hardener`, in that order.

2. **Do not proceed to the next stage until the current stage is
   actually done**, per that agent's own completion criteria — not per
   your impression of the conversation. Concretely:
    - `specifier` is done only once the user has explicitly approved the
      spec. If they haven't, stop and wait — do not call `coder`.
    - `coder` is done only once `./scripts/gate.sh <module> fast` passes for every module the change touches.
    - `cleaner` is done only once it reports a before/after CRAP table
      and the gate still passes.
    - `architect` is done only once it delivers an explicit verdict.
      If the verdict is `CHANGES REQUIRED`, go back to `coder` with the
      task list — do not call `hardener`.
    - `hardener` is done only once it reports survivor counts before and
      after, and `./scripts/gate.sh <module>` (full, with mutation testing) passes for every module the change touches.

3. **If any stage fails and cannot be fixed within that stage's own
   role**, stop and report to the user rather than improvising a fix
   from a different role. E.g. if `coder` can't make a test pass because
   the spec itself seems wrong, that goes back to the user, not into
   `coder` silently reinterpreting the spec.

4. **Give a short status line after each stage** — which agent ran,
   pass/fail, one line on what happened — so the user can follow the
   pipeline without reading every subagent transcript.

5. At the end, summarize what shipped: files changed, final CRAP/coverage
   numbers (as printed by `./scripts/crap.sh` and the gate), mutation survivors if `hardener` ran, and the architect's
   final verdict.

If the feature is trivial (a small, well-understood fix), say so and
propose the shortened `coder → cleaner` path (`/quick-fix`) instead of
running the full pipeline — but only propose it, don't decide it
unilaterally. Wait for the user to confirm before skipping stages.
