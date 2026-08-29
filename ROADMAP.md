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

The integrated physical-source identity experiment, structural equality-class
build rewrite, and uncached bitmap-lifetime audit are complete. Their designs,
decision matrices, and production-gate evidence are recorded in
[`ROADMAP_HISTORY.md`](ROADMAP_HISTORY.md). The remaining work concerns a
possible warm-result specialization.

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
