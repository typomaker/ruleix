# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
This project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.7.1] - 2026-08-24

### Changed

- Lossy equality planning builds the finest hash-prefix representation once
  and derives coarser levels by merging buckets, substantially reducing
  memory-bounded build time and allocation traffic.
- Uncached `All` execution filters existing candidates directly through
  standalone ordered rules instead of materializing their complete ranges.
- `CompareBy` uses a bounded second level of ordered range aggregates, reducing
  wide operator unions without changing per-search allocations.
- Uncached `All` execution filters an existing candidate bitmap through
  `Between` bounds, avoiding materialization of the complete range result.
- Exact ordered indexes use smaller aggregate blocks, reducing production-shaped
  uncached range-search latency while preserving exact comparison semantics.
- Exact nested `All` groups are flattened during index compilation, avoiding
  intermediate bitmap materialization while preserving inspected and lossy
  rule boundaries.

## [0.7.0] - 2026-08-24

### Added

- `Inspect` and the sealed `Inspector` interface expose immutable snapshots of
  the compiled strategy and build counts for one marked rule without changing
  its search representation.
  Lossy rules additionally report accounted memory, budget, item and distinct-
  value counts, selected bucket granularity, and a distribution-aware
  collision-rate estimate for lossy grouped-hash equality.
  Inspected rules also expose monotonic search, materialization, candidate-
  check, `All` range-pruning, and empty-result counters plus a fixed result-
  cardinality histogram, without forcing candidate paths to materialize
  bitmaps.
  Inspected `Local` execution additionally reports cache hits, misses,
  admissions, evictions, and adaptive expansions. Runtime scalars are
  monotonic across `Local` lifetimes for direct Prometheus counter export;
  Inspector no longer exposes live cache gauges.
- `Lossy` and `MemoryLimit` add opt-in, memory-bounded grouped-hash equality and
  ordered-bucket representations for supported scalar equality and ordered
  rules, including one aggregate budget around `All`. Approximate results may
  contain false positives but never omit an exact match.

### Changed

- Warm uninspected `Local` searches skip repeated bitmap-extrema probes during
  `All` range pruning because their child bitmaps are already cached.
- `Local` learns an `All` child order from real queries and reuses that plan
  until the selected first child changes cardinality class or another cached
  child becomes at least twice as selective. Plans survive `Local.Close` with
  the recyclable context and contain no result bitmaps. Warm equality bitmaps
  participate directly in the learned plan instead of being copied through
  temporary child results.
- Warm `Local` searches reuse cached ordered, `CompareBy`, and `Between`
  bitmaps during `All` planning instead of repeating ordered cardinality scans
  and temporary materialization.
- `Local.Close` returns admitted equality, ordered, `CompareBy`, `Between`, and
  exclusion cache bitmaps to the reusable context pool instead of leaving their
  allocations for garbage collection. Cleared per-node cache structures also
  remain with the context, avoiding their reallocation in the next `Local`
  lifetime while preserving cold admission and replacement state.
- Exact ordered indexes learn observed-domain logical routing intervals for
  numeric and `time.Time` values after the single streaming build pass. Search
  uses the same key to select a boundary block while retaining exact comparison
  semantics inside it.
- `Local.Close` replaces `Local.Reset`: closing returns the cleared internal
  bitmap context to its originating index for reuse, and the closed handle
  cannot be used again.
- `Inspector` now exposes only `Snapshot`. Build facts and runtime metrics are
  read from the returned immutable `InspectorSnapshot`, keeping every field in
  one observation tied to the same build generation and counter sample.
- Per-node `Local` caches for equality, exclusion, ordered, `CompareBy`, and
  `Between`
  rules can adapt from two to four bitmap entries when a repeatedly reused
  working set would otherwise thrash the cache. High-churn one-off values still
  retain no materialized bitmaps.

- Aggregate `Lossy(All(...))` limits now form a pooled budget: minimum viable
  child representations are reserved first and unused share bytes are
  deterministically redistributed to improve other children.
- Pooled lossy planning caches each leaf's next representation upgrade, and
  equality compilation reuses canonical hashes across bucket granularities.
- Pooled lossy equality planning prepares bucket representations once per leaf
  and reuses them across allocation passes without penalizing exact-fit builds.
- Lossy `All` benchmarks now report planning cost, search latency, candidate
  amplification, and observed false-positive rate across memory budgets.
- The production-shaped planner benchmark matrix now covers 10K, 100K, and 1M
  rules by default, with opt-in 5M and 10M cases, across build,
  retained-memory, peak-build-memory, GC-pressure, selective, small-result,
  and wildcard-heavy search workloads.
