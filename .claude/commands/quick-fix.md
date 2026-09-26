---
description: Run the shortened pipeline for a small, well-understood fix, using the same agents as ship-feature.
argument-hint: <fix description>
---

Ship the following fix through the shortened pipeline, in this exact
order, with no steps skipped and no steps reordered:

```
coder → cleaner → architect
```

Fix: $ARGUMENTS

This is the same pipeline and the same agents as `/ship-feature` — do
not invent new agents. What differs is **which stages run**:
`specifier` and `hardener` are both dropped.

- No `specifier` because this command is for a small, well-understood
  fix with no new behavior to spec out — not a substitute for judgment.
  If, once you're in it, the fix turns out to need a `.feature` file
  (new user-visible behavior, a scenario worth locking in), stop and
  say so rather than pushing it through without one. Don't decide
  silently either way — flag it to the user and let them redirect to
  `/ship-feature` if a spec is actually warranted.
- No `hardener` because mutation testing is the heaviest stage in the
  pipeline and isn't proportionate to a small, well-understood fix. If,
  once you're in it, the change touches logic that clearly warrants
  mutation coverage (new branching, a fix to something that already
  has hardener coverage), stop and say so rather than deciding
  unilaterally to skip it — let the user redirect to `/ship-feature` if
  hardening is actually warranted.

`architect` is still the last stage — a shortened pipeline is not a
license to skip the boundary check.

## Rules for this run

1. **Call each subagent explicitly, one at a time.** Do not let a
   subagent implicitly decide to act as the next one. You (the main
   agent) are responsible for invoking `@coder`, then `@cleaner`, then
   `@architect`, in that order.

2. **Do not proceed to the next stage until the current stage is
   actually done**, per that agent's own completion criteria — not per
   your impression of the conversation. Concretely:
    - `coder` is done only once `./scripts/gate.sh <module> fast` passes for every module the change touches.
    - `cleaner` is done only once it reports a before/after CRAP table
      and the gate still passes.
    - `architect` is done only once it delivers an explicit verdict.
      If the verdict is `CHANGES REQUIRED`, go back to `coder` with the
      task list — the pipeline is not done.

3. **If any stage fails and cannot be fixed within that stage's own
   role**, stop and report to the user rather than improvising a fix
   from a different role. E.g. if `coder` can't make a test pass
   because the requested fix is underspecified or turns out to be
   larger than expected, stop and say so — don't have `coder` guess at
   scope, and don't silently escalate to running `specifier` or
   `hardener` yourself.

4. **Give a short status line after each stage** — which agent ran,
   pass/fail, one line on what happened — so the user can follow the
   pipeline without reading every subagent transcript.

5. At the end, summarize what shipped: files changed, final CRAP/
   coverage numbers (as printed by `./scripts/crap.sh` and the gate), and the architect's final verdict. State
   explicitly that `hardener` did not run as part of this quick-fix.

If, once underway, the fix turns out not to be small and well-understood
after all — the change is larger than expected, needs a spec, or
touches logic that warrants mutation testing — stop and say so, and
propose switching to `/ship-feature` instead of pushing on regardless.
