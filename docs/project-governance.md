# Project documentation governance

This document is the canonical source for project and agent workflow rules.
`AGENTS.md` should remain a short entry point linking here instead of
duplicating detailed rules. New workflow rules should normally be added to
this document or another focused document under `docs/`, with `AGENTS.md`
updated only when a new canonical entry point is required.

## Agent workflow

- Complete the entire task as assigned, including every explicitly requested
  deliverable and acceptance criterion. Do
  not unilaterally reduce the task to a convenient slice, milestone, or partial
  implementation and present that subset as completion. Small, reviewable
  commits and intermediate checkpoints are allowed, but continue working after
  them until the full assigned outcome is achieved. Stop short of full
  completion only when the user explicitly narrows the scope or progress is
  genuinely blocked under the rules below; in that case, state exactly what
  remains and why.
- After fully completing each assigned task, create a Git commit containing all
  changes related to that task.
- Before committing, run the appropriate checks and `git diff --check`.
- Changed production code must have at least 90% test coverage. Measure coverage
  over the executable lines added or modified by the task (diff coverage), not
  the repository-wide aggregate. Add or update tests before completing the task
  when the changed-code coverage is below this threshold.
- Do not include unrelated user changes in the commit.
- Repository files must not exceed 500 lines. New files must comply immediately;
  an existing oversized file must be brought within the limit as part of any
  task that changes it. Move logically related content into focused modules or
  documents; if the excess consists of obsolete historical data, remove it
  instead.
- Document every change, report, verification, and experiment in `docs/` as
  part of the same task. Update an existing document when possible; create a
  focused document when no suitable canonical document exists.
- When adding or changing a benchmark, include a nearby comment with the latest
  local results and enough run parameters to make future measurements
  comparable. Update the relevant canonical document in `docs/` as well.
- A measured performance regression must first be reproduced against both the
  candidate commit and its previous commit under comparable conditions. Record
  the exact revisions, environment, commands, run parameters, and results. Use
  interleaved runs when environmental drift could explain the difference; a
  sequential series with unexplained drift is not sufficient evidence of a
  regression.
- Before removing or rejecting a performance experiment, collect comparable
  CPU and, when allocations or retained memory are affected, allocation or
  memory profiles for both the candidate commit and its previous commit. Use
  focused profiles, microbenchmarks, traces, or assembly inspection as needed
  to identify the specific additional work, allocation, retention, or changed
  layout responsible for the regression. Merely observing different benchmark
  numbers or a changed physical representation does not identify the cause.
- After localizing the bottleneck, attempt to remove, correct, or mitigate it
  and then repeat the same benchmark and profile comparison. Continue with
  reasonable in-scope investigations and fixes while they remain available;
  an initial failed fix is not grounds for rejection.
- A performance experiment may be rejected only when the regression's cause is
  conclusively established and documented as an expected or inherent
  consequence of the candidate design, and reasonable correction attempts did
  not remove it. Unresolved explanations must be recorded as hypotheses and
  keep the experiment under investigation; they must not be used to justify
  rejection. If conclusive attribution requires unavailable infrastructure or
  external input, report the work as blocked rather than rejected.
- Degradation or regression in any search function or search path is not
  acceptable. This includes correctness, observable behavior, latency,
  allocations, and retained-memory characteristics across all public search
  APIs and their supported modes. Such a regression must be corrected before
  completing the task, or the causing change must be rejected; it must not be
  intentionally accepted.
- Build latency and transient-allocation regressions may be accepted when they
  are the measured cost of a material search-quality or search-performance
  improvement explicitly prioritized by the project owner. The decision must
  compare both revisions, profile the added Build cost, preserve retained-memory
  limits and correctness, and be recorded in the canonical performance and
  optimization documents. This exception never permits a search regression.

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