- `All` uses cheap leaf cardinality estimates to order execution and stops
  materializing small candidate paths when an intermediate intersection is
  empty.
- `All` reuses each cheap cardinality estimate for empty detection and child
  ranking instead of calculating it twice per search.
- Nested `All` groups propagate specialized cheap empty checks through their
  estimate, avoiding sibling materialization when a descendant proves that the
  result is empty.
- `All` switches from bitmap execution to direct ID validation when a child's
  actual materialized result falls below the measured candidate threshold,
  covering predicates with conservative or unavailable cheap estimates.
- Late `All` candidate fallback reuses already materialized child bitmaps for
  ID validation instead of repeating their rule-specific lookup work.
- Small `All` candidate sets materialize only the most selective child and
  validate remaining predicates directly by internal rule ID.
- `All` switches from direct candidate validation to bitmap intersection above
  four candidate IDs, based on dense and sparse planner benchmarks through 16K
  candidates.
- `All` evaluates pointer-interned partial wildcard postings shared by two or
  more equality children once during uncached index searches.
- `Local` admits intermediate result bitmaps after their second recent use, so
  one-off query values no longer displace reusable entries or retain bitmap
  memory.
- `Between` validates the second interval bound directly when the more
  selective bound produces at most four candidates, avoiding a second range
  bitmap without retaining per-ID interval copies.
- Completed roadmap work and concluded experiments now move to
  `ROADMAP_HISTORY.md`, keeping `ROADMAP.md` limited to active and deferred
  work.

### Removed

- `Index.Explain`, `SearchPlan`, `PlanChild`, and `SearchStrategy`. Per-rule
  build facts and aggregate runtime behavior are available through `Inspect`.
- `Inspector.Reset` and build-snapshot pinning. Inspector build facts now
  follow the latest successful `Build` automatically.

## [0.6.0] - 2026-08-19

### Changed

- `Index.Search` and `Local.Search` now append matches to the destination slice
  instead of resetting its length. Callers that want to replace prior results
  must reset the slice explicitly with `dst = dst[:0]`.
- `Index.Search` and `Local.Search` now report whether the current search found
  at least one match.
- Small search results and visitor callbacks use allocation-free bitmap
  iteration, reducing traversal overhead while retaining batched iteration for
  wide results.

## [0.5.1] - 2026-08-19

### Changed

- Equality filters with one or two concrete values are specialized into
  compact unary or binary search rules during build.
- Wide search results are visited in batches to reduce iterator overhead.
- Small and interval intersections use specialized Roaring bitmap operations
  that avoid materializing unnecessary intermediate results.

## [0.5.0] - 2026-08-19

### Added

- Production-shaped build, search, retained-memory, and wildcard-heavy
  benchmarks for a 38,098-constraint workload with UUID IDs.
- Functional coverage for the complete production-shaped matching schema.
- `Local.Reset` for releasing per-node cached results while keeping the local
  search context usable.

### Changed

- Getters now return `(value, ok)` directly, eliminating temporary pointer
  allocations and making missing values explicit.
- Wildcard-only filters are collapsed during build, and `All` avoids
  materializing results when a selective candidate list can be scanned.
- Search bitmaps use copy-on-write sharing and skip exclusion scratch storage
  for schemas without effective exclusions.
- Equality postings use contiguous indexed storage with inline handling for up
  to two distinct values.
- Identical immutable posting bitmaps are interned within an index.
- Ordered indexes no longer retain duplicate endpoint and singleton aggregate
  storage.
- Exclusions are removed from the positive match tree and retain only their
  value postings.
- `CompareBy` now allocates ordered indexes only for comparison operators used
  by the current build.

### Fixed

- Local caches clear stale getter values when a cache entry becomes a wildcard.

### Removed

- `Path`, `Path3`, `Path4`, and `Path5`; nested optional values are now read by
  allocation-free getters returning `(value, ok)`.

## [0.4.2] - 2026-08-18

### Added

- CI linting with a pinned `golangci-lint` version and local `make` targets for
  linting, testing, and combined checks.

### Changed

- Ranked scratch buffers now use pointer-backed pool entries, avoiding an
  interface allocation when they are returned to `sync.Pool`.

### Fixed

- The bitmap pool test no longer assumes that `sync.Pool` must return the same
  object, making race-detector runs stable across garbage collections and
  scheduler migrations.

## [0.4.1] - 2026-08-17

### Fixed

