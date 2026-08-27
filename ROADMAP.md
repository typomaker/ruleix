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

#### Cost-based `All` executor rewrite

Replace the current cardinality-ordered executor incrementally rather than in
one change. The target planner chooses an operation, not only a child order:
consume an existing posting, validate a small candidate set by ID, filter an
existing bitmap in place, stream an ordered result, or materialize a complete
child bitmap. Preserve insertion-ordered results, exact matching semantics,
the immutable `Index`, lock-free concurrent `Index.Search`, and allocation-free
warm `Local.Search` throughout the migration.

Use the production-shaped planner matrix as the acceptance gate for every
step. Record `Index.Search` and cold, warm, parallel, and high-churn
`Local.Search` latency, bytes, and allocations, plus retained bytes per live
`Local`. Add focused cases for inaccurate estimates, expensive per-ID checks,
late empty children, correlated predicates, and the scan/materialize boundary.
Do not remove the existing executor until the replacement matches its results
and large-result performance and materially reduces selective-search work or
allocation traffic.

Implement the rewrite in this order:

1. **Add representation-specific candidate filters and ordered streaming.**
   Teach equality, exclusion, `CompareBy`, and `Between` representations to
   narrow an existing candidate set without first constructing their complete
   result where benchmarks justify it. Materialize an ordered union only when
   it is selected as a broad candidate source or is cheaper than filtering.
   Keep direct getters and comparisons out of shared mutable state. A first
   posting-by-posting ordered streaming experiment did not beat Roaring union
   materialization; reconsider streaming only for a workload where it can stop
   early or avoid substantially more union work.
2. **Retain bounded per-`Local` plans and results.** Cache query-shape plans,
   child bitmaps, and exact intersections independently. Validate a cached
   plan cheaply against current postings before reuse, and retain the current
   admission-after-repeat behavior so one-off values do not pin bitmaps. Bound
   caches by accounted bytes as well as entry count before increasing their
   working-set capacity. Exact-intersection results now share a 64 KiB
   accounted-byte budget per `Local`; apply the same accounting discipline to
   any future plan or child-cache capacity increase.
3. **Share sampled planner statistics across `Local` instances.** Only after
   the deterministic cost model is stable, add a compact `Index`-owned profile
   containing aggregate operation cost, actual cardinality, empty rate, and
   candidate rejection rate by bounded query shape. Each `Local` reads one
   immutable snapshot as a starting prior, accumulates an unsynchronized local
   overlay, and publishes batched deltas on `Close` or at a coarse interval.
   Never update shared atomics, take timers, or publish a new snapshot on every
   search. Cached bitmaps, query values, exact intersections, and final plan
   choices remain local.
4. **Control learning bias and memory.** Track sample counts and confidence,
   distinguish missing observations from zero cost, use occasional bounded
   exploration only in sampled local contexts, and let local evidence override
   the shared prior. Limit query shapes and shared profile bytes per compiled
   `All`, use deterministic eviction, and prove that adversarial high-cardinality
   query values cannot cause unbounded retention.
5. **Cut over and simplify.** Run the old and new planners against the same
    generated and production-shaped queries in correctness tests, including
    nested `All`, wildcard sharing, exclusions, lossy rules, duplicate external
    IDs, and empty and large results. Enable the new executor only after its
    benchmark gates pass, then remove the shadow planner, obsolete ranking
    interfaces, and compatibility branches in a separate reviewable change.

Treat shared statistics as optional hints. A fresh `Local`, a discarded
profile, and a profile trained by a different workload must all remain correct
and must fall back to the deterministic build-time model. Keep planner learning
separate from `Inspect`: inspection may reuse the batching mechanism, but
enabling or disabling an inspector must never change the selected plan.

#### Deferred candidate: remove repeated range-cardinality work

The production-shaped warm `Local.Search` regression is isolated to schemas
that contain ordered branches. Commit `05f8065` made `orderedRule`, `CompareBy`,
and `Between` participate in `All` cardinality ranking. Unlike the `v0.6.0`
path, which can obtain cardinality from an already cached materialized bitmap,
the current planner performs a fresh ordered boundary lookup and scans boundary
posting cardinalities before executing or checking the selected rules. Removing
range estimates from the current implementation improved warm local search by
9.8%; removing ordered filters from the schema reduced the difference to 0.7%.
The benchmark details are recorded in [`ROADMAP_HISTORY.md`](ROADMAP_HISTORY.md).

Address this without reverting the correctness and candidate-scanning work that
followed `05f8065`:

1. Add bounded, equality-informed planning. Evaluate cheap `Include`/`Exclude`
   estimates first. Once a candidate is at or below the measured candidate-scan
   threshold, materialize it and validate the remaining range predicates by ID
   instead of calculating their cardinalities.
2. Preserve selectivity across nested `All` nodes. A nested group such as
   `All(Include(platform.name), CompareBy(platform.version))` must be able to
   expose the cheap platform-name bound and stop before estimating the ordered
   version branch. Use bounded estimates or an equivalent partial plan rather
   than treating the nested group as one opaque estimator.
3. The warm `Local` range-estimate path now reuses cached ordered bitmaps and
   their cardinalities; the implementation and production evidence are
   recorded in [`ROADMAP_HISTORY.md`](ROADMAP_HISTORY.md).
4. If uncached range estimates remain material, benchmark per-item boundary
   prefixes or a conservative block-level estimate so the fallback needs a
   binary search and constant-time arithmetic rather than scanning up to one
   ordered block.

Accept the change only if it materially recovers full-schema warm and parallel
`Local` latency, preserves equality-only performance and allocations, and does
not regress range-only, high-churn, large-result, build-time, or retained-memory
benchmarks. Exact zero detection must remain conservative and search results
must remain unchanged.

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

An inspected rule should also accumulate low-priority metrics from sampled
`Local` executions without changing ordinary `Search`, `Visit`, or `Local`
hot paths. Expose them directly through `InspectorSnapshot`, without a nested
metrics object or reset API. Counters are cache hits, cache
misses, cache admissions, cache evictions, cache expansions, candidate checks,
`All` range prunings, and empty results. A
histogram reports result cardinality. Inspector exposes no gauges. Counters
remain monotonic sample totals for the inspector lifetime so callers can
calculate interval deltas without treating them as complete workload totals.

Collect no per-query explanation. `Index.Explain` and its plan types were
removed: callers need aggregate behavior they can act on, not a diagnostic
search that performs extra work. Schemas without `Inspect` must retain the
current hot path. Measure and document sampled-context overhead separately;
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
large bitmaps. Include cold, warm, cache-fill, churn, close/reuse, and many-
live-local cases in the production benchmark matrix. Do not move cached state
into the shared immutable index or weaken concurrent search safety.

## Prioritized roadmap

Work through these steps in priority order, promoting an optimization only
when production-shaped benchmarks demonstrate a material benefit:

1. Execute the cost-based `All` rewrite through explicit capabilities, static
   descriptors, operation-cost selection, and adaptive narrowing before adding
   shared runtime learning.
2. Add bounded cross-`Local` planner statistics only after the deterministic
   executor and per-`Local` plan validation pass their benchmark gates.
3. Improve `Lossy` allocation and representation selection for existing rules,
   using memory and false-positive quality benchmarks.
4. Optimize the uncached ordered estimate only if bounded planning and warm
   cache reuse still leave measurable overhead.
5. Investigate generation-based updates only after rebuild benchmarks
   demonstrate a bottleneck.

For every optimization, compare production-shaped build time, search time,
allocations, and retained memory. For lossy representations also compare peak
memory and observed false-positive rate; for `Local`, report retained bytes per
live cache.
