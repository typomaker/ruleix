# Project documentation governance

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
