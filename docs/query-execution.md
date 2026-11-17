# Query execution internals

This document records the internal execution structures shared by the compound
query operators. Public matching semantics remain in the architecture overview.

## `All` child operations

`All` does not have one fixed physical algorithm. During preparation, each
child receives an immutable `allChildExecution` descriptor containing its
available cardinality, posting, filtering, query-key, and direct-ID operations.
Keeping these `All`-specific operation slots together avoids repeated interface
discovery in the candidate loop.

Direct-ID discovery is a separate concern. A nested `All` advertises direct-ID
execution only when every child supports it. The same invariant is checked
against rule interfaces before preparation and against compiled descriptors
after preparation. An unsupported child explicitly falls back to one complete
materialization for the candidate batch, never one materialization per ID.

The root Local search can validate 9–16 candidates directly when its first
ranked operand already has a bitmap and all children support direct-ID
execution. It reads the borrowed bitmap and writes accepted IDs into the
separate result bitmap. The normal exact-result cache and exclusion processing
still consume that result. See [production dump measurements](performance-history.md#candidate-cardinality-and-the-request-tail).

The reusable Local pool tracks one pair of ready, borrowed bitmaps for
that direct-validation path. On the second eligible use of the pair, it
records up to eight intersecting IDs if the pair removes at least half
of the leading candidates. Later calls can use these IDs as their
candidate list. Reuse keys are bitmap identities and the entry is cleared
with the Local, so its validity does not depend on field names or a new
query API. See [the intersection reuse experiment](performance-history.md#reusing-one-intersection-across-local-searches).

## `Between` search sides and cache identity

A `betweenSearchSide` keeps an ordered rule and its already extracted query
bound together. `rankedSearchSides` selects the lower-cardinality side once and
both bitmap filtering and direct candidate validation consume that result. This
prevents the two execution paths from independently implementing side ordering.

The Local cache delegates key comparison to `betweenCache.find` and
`betweenCacheEntry.matches`. Cardinality peeks and bitmap hits therefore share
one definition of missing-bound and comparator equality. The search path passes
its already extracted bounds into the lookup, avoiding duplicate getter calls.
The first two entries stay inline. Overflow stores entry pointers in order by
bound presence and comparator value. Short overflow lookups use a linear scan,
and longer ones use binary search. Insertion moves pointers and preserves the
cached bitmaps and the Local lifetime of every entry.

## `CompareBy` operator metadata

`compareByOperatorTable` is the single table for direction and inclusion.
Build insertion, canonical descriptors, bitmap enumeration, and cardinality
estimation consume this metadata instead of maintaining separate switch-like
blocks. Storage and canonical directions are distinct fields because `EQ` uses
a storage direction for ordered rebuilding while its canonical operation has a
different, representation-level direction; range operators use the same value
for both.

## Verification

Verification on 2026-09-14 used an Apple M1 Max with Go on `darwin/arm64`.
The baseline was revision `6a3dbb8`; the candidate was its working tree plus
the complete refactoring. Benchmarks used `-benchmem -count=5`.

| Benchmark | Baseline median | Candidate median | Change | Allocations |
| --- | ---: | ---: | ---: | ---: |
| `BenchmarkBuildIndex` | 2,557,665 ns/op | 2,556,678 ns/op | -0.04% | 29,980-29,981/op unchanged |
| `BenchmarkBetweenNarrowIntersection/Specialized` | 2,700 ns/op | 2,667 ns/op | -1.22% | 4/op unchanged |
| `BenchmarkLocalBetweenReuse/Repeat/Local` | 59.74 ns/op | 57.62 ns/op | -3.55% | 1/op unchanged |
| `BenchmarkBetweenSelectiveSide/Specialized` | 29,548 ns/op | 29,421 ns/op | -0.43% | 14/op unchanged |
| `BenchmarkLocalCompareByReuse/Repeat/Index` | 44,888 ns/op | 45,068 ns/op | +0.40% | 12/op unchanged |
| `BenchmarkLocalCompareByReuse/Repeat/Local` | 17,636 ns/op | 17,664 ns/op | +0.16% | 3/op unchanged |

The small `CompareBy` timing changes are within run-to-run noise and do not
show a measured regression. Final verification also includes the full test
suite, the full race-enabled suite, `go vet ./...`, `git diff --check`, changed-line
coverage, and the 500-line file limit. The full package reported 90.7%
statement coverage; changed executable statements reported 99.2% (122/123).
