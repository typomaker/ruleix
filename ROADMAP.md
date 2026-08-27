# Roadmap

This file is the active implementation plan for reducing search work and
temporary bitmap materialization. Completed work and rejected experiments
belong in [`ROADMAP_HISTORY.md`](ROADMAP_HISTORY.md); release-facing behavior
belongs in [`CHANGELOG.md`](CHANGELOG.md). Do not keep completed, superseded,
or unrelated proposals here.

## Objective

Replace the remaining cardinality-ordered `All` executor with a bounded
operation-cost planner that chooses what to do next, not only which child has
the smallest estimated result. It must avoid constructing complete child
bitmaps when an existing posting, candidate filter, ordered stream, or direct
ID validation can produce the same exact result more cheaply.

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

### 1. Implement a deterministic total-cost model

**Implement**

- Assign conservative integer work units to acquiring a source, intersecting
  an existing bitmap, filtering current candidates, direct ID validation, and
  final materialization. Avoid timers and floating-point arithmetic.
- Estimate total work, not cardinality alone:

  ```text
  acquire candidate source
  + narrow or validate remaining rules
  + expected temporary bitmap bytes
  ```

- Cost query-dependent unions and ordered ranges even when their complete
  bitmap has not been built. Use immutable build facts plus constant-time query
  facts; never scan ordered postings only to estimate their cost.
- Prefer exact current-query postings and cached bitmaps over estimates. Use
  the measured candidate threshold of eight only when required information is
  unavailable or costs tie.
- Add table tests for dense versus sparse bitmaps with equal cardinality,
  expensive selective sources versus cheap broader postings, unknown costs,
  saturating arithmetic, and the scan/materialize boundary.

**Accept when**

- The planner can compare direct validation with an unmaterialized remaining
  child instead of immediately falling back to `candidates <= 8`.
- Cost calculation remains allocation-free and bounded by the number of `All`
  children.
- Focused decisions match measured winners across the benchmark matrix; revise
  cost constants when they do not.

**Result**

A deterministic build-time model can choose a cheap initial operation without
runtime learning and without assuming that the smallest result is cheapest.

### 2. Select the next operation adaptively

**Implement**

- Replace the one-time cardinality sort as the execution plan with a bounded
  loop over remaining children and supported operations.
- At the start and after every exact narrowing step, choose the least estimated
  remaining total cost among:
  - consume an immutable posting;
  - filter the current candidate bitmap;
  - validate candidate IDs;
  - iterate an ordered source;
  - materialize a complete child result and intersect it.
- Use actual candidate cardinality and actual serialized bitmap size after
  every operation. Stop immediately on an exact empty result.
- Maintain separate scoring for bitmap narrowing and direct validation. Direct
  checks should be ordered by expected rejection per cost unit and
  short-circuit on the first rejection.
- Bound replanning to an allocation-free scan of the remaining child slice;
  do not build a heap-backed plan graph.

**Accept when**

- An inaccurate estimate cannot force execution to materialize every remaining
  broad result after candidates have become small.
- A cheap broader posting may correctly precede an expensive narrower source.
- Candidate filters are chosen only when measured cheaper than materialization,
  rather than being invoked unconditionally by representation type.
- Focused late-empty, late-selective, correlated, and expensive-`MatchID`
  benchmarks improve without regressing broad-result searches.

**Result**

Production search becomes an operation-cost executor instead of a
cardinality-ordered executor with a bitmap-versus-ID switch.

### 3. Add representation-specific operations only when they avoid work

**Implement**

- Re-evaluate equality, ordered, `Between`, and `CompareBy` against the new
  planner one representation at a time.
- For ordered rules, prototype bounded or early-stopping iteration that can
  avoid a meaningful part of a union. Do not restore unconditional streaming;
  the previous version was slower than bulk union plus intersection.
- Keep existing accepted candidate filters only when the cost model can select
  them and focused benchmarks show reduced work. Remove capabilities that are
  never selected or duplicate a faster Roaring primitive.
- Preserve wildcard semantics, all stored comparison operators, lossy no-false-
  negative guarantees, inspection metrics, and range empty detection.

**Accept when**

- Each retained operation avoids named materialization or intersection work
  from step 1 and wins its focused benchmark.
- Equality filtering and ordered streaming remain rejected unless a new
  bounded workload demonstrates a material improvement in latency or bytes.
- The production matrix shows no allocation regression.

**Result**

The executor has a minimal set of proven representation-specific operations,
not speculative capabilities maintained only because they fit the design.

### 4. Make shared planner statistics useful and bounded

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

### 5. Finish byte-bounded `Local` state

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

### 6. Differential cutover and cleanup

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