- `CompareBy` once again evaluates the operator stored in each indexed
  constraint. Query-side operators are ignored.

## [0.4.0] - 2026-08-17

### Changed

- `CompareBy` now selects a typed comparison operator from the search value
  instead of parsing an operator stored in every indexed constraint.
- `CompareBy` stores all concrete values in one ordered index. A nil query
  operator disables the filter, while nil stored values remain wildcards.
- `CompareBy` value and operator accessors now both return pointers and can be
  composed with `Path`.

### Removed

- `ParseOperator` and string-based operators in favor of the `OperatorEQ`,
  `OperatorLT`, `OperatorLTE`, `OperatorGT`, and `OperatorGTE` constants.
- The redundant `BetweenConstraint` helper; nested intervals can be expressed
  directly with `Between` and `Path`.
- The obsolete benchmark baseline document.

## [0.3.0] - 2026-08-17

### Changed

- `Zip` now panics when the constraint and ID slice lengths differ and returns
  only the resulting sequence instead of a sequence and an error.

## [0.2.1] - 2026-08-17

### Changed

- `Local` now evicts the least recently used of its two cached bitmap results
  per filter node, preserving hot values when other query values interleave.

## [0.2.0] - 2026-08-17

### Added

- `Index.Local`, a per-goroutine search context that caches the two most recent
  intermediate bitmap results for every filter node.
- `Local.Search` and `Local.Visit` for repeated searches and streaming result
  iteration with the same local cache.
- Runnable and GoDoc examples showing how to share an immutable index while
  creating an independent `Local` in each goroutine.
- Benchmarks covering repeated values, alternating values, cache churn, and
  local-context creation across all filter types.

### Changed

- `Include`, `Exclude`, ordered filters, `CompareBy`, and `Between` can now reuse
  intermediate results through `Local`, reducing repeated bitmap construction
  and allocation pressure for workloads with query-value locality.
- Local cache storage is bounded to two bitmap results per filter node and is
  retained only for the lifetime of its `Local` context.

## [0.1.0] - 2026-08-16

### Added

- Strongly typed immutable rule indexes for equality, exclusion, ordered,
  interval, and dynamic comparison constraints.
- Wildcard support through nullable getters across the base filters.
- Composition with `All` and type-safe nested getters built with `Path` and
  `Path3` through `Path5`.
- Reusable builders with adaptive capacity hints for repeated full index
  rebuilds.
- `Search` with caller-owned destination buffers and `Visit` with early
  termination.
- `Zip` for building an input sequence from parallel constraint and ID slices.
- Benchmarks, differential tests, race-detector coverage, and runnable examples
  for the public API.

### Changed

- Result IDs are canonicalized while building the index, so searches return
  unique IDs in first-insertion order without per-query deduplication.
- Ordered indexes use block aggregates and binary search, substantially reducing
  the cost of broad range queries and removing the `go-rbtree` dependency.
- Equality posting lists now adapt from an inline ID to a compact array and then
  to a Roaring bitmap, reducing memory use for high-cardinality value sets.
- `All` materializes each child result once and intersects results from the
  smallest candidate set first.
- Oversized temporary bitmaps are no longer retained by the scratch pool.
- Builders can be reused after both successful and failed builds; each build
  produces an independent immutable index. Concurrent calls to `Build` on the
  same builder still require caller synchronization.

### Removed

- Mutable index updates and the global search/build lock in favor of an explicit
  build phase followed by lock-free concurrent searches.
- The redundant `EqValue` filter; `Include` is the single equality filter and
  handles wildcards directly.
- Nested rule wrappers in favor of typed getter composition with `Path`.

[Unreleased]: https://github.com/typomaker/ruleix/compare/v0.7.1...HEAD
[0.7.1]: https://github.com/typomaker/ruleix/compare/v0.7.0...v0.7.1
[0.7.0]: https://github.com/typomaker/ruleix/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/typomaker/ruleix/compare/v0.5.1...v0.6.0
[0.5.1]: https://github.com/typomaker/ruleix/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/typomaker/ruleix/compare/v0.4.2...v0.5.0
[0.4.2]: https://github.com/typomaker/ruleix/compare/v0.4.1...v0.4.2
[0.4.1]: https://github.com/typomaker/ruleix/compare/v0.4.0...v0.4.1
[0.4.0]: https://github.com/typomaker/ruleix/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/typomaker/ruleix/compare/v0.2.1...v0.3.0
[0.2.1]: https://github.com/typomaker/ruleix/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/typomaker/ruleix/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/typomaker/ruleix/releases/tag/v0.1.0
