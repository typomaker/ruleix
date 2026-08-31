# Project documentation governance

This document is the canonical source for project and agent workflow rules.
`AGENTS.md` should remain a short entry point linking here instead of
duplicating detailed rules. New workflow rules should normally be added to
this document or another focused document under `docs/`, with `AGENTS.md`
updated only when a new canonical entry point is required.

## Agent workflow

- After fully completing each assigned task, create a Git commit containing all
  changes related to that task.
- Before committing, run the appropriate checks and `git diff --check`.
- Do not include unrelated user changes in the commit.
- Document every change, report, verification, and experiment in `docs/` as
  part of the same task. Update an existing document when possible; create a
  focused document when no suitable canonical document exists.
- When adding or changing a benchmark, include a nearby comment with the latest
  local results and enough run parameters to make future measurements
  comparable. Update the relevant canonical document in `docs/` as well.
- Before removing a performance experiment that regresses its target or a gate
  workload, profile the reproducible parent and candidate under comparable
  conditions. Use focused profiles, microbenchmarks, or assembly inspection if
  needed to localize the delta. Record confirmed causes as measured findings
  and unresolved explanations as hypotheses; a regression may be rejected
  without a conclusive cause, but not without the profiling attempt.
- When a change reveals a performance regression or degradation, profile the
  affected workload before completing the task, identify and document the
  cause when the evidence permits, and attempt a correction when that cause is
  understood and the correction is practical and in scope. Re-run the same
  workload after the correction; clearly record unresolved causes or an
  intentionally accepted regression.

## Documentation ownership

The `docs/` directory is the canonical source of truth for Ruleix. Code,
benchmarks, and task reports must not be the only record of a completed change,
verification, or experiment.

Every task must update the relevant canonical document in the same commit:

- architecture and implementation changes belong in
  [`index-architecture.md`](index-architecture.md);
- accepted and rejected optimization experiments, their evidence, and their
  rationale belong in
  [`optimization-decisions.md`](optimization-decisions.md);
- comparable release and checkpoint measurements belong in
  [`performance-history.md`](performance-history.md);
- focused subsystem contracts and designs belong in the corresponding document
  under `docs/`, or in a new focused document when no suitable one exists.

Documentation must distinguish measured results from hypotheses. A performance
claim must include or reference the environment, benchmark command, run
parameters, baseline and candidate revisions, and relevant time/allocation/
retained-memory results. Rejected experiments remain documented so that the
same approach is not repeated without new evidence or materially different
conditions.

Detailed chronological working notes may remain elsewhere in the repository,
but they do not replace the maintained canonical summary in `docs/`.
