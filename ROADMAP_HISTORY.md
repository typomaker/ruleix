# Roadmap history

This document records completed roadmap work and concluded experiments. The
active [`ROADMAP.md`](ROADMAP.md) contains only work that may still be done.

When a roadmap item is completed or rejected, move it here (or to a dedicated
historical document) with the date, outcome, and enough benchmark or design
evidence to avoid repeating the work. Release-facing changes still belong in
[`CHANGELOG.md`](CHANGELOG.md).

## 2026-08-27: replan `All` after measured narrowing

The cost-based `All` executor now reconsiders direct candidate validation after
every exact bitmap intersection. A candidate set that becomes selective only
after intersecting two inaccurately estimated children no longer follows the
stale initial order and materializes every remaining broad result. The executor
uses the measured intersection cardinality, compares ID-check work with exact
remaining bitmap sizes, stops on an exact empty result, and keeps replanning
bounded by the existing child slice with no heap-backed plan state. Focused
tests cover inaccurate estimates, the mid-execution bitmap/ID boundary, skipped
late materialization, early rejection, and Inspector's resulting strategy.

On Apple M1 Max, three 300 ms runs measured the existing broad-sibling cost
case at 284.9--293.7 ns/op, 32 B/op, and 2 allocs/op; the late selective
materialization case at 513.5--519.2 ns/op, 176 B/op, and 12 allocs/op.
The focused post-intersection replan measured a median 413.8 ns/op, 144 B/op,
and 10 allocs/op across five 1 s runs.
The final production-shaped gate measured `Index.Search` at 42.6--42.9 us/op,
73,395--73,398 B/op, and 28 allocs/op, and warm `Local.Search` at
579.6--584.0 ns/op with 0 B/op and 0 allocations/op. Reproduce with:

```sh
go test -run '^$' -bench '^BenchmarkAllReplanAfterIntersection$' \
  -benchmem -benchtime=1s -count=5 .
go test -run '^$' \
  -bench '^(BenchmarkAllCostBasedBroadSibling|BenchmarkAllLateMaterializedCandidateFallback|BenchmarkProductionShapeSearch)' \
  -benchmem -benchtime=300ms -count=3 .
```

## 2026-08-27: separate static execution analysis from query planning

`Build` now compiles immutable execution facts beside each `All` child's
capability and cost descriptor: posting count, minimum, maximum, and total
posting cardinality, plus wildcard behavior and cardinality. Equality
(including unary and binary specializations), ordered, `Between`, `CompareBy`,
lossy equality and ordered, and match-all representations provide these facts.
Wrappers are removed before analysis, so inspection and lossy policy do not
hide the selected representation.

The shadow planner no longer calls general cardinality estimators. It inspects
only exact query postings, already-cached local bitmaps, and constant-time cheap
cardinalities; a focused test proves that an ordered-cost estimator is not
called. Descriptors retain no query values and require no runtime metrics.

On Apple M1 Max, the shadow decision measured a median 91.46 ns/op with 0 B/op
and 0 allocations/op across five 1 s runs. The production-shaped acceptance
check measured median `Index.Search` 38.1 us/op, 73,394 B/op, 28 allocs/op and
warm `Local.Search` 552.7 ns/op, 0 B/op, 0 allocs/op across three 500 ms runs.
Reproduce with:

```sh
go test -run '^$' -bench '^BenchmarkAllShadowDecision$' \
  -benchmem -benchtime=1s -count=5 .
go test -run '^$' -bench '^BenchmarkProductionShapeSearch/(Index|Local)$' \
  -benchmem -benchtime=500ms -count=3 .
```

## 2026-08-27: introduce execution capabilities and shadow cost model

Compiled `All` rules now retain compact immutable descriptors for exact
postings, constant-time and ordered estimates, direct ID matching, candidate
filtering, ordered posting streaming, and complete materialization. Each
operation has a coarse build-time cost class and unsupported operations remain
explicit. In particular, the shadow planner cannot turn materialization into
an implicit per-candidate `MatchID` fallback.

The opt-in shadow decision selects a proposed candidate source and validation
mode for tests and benchmarks only; production `Search` does not invoke it or
change results. Equality/range interfaces remain allocation-free, and ordered
and `CompareBy` representations now expose ordered posting streams for future
executor steps. On Apple M1 Max, the three-child shadow decision measured a
median 104.6 ns/op with 0 B/op and 0 allocations/op across five 1 s runs.
Reproduce with:

```sh
go test -run '^$' -bench '^BenchmarkAllShadowDecision$' \
  -benchmem -benchtime=1s -count=5 .
```

## 2026-08-24: move Inspector telemetry off ordinary search paths

Inspector now keeps a plain compiled tree for shared `Index` execution and for
63 of every 64 `Local` contexts. The selected Local uses a separately prepared
observed tree, accumulates ordinary counters, and publishes them on `Close`.
Runtime snapshots are therefore delayed best-effort samples; build facts remain
exact. Static node-to-inspector bindings replace the dynamic observer stack,
and inspection no longer enables Local range pruning.

On Apple M1 Max, medians of five 300 ms runs showed no material inspected/plain
difference: Index leaf 91.79 versus 92.79 ns/op, Index `All` 192.7 versus 193.4
ns/op, warm Local leaf 40.77 versus 40.56 ns/op, and warm Local `All` 111.0
versus 110.9 ns/op. Allocation counts and bytes were identical. Reproduce with:

```sh
go test -run '^$' -bench '^BenchmarkInspectRuntimeOverhead/' \
  -benchmem -benchtime=300ms -count=5 .
```

## 2026-08-24: delayed shared-Index Inspector batching experiment

An exact delayed-publication prototype gave inspected `Index.Search` calls
exclusive metric accumulators from a `sync.Pool`, published each accumulator
after 256 observations, and used a finalizer to preserve partial batches when
the runtime discarded idle pool entries. It removed per-event atomic additions
but added context acquisition and observer bookkeeping to every inspected
search.

The prototype was rejected after seven 500 ms runs on Apple M1 Max. The median
maximum-width four-ID candidate scan was 347.6 ns/search with delayed batching,
versus the existing 335.8 ns direct-atomic baseline; its paired plain case was
315.5 ns/search. A top-level inspected `All` measured 237.5 ns/search versus
222.1 ns plain. Exact shared-Index batching therefore needs a cheaper source of
exclusive per-execution storage before it can outperform direct atomics.
`Local` remains the effective batching boundary because it already owns such
storage and can publish its exact tail at `Close` without acquisition overhead.

## 2026-08-24: Inspector candidate-check batching experiment

Candidate-check batching was prototyped for `All` but rejected. The planner caps
candidate scans at four IDs; on Apple M1 Max the maximum-width inspected scan
measured 348.1 ns/search with batching versus 342.6 ns with direct atomic
updates (medians of seven 500 ms runs). Batch bookkeeping therefore cost more
than the three avoided additions. The redundant `InspectorSnapshot.Search` and
`Materialization` metrics were removed: both tracked the same bitmap-result
observation and neither consistently represented a public search call.

## 2026-08-24: batch inspected `Local` runtime metrics

Inspected `Local` searches now update context-owned ordinary counters and merge
them into the Inspector's atomic lifetime totals on `Local.Close`. This removes
atomic read-modify-write operations from the Local hot path while preserving
exact monotonic totals after close. A snapshot intentionally excludes metrics
buffered by Local contexts that remain open; shared `Index` searches retain
their atomic updates and concurrent safety.

On Apple M1 Max, medians of five 300 ms runs put a warm inspected equality leaf
at 49.91 ns/search versus 41.70 ns without inspection, down from the preceding
51.62 ns inspected baseline. A warm inspected top-level `All` measured 176.6 ns
versus 173.2 ns without inspection. Paired allocation counts and bytes remained
unchanged.

Reproduce with:

```sh
go test -run '^$' -bench '^BenchmarkInspectRuntimeOverhead/' \
  -benchmem -benchtime=300ms -count=5 .
```

## 2026-08-24: conclude `Local` cache-policy tuning

The existing second-use admission, two-entry LRU, measured growth to four
entries, bitmap recycling, and cleared-structure reuse cover the roadmap's
repeated-value, alternating-value, high-churn, close/reuse, and retained-memory
cases. A final experiment embedded the initial two admission keys in each typed
cache instead of allocating them on the first miss. It was not retained: the
production-shaped short-lived lifecycle removed 10 allocations (120 to 110)
and stayed near 373 us, but the larger always-live cache structures increased
warm and four-query retained memory by about 592 B per `Local` (88,880 to
89,472 B and 105,730 to 106,322 B). Ordered and `Between` repeated, alternating,
three-/four-value, hot-with-interlopers, and churn latency remained effectively
flat.

On Apple M1 Max, the retained-memory comparison used three 150 ms runs. The
earlier second-use admission, adaptive capacity, bitmap recycling, and
structure-reuse entries in this history contain their longer benchmark runs.
No further policy change is justified without a new production workload that
shows a material miss-rate, latency, or retained-memory problem.

Reproduce the final comparison with and without the inline-key candidate using:

```sh
go test -run '^$' \
  -bench '^(BenchmarkLocalOrderedReuse|BenchmarkLocalBetweenReuse|BenchmarkProductionShapeLocalRetainedMemory|BenchmarkProductionShapeLocalClose)$' \
  -benchmem -benchtime=150ms -count=3 .
```

## 2026-08-24: reuse cleared per-node `Local` cache structures

`Local.Close` now keeps the small typed cache structures attached to its
recyclable context after returning all owned bitmaps to the bounded scratch
pool. Each structure resets its keys, admission history, replacement state,
and adaptive overflow before reuse, so the next `Local` lifetime remains
logically cold and starts with two entries. Learned bitmap-free `All` plans
continue to survive context reuse independently.

On Apple M1 Max, five 300 ms runs of the production-shaped short-lived
`Local` benchmark remained effectively flat at a median 373.7 us per six-search
lifecycle, while allocation traffic fell from approximately 307.7 KB and 130
allocations to 306.7 KB and 120 allocations. The empty reused-context lifecycle
improved from a median 38.6 ns to 33.8 ns, with its existing 24 B and one
allocation unchanged.

Reproduce with:

```sh
go test -run '^$' \
  -bench '^(BenchmarkLocalLifecycleReuse|BenchmarkProductionShapeLocalClose)$' \
  -benchmem -benchtime=300ms -count=5 .
```

## 2026-08-24: equality-first lazy range planning experiment

Two `All` executor candidates deferred ordered and range estimates until after
cheap equality estimates and searches. The first materialized cheap children
in estimated-cardinality order before continuing with ranges. The second
intersected all cheap postings with `FastAnd`, measured the actual candidate
set, used direct ID validation below 256 candidates, and only otherwise
estimated and sorted the deferred ranges.

Neither candidate was retained. On the 38,098-rule production-shaped workload
on Apple M1 Max, warm `Local.Search` regressed from a 6.097 us baseline to about
23.0 us for the ordered candidate and 21.9 us for the staged `FastAnd`
candidate. The staged version also increased allocation traffic from 4,665 B
and four allocations to 13,501 B and 15 allocations per search. `Index.Search`
regressed from 97.68 us to about 111.9 us.

The equality postings in this distribution remain broad because most missing
stored values are wildcards. A useful equality predicate, platform name, is
nested in an `All` with the ordered platform version, so treating the nested
node as one deferred unit hides that selectivity. The candidates therefore
paid to materialize and intersect many broad equality bitmaps, did not become
small enough for direct range validation, and still paid for range planning.

