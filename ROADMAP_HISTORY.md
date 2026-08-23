# Roadmap history

This document records completed roadmap work and concluded experiments. The
active [`ROADMAP.md`](ROADMAP.md) contains only work that may still be done.

When a roadmap item is completed or rejected, move it here (or to a dedicated
historical document) with the date, outcome, and enough benchmark or design
evidence to avoid repeating the work. Release-facing changes still belong in
[`CHANGELOG.md`](CHANGELOG.md).

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
