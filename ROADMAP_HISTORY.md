# Roadmap history

This document records completed roadmap work and concluded experiments. The
active [`ROADMAP.md`](ROADMAP.md) contains only work that may still be done.

When a roadmap item is completed or rejected, move it here (or to a dedicated
historical document) with the date, outcome, and enough benchmark or design
evidence to avoid repeating the work. Release-facing changes still belong in
[`CHANGELOG.md`](CHANGELOG.md).

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