Reconsider equality-first execution only together with partial planning of
nested `All` nodes, or with a cheap way to predict the combined equality
intersection before materializing every posting. Merely partitioning top-level
children into equality and range phases is counterproductive for this
production shape.

## 2026-08-24: post-intersection `All` candidate-scan experiment

An executor candidate switched from bitmap materialization to direct ID
validation when the first three broad postings intersected to at most four
IDs. This attempted to extend the existing measured candidate threshold to the
accumulated intersection while preserving the earlier range-pruning window.

The candidate was not retained. On Apple M1 Max, medians of five isolated
four-child runs increased from 525.8 ns/search for the retained eager tail to
531.6 ns/search (1.1%). Allocation count remained 16 per search, and allocation
traffic fell only from 248 B to 240 B. Creating and copying a separate result
bitmap outweighed avoiding the remaining broad posting materialization.

Reconsider this transition only if candidates can be filtered in place or the
remaining child representations are materially more expensive than immutable
bitmap unions.

## 2026-08-24: cumulative adaptive-cache inspection

`InspectorSnapshot.CacheExpansion` now exposes a monotonic count of adaptive
cache transitions from two to four bitmap entries. The counter aggregates all
observed `Local` lifetimes and remains unchanged when a `Local` closes, making
it directly exportable as a Prometheus counter. Existing hit, miss, admission,
and eviction measurements were already monotonic counters. The former live
cache entry and capacity gauges were removed, leaving Inspector runtime output
with only monotonic counters and histograms.

Expansion recording adds one atomic increment only on the rare growth path and
does not change ordinary or steady-state inspected searches. Tests cover an
adaptive `Between` cache across two sequential `Local` lifetimes and verify
cumulative expansion accounting after each context closes.

## 2026-08-24: adaptive `Local` cache capacity for `Between`

The specialized `Between` cache now follows the same measured two-to-four
entry policy as the other value caches. It grows its small admission-key filter
first, then adds two bitmap entries only after repeated admitted evictions prove
that a reusable working set exceeds the initial capacity. Unique interval churn
therefore retains no materialized result bitmap.

On Apple M1 Max, medians of five 10K-rule runs measured 66.36 ns/search for a
three-interval cycle and 68.38 ns/search for a four-interval cycle, versus
approximately 2.93 us and 2.92 us with uncached `Index.Search`. Both warm
`Local` cases used 16 B and one allocation per search instead of 8,270 B and
five allocations. Unique churn remained equivalent to uncached search at about
2.97 us with the same bytes and allocations. In the production-shaped
four-query retained-memory case, the larger working set increased one live
`Local` from approximately 71.9 KB to 105.3 KB; cold and two-query warm locals
remained unchanged at approximately 1.7 KB and 88.4 KB.

Reproduce with:

```sh
go test -run '^$' -bench '^BenchmarkLocalBetweenReuse$' \
  -benchmem -benchtime=200ms -count=5 .
```

## 2026-08-24: `Local.Close` cache-bitmap recycling

The production benchmark suite now includes a short-lived `Local` lifecycle:
acquire a context, run six searches over a two-query working set to admit and
reuse its caches, then call `Close`. Previously `Close` returned the context to
the index but cleared its node-cache references without returning the admitted
bitmaps to the context's scratch pool. The next `Local` therefore reused the
context and node array while allocating new cache bitmaps.

`resetLocal` now releases cache-owned bitmaps from equality, ordered,
`CompareBy`, `Between`, and exclusion entries through the bounded bitmap pool
before clearing node state. Cache admission obtains replacement bitmaps from
the same pool. The next `Local` remains logically cold, while its cache entries
can reuse released bitmap objects and top-level storage.

On Apple M1 Max with Go 1.26.0, medians of five one-second runs of
`BenchmarkProductionShapeLocalClose` changed from 388.8 us, 308.8 KB, and 174
allocations per lifecycle to 373.6 us, 307.7 KB, and 130 allocations. This is a
3.9% latency improvement and a 25.3% allocation-count reduction. Retained bytes
fall only slightly because Roaring `Clear` releases container references; the
optimization primarily reuses bitmap objects and top-level buffers rather than
the materialized container payload.

Reproduce with:

```sh
go test -run '^$' -bench '^BenchmarkProductionShapeLocalClose$' \
  -benchmem -benchtime=1s -count=5 .
```

## 2026-08-24: late `All` candidate bitmap reuse

When bitmap execution discovers a small materialized child after one or more
broader children, direct candidate validation now checks those earlier
children against their already materialized bitmaps. Previously it invoked
their rule matchers again, repeating representation-specific lookup work. Later
unmaterialized children remain lazy, and inspected rules retain their candidate-
check accounting.

On Apple M1 Max, medians of five isolated runs improved from 347.4 ns to
335.7 ns per search (3.4%), with allocations unchanged at 120 B and eight
allocations.

Reproduce with:

```sh
go test -run '^$' -bench '^BenchmarkAllLateMaterializedCandidateFallback$' \
  -benchmem -benchtime=300ms -count=5 .
```

## 2026-08-24: lossy equality collision diagnostics

Grouped-hash equality representations now publish a build-time estimated
false-positive rate through `InspectorSnapshot`. The estimate uses the actual
concrete posting distribution: it divides different-value ordered item pairs
that share the selected hash bucket by all different-value ordered item pairs.
Wildcards are excluded because they remain exact matches, and duplicate-value
items are excluded because they are not false positives for that value.

The calculation runs while the planner already groups postings into candidate
buckets and retains only scalar counters, so it adds no query-time work or
runtime instrumentation. Ordered representations continue to report the rate
as unavailable because their approximation error depends on the query's range
boundary.

On Apple M1 Max, three three-iteration 10K-rule, four-child planning runs had
medians of 55.1 ms, 21.2 MB, and 362.3K allocations at a 50% budget, and
55.7 ms, 21.7 MB, and 366.4K allocations at a 25% budget. Reproduce the build
baseline with:

```sh
go test -run '^$' \
  -bench '^BenchmarkLossyAllPlanning/Children4/Budget(50|25)$' \
  -benchmem -benchtime=3x -count=3 .
```

## 2026-08-24: materialized `All` candidate fallback

The bitmap executor now reuses each materialized child's actual cardinality.
When a conservative or unavailable cheap estimate selected bitmap execution
but the materialized result contains at most four IDs, `All` switches to direct
ID validation for the other children. This extends the measured candidate-scan
threshold to predicates whose selectivity cannot be known cheaply and avoids
materializing the remaining bitmaps.

On Apple M1 Max, medians of five isolated four-child runs improved from about
770 ns, 408 B, and 28 allocations per search to 235 ns, 64 B, and 4 allocations
for one materialized candidate. Four candidates improved from about 774 ns to
452 ns with the same allocation reduction. Existing range-pruning, estimate-
ranking, and candidate crossover matrices retained their prior behavior.

Reproduce with:

```sh
go test -run '^$' -bench '^BenchmarkAllMaterializedCandidateFallback/' \
  -benchmem -benchtime=300ms -count=5 .
```

## 2026-08-24: nested `All` cheap empty propagation

A nested `All` now incorporates specialized child emptiness checks into its
cheap cardinality estimate. This lets an enclosing `All` stop before
materializing any sibling when a descendant can prove an empty result but
cannot cheaply estimate a non-empty cardinality. Estimator-backed children
remain single-pass: their estimate is reused as the definitive empty check.

On Apple M1 Max, medians of five isolated runs with a 1,024-ID sibling improved
from 4.00 us, 6,547 B, and 11 allocations per search to 39.65 ns with no
allocation.

Reproduce with:

```sh
go test -run '^$' -bench '^BenchmarkNestedAllCheapEmptyCheck$' \
  -benchmem -benchtime=300ms -count=5 .
```

## 2026-08-24: single-pass `All` estimate ranking

`All` now reuses each child's cheap cardinality estimate for both its
definitive empty check and execution ranking. Previously the common equality,
ordered, `Between`, `CompareBy`, lossy, and nested-`All` implementations
calculated the same estimate once through `isCardinalityZero` and again while
building the ranked child list. Children that expose only a specialized cheap
empty check still participate before any result bitmap is materialized.

On Apple M1 Max, medians of five end-to-end executor runs with `CompareBy`
children improved from 65.7 us to 63.5 us for two children, 144.8 us to
128.4 us for four children, and 267.5 us to 257.1 us for eight children.
Allocation counts and bytes were unchanged.

Reproduce with:

```sh
go test -run '^$' -bench '^BenchmarkAllEstimateRanking/' \
  -benchmem -benchtime=300ms -count=5 .
```

## 2026-08-24: pre-materialization `All` range experiment

A planner candidate asked range-capable children for their minimum and maximum
internal IDs before materializing the first bitmap. Intersecting these cheap
bounds can prove an `All` result empty even earlier than the retained hybrid
executor. Equality leaves, their unary and binary specializations, nested
`All`, and immutable bitmap leaves participated in the prototype.

The candidate was not retained. On Apple M1 Max, medians of five runs reduced
the isolated four-child range-disjoint case from 237.6 ns, 56 B, and 4
allocations to 44.3 ns with no allocation, and the eight-child case from 338.6
ns to 59.1 ns. However, asking every child for bounds on the common overlapping
path increased four-child latency from 569.9 ns to 598.0 ns (4.9%) and
eight-child latency from 1096 ns to 1192 ns (8.8%), with allocation counts
unchanged. The existing materialize-then-check hybrid therefore remains the
better general policy. Reconsider preflight bounds only if build-time stored
bounds or a query-shape gate can avoid work on overlapping searches.

Reproduce the retained baseline with:

```sh
go test -run '^$' -bench '^BenchmarkAllLeadingIntersection/' \
  -benchmem -benchtime=200ms -count=5 .
```

## 2026-08-24: empty-aware `All` range pruning

The broad bitmap executor uses a hybrid plan. It materializes and intersects
the first three ranked children sequentially, stopping when the next child's
internal-ID range is disjoint from the accumulated intersection, then
materializes the remainder eagerly. Comparing the minimum and maximum IDs is
constant-time and allocation-free; it proves the complete `All` empty without
scanning bitmap containers or materializing later children. The third-child
check catches late disjointness after the first intersection narrows the
candidates, while the eager tail limits overhead on workloads that normally
reach the final rule.
General disjoint bitmaps with overlapping ranges remain on the existing
`FastAnd` path because a speculative `Intersects` check measurably regressed
overlapping workloads. An explicitly inspected `All` exposes the monotonic
number of these early exits through `InspectorSnapshot.RangePruning`; schemas
without `Inspect` retain the original rule size and do not execute metric
updates.

On Apple M1 Max, medians of five runs improved a four-child range-disjoint
query from 387.7 ns, 164 B, and 11 allocations per search to 205.0 ns, 56 B,
and 4 allocations. The eight-child case improved from 724.7 ns, 276 B, and 19
allocations to 282.8 ns, 56 B, and 4 allocations. Overlapping four- and
eight-child cases remained within 3% of the eager baseline and retained the
same allocation counts.

The later hybrid extension reduced a third-child range miss from a median
494.6 ns, 200 B, and 13 allocations to 376.8 ns, 144 B, and 10 allocations.
Against the original eager implementation, the representative top-level
`BenchmarkAll` flat case improved from 2528 ns, 344 B, and 6 allocations to
2476 ns, 232 B, and 2 allocations; the nested case improved from 3370 ns,
808 B, and 10 allocations to 3284 ns, 696 B, and 6 allocations.

