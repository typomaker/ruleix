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

Keep the roadmap focused on optimizing the existing API: execution planning,
build-time analysis, diagnostics, memory-bounded indexes, and reusable local
search state.

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
[`ROADMAP_HISTORY.md`](ROADMAP_HISTORY.md). Introduce representation-specific
cost classes only if measurements show a material benefit.

For `All`, start with the most selective predicate. Refine the ordering only
when cheap estimates can distinguish the cost of materializing a bitmap from
checking the current candidate set.

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

### Analyzer and planner diagnostics

Keep build-time analysis separate from query execution. The analyzer should
select representations and prepare compact, immutable statistics that the
planner can consume without rescanning postings or retaining expensive runtime
instrumentation.

Develop `Inspect` as the single stable view of one compiled rule. It should
report build-time facts through one `Inspector.Snapshot`: exact or lossy mode,
strategy, entry and rule counts, accounted memory, budget, item and distinct
value counts, granularity, and false-positive rate where available. Allow
separate inspectors on children of a pooled `Lossy(All(...))` rather than
returning arrays of internal child details.

An inspected rule should also accumulate metrics from real `Search`, `Visit`,
and `Local` executions. Expose them directly through `InspectorSnapshot`,
without a nested metrics object or reset API. Counters are searches, cache
hits, cache misses, cache admissions, cache evictions, materializations,
candidate checks, and empty results. Gauges are cache entries and cache
capacity. A histogram reports result cardinality. Counters remain monotonic
for the inspector lifetime so callers can calculate interval deltas.

Collect no per-query explanation. Remove `Index.Explain` and its plan types:
callers need aggregate behavior they can act on, not a diagnostic search that
performs extra work. Schemas without `Inspect` must retain the current hot
path. Measure and document the overhead that explicitly inspected rules add;
avoid timers, forced bitmap materialization, exact rechecks of lossy results,
and other metrics whose collection changes the selected execution strategy.

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
as `Lossy(Include(...), MemoryLimit(20<<20))`, not lossy-specific parameters on
every rule constructor.

The policy now supports one pooled budget around `All`; its deterministic
allocation, redistribution behavior, composition invariant, initial cost and
quality benchmark, and equality and ordered planning optimizations are recorded
in [`ROADMAP_HISTORY.md`](ROADMAP_HISTORY.md). Continue improving budget
allocation and representation selection for the existing supported rules only
when a concrete workload and benchmark evidence justify the change.

Require production-shaped benchmarks for build time, peak and retained memory,
search latency, allocations, and observed false-positive rate. The detailed
design constraints and open decisions are recorded in
[`docs/lossy-index.md`](docs/lossy-index.md).

### `Local` cache efficiency

Keep `Local` as an explicitly per-goroutine, bounded cache of intermediate
search results. Optimize admission, replacement, and reset behavior against
repeated-value, alternating-value, and high-churn workloads while accounting
for both hit-rate gains and retained bytes per live `Local`.

Evaluate cache specialization by existing rule representation only when it
reduces search latency or allocations without making one-off queries retain
large bitmaps. Include cold, warm, cache-fill, churn, reset, and many-live-local
cases in the production benchmark matrix. Do not move cached state into the
shared immutable index or weaken concurrent search safety.

## Prioritized roadmap

Work through these steps in priority order, promoting an optimization only
when production-shaped benchmarks demonstrate a material benefit:

1. Extend cheap estimates and lazy, empty-aware `All` execution where
   benchmarks show a benefit.
2. Benchmark and add candidate scanning below a measured crossover threshold.
3. Consolidate diagnostics in `Inspect`: add build facts and per-rule runtime
   counters, gauges, and result-cardinality histograms, then remove
   query-specific `Index.Explain`.
4. Improve `Lossy` allocation and representation selection for existing rules,
   using memory and false-positive quality benchmarks.
5. Benchmark inspected-rule metric overhead and tune its per-rule `Local`
   cache accounting without adding mutable diagnostic state to the index.
6. Tune the remaining `Local` admission, eviction, reset behavior, and retained
   memory for production-shaped repeated and high-churn searches.
7. Investigate generation-based updates only after rebuild benchmarks
   demonstrate a bottleneck.

For every optimization, compare production-shaped build time, search time,
allocations, and retained memory. For lossy representations also compare peak
memory and observed false-positive rate; for `Local`, report retained bytes per
live cache.
