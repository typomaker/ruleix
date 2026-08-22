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

### Architectural guardrails

Performance work should preserve the properties that make the current index
useful: an immutable index after `Build`, lock-free concurrent searches,
`uint32` internal rule IDs, and Roaring bitmaps as the primary representation
for large candidate sets. Do not add mutex-protected mutable postings, switch to
larger IDs, or replace Roaring without workload-specific benchmark evidence.

Keep the first planner internal and deliberately small. Avoid a general-purpose
database optimizer, expensive runtime statistics, or public API changes until a
measured need justifies them.

### Adaptive `All` execution planner

The primary optimization candidate is a hybrid executor that avoids
materializing every child bitmap before intersection. Wildcard-heavy fields can
match nearly the whole index, so their expected result cardinality, including
both the wildcard and value-specific postings, matters more than the size of
either posting alone.

Develop this incrementally:

1. Add a cheap internal cardinality estimate for the simple leaf indexes.
   Distinguish estimated output size from execution cost in the design, but use
   cardinality alone in the first implementation. Permit coarse or explicitly
   inexact estimates for range indexes rather than duplicating search work.
2. Make `All` sort children by estimate and materialize them one at a time.
   Check for an empty result after every intersection and never build remaining
   child results once execution can terminate.
3. Add a cheap, type-safe way to validate one internal rule ID, either through
   leaf-level `MatchID` operations or a compiled-rule representation indexed by
   rule ID. Once the candidate set becomes small, validate the remaining
   predicates directly and stay in candidate mode for the rest of the query.
4. Choose the bitmap-to-candidate threshold from benchmarks, not from a fixed
   assumption. Start with one shared threshold; introduce operator-specific
   cost classes only if measurements show a material benefit.

Compare leaf-level `MatchID` with a cache-friendly compiled-rule fallback before
committing to reverse constraint storage in every leaf. The latter may improve
candidate scans but also duplicates source data and increases retained memory.

Planner ordering is operator-specific. For `All`, start with the most selective
predicate. If `Any` or `Not` is added later, design their planning separately:
`Any` may prefer cheap, broad predicates and can stop after covering the rule
universe, while `Not` should avoid materializing a large complement when a
small parent candidate set can be checked directly.

### Planner and memory benchmark matrix

Establish a reproducible baseline before implementing the planner. Cover 10K,
100K, and 1M rules initially, with larger 5M and 10M cases where the test host
allows them. Include sparse constraints, dense constraints, high- and
low-cardinality values, highly skewed wildcard distributions, empty and small
results, large results, nested combinators, and range-heavy queries.

Measure build time, search time and allocations, retained index bytes, peak
build memory, GC pressure, posting count and size, and wildcard ratio. Compare
bitmap-only, candidate-only, and adaptive execution over candidate counts from
1 through at least 16K. Benchmark bitmap-pool reuse under concurrent searches;
keep it only where it improves end-to-end allocation or latency behavior.

The first planner iteration is successful only if it preserves the public API,
immutability, and concurrent search safety; avoids eager materialization; exits
early on empty results; improves selective and wildcard-heavy workloads; and
does not materially regress large-result latency or allocations.

### Shared wildcard evaluation in `All`

When two or more positive child rules have the same wildcard posting bitmap,
evaluate that shared wildcard only once. For concrete query values, preserve
each child's value-specific match and use the identity
`(W ∪ A) ∩ (W ∪ B) = W ∪ (A ∩ B)` instead of materializing `W` in
every intermediate child result.

Build-time bitmap interning already gives equal wildcard postings shared
storage; this candidate targets the remaining repeated search work. Start with
equality filters under one `All`, keep exclusions and the two `Between` bounds
out of the initial fast path, and require benchmarks with large partial
wildcards. Compare against the existing path for small bitmaps and warmed
`Local` caches, where grouping overhead may outweigh the saved unions.

### Optional planner diagnostics