Reproduce with:

```sh
go test -run '^$' -bench '^BenchmarkAllLeadingIntersection/' \
  -benchmem -benchtime=200ms -count=5 .
go test -run '^$' -bench '^BenchmarkAllLateRangePruning$' \
  -benchmem -benchtime=300ms -count=5 .
```

## 2026-08-23: cache-aware `All` priority experiment

A `Local`-only planner candidate combined estimated candidate cardinality with
cache state. For candidate scanning with `N` children, it ranked a miss as
`cardinality * N` and a hit as `cardinality * (N - 1)`, approximating one
materialization plus validation of every candidate against the remaining
children. Bitmap execution kept cardinality-only ordering because it
materializes every child regardless of their initial order.

The candidate was not retained. On Apple M1 Max, an ordered `All` workload with
an uncached three-ID child measured a median 1.95 us/search, 344 B/search, and 6
allocations/search. Preferring an already cached four-ID child remained about
1.95 us/search with the same bytes and allocations. An equality variant made
the cached four-ID source roughly 1-2% slower than the uncached three-ID source.
The cache lookup and bitmap copy were not cheaper enough to offset validating
the extra candidate, while checking cache state added work to every warm
`Local` ranking. Keep result cardinality as the priority unless a future cache
representation can lend an immutable bitmap without copying or measured
per-rule materialization costs provide a stronger signal.

## 2026-08-23: adaptive `Local` value-cache capacity

Equality, exclusion, ordered, and `CompareBy` caches now start with two bitmap
entries and grow to four only after repeated misses demonstrate a reusable
working set. The admission-key filter grows first; unique churn therefore
retains four small keys but no materialized bitmap. Two subsequent admitted
evictions trigger bitmap growth, avoiding expansion from a raw low hit rate.

On Apple M1 Max, medians of five 10K-rule ordered runs improved a three-value
cycle from 48.3 us and 40,543 B/search to 18.1 us and 1,195 B/search, and a
four-value cycle from 48.4 us and 40,543 B/search to 18.1 us and 1,198 B/search.
Both stayed at 3 allocations/search after warming instead of 14. Repeat,
alternate, and hot-with-interlopers results remained near 18.2 us; unique churn
remained near uncached search at 43.1 us with no retained bitmap. In the
production-shaped live-cache benchmark, the four-query case retained about
71.8 KB per `Local`; cold locals remained at 1,664 bytes.

Reproduce with:

```sh
go test -run '^$' \
  -bench '^BenchmarkLocalOrderedReuse/(Cycle3|Cycle4)/(Index|Local)$' \
  -benchmem -benchtime=300ms -count=5 .
go test -run '^$' \
  -bench '^BenchmarkProductionShapeLocalRetainedMemory/(Cold|Warm|Adaptive)$' \
  -benchtime=50x -count=5 .
```

## 2026-08-22: production-shaped baseline

The existing 38,098-rule benchmark is the initial reproducible baseline for the
planner work. Measurements are medians of five runs on Apple M1 Max, using Go's
default `GOMAXPROCS` of 10.

| Metric | Baseline |
|---|---:|
| `Index.Search` | 104.3 µs/op, 108,250 B/op, 34 allocs/op |
| warm `Local.Search` | 4.60 µs/op, 4,665 B/op, 4 allocs/op |
| parallel warm local search | 2.39 µs/search |
| `Build` | 34.85 ms/op, 5,128,740 B/op, 24,626 allocs/op |
| retained index memory | 1,288,998 B/index, 33.83 B/ID |
| shuffled retained index memory | 1,289,504 B/index, 33.85 B/ID |
| cold `Local` retained memory | 1,664 B/local |
| warm `Local` retained memory | 88,216 B/local |

Reproduce the timed and allocation measurements with:

```sh
go test -run '^$' \
  -bench '^(BenchmarkProductionShapeSearch|BenchmarkProductionShapeParallelLocalBatch100|BenchmarkProductionShapeBuild)$' \
  -benchmem -benchtime=200ms -count=5 .
```

Retained-memory benchmarks deliberately keep five indexes or locals alive:

```sh
go test -run '^$' \
  -bench '^(BenchmarkProductionShapeRetainedMemory|BenchmarkProductionShapeShuffledRetainedMemory|BenchmarkProductionShapeLocalRetainedMemory)$' \
  -benchtime=5x -count=5 .
```

## 2026-08-22: scalable production-shaped benchmark matrix

The production-shaped workload can now be generated at 10K, 100K, and 1M
rules while preserving the original field and wildcard ratios. The matrix
measures build time and allocations, retained index bytes, and search latency
and allocations for small-result, selective, and wildcard-heavy queries. Each
search case also reports its actual match count so distribution changes remain
visible in benchmark output.

Reproduce timed and allocation measurements with:

```sh
go test -run '^$' \
  -bench '^(BenchmarkProductionScaleSearch|BenchmarkProductionScaleBuild)/' \
  -benchmem -benchtime=200ms -count=5 .
```

Measure retained memory with a fixed iteration count:

```sh
go test -run '^$' -bench '^BenchmarkProductionScaleRetainedMemory/' \
  -benchtime=1x -count=5 .
```

Peak build heap and GC pressure use separate benchmarks so disabling GC for an
unambiguous peak sample does not distort the collection count. Both prime the
reusable builder before measuring, exclude caller-owned source data from the
heap baseline, and should be run with a fixed iteration count:

```sh
go test -run '^$' \
  -bench '^BenchmarkProductionScaleBuild(PeakMemory|GCPressure)/' \
  -benchtime=1x -count=5 .
```

`BuildPeakMemory` reports heap growth with GC disabled around the build.
`BuildGCPressure` reports GC cycles and cumulative stop-the-world pause time
with the process's normal GC setting.

The build matrix also reports logical postings per index, average and maximum
IDs per posting, and the percentage of leaf memberships stored in wildcard
postings. These distribution metrics are computed from the generated input in
the same units as leaf indexes, before bitmap interning and other physical
representation optimizations, so benchmark runs remain comparable when the
internal layout changes.

The same matrix includes 5M and 10M rule cases when
`RULEIX_BENCHMARK_LARGE=1` is set. They are opt-in because constructing the
production-shaped source data and index can require several gigabytes of peak
memory. Run a single large size by combining the environment variable with a
benchmark subtest filter, for example:

```sh
RULEIX_BENCHMARK_LARGE=1 go test -run '^$' \
  -bench '^BenchmarkProductionScaleSearch/Rules5000000/' \
  -benchmem -benchtime=200ms -count=5 .
```

The search matrix also has explicit large-result, nested-`All`, and range-heavy
cases. The range case populates both time intervals deterministically from the
same production-shaped source data; the large-result and nested cases use
smaller dedicated schemas so they measure the intended planner shape instead
of an unrelated selective leaf in the full schema.

## Adaptive `All` planner: initial stages

The first two planner stages were implemented before this history was created.
`All` orders children only when a leaf can estimate cardinality without range
traversal or bitmap materialization, preserves schema order for unknown costs,
and executes small estimated results lazily with an empty-intersection exit.
Broad results retain the materialize-and-rank path to protect production-shaped
and nested workloads. Further estimates remain benchmark-dependent.

The next stage added internal, type-safe single-ID validation to every positive
leaf and nested `All`. Small candidate paths now materialize only the most
selective child and validate all remaining predicates directly against its IDs,
without reverse constraint storage or a public API change. This leaf-level
approach was preferred over a compiled-rule array because it reuses existing
posting data and adds no retained copy of the source constraints.

## 2026-08-22: adaptive `All` execution threshold

`BenchmarkAllExecutionThreshold` compares candidate-only, bitmap-only, and
adaptive execution for dense and sparse postings, 2, 4, and 8 children, and
candidate cardinalities from 1 through 16K. On Apple M1 Max, candidate scans
consistently won at 1 and 4 IDs. Results became shape-dependent by 16 IDs, and
bitmap execution won decisively for dense postings and wider `All` groups from
that point onward. The first shared threshold is therefore 4 IDs.

End-to-end 10K-rule benchmarks confirmed the choice. Medians of five runs
improved flat `All` search from 13.75 us/op to 2.58 us/op, nested `All` from
10.79 us/op to 2.88 us/op, and skewed equality with a 100-ID selective posting
from 2.78 us/op to 708 ns/op. The one-ID selective case remained effectively
unchanged at 242 ns/op. The bitmap path trades additional scratch allocation
for substantially lower latency above the threshold.

Reproduce the threshold matrix with:

```sh
go test -run '^$' -bench '^BenchmarkAllExecutionThreshold/' \
  -benchmem -benchtime=200ms -count=5 .
```

## 2026-08-22: shared equality wildcards in `All`

Bitmap interning makes identical partial wildcard postings pointer-identical.
For two or more equality children under the same `All`, `Index.Search` now uses
`W union (A1 intersection ... intersection An)` and materializes the shared
wildcard once. The optimization deliberately excludes exclusions, ordered
rules, `Between` bounds, and `Local.Search`; warm locals retain their existing
per-node cached equality results.

`BenchmarkSharedWildcardAll` isolates 10K-rule postings with 25% and 75%
partial wildcards and 2, 4, and 8 equality children. On Apple M1 Max, medians
for repeated/shared wildcard evaluation were 3.86 us/421 ns with two children,
5.53 us/599 ns with four, and 8.97 us/834 ns with eight at 25% wildcards. At
75%, the corresponding medians were 1.60 us/217 ns, 2.51 us/270 ns, and
4.28 us/370 ns. The specialized identity also reduced benchmark allocation
traffic in every case.

Reproduce the matrix with:

```sh
go test -run '^$' -bench '^BenchmarkSharedWildcardAll/' \
  -benchmem -benchtime=200ms -count=5 .
```

## 2026-08-22: bitmap-pool reuse under concurrent search

`BenchmarkBitmapPoolParallelSearch` compares normal shared `Index` scratch
reuse with the same internal search starting from an empty pool on every
operation. The production-scale shape uses 100K rules, four equality children,
and a 1K-result query so the adaptive `All` executor takes its bitmap path.
`RunParallel` exercises the pool's per-P behavior and concurrent contention.

On Apple M1 Max, medians of five runs were 1.820 us/op with shared reuse and
3.304 us/op with a fresh pool, a 45% latency reduction. Allocation traffic fell
from 4,608 B/op and 33 allocs/op to 2,307 B/op and 9 allocs/op. The pool remains
part of `Index` because it improves end-to-end concurrent latency and allocation
behavior.

Reproduce the comparison with:

```sh
go test -run '^$' -bench '^BenchmarkBitmapPoolParallelSearch/' \
  -benchmem -benchtime=300ms -count=5 .
```

## 2026-08-22: opt-in planner diagnostics

`Index.Explain` now performs a diagnostic search and reports the optimized
top-level `All` child positions, available cardinality estimates, actual
cardinalities, execution order, materialization decisions, candidate
cardinality, final cardinality, and the selected candidate-scan or bitmap-
intersection strategy. Non-`All` roots report the single-rule strategy and
result cardinality.

Diagnostics are computed only by an explicit `Explain` call. The immutable
index retains no counters or traces, so ordinary `Search`, `Visit`, and `Local`
hot paths are unchanged. `Explain` intentionally materializes every direct
child to provide actual cardinalities, including predicates that normal
candidate execution validates by internal ID.

## 2026-08-22: second-use admission for `Local`

