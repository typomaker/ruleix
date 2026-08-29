# Roadmap

This file is the active implementation plan for reducing search work and
temporary bitmap materialization. Completed work and rejected experiments
belong in [`ROADMAP_HISTORY.md`](ROADMAP_HISTORY.md); release-facing behavior
belongs in [`CHANGELOG.md`](CHANGELOG.md). Do not keep completed, superseded,
or unrelated proposals here.

## Objective

Finish the operation-cost `All` executor by proving that every retained local
state has a byte bound. Shared learning was removed after it failed its
cold-start evidence gate. The deterministic planner, adaptive operation loop,
and representation-specific operation audit are complete; their measured
results are recorded in
[`ROADMAP_HISTORY.md`](ROADMAP_HISTORY.md).

The current Apple M1 Max production-shaped cutover measurements are the
reference point, not a success claim:

| Path | Latency | Temporary memory | Allocations |
| --- | ---: | ---: | ---: |
| `Index.Search` | 41.5--42.1 us/op | 73,571--73,572 B/op | 31 allocs/op |
| warm `Local.Search` | 569.0--570.2 ns/op | 0 B/op | 0 allocs/op |

The rewrite is successful only when it materially reduces `Index.Search`
temporary bytes or latency on production-shaped and focused workloads while
preserving zero-allocation warm `Local.Search`. A focused microbenchmark win is
not sufficient for final cutover if production allocation traffic increases.

## Non-negotiable constraints

- Preserve the public API, exact matching semantics, first-insertion result
  order, duplicate external-ID behavior, and all `Inspect` observations.
- Keep the built `Index` immutable and ordinary concurrent `Index.Search`
  lock-free. Planner learning may publish only from sampled `Local` contexts.
- Keep `uint32` internal IDs and Roaring bitmaps for broad candidate sets.
- Keep the common `All` planning path allocation-free for up to eight children
  and reuse bounded pooled storage for larger groups.
- Never materialize a complete child result once per candidate as an implicit
  `MatchID` fallback. Unsupported operations must remain explicit.
- Treat learned statistics as optional hints. Missing, stale, discarded, or
  adversarial profiles must affect performance only, never correctness.
- Introduce no public planner API until internal benchmarks demonstrate a need.
- Accept, reject, or revise every optimization using repeatable benchmark
  evidence. When an experiment regresses, preserve its measurements and
  implementation in a dedicated commit, identify the missing workload guard,
  representation choice, or reuse boundary, and try a bounded revision before
  deciding whether it belongs only in history. Do not leave a known regression
  enabled on the final cutover path.

## Required benchmark and correctness gate

Run the focused tests introduced by each step and, before accepting every
executor change, run at least:

```sh
go test ./...
go test -race ./...
go test -run '^$' -bench '^BenchmarkProductionShapeSearch/(Index|Local)$' \
  -benchmem -benchtime=1s -count=5 .
go test -run '^$' -bench '^BenchmarkProductionShapeParallelLocalBatch100$' \
  -benchmem -benchtime=1s -count=5 .
go test -run '^$' -bench '^BenchmarkProductionScaleSearch/' \
  -benchmem -benchtime=500ms -count=3 .
go test -run '^$' -bench '^BenchmarkProductionShapeLocalRetainedMemory$' \
  -benchtime=3x -count=3 .
```

Compare medians against the parent commit and the reference above. Also run
empty, selective, wildcard-heavy, range-heavy, large-result, nested-`All`,
high-churn, and inaccurate-estimate cases. Each new or changed benchmark must
include a nearby comment containing its latest local result and a complete
reproduction command.

An implementation step may be accepted when it provides a clear focused win
and does not materially regress the production matrix. Treat a repeatable
regression above 3% in latency, any new warm-`Local` allocation, or unexplained
growth in `Index.Search` allocation count or bytes as a failed gate. Record
noisy results and rerun with a longer benchtime before deciding.

## Implementation plan

The integrated physical-source identity experiment is complete and accepted.
Its design, decision matrix, and production-gate evidence are recorded in
[`ROADMAP_HISTORY.md`](ROADMAP_HISTORY.md). The next work is grouped by the
lifetime of the affected data so that build-time allocation changes are
measured separately from query-time bitmap work.

### Step 1: remove equality-class build callbacks

Replace the per-posting `func(uint32)` ownership callbacks in
`compileAllEqualityClasses` with structural two-pass bookkeeping while keeping
physical-source collision checks, canonical operands, and dense equality class
IDs:

1. Count distinct physical child owners for each `equalitySourcePair` and
   assign a dense class only to repeated pairs.
2. Walk the compiled children again and write class IDs directly through
   stable indexes or slots, without retaining closures.
3. Pre-size the maps and slices from provider and posting counts where those
   bounds are already available.

Gate this step primarily on `Index.Build`: recover the allocation-count
regression relative to `v0.8.1` without materially regressing build latency,
search latency, or the accepted physical-source semantics. Profile allocations
again after the rewrite and verify that `compileAllEqualityClasses.func1` no
longer dominates allocated objects.

### Step 2: reduce uncached search materialization

Treat range filtering and shared wildcards as one bitmap-lifetime problem:

1. Audit `Between.filterCandidates`, `collectSharedWildcards`, and their
   intermediate `And`/`Or` operations for cloned bitmap and array containers.
2. Introduce one bounded, reusable materialization workspace per search when
   ownership rules permit it; never mutate immutable index bitmaps.
3. Avoid materializing an intermediate result when the same restriction can be
   applied directly to the current candidate set.

The target is a repeatable reduction in uncached `Index.Search` bytes or
latency. Validate range-heavy, wildcard-heavy, mixed, empty, and large-result
cases separately so that a win does not merely move allocation traffic between
representations.

### Step 3: add cardinality-gated direct-ID filtering

Prototype early validation of selective direct-ID constraints before
`AndAny`. Enable it only when cheap cardinality evidence predicts less work
than bitmap intersection. Build a benchmark matrix for small, medium, and
large postings and for one versus several direct-ID constraints.

If the prototype regresses a workload, retain the experiment and profile as a
separate commit, determine whether the missing condition is a cardinality
threshold, result-density check, or operation-order constraint, and test that
bounded guard before rejecting the approach.

### Step 4: specialize small warm-Local results

Only after the uncached path is stable, measure whether result iteration is
still a material warm-`Local` CPU bottleneck. If so, add a representation-safe
fast path for empty, singleton, or otherwise small results. Preserve result
order and require `0 B/op`, `0 allocs/op`, and no repeatable latency regression
for every existing warm-`Local` case.

### Step 5: final combined cutover gate

Run the complete correctness and benchmark gate after combining the accepted
steps. Compare against both the parent commit and `v0.8.1`, including:

- `Index.Build` latency, bytes, and allocations;
- uncached `Index.Search` across condition mix and posting cardinality;
- warm `Local.Search` for empty, singleton, small, and large results;
- parallel local batches and retained local memory.

Record every accepted result and every retained/rejected experiment in
`ROADMAP_HISTORY.md`. Update the reference table above with the final local
medians only after the combined implementation passes this gate.