After the execution strategy is stable, consider an opt-in `Explain` facility
that reports estimates, actual cardinalities, ordering, and the switch between
bitmap and candidate modes. Build-time metadata such as total rules, wildcard
count, distinct values, and maximum posting cardinality can support both
diagnostics and cheap estimates. Keep instrumentation out of the default hot
path.

### Rule inspection API

Add a transparent `Inspect` decorator that binds a caller-owned
`RuleInspector` to the compiled runtime representation of one rule during
`Build`. This gives callers a stable way to observe a particular rule inside a
composite tree without treating the declarative `Rule` itself as a runtime
handle:

```go
var customer ruleix.RuleInspector

schema := ruleix.All(
	ruleix.Inspect(
		&customer,
		ruleix.Lossy(
			ruleix.Include(...),
			ruleix.MaxMemory(20<<20),
		),
	),
)

index, err := ruleix.New[Constraint, string](schema).Build(entries)
if err != nil {
	// handle the build error
}

stats := customer.Stats()
```

`Inspect` must not change matching semantics or constrain the optimizer's
choice of representation. The inspector should be unbound before a successful
build and, afterward, report the representation the planner actually selected.
Initial statistics may be a subset of mode (`Exact` or `Lossy`), retained
memory, configured memory limit, cardinality, item and distinct-value counts,
strategy, granularity, and estimated false-positive rate.

Keep this mechanism general rather than coupling it to lossy indexes. Future
runtime metadata may include optimizer decisions, bucket or prefix
configuration, and opt-in performance counters. Specify binding lifetime,
repeated-build behavior, build-failure behavior, concurrency guarantees, and
the stability of the returned statistics before promoting the API. The full
proposal and open decisions are recorded in
[`docs/inspect-api.md`](docs/inspect-api.md).

### Rebuild scalability

Retain full immutable rebuilds until benchmarks show that update frequency,
peak memory, or publication latency is a real bottleneck. A lightweight store
may build a new index separately and atomically publish the completed
generation, keeping searches read-only.

If full rebuilds eventually become prohibitive, evaluate immutable base and
delta generations with tombstones and periodic compaction. Account for the
temporary memory of two generations and build allocations. Do not make mutable
posting lists the primary update mechanism.

### Ordered and time-range indexes

Measure traversal of indexes with many distinct boundaries before changing
their representation. If repeated unions dominate, evaluate block-level
prefix/suffix aggregates before a segment tree: both trade retained memory and
build cost for fewer bitmap operations, but block aggregation is the simpler
first experiment.

For `TimeRange`, estimate and execute the more selective bound first, then use
candidate validation for the other bound when the set is small. Storing bounds
by internal rule ID may make this validation cheap, but its memory cost should
be compared with the compiled-rule approach used by the general planner.

### Memory-bounded lossy indexes

Add an opt-in `Lossy` policy for indexes whose exact representation would
exceed a caller-provided memory budget. Lossy search results may contain false
positives, but must never omit an exact match. When the exact representation
fits the budget, `Build` should retain it rather than approximating needlessly.
Keep this policy orthogonal to operators: the intended API is a decorator such
as `Lossy(Include(...), MaxMemory(20<<20))`, not lossy-specific parameters on
every rule constructor.

Develop the feature in stages:

1. Define the public contract for `Lossy`, `MaxMemory`, unsupported rule/value
   combinations, invalid budgets, and build failures. Keep false-positive
   targets, hash counts, bucket sizes, prefixes, and shifts internal.
2. Split `Build` conceptually into analysis, planning, and materialization so it
   can estimate an exact representation and choose a strategy without first
   constructing and discarding a full exact index.
3. Prototype operator-aware strategies for equality and ordered ranges. Use
   canonical value encodings rather than Go's in-memory layout; investigate a
   monotonic `uint64` key for ordered scalar and time values so range rules can
   share bucket machinery.
4. Select granularity automatically from the budget, actual data distribution,
   operator, and value type. Resolve type-specific behavior once during
   `Build`; do not add type switches to the search hot path.
5. Add build statistics describing the selected exact or lossy representation,
   retained memory, budget, item and distinct-value counts, strategy, and
   granularity, exposed through the proposed `Inspect` API. Report an estimated
   false-positive rate only where it can be computed meaningfully.