Per-node `Local` caches now admit a materialized bitmap only after the same
query value is observed twice recently. The two-entry LRU remains unchanged for
admitted values, while one-off values are computed directly into search scratch
space and retain only a small two-key admission filter. Once its observed keys
are admitted, that temporary filter is released. This applies uniformly to
equality, ordered, `CompareBy`, `Between`, and exclusion nodes.

On Apple M1 Max, medians of three 10K-rule runs preserved Repeat and Alternate
performance. The HotWithInterlopers case improved from 34.6 us to 28.9 us for
ordered filters, from 34.1 us to 28.7 us for `CompareBy`, and from 1.74 us to
1.16 us for `Between`, while Churn remained equivalent to uncached search and
no longer retains its materialized results. Production-shaped warm local search
remained effectively unchanged at 4.64 us/search with 4 allocations.

Reproduce the workload matrix with:

```sh
go test -run '^$' \
  -bench '^(BenchmarkLocalEqualityReuse|BenchmarkLocalOrderedReuse|BenchmarkLocalCompareByReuse|BenchmarkLocalBetweenReuse|BenchmarkLocalExcludeReuse)$' \
  -benchmem -benchtime=300ms -count=5 .
```

## Ordered index aggregation

Ordered indexes use block aggregates and binary search, reducing repeated
unions for broad range queries. The remaining `TimeRange` candidate is tracked
separately in the active roadmap.

## 2026-08-22: build-time range cardinality estimates

Ordered indexes now retain one cumulative cardinality per 128-value block.
Because distinct values inside one ordered leaf have disjoint posting lists,
the planner can combine those build-time prefix sums with one boundary block
to obtain an exact range cardinality without materializing a bitmap or walking
the full range. The retained overhead is approximately eight bytes per block.

`Greater`, `GreaterOrEqual`, `Less`, `LessOrEqual`, and `CompareBy` expose these
exact estimates to the adaptive `All` planner. `Between` exposes the upper bound
of the smaller side, which is sufficient for safe candidate selection, and
uses the same estimates to choose its materialized side. Empty ranges are
rejected before materialization. This lets selective range predicates become
the first candidate source instead of remaining behind known equality costs.

The estimate is checked against aggregate traversal at block boundaries and
outside the indexed domain. `Index.Explain` coverage verifies that a one-ID
range switches an otherwise broad `All` query to candidate-scan execution.
Reproduce the estimator microbenchmark with:

```sh
go test -run '^$' -bench '^BenchmarkOrderedIndexCardinalityEstimate/' \
  -benchmem -benchtime=300ms -count=5 .
```

## 2026-08-22: selective `Between` bound validation

`Between` now materializes the bound with the smaller exact cardinality first.
When that bound produces at most four IDs, it validates the other bound
directly against those candidates instead of materializing and intersecting a
second range bitmap. The threshold is shared with the measured adaptive `All`
candidate limit, and the implementation reuses ordered posting membership
without retaining a second copy of stored interval bounds.

The direct path preserves independent wildcard semantics for both bounds and
is also used by uncached and cache-fill `Local` searches. Wider candidate sets
retain aggregate bitmap intersection. This completes the active ordered/time-
range roadmap item without changing the public API or retained index layout.

## Roaring bitmap decisions

Benchmark implementations and detailed evidence live in `benchmark_test.go`.

Adopted:

- `ManyIterator` for result materialization at cardinality 4096 and above;
  smaller results use allocation-free `Iterate`.
- `AndAny` for `Between` intersections without a temporary union.
- `FastAnd` for final intersections of up to eight child bitmaps.
- `AddRange` for constructing the universe of internal IDs.

Evaluated and not adopted:

- `CheckedAdd`, `HeapOr`, and `ParAnd` were slower without a compensating
  allocation or retained-memory benefit.
- `FastOr` and `ParOr` won selected microbenchmarks but regressed real or skewed
  searches.
- `OrCardinality`, `AndCardinality`, and `Intersects` duplicate work unless a
  planner can use their answer to avoid materialization.
- `IntersectsWithInterval` is useful for ID ranges, which the current API does
  not expose.
- `RunOptimize` reduced some bitmap sizes but regressed production searches.
- `AddMany` helps bulk insertion, while the builder's contract is streaming.
- `Stats`, `DenseSize`, and `HasRunCompression` do not predict posting overlap
  well enough to justify hot-path scans.
- `Minimum`, `Maximum`, `NextValue`, `PreviousValue`, `Rank`, and `Select` need a
  pagination or ID-range API.
- `RemoveRange` and `Flip` need mutable deletion or complement operations.
- `Freeze` and `FrozenView` need a persistent or memory-mapped index lifetime;
  using them only in memory complicates ownership and peak memory.

Historical release-to-release benchmark comparisons remain in
[`BENCHMARK_OPTIMIZATIONS.md`](BENCHMARK_OPTIMIZATIONS.md).

## 2026-08-22: transparent rule inspection

`Inspect` now selects an internal implementation, assigns it through a caller-
owned `*Inspector`, and binds it to the representation chosen for one decorated
rule by a successful `Build`. Its direct methods report exact mode, the selected
strategy, consumed entry count, and unique external-rule count. The first
method call pins one coherent snapshot; `Reset` makes the next call pin the
latest successful build. Failed builds do not replace the published state.

Inspection decorators are removed before an index is published and the cleaned
tree is optimized again. Consequently inspection adds no wrapper, counter, or
branch to `Search`, `Visit`, or `Local`, and does not block equality or `All`
specialization. One inspector may identify only one schema location per build;
using it for multiple rules returns a build error.

`Inspector` is a sealed interface rather than a public concrete type. This
allows rule-specific snapshot implementations to evolve behind stable
observation methods.

## 2026-08-22: lossy index public contract

The first memory-bounded lossy-index stage now has a concrete API and failure
contract in [`docs/lossy-index.md`](docs/lossy-index.md). The planned API is a
sealed `LossyOption`, `Lossy(rule, options...)`, and a single required
`MemoryLimit(uint64)` byte limit. Strategy controls and false-positive targets
remain internal.

The limit covers storage retained exclusively by the decorated runtime rule,
with explicit exclusions for transient build state and index-wide structures.
Exact storage remains selected when it fits. Unsupported combinations fail
only when approximation is actually required; invalid policies, accounting
overflow, insufficient budgets, nested policies, and a policy directly around
`All` fail the build without publishing an index or inspector snapshot.
Independently budgeted children of `All` remain composable. These decisions
complete the contract stage without exporting constructors before a usable
analysis and planning path exists.

Memory accounting is deterministic rather than allocator-derived. Future
`Inspector.MemoryUsage` and `Inspector.MemoryLimit` methods report bytes under
an explicit representation model: canonical encoded values, portable Roaring
sizes, and fixed architecture-independent charges for logical slots and
metadata. Go object headers, capacity slack, allocator classes, and GC metadata
remain separate benchmark concerns. Planning and inspection use the same
formula, so identical inputs and strategies produce stable values across Go
versions and architectures.

## 2026-08-22: staged build pipeline

`Build` now has explicit analysis, planning, and materialization phases.
Analysis consumes and validates the source once, assigns stable internal IDs,
and retains representation-independent entries without constructing posting
indexes. The initial planner deliberately selects only the existing exact
mode; materialization then creates fresh rule state and populates that selected
representation.

This preserves immutable indexes, first-insertion result order, duplicate-ID
semantics, rebuild hints, and failure publication behavior. More importantly,
future lossy planners can inspect analyzed input and select a representation
before any complete exact index is constructed and discarded.

## 2026-08-22: canonical scalar encodings for lossy prototypes

The first lossy-index foundation now provides architecture-independent,
type-tagged encodings for booleans, strings, integer scalars, and floating-
point scalars. Floating-point encodings canonicalize signed zero and NaN so
semantically equivalent comparison values cannot be separated by a future
bucket strategy.

Ordered integer and floating-point scalars also have monotonic `uint64` keys.
The mapping follows `cmp.Compare` semantics, including NaN before non-NaN and
equal keys for negative and positive zero. Boundary tests cover every integer
width, infinities, NaN, signed zero, and unsupported types. Time values remain
deferred: the full `time.Time` domain cannot be represented injectively by one
`uint64`, so a time strategy needs an explicit supported range or a wider key.

## 2026-08-22: operator-aware lossy strategy prototypes

Test-only prototypes now exercise two representations against the production
canonical encodings: FNV-1a grouped hashes for equality and dense, ordered
buckets scaled to the observed scalar-key domain for range comparisons. The
property coverage checks multiple granularities and proves that every exact
equality and greater-or-equal match remains in the approximate result.

The 100K-value benchmark confirms that the operators need different lookup
layouts. Equality lookup stayed near 54-56 ns across 8, 12, and 16 bucket bits,
while accounted memory and candidates changed from 210,240 bytes / 386 IDs to
363,832 bytes / 30 IDs and 1,975,832 bytes / 2 IDs. Ordered range lookup uses a
dense bucket array so it can start at the query boundary without scanning a
map. Twelve bits gave the useful middle point for the selective benchmark at
about 320 ns, 296,008 accounted bytes, and 100 candidates. Sixteen bits kept
the same candidates but regressed to about 2.5 us and 1.4 MB because it unions
many tiny postings. Consequently the planner must choose granularity from the
budget and distribution instead of always selecting the finest representation.

The original experiment can be reproduced with:

```sh
go test -run '^$' -bench '^BenchmarkLossyStrategyPrototypes/' \
  -benchmem -benchtime=300ms -count=5 .
```

## 2026-08-22: first memory-bounded lossy representations

`Lossy` and `MemoryLimit` now promote the grouped-hash equality and observed-
domain ordered-bucket prototypes into compiled search representations. The
first supported rules are scalar `Include` and the four scalar ordered
comparisons. Build retains the exact rule when its deterministic accounted
size fits; otherwise it selects the finest bucket granularity that fits the
limit. Search preserves wildcards, duplicate external IDs, immutable
publication, and the no-false-negative invariant.

The policy rejects missing, repeated, and zero limits, nested policies, and a
limit directly around `All`. Unsupported scalar types fail only when the exact
representation exceeds the limit. `Inspect` reports the actual exact or lossy
mode and compiled strategy. Time values remain deferred pending an explicit
ordered-key range or wider key.

## 2026-08-22: lossy representation inspection statistics

`Inspector` now reports deterministic accounted memory usage and the configured
limit, indexed item and distinct-value counts, and the selected bucket count
for a `Lossy` rule. Optional measurements return an availability flag, so an
unavailable value cannot be confused with a meaningful zero. The same pinned
snapshot contains mode, strategy, counts, and representation statistics.

Inspection describes the representation actually selected by the build,
including exact representations retained when they fit the budget. Both
`Inspect(Lossy(...))` and `Lossy(Inspect(...))` bind to the same compiled
metadata. Temporary metadata decorators are removed with inspectors before
index publication, so searches retain no diagnostic wrapper. No false-positive
estimate is reported because the supported strategies do not have a meaningful
workload-independent probability model.

## 2026-08-22: aggregate lossy budgets for All

`Lossy(All(...), MemoryLimit(n))` now bounds the combined accounted leaf
representations. The planner retains every leaf exactly when their total exact
size fits. Otherwise it assigns bytes in proportion to exact size with a
deterministic largest-remainder allocation; nested `All` groups retain their
search shape but participate in one flattened allocation. A leaf that cannot
fit its assigned share fails the build rather than exceeding the aggregate
limit.

