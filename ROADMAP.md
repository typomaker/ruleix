# Roadmap

This file is the active implementation plan for reducing search work and
temporary bitmap materialization. Completed work and rejected experiments
belong in [`ROADMAP_HISTORY.md`](ROADMAP_HISTORY.md); release-facing behavior
belongs in [`CHANGELOG.md`](CHANGELOG.md). Do not keep completed, superseded,
or unrelated proposals here.

## Objective

Finish the operation-cost `All` executor by making optional shared learning
useful or removing it, and proving that every retained local and shared state
has a byte bound. The deterministic planner, adaptive operation loop, and
representation-specific operation audit are complete; their measured results
are recorded in
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

### 1. Make shared planner statistics useful and bounded

**Implement**

- Do not collect planner fields that no decision reads. Either feed operation
  cost, actual cardinality, empty rate, and candidate rejection rate into the
  cost model or remove those fields.
- Define a query shape from facts available before plan reuse, such as field
  presence, equality versus ordered access, wildcard/concrete state, and a
  bounded range-width class. Do not call a bucket of the previously selected
  first child's cardinality the current query shape.
- Keep a fixed number of shapes and child-order entries per compiled `All`.
  Store no query values, getters, or unbounded keys in the shared profile.
- Let each `Local` read one immutable snapshot, accumulate an unsynchronized
  overlay, and publish batched deltas only when sampled. Keep publication off
  every-search paths.
- Combine the shared prior with local evidence using sample counts and bounded
  confidence. Exact current-query facts always override learned values.
- Preserve deterministic exploration in a small fraction of sampled `Local`
  contexts so an early popular plan cannot suppress contrary evidence.

**Accept when**

- Two deliberately different query shapes learn different useful plans and a
  new `Local` selects the matching shape before its first search plan executes.
- A profile trained on the opposite workload is cheaply rejected or overridden
  and never changes results.
- Shared memory is bounded by compiled schema size and configured constants;
  ordinary and warm `Local.Search` retain their allocation classes.
- A benchmark demonstrates that sharing improves a new `Local` versus starting
  from deterministic planning. Otherwise remove shared learning and retain the
  deterministic planner.

**Result**

Cross-`Local` learning either provides a measured cold-start benefit with a
bounded implementation or is removed instead of accumulating unused telemetry.

### 2. Finish byte-bounded `Local` state

**Implement**

- Maintain separate accounted budgets for child bitmaps, exact intersections,
  learned plans, and planner profiles. Include Roaring payload, input bitmap
  references, keys, and overflow structures in the accounting model.
- Validate cached plans against exact current postings and capabilities before
  reuse. Cache exact intersections only when all input bitmap identities and
  the cache epoch match.
- Keep admission-after-repeat for value bitmaps. Exercise alternating values,
  one-off churn, adaptive capacity, oversized results, `Close`, and pool reuse.
- Report retained bytes per cold, warm, adaptive, and adversarial `Local`.

**Accept when**

- No query stream can make retained per-`Local` bitmap or plan state grow
  without the documented bound.
- Declining an oversized cache entry affects only reuse, not correctness.
- `Close` releases all accounted payload and a recycled `Local` starts with
  reset admission, observation, and profile state.

**Result**

The faster planner has a predictable memory cost per live goroutine and cannot
trade temporary `Index.Search` allocations for unbounded retained `Local`
memory.

### 3. Differential cutover and cleanup

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