6. Extend the policy to `All` only after defining how one budget is divided
   among children and proving that composition preserves the no-false-negative
   invariant.

Require property tests comparing lossy and exact results across supported
operators and value types, adversarial boundary tests for ordered encodings,
and production-shaped benchmarks for build time, peak and retained memory,
search latency, allocations, and observed false-positive rate. The detailed
design constraints and open decisions are recorded in
[`docs/lossy-index.md`](docs/lossy-index.md).

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
  smaller results use allocation-free `Iterate`, which is about 2.5x faster
  than `Iterator` for 16 results. `Iterate` also powers `Visit` and is about
  2-3x faster when consumers stop after 16 results.
- `AndAny`: used by `Between` to intersect the selective side with several
  matching postings without first materializing their union.
- `FastAnd`: used for final intersections of up to eight child bitmaps. Larger
  intersections retain the pooled sequential path.
- `AddRange`: used to construct the universe of internal IDs when exclusions
  are present. For 100,000 consecutive IDs it took about 170 ns and 128 B,
  versus 560 µs and 66 KB for individual `Add` calls on Apple M1 Max.

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
- `Minimum`, `Maximum`, `NextValue`, and `PreviousValue`: boundary lookup is
  allocation-free and cheap (about 3 ns for either bound and 25-26 ns for an
  adjacent sparse value on Apple M1 Max), but the current API has no result
  pagination or ID-range operation that can use it.
- `Rank` and `Select`: both make deep pagination independent of the number of
  preceding matches. For a 16-item page at offset 65,536, `Select` plus iterator
  positioning took about 130 ns for dense results and 690 ns for sparse results,
  versus 273 µs and 196 µs for walking from the start. `Rank` took about 5 ns
  and 205 ns versus 409 µs and 315 µs for the equivalent walk. They add no
  allocations beyond the result iterator, but the current API has no offset,
  limit, or cursor operation that can use them.
- `RemoveRange` and `Flip`: both efficiently mutate contiguous ranges. Removing
  100,000 IDs took about 260 ns versus 1.13 ms for individual `Remove` calls;
  flipping a range in an alternating bitmap took about 6 µs versus 15 µs for
  XOR with a prebuilt range bitmap, while allocating about half as much. The
  immutable index lifecycle has no deletion or complement operation that can
  use either primitive.
- `Freeze` and `FrozenView`: frozen views preserve read performance for
  intersection and iteration. They also made a sparse union about 6x faster in
  the microbenchmark because copy-on-write containers can be shared, but every
  posting needs its own serialized buffer plus view metadata; opening one view
  allocated 176 B for a two-container dense bitmap and 6.6 KB for a
  153-container sparse bitmap. The current index has no persistent format or
  memory-mapped lifetime over which to share and manage those buffers, so
  freezing all in-memory postings would complicate ownership and duplicate peak
  build memory. Reconsider as part of an index persistence design rather than
  as an isolated in-memory optimization.

### Candidates still to evaluate

Evaluate these only where their semantics match an existing or planned path:

1. `Xor` if a symmetric-difference rule or planner operation is introduced.
2. `AddOffset` if indexes need to merge independently built ID shards.

## Suggested evaluation order

1. Capture production-shaped latency, allocation, build, and memory baselines.
2. Add cheap estimates and lazy, empty-aware `All` execution.
3. Benchmark and add candidate scanning below a measured crossover threshold.
4. Revisit shared wildcard evaluation in light of the planner: retain the
   specialized identity only where it still wins.
5. Add optional planner diagnostics, then optimize other logical operators when
   their semantics exist.
6. Investigate range aggregation or generation-based updates only after their
   respective benchmarks demonstrate a bottleneck.
7. Revisit the remaining bitmap operations only when the corresponding index
   lifecycle or planner use case exists.

For every optimization, compare production-shaped build time, search time,
allocations, and retained memory. Keep new feature candidates deferred unless
real usage demonstrates that the existing filters cannot express them without
unacceptable complexity or cost.
