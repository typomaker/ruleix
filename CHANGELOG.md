# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
This project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/albertsultanov/ruleix/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/albertsultanov/ruleix/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/albertsultanov/ruleix/releases/tag/v0.1.0
