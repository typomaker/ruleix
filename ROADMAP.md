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

### L1. Refresh the baseline and identify the new hot path

- Build benchmark binaries for the same commit and run the production-shaped
  Local, parallel Local, result-cardinality, retained-memory, and full
  production-scale families with fixed CPU, Go version, and benchmark flags.
- Capture a CPU profile for warm production-shaped Local and separate time
  spent in exact query-key validation, cached-ID result copying, fallback child
  lookup, and benchmark/query construction.
- Add a focused benchmark that alternates the two production queries and
  reports first-entry and second-entry exact-cache hits. Do not change executor
  behavior in this step.
- Record medians and profile attribution in `ROADMAP_HISTORY.md`; update stale
  reference numbers elsewhere in this roadmap when the new measurements are
  repeatable.

Acceptance: seven interleaved one-second runs have a stable median, all warm
Local cases remain at 0 B/op and 0 allocations, and the profile confirms that
query-key validation is large enough to measure the next experiment. If exact
validation is below 15% cumulative CPU, skip L2 and choose the largest measured
non-benchmark hot path instead.

### L2. Validate both cached query keys in one pass

- Add an internal two-entry matcher for supported equality, ordered,
  `Between`, and `CompareBy` leaves. Each matcher must read its query getter
  once and compare the value with both cached entries using the operation's
  existing exact equality semantics.
- Carry a two-bit viable-entry mask through schema order and stop as soon as it
  becomes zero. Preserve the existing entry preference when both keys match.
- Keep sampled inspection, exclusions, stale epochs, unsupported operations,
  non-compact results, and cache misses on their current fallback paths.
- Add focused getter-count tests for first-entry hit, second-entry hit, both
  entries equal, early miss, query change, cache invalidation, `CompareBy`
  operator handling, and mixed supported/unsupported schemas.
- Compare a prebuilt candidate with its parent in seven interleaved one-second
  production-shaped runs, then run the complete gate below.

Acceptance: production-shaped warm Local improves by at least 5% with at least
five of seven paired wins, remains at 0 B/op and 0 allocations, changes retained
warm Local memory by no more than 1%, and causes no repeatable regression above
3% in parallel, cardinality, scale, or fallback workloads. Otherwise reject L2
and use the L1 parent for L3.

### L3. Fuse exact-key validation for the complete `All` schema

- Re-profile the accepted L2 implementation, or the L1 parent if L2 was
  rejected. Proceed only if per-child capability dispatch or generic matcher
  calls account for at least 10% cumulative warm-Local CPU.
- Compile an immutable root validator during `Build` that evaluates the same
  two-entry viable mask without repeating per-search capability discovery.
- Keep inspector wrappers and unsupported children as explicit boundaries; do
  not generate unsafe code, add a public planner API, or specialize only the
  production benchmark schema.
- Test empty and nested `All`, 1/8/16/32 supported children, mixed supported
  and unsupported leaves, sampled inspectors, stale cache epochs, and
  concurrent searches.
- Run strict interleaved A/B against the latest accepted parent, not against
  the pre-L2 baseline.

Acceptance: the incremental gain over that parent is at least 5%, warm Local
stays at 0 B/op and 0 allocations, `Index.Build` latency and retained index
bytes do not regress by more than 3%, and every standard Local gate remains
within its threshold. If dispatch is below the profiling threshold or the A/B
gate fails, record and reject L3 before L4.

### L4. Tune compact exact-result retention by measured cardinality

- Re-profile the latest accepted parent. Proceed only if bitmap copying or
  enumeration remains material for exact-cache hits above the current 64-ID
  compact limit.
- Benchmark compact limits of 64, 96, 128, and 256 IDs across empty, singleton,
  8, 45, 64/65, 96/97, 128/129, 256/257, and 4,095-result workloads. Include
  high-churn queries that exercise eviction under the existing 64 KiB cache
  budget.
- Select a limit from the complete latency/retained-memory matrix; do not tune
  it from the 45-result production case, which is already compact.
- Preserve bitmap fallback for wider results and every search with exclusions.
  Charge slice capacity to the existing cache budget exactly once.

Acceptance: at least one representative result class above 64 IDs improves by
10% or more, no class regresses by more than 3%, warm retained memory grows by
no more than 5%, eviction churn does not increase allocations, and the latest
accepted production-shaped parent remains statistically stable. If no tested
limit passes all conditions, retain 64 and reject L4.

### L5. Optimize cold and high-churn Local execution

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

For L2--L5, run focused tests plus the following after every candidate and
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