The correctness argument follows directly from conservative composition: each
compiled leaf result is a superset of its exact result, and intersecting those
supersets cannot remove an ID present in every exact child. Tests exercise both
exact and lossy composite selection, enforce the aggregate accounting limit,
compare composite results against an exact index across varied queries, and
continue to reject nested `Lossy` policies.

## 2026-08-22: pooled lossy All allocation

The aggregate `Lossy(All(...))` allocator now discovers and reserves every
leaf's minimum viable representation before distributing the remaining budget
in proportion to exact-size headroom. It compiles those initial selections,
reclaims bytes left unused by discrete representation granularities, and
deterministically spends the pool on the smallest affordable child upgrades.

This removes failures caused solely by an undersized proportional share when a
feasible allocation exists within the aggregate limit. The selected leaf
representations remain deterministic, and their combined accounted memory
never exceeds the caller's limit. A skewed equality test covers a small child
with a relatively expensive minimum beside a much larger, highly compressible
child and verifies both successful construction and the hard aggregate bound.

## 2026-08-22: pooled lossy All cost and quality baseline

`BenchmarkLossyAllPlanning` now measures exact and pooled 50% and 25% memory
budgets at 10K entries with two, four, and eight equality children.
`BenchmarkLossyAllSearchQuality` reports search latency, allocations, candidates
per query, and the observed false-positive rate for a four-child workload.

The first benchmark run exposed repeated work in pooled redistribution. The
allocator now caches each leaf's next compiled upgrade, and equality lossy
compilation hashes each distinct value once per compilation instead of once per
bucket granularity. On Apple M1 Max, the two-child 50% case fell from about
3.4 seconds and 60.9 million allocations to 821 milliseconds and 6.4 million
allocations. Exact construction took 4.5 milliseconds and 6.3 thousand
allocations. The improvement is material, but the remaining gap makes repeated
representation construction the next allocator bottleneck; operator expansion
is not justified until that cost is reduced.

## 2026-08-22: pooled equality representation planning

Pooled `Lossy(All(...))` planning now prepares each equality leaf's complete
bucket-granularity ladder once and reuses those immutable candidates during
minimum-limit discovery, proportional allocation, and redistribution. The
ladder remains lazy: when the exact representation fits the aggregate budget,
no lossy buckets are constructed.

On Apple M1 Max, the median two-child 50% planning case fell from 821
milliseconds and about 6.4 million allocations to 24.6 milliseconds and
158.6 thousand allocations. The exact case remained on its direct path at 2.8
milliseconds and 6.1 thousand allocations. Selection semantics, accounted
memory, deterministic redistribution, and the public API are unchanged.

Reproduce the matrix with:

```sh
go test -run '^$' -bench '^BenchmarkLossyAll(Planning|SearchQuality)/' \
  -benchmem -benchtime=300ms -count=5 .
```

## 2026-08-22: pooled ordered representation planning

Pooled `Lossy(All(...))` planning now prepares each ordered leaf's complete
bucket-granularity ladder once and reuses those immutable candidates during
minimum-limit discovery, proportional allocation, and redistribution. As with
equality leaves, exact representations retain the lazy direct path and do not
construct lossy buckets when the aggregate budget can hold them.

`BenchmarkLossyAllOrderedPlanning` covers 10K-entry conjunctions with two and
four ordered leaves at 75% of their exact accounted size. On Apple M1 Max, the
median two-child case fell from 994 milliseconds and about 7.79 million
allocations to 50.6 milliseconds and 519 thousand allocations. The four-child
case fell from 2.41 seconds and about 19.3 million allocations to 121
milliseconds and 1.33 million allocations. Selection semantics, deterministic
accounting, and the public API are unchanged.

Reproduce the matrix with:

```sh
go test -run '^$' -bench '^BenchmarkLossyAllOrderedPlanning/' \
  -benchmem -benchtime=200ms -count=3 .
```

## 2026-08-23: lossy scalar and ordered-boundary properties

Lossy/exact superset tests now cover equality over signed integers, unsigned
integers, floating-point values, and strings, plus all four inclusive and
exclusive ordered operators over signed integers, unsigned integers, and
floating-point values. The generated cases include missing stored and query
values, repeated postings, integer extrema, infinities, NaN, signed zero, and
the smallest non-zero floating-point values.

These tests exercise the compiled search representations, not only their
canonical encodings, and verify that lossy searches never omit any exact
result at adversarial ordered-domain boundaries. This completes the roadmap's
property and boundary-test requirement.

## 2026-08-23: lossy scale benchmark matrix

The reproducible lossy baseline now covers 10K, 100K, and 1M rules for both
four-child equality and ordered conjunctions. Exact, 50%, and 25% accounted-
memory budgets report build time and allocations, accounted bytes, search
latency and allocations, candidates per query, and observed false-positive
rate relative to an exact index.

Use a fixed iteration count for build comparisons so the 1M-rule cases remain
practical:

```sh
go test -run '^$' -bench '^BenchmarkLossyScalePlanning/' \
  -benchmem -benchtime=1x -count=3 .
go test -run '^$' -bench '^BenchmarkLossyScaleSearch/' \
  -benchmem -benchtime=300ms -count=5 .
```

## 2026-08-23: lossy leaf estimates in `All`

Lossy equality leaves now expose their result cardinality directly from the
selected immutable hash bucket. Their wildcard and concrete bucket
postings are disjoint, so the estimate is exact without materializing a result
bitmap. The same representations also validate a single internal ID directly.
This lets the adaptive `All` planner put a selective lossy leaf first, reject
known-empty queries early, and use candidate scanning at the existing measured
four-ID threshold.

`BenchmarkLossyAllSelectivePlanning` isolates a 100K-rule, eight-child `All`
whose lossy equality leaf returns one candidate. On Apple M1 Max, medians of
five runs improved from 1.186 us/op with the lossy estimate hidden to 602
ns/op with adaptive planning. Allocation traffic fell from 504 B/op and 35
allocations to 232 B/op and 13 allocations.

Reproduce the comparison with:

```sh
go test -run '^$' -bench '^BenchmarkLossyAllSelectivePlanning/' \
  -benchmem -benchtime=200ms -count=5 .
```

## 2026-08-23: streaming input materialization before lossy selection

`Build` now validates, assigns the stable internal ID, and inserts each
constraint before asking the input iterator for its next value. It no longer
retains a second slice containing every yielded constraint until posting-list
materialization. This also makes `Build` compatible with iterators that reuse
mutable backing storage between yields. Validation failures still stop input
consumption at the failing entry, discard the partial state, and leave the last
successful builder hints unpublished.

The final exact-versus-lossy decision remains after complete input consumption.
Selecting a hash granularity earlier would require repeated downgrades as
postings grow; ordered representations also depend on the final value range.
Keeping that decision at the end preserves deterministic representation
selection, accounted retained memory, and the no-false-negatives guarantee,
while streaming insertion removes the input-sized peak allocation that
motivated early switching.

`BenchmarkLossyStreamingBuild` uses 100K distinct 256-byte constraints and a
1 MiB equality budget. On Apple M1 Max, five three-iteration runs reduced
allocated bytes from about 203.6 MB/build to 48.0 MB/build (76%) and median
build time from 126.9 ms to 120.0 ms (5%). Allocation count and retained
accounted index bytes were materially unchanged because the removed cost was
the temporary analyzed-entry slice. Reproduce with:

```sh
go test -run '^$' -bench '^BenchmarkLossyStreamingBuild$' \
  -benchmem -benchtime=3x -count=5 .
```

## 2026-08-23: lossy ordered estimates in `All`

Lossy ordered leaves now compute their exact result cardinality by summing the
selected immutable bucket cardinalities and the disjoint wildcard posting.
They also detect empty results and validate a candidate ID directly against
the same bucket range. This lets the adaptive `All` planner order selective
lossy range predicates before broad children, exit before materialization when
the range is empty, and use candidate scanning at the existing measured
four-ID threshold.

`BenchmarkLossyAllSelectiveOrderedPlanning` isolates a 100K-rule, eight-child
`All` whose lossy ordered leaf returns one candidate. On Apple M1 Max, medians
of five runs improved from 1.421 us/op with the estimate hidden to 402 ns/op
with adaptive planning. Allocation traffic fell from 628 B/op and 46
allocations to 64 B/op and four allocations.

Reproduce the comparison with:

```sh
go test -run '^$' -bench '^BenchmarkLossyAllSelectiveOrderedPlanning/' \
  -benchmem -benchtime=200ms -count=5 .
```

## 2026-08-23: nested `All` estimates and empty propagation

A nested `All` now exposes the minimum cheap estimate available from its
children. Because an intersection cannot be larger than any child result, this
is a safe planner estimate even when other children have no cheap estimate.
Nested groups also propagate a cheaply known empty child to their parent. The
outer planner can therefore rank a selective nested group ahead of a broad
sibling and stop before materializing anything when a descendant is known to
be empty.

`BenchmarkNestedAllEstimate` isolates a broad 100K-ID outer child followed by
a nested group with one matching ID. On Apple M1 Max, medians of five runs
improved from 431 ns/op with the nested estimate hidden to 393 ns/op with the
estimate propagated. Allocation traffic fell from 144 B/op and 10 allocations
to 96 B/op and six allocations. Reproduce with:

```sh
go test -run '^$' -bench '^BenchmarkNestedAllEstimate$' \
  -benchmem -benchtime=200ms -count=5 .
```

## 2026-08-23: inspected-rule execution metrics

`Inspect` now accumulates monotonic counters from real execution paths for
bitmap searches, materializations, direct candidate checks, and empty results.
A fixed, allocation-free histogram groups observed result cardinalities into
zero, one, 2-4, 5-16, 17-256, and above-256 buckets. An inspected top-level
`All` records completed searches and their final cardinality while retaining
the specialized append path; an inspected child records direct candidate
checks without forcing materialization.

The observation wrapper is present only for explicitly inspected rules.
Planner capability checks unwrap it for shared wildcards and direct candidate
membership, and build traversal unwraps it for bitmap interning, exclusions,
and search preparation. Schemas without `Inspect` retain their previous
compiled tree and hot path. Cache hit, miss, admission, eviction, entry, and
capacity accounting remains a separate follow-up because it must be attached
to each `Local` without adding shared mutable cache state to the index.

## 2026-08-24: inspector follows the latest successful build

`Inspector.Reset` and build-snapshot pinning were removed. Every build-fact
method now reads the latest immutable snapshot atomically published by a
successful `Build`; failed builds continue to leave that snapshot unchanged.
This removes lifecycle state and an easy-to-miss refresh requirement from the
inspection API. Runtime counters remain monotonic for the inspector lifetime.

Individual method calls remain safe during concurrent publication. Callers
that require several fields to come from exactly one build must serialize
those reads with rebuilds externally; the method-oriented interface no longer
retains old index generations for that purpose.

## 2026-08-24: explicit inspector snapshots

`Inspector` now exposes only `Snapshot() InspectorSnapshot`. All build-fact
and runtime-metric methods moved to the returned immutable value. `Snapshot`
loads the latest published build generation once and copies the current atomic
runtime counters, so every later method call on that value observes the same
generation and counter sample even when rebuilds and searches continue.

This restores coherent multi-field inspection without bringing back hidden
pinning or a refresh lifecycle. A caller chooses the observation boundary
explicitly by taking another snapshot.

## 2026-08-24: diagnostics consolidated in Inspect

