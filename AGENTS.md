# Agent Instructions

- After fully completing each assigned task, always create a Git commit containing all changes related to that task.
- Before committing, run the appropriate checks and `git diff --check`.
- Do not include unrelated user changes in the commit.
- The `docs/` directory is the canonical source of truth for the project. Every
  change, report, verification, and experiment must be documented there as part
  of the same task. Update the relevant existing document when possible; create
  a new focused document when no suitable canonical document exists.
- Record accepted and rejected optimization decisions, including the evidence
  and rationale, in `docs/optimization-decisions.md`.
- Record comparable release-to-release and release-to-checkpoint performance
  changes in `docs/performance-history.md`; do not infer missing measurements
  from incomparable benchmarks.
- When adding or changing a benchmark, always include a nearby comment with the
  latest local results and enough run parameters to make future measurements
  comparable. Also update the relevant canonical document in `docs/`.
