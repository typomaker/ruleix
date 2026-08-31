# Roadmap

This file is the active implementation plan. Completed work and rejected
experiments belong in [`ROADMAP_HISTORY.md`](ROADMAP_HISTORY.md);
release-facing behavior belongs in [`CHANGELOG.md`](CHANGELOG.md). Do not keep
completed, superseded, or unrelated proposals here.

## Objective

Measure and improve warm `Local` execution from the latest accepted exact
query-key cache implementation. Each experiment must use the immediately
preceding accepted revision as its parent and preserve zero-allocation warm
searches, bounded retained memory, deterministic results, and concurrent search
safety.

The completed selective aggregate and nested `Lossy` policy plan, including
verification results and rejected scoring experiments, is recorded in
[`ROADMAP_HISTORY.md`](ROADMAP_HISTORY.md). Its release-facing behavior is
documented in [`CHANGELOG.md`](CHANGELOG.md), [`README.md`](README.md), and
[`docs/lossy-index.md`](docs/lossy-index.md).

## Warm-Local optimization queue

The current reference is the exact-query-key result-cache implementation: a
same-commit interleaved refresh measured 227.5 ns/op for
`BenchmarkProductionShapeSearch/Local`, with 0 B/op and 0 allocations. Refresh
it again before L2's parent/candidate comparison if machine state moves.

Every step is conditional on the previous step. The parent for an experiment
is always the latest accepted commit, never an older recorded reference.
If an experiment fails its gate, remove it from the active implementation,
record its code, measurements, and rejection reason in
[`ROADMAP_HISTORY.md`](ROADMAP_HISTORY.md), and run the next applicable step
from the last accepted parent. Do not combine two unmeasured ideas in one
candidate.

Before removing any candidate that regresses a measured workload, capture
comparable CPU profiles for the separately prebuilt parent and candidate under
the same workload, CPU setting, and duration. Attribute the additional time to
specific functions or call sites and use focused profiles, microbenchmarks, or
assembly inspection when the top-level profiles do not isolate the cause.
Record the evidence and distinguish a confirmed cause from a remaining
hypothesis. An unexplained regression may still be rejected, but profiling it
is a mandatory part of completing the experiment and must happen while the
candidate is still reproducible.

### L5. Optimize cold and high-churn Local execution

- Use the accepted 256-ID compact-result limit from L4 as the parent; its full
  matrix and verification are recorded in [`ROADMAP_HISTORY.md`](ROADMAP_HISTORY.md).
- Profile cache-miss and alternating-query workloads against the latest
  accepted parent and identify whether ranking, child materialization, bitmap
  intersection, or result enumeration is dominant.
- Implement only the dominant bounded optimization: earlier empty-intersection
  termination, a revised direct-ID/cardinality cutover, or reduced duplicate
  child materialization. Add adversarial tests for inaccurate learned
  cardinalities before changing a threshold.
- Measure cold Local, two-query warm Local, working sets just below and above
  cache capacity, selective, wildcard-heavy, range-heavy, nested-`All`, and
  large-result searches. Verify that the ordinary warm exact-cache hit is not
  lengthened for functionality it does not use.
- Reject intra-search goroutines at this latency unless a standalone prototype
  first shows that useful work exceeds scheduling and synchronization cost.
  Parallelism remains a batch-level optimization and must preserve result order
  and cancellation semantics.

Acceptance: the targeted miss/high-churn workload improves by at least 10%, no
new warm-Local allocation appears, ordinary warm Local and parallel batches do
not regress by more than 3%, retained memory stays within the existing bounded
budgets, and all correctness gates pass. Record the chosen threshold and the
failed alternatives before closing the Local queue.

### Local queue verification gate

For L3--L5, run focused tests plus the following after every candidate and
again after removing any rejected implementation:

```sh
go test ./...
go test -race ./...
go test -run '^$' -bench '^BenchmarkProductionShapeSearch/(Index|Local)$' \
  -benchmem -benchtime=1s -count=5 .
go test -run '^$' -bench '^BenchmarkProductionShapeParallelLocalBatch100$' \
  -benchmem -benchtime=1s -count=5 .
go test -run '^$' -bench '^BenchmarkProductionScaleSearch/' \
  -benchmem -benchtime=500ms -count=3 .
go test -run '^$' -bench '^BenchmarkWarmLocalResultCardinality/' \
  -benchmem -benchtime=1s -count=5 .
go test -run '^$' -bench '^BenchmarkProductionShapeLocalRetainedMemory$' \
  -benchtime=3x -count=3 .
git diff --check
```

For each latency decision, use seven interleaved one-second runs of separately
prebuilt parent and candidate test binaries in addition to the normal gate.
Preserve the exact commands, raw medians, paired win count, bytes/op,
allocations/op, retained-memory totals, machine, and Go version in
`ROADMAP_HISTORY.md`. A later step may rely only on an earlier result that has
passed this gate and been committed.

When a candidate regresses any decision workload, also preserve the parent and
candidate profile commands, comparable profile attribution, and any focused
evidence used to explain the delta. Do not remove the reproducible candidate
until that diagnosis has been captured.