`Index.Explain` and its `SearchPlan`, `PlanChild`, and `SearchStrategy` types
were removed after `Inspect` gained coherent build facts, monotonic per-rule
runtime counters, gauges, and a result-cardinality histogram. Diagnostics now
describe aggregate behavior observed on real `Search`, `Visit`, and `Local`
executions instead of running an extra query that materializes every direct
`All` child and can therefore change the work being measured.

This completes the roadmap consolidation step while preserving the ordinary
schema hot path: only explicitly inspected rules receive observation wrappers.

## 2026-08-24: inspected-rule runtime overhead baseline

`BenchmarkInspectRuntimeOverhead` compares equivalent 10K-rule schemas with
and without `Inspect` for shared `Index` and warm `Local` searches. It covers a
materialized equality leaf, the specialized top-level `All` path, and direct
candidate checks against an inspected broad child. The benchmark verifies that
each inspected shape records the execution path it actually takes.

On Apple M1 Max, medians of five runs put an inspected equality leaf at 96.6 ns
for `Index.Search`, versus 90.2 ns without metrics, and at 45.7 ns for warm
`Local.Search`, versus 40.8 ns. An inspected top-level `All` measured 236.7 ns
versus 233.7 ns for `Index.Search` and 183.3 ns versus 181.1 ns for warm
`Local.Search`. Observation added no allocations or allocation bytes in any
paired case. The fixed atomic-counter cost is most visible on the shortest
leaf path; the production-representative composite path remained within about
1-2% in these runs.

Reproduce with:

```sh
go test -run '^$' -bench '^BenchmarkInspectRuntimeOverhead/' \
  -benchmem -benchtime=300ms -count=5 .
```

## 2026-08-24: inspected `Local` cache accounting

Explicitly inspected rules now count `Local` cache hits, misses, admissions,
and evictions. Live gauges aggregate retained bitmap entries and entry capacity
across inspected caches; `Local.Close` releases that cache's contribution while
the event counters remain monotonic. Observation is scoped to the inspected
subtree and leaves ordinary index searches without atomic counter traffic.

The existing inspector overhead benchmark continues to report zero allocation
bytes and zero allocations for both ordinary and inspected warm `Local`
equality searches. On Apple M1 Max, medians of three runs were 41.05 ns/search
without inspection and 50.68 ns/search with cache accounting.

Reproduce with:

```sh
go test -run '^$' \
  -bench '^BenchmarkInspectRuntimeOverhead/Local/(Leaf|InspectedLeaf)$' \
  -benchmem -benchtime=300ms -count=3 .
```

## 2026-08-24: reusable closed `Local` contexts

`Local.Close` replaces the public reset lifecycle. Close clears query-derived
caches and returns the internal bitmap context to a pool owned by the same
immutable index; a closed handle cannot be used again. Only the internal
context is pooled, preventing an old `*Local` alias from referring to a handle
that has since been issued to another goroutine.

`BenchmarkLocalLifecycleReuse` compares construction and cleanup with acquiring
and closing the pooled context. On Apple M1 Max, medians of five runs improved
from 70.48 ns, 256 B, and 2 allocations per lifecycle to 35.27 ns, 24 B, and 1
allocation. The remaining allocation is the non-reusable public handle that
prevents use-after-close aliases from reaching a context issued to another
goroutine.

Reproduce with:

```sh
go test -run '^$' -bench '^BenchmarkLocalLifecycleReuse$' \
  -benchmem -benchtime=300ms -count=5 .
```

## 2026-08-24: bounded equality-first planning

`All` now evaluates constant-time equality bounds before ordered cardinality
estimates. Once a cheap bound is at or below the measured four-ID candidate
scan threshold, the executor materializes that candidate and validates the
remaining predicates by ID without performing ordered boundary-cardinality
work. Nested `All` nodes expose their cheapest equality bound, including exact
empty results, instead of remaining opaque to the outer planner. Inspected and
lossy equality rules preserve the same behavior.

Regression tests verify both flat and nested plans without relying on timing:
the ordered-like estimator is never invoked, its bitmap is never materialized,
and only candidate ID checks run. The full test suite and `git diff --check`
pass with no public API changes.

Wider cheap bounds deliberately retain the existing complete ranking path.
Experiments that scanned 150 production-shape equality candidates against the
remaining ordered rules, or materialized those ordered rules without ranking,
were substantially slower and were discarded. The production query's cheap
intersection therefore does not yet recover warm `Local` latency; extending
bounded planning beyond the four-ID threshold remains active roadmap work.

Reproduce the functional and threshold coverage with:

```sh
go test ./...
go test -run '^$' \
  -bench '^(BenchmarkNestedAllEstimate|BenchmarkAllMaterializedCandidateFallback)$' \
  -benchmem -benchtime=500ms -count=5 .
```

## 2026-08-24: rejected bounded equality-intersection experiment

A partial `All` plan was prototyped that descended through nested groups and
intersected equality postings containing at most 256 IDs. It retained the
existing four-ID candidate-scan threshold: only an intersection at or below
that threshold could bypass the remaining ordered cardinality estimates.
Functional coverage confirmed that the approach preserved results and could
avoid an ordered-like estimator in a constructed nested case.

The optimization was rejected because the production shape did not improve.
On Apple M1 Max with Go 1.26.0, paired five-run, 500 ms measurements compared
the 256-ID probe with the same implementation locally disabled. Median
`Index.Search` was 102.1 us in both configurations; warm `Local.Search` was
4.215 us with the probe and 4.241 us without it. Equality-only Index and Local
searches also remained within run noise, and allocation counts and bytes were
unchanged. The experiment added planner complexity and eager bitmap work
without materially recovering full-schema warm or parallel `Local` latency,
so none of its runtime code was retained.

This closes the wider equality-intersection variant while preserving the
strict candidate threshold supported by `BenchmarkAllExecutionThreshold`.
Future work should target the next prioritized item rather than retrying eager
equality intersection without new workload evidence or a cheaper partial-plan
representation.

Reproduce with:

```sh
go test -run '^$' \
  -bench '^(BenchmarkProductionShapeSearch|BenchmarkProductionShapeEqualityOnlySearch)$' \
  -benchmem -benchtime=500ms -count=5 .
```

## 2026-08-24: range-cardinality rollback experiment

An isolated copy of `f50d22b` was benchmarked with the range-cardinality part
of `05f8065` removed while retaining all later ordered lookup optimizations.
`orderedRule`, `compareByRule`, and `betweenRule` no longer implemented the
planner's `cardinalityEstimator`; their original measured-cardinality paths
were restored, and `orderedIndex.blockPrefix` was removed.

On Apple M1 Max with Go 1.26.0, medians of eight one-second runs were:

| Production-shaped operation | `f50d22b` | Without range estimates | Delta |
| --- | ---: | ---: | ---: |
| `Index.Search` | 96.17 us | 93.12 us | -3.2% |
| warm `Local.Search` | 5.870 us | 5.293 us | -9.8% |
| parallel warm local search | 2.770 us/search | 2.489 us/search | -10.1% |
| `Build` | 32.38 ms | 33.96 ms | +4.9% |

Search allocation counts and bytes were effectively unchanged. Five retained
memory runs were also indistinguishable: the index retained about 1.289 MB,
warm local state retained 88,448 B, and adaptive local state retained about
105,296 B in both versions. The build-time difference should be treated as
inconclusive because the two suites were run sequentially; removing the prefix
itself did not produce a measurable retained-memory improvement at this data
shape.

The experiment confirms that runtime range estimates still cost roughly 10%
on the current warm-local production shape. It does not recover the full
pre-`05f8065` performance because subsequent planner and cache changes remain,
and restoring measured range cardinality is not a complete replacement for a
lazy, equality-informed plan.

As a control, `BenchmarkProductionShapeEqualityOnlySearch` removes both
`Between` fields and both `CompareBy` fields while retaining the same 38,098
constraints and all 14 `Include` fields, including platform name. Comparing
the same current and rollback binaries over eight one-second runs gave 3.526 us
versus 3.502 us for warm `Local.Search`, a difference of only -0.7%. This is
within run-to-run variation and is substantially smaller than the -9.8% seen
with the complete schema. Equality-only `Index.Search` measured 12.11 us versus
11.72 us (-3.3%), but the sequential suites exhibited drift of comparable size
and do not establish an index-search effect. The local control therefore
isolates the reproducible regression to the presence of range/ordered branches,
not to the general `All` or `Include` planning path.

Reproduce each variant with:

```sh
go test -run '^$' \
  -bench '^(BenchmarkProductionShapeSearch|BenchmarkProductionShapeParallelLocalBatch100|BenchmarkProductionShapeBuild)$' \
  -benchmem -benchtime=1s -count=8 .

go test -run '^$' \
  -bench '^BenchmarkProductionShape(RetainedMemory|LocalRetainedMemory)$' \
  -benchmem -benchtime=3x -count=5 .

go test -run '^$' -bench '^BenchmarkProductionShapeEqualityOnlySearch$' \
  -benchmem -benchtime=1s -count=8 .
```

## 2026-08-24: cache-aware warm `Local` ordered planning

`All` now consults already-admitted per-node `Local` caches before performing
fresh cardinality estimates for `orderedRule`, `CompareBy`, and `Between`.
Planning uses a read-only lookup for cardinality and, for a direct child,
reuses the cached immutable bitmap itself. This avoids both the repeated
ordered boundary/cardinality scan and a second temporary materialization. Cache
misses do not create entries, affect admission, or count twice; successful
reuse preserves hit accounting and replacement state. Nested `All` estimates
recursively consume cached child cardinalities without treating the nested
group as an exact materialized result.

On Apple M1 Max with Go 1.26.0, medians of five one-second production-shaped
runs improved warm `Local.Search` from the previously recorded 5.870 us to
5.346 us (-8.9%). Parallel warm local search improved from 2.770 us/search to
2.383 us/search (-14.0%). The full-schema result retained 4 allocations and
about 4.66 KB per search. Equality-only warm local search remained at 3.875 us,
2 allocations, and 2.33 KB. Ordered and `CompareBy` unique churn remained
equivalent to `Index.Search`, while repeated `Between` retained its existing
16 B and one allocation per search. Warm and adaptive local retained memory
were unchanged at 88,448 B and 105,296 B per local.

A five-second CPU profile measured `allRule.rankChildren` at 7.0% cumulative
CPU, down from 13.7% in the preceding profile, and no
`orderedIndex.estimateCardinality` specialization remained in the hot list.
The Roaring copy-on-write intersection remains the primary application-level
search cost.

Reproduce with:

```sh
go test -run '^$' \
  -bench '^(BenchmarkProductionShapeSearch|BenchmarkProductionShapeEqualityOnlySearch|BenchmarkProductionShapeParallelLocalBatch100)$' \
  -benchmem -benchtime=1s -count=5 .

go test -run '^$' \
  -bench '^(BenchmarkLocalOrderedReuse|BenchmarkLocalCompareByReuse|BenchmarkLocalBetweenReuse)/(Repeat|Alternate|Churn)/(Index|Local)$' \
  -benchmem -benchtime=500ms -count=3 .

go test -run '^$' -bench '^BenchmarkProductionShapeLocalRetainedMemory$' \
  -benchmem -benchtime=3x -count=3 .
```

## 2026-08-24: production search CPU and allocation profile

The 38,098-rule production-shaped search benchmark was profiled separately for
`Index` and a warm `Local`, so the very different execution paths do not blur
each other's samples. On Apple M1 Max with Go 1.26.0, medians of five
one-second runs were 100.861 us, 91,914 B, and 32 allocations per `Index`
search, and 4.076 us, 4,662 B, and four allocations per warm `Local` search.

