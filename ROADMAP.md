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
  evidence. Remove rejected production code and record the result in history.

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

Complete the steps in order. A later step may rely only on capabilities that
survived the earlier benchmark gate.

### 1. Differential cutover and cleanup

**Implement**

- Keep a test-only recursive materialize-all reference executor. Compare it
  with the adaptive executor over generated schemas and queries covering every
  representation, nested combinators, wrappers, wildcard sharing, exclusions,
  lossy rules, duplicate IDs, empty results, and broad results.
- Add fuzzing for result equivalence and preserve deterministic insertion order.
- Run the race detector and the complete benchmark gate. Compare final results
  with both the parent commit and the reference measurements at the top of this
  file.
- Remove the old cardinality-only decision path, unused shadow code, unread
  statistics, obsolete interfaces, and compatibility branches only after the
  new production executor passes all gates.
- Move each completed or rejected step to `ROADMAP_HISTORY.md` with commands,
  hardware, medians, bytes, allocations, and the decision rationale.

**Accept when**

- Differential, fuzz, ordinary, and race tests pass.
- Warm `Local.Search` remains at 0 B/op and 0 allocs/op.
- Production-shaped `Index.Search` materially improves latency or temporary
  bytes without increasing allocation count, and focused workloads explain the
  improvement.
- Large-result, parallel, retained-memory, build-time, and lossy-quality gates
  show no material regression.

**Result**

One production executor remains. Its capabilities, cost model, learned hints,
and memory budgets are all exercised by code and benchmarks; there is no
shadow architecture or roadmap claim stronger than the measured result.
