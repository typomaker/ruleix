# Roadmap

This document collects possible directions for `ruleix`. Items are exploratory:
their presence here does not promise implementation or inclusion in a specific
release. Candidates should be promoted only after their semantics, indexing
cost, and expected usage are understood.

Keep this file limited to active and deferred work. Once a roadmap step is
completed or an experiment reaches a decision, remove it from this file and
record the outcome and supporting evidence in
[`ROADMAP_HISTORY.md`](ROADMAP_HISTORY.md) or a dedicated historical document.

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

The initial estimate-based ordering, lazy empty-aware execution, and benchmark-
selected bitmap-to-candidate threshold are recorded in
[`ROADMAP_HISTORY.md`](ROADMAP_HISTORY.md). Introduce operator-specific cost
classes only if measurements show a material benefit.

Planner ordering is operator-specific. For `All`, start with the most selective
predicate. If `Any` or `Not` is added later, design their planning separately:
`Any` may prefer cheap, broad predicates and can stop after covering the rule
universe, while `Not` should avoid materializing a large complement when a
small parent candidate set can be checked directly.

### Planner and memory benchmark matrix

The reproducible production baseline now covers 10K, 100K, and 1M rules by
default, with opt-in 5M and 10M cases for capable hosts. Its search matrix
includes empty and small results, selective and wildcard-heavy queries, large
results, nested combinators, and range-heavy queries; its definition and
reproduction commands are recorded in
[`ROADMAP_HISTORY.md`](ROADMAP_HISTORY.md). Include sparse constraints, dense constraints, high- and
low-cardinality values, and highly skewed wildcard distributions in future
workload-specific additions.

The baseline measures build time, search time and allocations, retained index
bytes, peak build heap, GC pressure, logical posting count and size, and
wildcard ratio. Compare
bitmap-only, candidate-only, and adaptive execution over candidate counts from
1 through at least 16K.

The first planner iteration is successful only if it preserves the public API,
immutability, and concurrent search safety; avoids eager materialization; exits
early on empty results; improves selective and wildcard-heavy workloads; and
does not materially regress large-result latency or allocations.

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

## Roaring bitmap candidates

Prior adopted and rejected experiments are recorded in
[`ROADMAP_HISTORY.md`](ROADMAP_HISTORY.md). Evaluate the remaining operations
only where their semantics match an existing or planned path:

1. `Xor` if a symmetric-difference rule or planner operation is introduced.
2. `AddOffset` if indexes need to merge independently built ID shards.

## Suggested evaluation order

1. Extend cheap estimates and lazy, empty-aware `All` execution only where
   benchmarks show a benefit.
2. Benchmark and add candidate scanning below a measured crossover threshold.
3. Optimize other logical operators when their semantics exist.
4. Investigate generation-based updates only after rebuild benchmarks
   demonstrate a bottleneck.
5. Revisit the remaining bitmap operations only when the corresponding index
   lifecycle or planner use case exists.

For every optimization, compare production-shaped build time, search time,
allocations, and retained memory. Keep new feature candidates deferred unless
real usage demonstrates that the existing filters cannot express them without
unacceptable complexity or cost.