Sequential five-second CPU profiles with `GOMAXPROCS=1` identify two distinct
cost centers. In warm `Local` search,
`allRule.intersectRankedInOrderObserved` accounted for 52.5% cumulative CPU and
Roaring `Bitmap.And` for 48.9%. Its `arrayContainer.iandBitmap` implementation
accounted for 33.1% cumulative CPU, including 14.9% flat in
`bitmapContainer.bitValue`. `allRule.rankChildren` was only 7.3% cumulative,
all of it attributed to reuse of the cached local plan, so planning is no
longer the primary optimization target.

An allocation profile of the same warm path attributes 95.7% of allocated
space to Roaring `arrayContainer.clone`, reached from the copy-on-write
`Bitmap.And` operation. This explains both the benchmark's approximately
4.66 KB per search and a material part of the runtime allocation/GC samples.
The highest-value follow-up is therefore to avoid or amortize the writable
container clone for the first accumulated intersection, while preserving the
immutability of cached postings. Reducing result conversion is secondary:
`appendBitmapValues` accounted for only 3.1% cumulative CPU.

Uncached `Index` search has a different bottleneck. Roaring `Bitmap.Or`
accounted for 54.8% cumulative CPU while materializing ordered results;
`betweenRule.searchBitmaps` accounted for 50.7%, and the time-valued
`orderedIndex.walk` for 45.1%. Roaring `union2by2` alone used 28.1% flat CPU.
For repeated production queries, using a warm `Local` avoids most of this work
and was about 24.7 times faster in the median benchmark. If uncached `Index`
latency must improve, range-result materialization and union strategy are the
relevant targets, rather than child ranking.

Reproduce the measurements and collect profiles with:

```sh
go test -run '^$' -bench '^BenchmarkProductionShapeSearch/(Index|Local)$' \
  -benchmem -benchtime=1s -count=5 .

GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkProductionShapeSearch/Index$' -benchtime=5s -count=1 \
  -cpuprofile=/tmp/ruleix-production-index.cpu .
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkProductionShapeSearch/Local$' -benchtime=5s -count=1 \
  -cpuprofile=/tmp/ruleix-production-local.cpu .

go tool pprof -top -cum /tmp/ruleix-production-index.cpu
go tool pprof -top -cum /tmp/ruleix-production-local.cpu

GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkProductionShapeSearch/Local$' -benchtime=1s -count=1 \
  -memprofile=/tmp/ruleix-production-local.mem -memprofilerate=1 .
go tool pprof -top -alloc_space /tmp/ruleix-production-local.mem
```

The allocation profiler's rate of one intentionally increases benchmark
latency; use that run only for allocation attribution, not timing comparison.

## 2026-08-24: sequential intersection follow-up experiments

Three small executor changes tested whether the production profile's hot
copy-on-write intersection could be avoided without changing bitmap ownership.
None was retained.

Switching from bitmap intersection to direct rule validation after an
accumulated result reached the existing four-candidate limit was effectively
neutral: warm `Local` search measured a median 4.129 us versus the immediately
preceding 4.076 us baseline, with the same 4,662 B and four allocations. The
production intersection does not become that selective early enough for this
branch to matter.

Using a separate 256-ID limit for the accumulated result was decisively worse.
Warm `Local` search rose to a median 182.959 us, 4,940 B, and ten allocations;
`Index` search rose to 202.392 us despite allocation traffic falling to 32,120
B and 20 allocations. Replacing bitmap operations with hundreds of scalar
checks across the remaining production rules costs far more than the avoided
container clones.

Finally, the first `dst.Or(first); dst.And(second)` pair was replaced with the
functional `roaring.And(first, second)`, followed by a copy-on-write transfer
of the exact result into `dst`. Warm `Local` search regressed to a median 4.852
us, 5,439 B, and 22 allocations; `Index` search measured 105.022 us, 92,695 B,
and 50 allocations. Although functional `And` constructs only intersecting
containers, the current `Rule.search` contract still requires moving that
result into the caller-owned destination, so bitmap creation and ownership
transfer outweigh the saved lazy clone.

These results narrow the useful next experiment to ownership rather than
another intersection heuristic: either let an internal search return an owned
bitmap, or add an `AndTo(dst, first, second)` primitive that builds the exact
intersection directly in reusable destination storage. Raising candidate
limits or wrapping functional `And` around the existing destination contract
should not be revisited for this workload.

Reproduce the retained baseline used for these comparisons with:

```sh
go test -run '^$' -bench '^BenchmarkProductionShapeSearch/(Index|Local)$' \
  -benchmem -benchtime=1s -count=5 .
```

## 2026-08-24: owned nested intersection experiment

A follow-up prototype let nested `All` return an owned bitmap directly to its
parent. The nested group materialized its ranked children, used `FastAnd`, and
transferred the result without copying it through the caller-provided `dst`.
This tested the ownership half of the preceding profile's recommendation while
leaving the public search API unchanged.

The change was not retained. Against a three-run baseline of approximately
4.11 us, 4,662 B, and four allocations, five one-second warm `Local` runs
measured a median 4.482 us, 4,808 B, and eight allocations. `Index` remained
near 102.35 us but increased from 32 to 36 allocations. Avoiding the
destination transfer was not sufficient: `FastAnd` still created a new result
bitmap for each nested group, and its four incremental allocations outweighed
the saved copy-on-write work.

An owned-result contract is therefore only promising together with either a
reusable destination-aware intersection primitive such as
`AndTo(dst, first, second)`, which Roaring v2.4.4 does not expose, or a bounded
cache that amortizes the compound result across repeated `Local` queries.

## 2026-08-24: compile-time flattening of exact nested `All`

Exact nested `All` groups are now flattened into their parent during index
optimization. Conjunction is associative, so the compiled executor can rank
and intersect the leaf postings directly instead of materializing a compound
bitmap and intersecting that temporary result again. Inspected nodes keep
their wrapper and lossy nodes have distinct runtime types, preserving those
execution and observation boundaries.

On Apple M1 Max with Go 1.26.0, the initial five-run comparison improved warm
production-shaped `Local` search from the preceding 4.076 us, 4,662 B, and
four allocations to a median 2.510 us, 2,331 B, and two allocations: 38.4%
lower latency and half the allocation traffic. A later five-run validation
measured a 2.599 us median. Parallel warm local search measured a median 1.364
us/search, 234,528 B, and 207 allocations per 100-search batch. `Index` search
measured a median 101.536 us, 89,583 B, and 30 allocations, removing two
allocations and about 2.3 KB while remaining close to its previous latency.

A new five-second CPU profile attributes 55.0% cumulative CPU to the remaining
root `All` intersection and 54.4% to Roaring `Bitmap.And`. Only one
copy-on-write `arrayContainer.clone` remains, at 10.2% cumulative CPU, versus
the two compound/root clones and approximately 4.66 KB per search before
flattening. `allRule.rankChildren` accounts for 10.8% cumulative CPU.

Two follow-ups were rejected. Returning an owned `FastAnd` result had already
shown that allocation of a new result bitmap outweighs ownership transfer.
A root-only reusable candidate buffer eliminated search allocations but
regressed warm `Local` search to a median 10.433 us because repeated public
`Bitmap.Contains` calls redo container lookup for every candidate and child.
The remaining clone requires a container-level reusable `AndTo` primitive;
vendoring or forking Roaring solely for that operation is not justified by the
remaining approximately 2.3 KB and two allocations without an upstreamable
API and broader workload evidence.

Warm and adaptive retained memory measured 88,832 B and 105,680 B per local,
384 B above the preceding nested-plan baseline because the flattened root plan
stores one additional child slot. Build remained at a median 34.87 ms, 5.83
MB, and about 24.7K allocations.

Reproduce the retained measurements with:

```sh
go test -run '^$' \
  -bench '^(BenchmarkProductionShapeSearch|BenchmarkProductionShapeEqualityOnlySearch|BenchmarkProductionShapeParallelLocalBatch100)$' \
  -benchmem -benchtime=1s -count=5 .

go test -run '^$' \
  -bench '^(BenchmarkProductionShapeBuild|BenchmarkProductionShapeLocalRetainedMemory)$' \
  -benchmem -benchtime=3x -count=3 .

GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkProductionShapeSearch/Local$' -benchtime=5s -count=1 \
  -cpuprofile=/tmp/ruleix-production-flat-local.cpu .
```

## 2026-08-24: smaller exact ordered-index aggregate blocks

The repeated production CPU profile attributed 57.7% cumulative `Index`
search CPU to Roaring `Bitmap.Or`, 49.5% to the uncached `Between` range, and
45.5% to `orderedIndex.walk`. Exact ordered indexes already aggregate complete
blocks, so reducing the block size from 128 to 64 limits the number of
individual posting lists visited at the two range boundaries.

On Apple M1 Max with Go 1.26.0, five one-second production-shaped runs reduced
median uncached `Index` search from 101.691 us to 92.738 us (8.8%). Allocation
count remained 30/search and allocated bytes changed from 89,582 to 89,695.
Warm `Local` search remained effectively unchanged at 2.585 us, 2,331 B, and
two allocations. Three retained-memory runs measured about 1.315 MB/index
versus 1.289 MB with 128-value blocks, a 2.0% increase. Build allocation rose
from approximately 4.90 MB to 5.13 MB while build time remained near 35 ms.

A 32-value variant was rejected: median uncached search regressed to 145.269
us, retained memory rose to approximately 1.380 MB/index, and build allocation
rose to 5.42 MB. At that granularity, the extra aggregate bitmaps and unions
outweigh the smaller boundary fragments.

Reproduce the search comparison with:

```sh
go test -run '^$' -bench '^BenchmarkProductionShapeSearch/(Index|Local)$' \
  -benchmem -benchtime=1s -count=5 .
```

Reproduce the build and retained-memory checks with:

```sh
go test -run '^$' \
  -bench '^(BenchmarkProductionShapeBuild|BenchmarkProductionShapeRetainedMemory)$' \
  -benchmem -benchtime=300ms -count=3 .
```

## 2026-08-24: candidate-aware uncached `Between`

Uncached `All` execution can now pass its accumulated bitmap directly to a
following `Between` child. The range rule narrows that bitmap with Roaring
`AndAny` for each bound instead of first materializing both complete ordered
range unions and intersecting their result. The optimization only removes IDs
from the caller-owned candidate set and uses the existing exact block postings.
Warm `Local` execution deliberately retains its materialized-cache path.

On Apple M1 Max with Go 1.26.0, five one-second production-shaped runs reduced
median `Index` search from the 64-value-block baseline of 92.738 us to 45.715
us (50.7%). Allocated bytes fell from 89,695 to 73,396 per search and
allocations fell from 30 to 28. Median warm `Local` search was 2.602 us with
the unchanged 2,331 B and two allocations.

The new five-second `Index` CPU profile attributes 21.9% cumulative CPU to
`betweenRule.filterCandidates` and 20.4% to `Bitmap.AndAny`; the previous full
range materialization attributed 57.7% to `Bitmap.Or`, 49.5% to
`betweenRule.searchBitmaps`, and 45.5% to `orderedIndex.walk`. Remaining
`Bitmap.Or` work is 16.7% cumulative, led by `CompareBy` ordered unions rather
than the `Between` range.

A first prototype also applied filtering to warm `Local` searches and was
rejected: it bypassed admitted range-cache entries and regressed median search
to about 19.4 us, 21,506 B, and 12 allocations. Restricting the optimization
to uncached `Index` searches restores the cache-backed local path.

