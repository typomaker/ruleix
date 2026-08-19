# Roadmap

This document collects possible directions for `ruleix`. Items are exploratory:
their presence here does not promise implementation or inclusion in a specific
release. Candidates should be promoted only after their semantics, indexing
cost, and expected usage are understood.

## Current priority: performance and memory optimization

Optimize the existing feature set before expanding the public API. Prefer work
that improves production-shaped build time, search latency, or retained memory
and require benchmark evidence before adopting an optimization.

New filters and combinators are deferred until a concrete use case cannot be
expressed reasonably with the existing API. When that happens, validate the
semantics and indexing cost before promoting the feature back into active work.

## Deferred feature candidates

### Not equal

Add `!=` support to `CompareBy` and consider a discoverable `NotEqual` filter.
Although `Exclude` covers a single forbidden value, comparison-oriented schemas
often expect the conventional set of stored comparison operators to be
complete.

### Set membership

Add `In` and `NotIn` filters for constraints that allow or reject several
values. Common examples include countries, sales channels, customer tiers, and
statuses. Native membership filters could avoid duplicating a stored rule for
each value while making its intent explicit.

Before implementation, define:

- wildcard and empty-set behavior;
- the representation of stored and query values;
- how repeated external IDs interact with multiple allowed or forbidden sets.

### Interval overlap

Add `Overlaps` for queries that need any intersection between two intervals:

```text
stored.from <= query.until AND query.from <= stored.until
```

This complements `Between`, which currently requires the stored interval to
fully cover the query interval. Open bounds and inclusive versus exclusive
endpoints need explicit semantics.

### Logical OR

Add an `Any` combinator for logical OR between child rules. This would express
conditions such as `(country = DE OR tier = gold)` without expanding them into
multiple stored rules with the same external ID.

The design must preserve predictable wildcard, exclusion, deduplication, and
cardinality behavior when `Any` is nested inside `All` or another `Any`.

### Presence checks

Consider `Exists` and `Missing` filters for rules that distinguish a present
query field from an absent one. This requires separating presence semantics
from the current use of `nil` as a stored wildcard.

## Domain-specific ideas

These are useful in some workloads but should remain outside the core unless
demand and an efficient indexing strategy are clear:

- string prefix, suffix, substring, glob, or regular-expression matching;
- CIDR and IP address matching;
- collection operators such as `ContainsAny` and `ContainsAll`;
- arbitrary predicates that cannot be indexed ahead of time.

## Roaring bitmap optimization backlog

Track bitmap API experiments here so they are not repeated and so rejected
optimizations can be reconsidered if the workload changes. Benchmark evidence
for these decisions lives in `benchmark_test.go`.

### Adopted

- `ManyIterator`: used for result materialization at cardinality 4096 and above;
  smaller results retain `Iterator`.
- `AndAny`: used by `Between` to intersect the selective side with several
  matching postings without first materializing their union.
- `FastAnd`: used for final intersections of up to eight child bitmaps. Larger
  intersections retain the pooled sequential path.

### Evaluated, not currently used

- `CheckedAdd`: 10-30% slower than `Add` for sequential, sparse, shuffled,
  and duplicate-heavy streaming insertion, with no allocation or retained-size
  benefit. The builder does not need its inserted/not-inserted result.
- `FastOr`: faster than sequential union for uniform postings, but returning a
  new bitmap and copying it into pooled search state regressed real searches.
- `HeapOr`: substantially slower and more allocation-heavy for both uniform and
  skewed posting lists.
- `ParOr`: can beat `FastOr` for many similarly sized sparse postings, but is
  several times slower for skewed or heavily overlapping postings. Revisit only
  with a cheap shape-aware planner.
- `ParAnd`: does not amortize goroutine and merge overhead in normal indexes or
  in the tested 10-million-ID case.
- `OrCardinality`: useful only as an input to a future adaptive union planner;
  calling it before a union that is still required duplicates work.
- `AndCardinality`: a cheap selectivity/emptiness estimate, but a preflight
  before required materialization regresses the matching path. Reserve for a
  future planner.
- `Intersects`: very fast for rejecting disjoint bitmaps, but duplicates work
  when the intersection must subsequently be materialized. Reserve for a
  planner that can predict mostly-empty intersections.
- `IntersectsWithInterval`: 3-9x faster than building a temporary interval
  bitmap for dynamic ID ranges. Current ordered filters range over values, not
  internal IDs; reconsider for an ID-range or pagination API.
- `RunOptimize`: reduced some bitmap sizes but significantly regressed
  production-shaped search because operations on the resulting run containers
  became more expensive.
- `AddMany`: substantially improves bulk insertion, but the current build is
  streaming. Reconsider with an optional bulk builder that can bound the memory
  used by per-posting ID buffers.
- `Stats`, `DenseSize`, and `HasRunCompression`: `DenseSize` is constant-time
  and about 3 ns, but only reports the dense representation implied by the
  maximum ID. `HasRunCompression` scans until it finds a run container and took
  about 108 ns across 153 array containers. `Stats` scans every container and
  took about 14 ns for two containers, 0.5 µs for 153 array containers, and 0.7
  µs for 500 run containers on Apple M1 Max. All are allocation-free, but none
  predicts posting overlap; do not pay the linear scans on every search without
  a concrete adaptive strategy that demonstrates an end-to-end gain.

### Candidates still to evaluate

Evaluate these only where their semantics match an existing or planned path:

1. `Iterate`, `Minimum`, `Maximum`, `NextValue`, and `PreviousValue` for result
   materialization, early limits, or future pagination.
2. `Rank` and `Select` for offset/limit pagination without walking all earlier
   matches.
3. `AddRange`, `RemoveRange`, and `Flip` if bulk ID allocation, deletion, or
   complement operations become part of the index lifecycle.
4. `Freeze` and `FrozenView` for persistent or memory-mapped immutable indexes.
5. `Xor` if a symmetric-difference rule or planner operation is introduced.
6. `AddOffset` if indexes need to merge independently built ID shards.

## Suggested evaluation order

1. Evaluate iterator and boundary APIs for faster result materialization and
   early limits.
2. Evaluate `Rank` and `Select` if pagination becomes an active requirement.
3. Revisit the remaining bitmap operations only when the corresponding index
   lifecycle or planner use case exists.

For every optimization, compare production-shaped build time, search time,
allocations, and retained memory. Keep new feature candidates deferred unless
real usage demonstrates that the existing filters cannot express them without
unacceptable complexity or cost.