Reproduce the measurements and CPU profile with:

```sh
go test -run '^$' -bench '^BenchmarkProductionShapeSearch/(Index|Local)$' \
  -benchmem -benchtime=1s -count=5 .
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkProductionShapeSearch/Index$' -benchtime=5s -count=1 \
  -cpuprofile=/tmp/ruleix-production-candidate-index.cpu .
```

## 2026-08-24: candidate-aware `CompareBy` experiment

The candidate-filter contract retained for uncached `Between` was also tested
on `CompareBy`. The prototype collected the wildcard, equality posting, and
the four operator-specific ordered ranges, then narrowed the existing `All`
candidate bitmap through one `AndAny` call.

The change was not retained. Against the candidate-aware `Between` baseline of
45.715 us, 73,396 B, and 28 allocations per production-shaped `Index` search,
the 32-entry inline posting buffer measured a 45.407 us median, 73,573 B, and
31 allocations. Raising the inline buffer to 128 did not remove the internal
union allocations and regressed the median to 48.107 us with the same bytes
and allocation count. Combining five operator families creates enough union
inputs that `AndAny` does not improve on `CompareBy`'s existing direct union.

Keep candidate filtering specialized to `Between`. A future `CompareBy`
optimization should instead reduce the number of operator-range postings, for
example with per-operator prefix/suffix aggregates, before reconsidering the
candidate-filter contract.

Reproduce the retained baseline with:

```sh
go test -run '^$' -bench '^BenchmarkProductionShapeSearch/(Index|Local)$' \
  -benchmem -benchtime=1s -count=5 .
```

## 2026-08-24: bounded `CompareBy` range aggregates

Each populated `CompareBy` ordered index now builds a second aggregation level
over groups of eight ordinary blocks when the index contains at least two full
groups. A wide operator range visits one bitmap per group plus its boundary
blocks. Unlike a cumulative prefix or suffix bitmap for every boundary, this
duplicates each covered posting only once and keeps the additional retained
memory bounded.

On Apple M1 Max with Go 1.26.0, five one-second production-shaped runs reduced
median uncached `Index` search from 46.323 us to 43.426 us (6.3%). Allocated
bytes remained 73,396/search and allocations remained 28. Warm `Local` search
remained effectively flat at a 2.620 us median, 2,331 B, and two allocations.
Five fixed-count retained-memory runs remained near 1.315 MB/index. Median build
time was 35.0 ms with approximately 5.50 MB and 24,858 allocations.

Reproduce with:

```sh
go test -run '^$' -bench '^BenchmarkProductionShapeSearch/(Index|Local)$' \
  -benchmem -benchtime=1s -count=5 .
go test -run '^$' \
  -bench '^(BenchmarkProductionShapeBuild|BenchmarkProductionShapeRetainedMemory)$' \
  -benchmem -benchtime=5x -count=5 .
```

## 2026-08-24: candidate-aware `CompareBy` after range aggregation

Candidate filtering was repeated after bounded range aggregates reduced the
number of postings produced by each populated operator index. The prototype
again collected wildcard, equality, and operator-range bitmaps and narrowed
the existing `All` candidate through one `AndAny` call.

The change was not retained. Against the aggregated baseline of 43.426 us,
73,396 B, and 28 allocations, five one-second production-shaped runs measured
a 46.270 us median, 73,573 B, and 31 allocations. The reduced input count did
not remove Roaring's internal union allocation cost, and direct `Or` remains
faster for this `CompareBy` distribution. Do not reconsider this contract
without a different intersection primitive or a workload with substantially
smaller operator ranges.

## 2026-08-24: per-item ordered cardinality prefixes

An ordered-index prototype stored a cardinality prefix for every item in every
block. After the existing boundary binary search, uncached range estimates
could then combine the boundary fragment and complete-block prefix in constant
time instead of summing at most 64 posting cardinalities.

The change was not retained. Against the bounded-aggregate production baseline
of 43.426 us, five one-second `Index` runs measured a 44.408 us median (2.3%
slower), with the same 73,396 B and 28 allocations per search. Warm `Local`
remained near 2.62 us. The small bounded scan is cheaper than another retained
array lookup on this workload, so its additional memory is not justified.

## 2026-08-24: candidate-aware standalone ordered rules

Uncached `All` execution now narrows an existing candidate bitmap directly
through `Greater`, `GreaterOrEqual`, `Less`, and `LessOrEqual`. The ordered rule
passes its wildcard and exact block postings to `AndAny` instead of first
materializing the complete range. Warm `Local` execution keeps its admitted
bitmap-cache path.

On Apple M1 Max with Go 1.26.0, five one-second runs of a 38,098-entry broad
equality plus ordered-range benchmark reduced median search from 132.973 us,
48,915 B, and 16 allocations to 53.123 us (60.1%), 39,879 B, and 11
allocations. Reproduce with:

```sh
go test -run '^$' -bench '^BenchmarkAllOrderedCandidateFiltering$' \
  -benchmem -benchtime=1s -count=5 .
```

## 2026-08-24: planner follow-up after range optimizations

A fresh five-second single-CPU profile of the 38,098-entry production-shaped
`Index` search measured 46.549 us, 73,311 B, and 28 allocations. The remaining
search CPU is representation work after ranking: `Bitmap.Or` is 19.3%
cumulative, platform-version `CompareBy` is 13.1%, candidate-aware `Between`
is 15.5%, and `Bitmap.AndAny` is 14.1%. `All` ranking itself no longer appears
as a material leaf cost.

No additional planner heuristic was retained. Equality-first staging,
post-intersection candidate scans, cache-aware ranking, and range-bound pruning
prototypes are already rejected earlier in this history. The current profile
does not justify repeating them: the next exact-search gains require cheaper
rule representations or bitmap primitives, not another ordering pass.

Reproduce with:

```sh
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkProductionShapeSearch/Index$' -benchtime=5s -count=1 \
  -cpuprofile=/tmp/ruleix-production-after-ordered.cpu .
go tool pprof -top -cum /tmp/ruleix-production-after-ordered.cpu
```

## 2026-08-24: hierarchical Lossy equality construction

Lossy equality planning now builds the finest 16-bit hash-prefix bucket
representation once, then derives each coarser level by merging adjacent
buckets. Previously every one of the 17 candidate granularities iterated all
distinct values and added their postings independently. Hash-prefix buckets
are exactly nested, so the new construction preserves representation choice,
memory accounting, and estimated false-positive rates.

On Apple M1 Max with Go 1.26.0, three 500 ms runs reduced the two-child 50%
budget median build from 25.906 ms and 158,604 allocations to 7.862 ms (69.7%)
and 140,399 allocations. The eight-child 50% case improved from 113.217 ms and
718,039 allocations to 33.413 ms (70.5%) and 645,387 allocations. Exact builds
remain unchanged because they do not construct lossy candidates.

Reproduce with:

```sh
go test -run '^$' -bench '^BenchmarkLossyAllPlanning' \
  -benchmem -benchtime=500ms -count=3 .
```

## 2026-08-24: rebuild scalability checkpoint

Production-shaped full rebuilds remain approximately linear through one
million rules. On Apple M1 Max with Go 1.26.0, three fixed-count runs measured
median build times of 11.381 ms for 10K rules, 95.990 ms for 100K, and 1.081 s
for 1M. Allocation traffic was 2.86 MB, 19.83 MB, and 216.35 MB respectively.
With GC disabled around the measured build, incremental peak heap was 1.92 MB,
9.71 MB, and 100.62 MB.

Generation-based base/delta indexes were not introduced. Repeated 10K builds
with small size changes showed that per-node hints reduce allocation traffic by
about 6-7%, but do not provide a stable latency improvement; overlapping old
and new indexes used roughly 4.6-4.8 MB of incremental live heap in the memory
benchmark. A 1M rebuild is material, but no update frequency, publication SLA,
or memory ceiling currently demonstrates that delta lookup, tombstone, and
compaction complexity would pay for itself. Reconsider generations when a real
deployment requires rebuild publication materially faster than roughly one
second per million rules or cannot tolerate the measured temporary heap.

Reproduce with:

```sh
go test -run '^$' \
  -bench '^(BenchmarkProductionScaleBuild|BenchmarkProductionScaleBuildPeakMemory)$' \
  -benchmem -benchtime=1x -count=3 .
go test -run '^$' -bench '^BenchmarkRepeatedBuild' \
  -benchmem -benchtime=3x -count=3 .
```

## 2026-08-25: bounded warm-Local result cache

Each learned `Local` `All` plan now retains the two most recent exact root
intersections. The cache key is the ordered set of immutable child bitmap
pointers plus an epoch incremented whenever a child cache entry is replaced.
Hits can therefore share the cached result containers with the scratch bitmap
without the copy-on-write clone previously required by the first `Bitmap.And`.
Inspected searches bypass the result cache so runtime observations continue to
describe child execution, and `Local.Close` returns retained result bitmaps to
the existing bounded bitmap pool.

On Apple M1 Max with Go 1.26.0, five one-second runs reduced the two-query warm
production `Local` median from 2.418 us, 2,331 B, and two allocations to about
0.55 us with zero measured bytes or allocations per search. The uncached
`Index` path remained near 39 us, 73,394 B, and 28 allocations. Warm retained
memory increased from the preceding roughly 88.8 KB to 95.2 KB per `Local`;
adaptive four-query retained memory measured 107.6 KB. The short-lived six-
search lifecycle measured a 299 us median, 293 KB, and 110 allocations.

Two `Index` follow-ups were rejected. Scalar validation of a `Between` rule for
candidate sets up to 256 cut traffic to 54,216 B and 18 allocations but
regressed median search to 104.3 us. Enabling the bounded range aggregates for
both `Between` sides also failed to improve the baseline, measuring a noisy
41.6 us median with unchanged allocations. The retained `AndAny` path remains
the better CPU tradeoff until Roaring exposes reusable union/intersection
workspace or a destination-aware primitive.

Reproduce with:

```sh
go test -run '^$' \
  -bench '^BenchmarkProductionShape(Search|ParallelLocalBatch100|LocalClose)$' \
  -benchmem -benchtime=1s -count=5 .
go test -run '^$' -bench '^BenchmarkProductionShapeLocalRetainedMemory$' \
  -benchmem -benchtime=3x -count=3 .
```

## 2026-08-27: cost-based initial `All` operation

`All` now chooses between direct candidate validation and bitmap execution by
comparing estimated work instead of using cardinality eight as the decision
boundary. The planner uses the exact serialized size of already available
planning or cached bitmaps, which distinguishes cheap dense containers from
large sparse postings without scanning them. It validates only when every
remaining rule supports direct ID matching; unavailable cost information keeps
the benchmark-selected threshold of eight as a conservative fallback.

On Apple M1 Max with Go 1.26.0, a 16-candidate query with a sparse 100K-ID
sibling measured a 285.5 ns median, 32 B, and two allocations, versus 25.7 us,
135,910 B, and 53 allocations through the threshold fallback. Five one-second
production-shaped runs measured a 40.645 us median for `Index.Search`, 73,396 B,
and 28 allocations. Warm `Local.Search` measured 560.4 ns with zero measured
bytes or allocations.

Reproduce with:

```sh
go test -run '^$' -bench '^BenchmarkAllCostBasedBroadSibling$' \
  -benchmem -benchtime=1s -count=5 .
go test -run '^$' -bench '^BenchmarkProductionShapeSearch/(Index|Local)$' \
  -benchmem -benchtime=1s -count=5 .
```
