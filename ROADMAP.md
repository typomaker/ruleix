# Roadmap

This file is the active implementation plan. Completed work and rejected
experiments belong in [`ROADMAP_HISTORY.md`](ROADMAP_HISTORY.md);
release-facing behavior belongs in [`CHANGELOG.md`](CHANGELOG.md). Do not keep
completed, superseded, or unrelated proposals here.

## Objective

Change aggregate `Lossy(All(...), MemoryLimit(n))` planning so memory pressure
does not proportionally degrade every leaf. Keep leaves exact while the
aggregate exact representation fits, and, once it does not, spend the smallest
necessary loss of precision on discrete representation downgrades. Prefer
downgrading the leaves responsible for the largest retained-memory pressure and
leave smaller exact leaves unchanged whenever a feasible plan permits it.

After the aggregate policy is stable, allow `Lossy` policies to nest. A nested
limit is a local upper bound; every ancestor limit remains a hard upper bound
for its complete subtree. An ancestor may force a nested subtree below its
local limit, but may never grant it more memory than that limit.

The work is successful only if every selected representation is deterministic,
the accounted retained memory of every policy subtree respects its effective
limit, and every approximate result remains a conservative superset of the
corresponding exact result.

## Required semantics

- `Lossy(rule, MemoryLimit(n))` continues to mean a hard limit on accounted
  retained representation bytes, not an equal per-child allocation.
- Build every supported leaf's exact candidate first. If the sum fits the
  applicable limit, keep every leaf exact.
- A representation may change only at a supported discrete precision level.
  Do not invent a byte-level precision coefficient that the representation
  cannot realize.
- Under pressure, select one downgrade at a time and recompute aggregate use.
  The initial deterministic policy is: maximize bytes released; break ties by
  larger current leaf usage, then stable schema order. Keep the selector
  isolated so later measurements can replace this heuristic with a measured
  quality-per-byte score without changing budget semantics.
- Stop as soon as the aggregate fits. Leaves not required to satisfy the limit
  remain exact.
- If one pass through all leaves is insufficient, continue selecting further
  downgrade steps from all leaves that still have a coarser representation.
- Fail `Build` when the sum of all minimum viable representations exceeds the
  applicable limit. Never silently exceed a limit or drop a rule.
- Flatten ordinary nested `All` nodes for allocation decisions while retaining
  their search structure and inspection boundaries.
- For nested policies, the effective limit of a subtree is the smaller of its
  local `MemoryLimit` and the budget made available by its parent. A parent may
  further downgrade a compiled child policy but may not relax the child's
  local cap.
- Preserve wildcard behavior, exact-match inclusion, first-insertion result
  order, duplicate external-ID behavior, immutable published indexes, and
  lock-free concurrent `Index.Search`.
- Planning depends only on the current `Build` input. Adding data or changing a
  schema takes effect on the next build; no mutable in-place replanning is
  introduced by this work.

## Implementation plan

### 4. Measure selection quality and build cost

- Extend lossy planning benchmarks with 2, 4, 8, and 16 children; balanced and
  single-heavy size distributions; equality, ordered, and mixed schemas; and
  budgets immediately below exact, at 75%, 50%, 25%, and minimum viable size.
- Report planning time, build allocations, peak temporary build memory,
  accounted retained bytes, number of downgraded leaves, candidates per query,
  and observed false-positive rate.
- Compare the selective policy with the parent proportional allocator. If
  maximizing released bytes causes a material search-quality regression,
  prototype a deterministic marginal score using released bytes and an
  operator-specific precision-loss estimate, then document and test the chosen
  score before proceeding.
- Reject unbounded candidate retention or a policy that improves retained
  memory only by creating unacceptable build-time or search-quality costs.

Acceptance: selective planning preserves the hard limit and conservative
correctness across the matrix, demonstrates the intended exact-leaf retention,
and has a recorded, reproducible quality/build-cost tradeoff.

### 5. Introduce a hierarchical budget model for nested `Lossy`

- Replace the current boolean `inside` rejection state with an explicit policy
  tree containing local caps, aggregate children, and leaf candidate ladders.
- Compute each subtree's all-exact usage, minimum viable usage, and locally
  capped best plan bottom-up.
- Allocate pressure top-down: treat a nested policy as a bounded subtree whose
  plan can advance to coarser states when required by an ancestor.
- Preserve ordinary nested-`All` flattening only inside the nearest policy
  boundary; do not lose local caps or `Inspect` ownership when flattening.
- Define direct nesting such as `Lossy(Lossy(rule, 30MB), 100MB)` as an
  effective `30MB` cap, and the reverse limits as an effective `30MB` cap
  forced by the outer policy. Reject duplicate `MemoryLimit` options on the
  same `Lossy` node as before.

Acceptance: nested policies build whenever all subtree minima fit their
effective limits, fail with a path-specific error otherwise, and no child or
ancestor inspection reports usage above its effective cap.

### 6. Complete nested-policy correctness and determinism coverage

- Test direct `Lossy(Lossy(...))`, `Lossy(All(...Lossy(...)))`, nested lossy
  groups, multiple siblings with local caps, and at least three policy levels.
- Cover inner-limit-smaller, outer-limit-smaller, equal-limit, exact-fit,
  one-byte-under, minimum-fit, and impossible configurations.
- Compare every approximate query result with an exact index over varied and
  randomized data, including absent getters, wildcards, range boundaries,
  duplicate values, and duplicate external IDs.
- Rebuild identical inputs repeatedly and assert stable selected strategies,
  granularities, memory accounting, and diagnostics.
- Add overflow and malformed-policy tests that identify the failing policy
  path without relying on unstable byte totals in the error string.

Acceptance: race-enabled and randomized tests find no false negatives,
nondeterministic plans, limit violations, or inspection-boundary loss.

### 7. Finalize diagnostics and documentation

- Make `Inspect` report local configured limit, effective limit when constrained
  by an ancestor, accounted subtree usage, selected mode, and granularity
  without exposing mutable planner state.
- Update `README.md` and `docs/lossy-index.md` with selective downgrade
  semantics, the deterministic tie-break order, rebuild behavior, nested-limit
  examples, and impossible-budget errors.
- Add a release-facing `CHANGELOG.md` entry and move completed implementation
  decisions, benchmark results, and rejected scoring experiments to
  `ROADMAP_HISTORY.md`.
- Keep the public API unchanged unless implementation proves that configured
  and effective limits cannot be explained through the existing inspection
  model. Any new public option such as priority or minimum precision requires a
  separate measured proposal.

Acceptance: documentation, inspection snapshots, implementation, and tests use
the same terminology and describe the same limit hierarchy.

## Required verification gate

Before accepting each implementation step, run its focused tests plus:

```sh
go test ./...
go test -race ./...
git diff --check
```

Before accepting planner changes in steps 3--7, also run the lossy planning,
quality, search, streaming-build, and production-scale benchmark families with
repeatable commands and compare medians with the parent commit. Every new or
changed benchmark must include a nearby comment containing its latest local
result, machine and Go version, dataset and budget parameters, and complete
reproduction command, as required by `AGENTS.md`.

Treat any false negative, accounted-byte limit violation, nondeterministic
selection, unbounded temporary-memory growth, or new race as a failed gate.
Record noisy measurements and rejected heuristics in
[`ROADMAP_HISTORY.md`](ROADMAP_HISTORY.md) rather than silently changing the
selection policy.

## Follow-up warm-Local optimization queue

Start this queue only after the active `Lossy` plan above is complete, or in an
isolated branch whose benchmark baseline contains all accepted earlier work.
The current reference is the exact-query-key result-cache implementation: an
interleaved parent/candidate comparison measured 228.1 ns/op for
`BenchmarkProductionShapeSearch/Local`, with 0 B/op and 0 allocations. Refresh
that reference before the first experiment because machine state and preceding
accepted changes may move it.

Every step is conditional on the previous step. The parent for an experiment
is always the latest accepted commit, never the original 228.1 ns reference.
If an experiment fails its gate, remove it from the active implementation,
record its code, measurements, and rejection reason in
[`ROADMAP_HISTORY.md`](ROADMAP_HISTORY.md), and run the next applicable step
from the last accepted parent. Do not combine two unmeasured ideas in one
candidate.

### L1. Refresh the baseline and identify the new hot path

- Build benchmark binaries for the same commit and run the production-shaped
  Local, parallel Local, result-cardinality, retained-memory, and full
  production-scale families with fixed CPU, Go version, and benchmark flags.
- Capture a CPU profile for warm production-shaped Local and separate time
  spent in exact query-key validation, cached-ID result copying, fallback child
  lookup, and benchmark/query construction.
- Add a focused benchmark that alternates the two production queries and
  reports first-entry and second-entry exact-cache hits. Do not change executor
  behavior in this step.
- Record medians and profile attribution in `ROADMAP_HISTORY.md`; update stale
  reference numbers elsewhere in this roadmap when the new measurements are
  repeatable.

Acceptance: seven interleaved one-second runs have a stable median, all warm
Local cases remain at 0 B/op and 0 allocations, and the profile confirms that
query-key validation is large enough to measure the next experiment. If exact
validation is below 15% cumulative CPU, skip L2 and choose the largest measured
non-benchmark hot path instead.

### L2. Validate both cached query keys in one pass

- Add an internal two-entry matcher for supported equality, ordered,
  `Between`, and `CompareBy` leaves. Each matcher must read its query getter
  once and compare the value with both cached entries using the operation's
  existing exact equality semantics.
- Carry a two-bit viable-entry mask through schema order and stop as soon as it
  becomes zero. Preserve the existing entry preference when both keys match.
- Keep sampled inspection, exclusions, stale epochs, unsupported operations,
  non-compact results, and cache misses on their current fallback paths.
- Add focused getter-count tests for first-entry hit, second-entry hit, both
  entries equal, early miss, query change, cache invalidation, `CompareBy`
  operator handling, and mixed supported/unsupported schemas.
- Compare a prebuilt candidate with its parent in seven interleaved one-second
  production-shaped runs, then run the complete gate below.

Acceptance: production-shaped warm Local improves by at least 5% with at least
five of seven paired wins, remains at 0 B/op and 0 allocations, changes retained
warm Local memory by no more than 1%, and causes no repeatable regression above
3% in parallel, cardinality, scale, or fallback workloads. Otherwise reject L2
and use the L1 parent for L3.

### L3. Fuse exact-key validation for the complete `All` schema

- Re-profile the accepted L2 implementation, or the L1 parent if L2 was
  rejected. Proceed only if per-child capability dispatch or generic matcher
  calls account for at least 10% cumulative warm-Local CPU.
- Compile an immutable root validator during `Build` that evaluates the same
  two-entry viable mask without repeating per-search capability discovery.
- Keep inspector wrappers and unsupported children as explicit boundaries; do
  not generate unsafe code, add a public planner API, or specialize only the
  production benchmark schema.
- Test empty and nested `All`, 1/8/16/32 supported children, mixed supported
  and unsupported leaves, sampled inspectors, stale cache epochs, and
  concurrent searches.
- Run strict interleaved A/B against the latest accepted parent, not against
  the pre-L2 baseline.

Acceptance: the incremental gain over that parent is at least 5%, warm Local
stays at 0 B/op and 0 allocations, `Index.Build` latency and retained index
bytes do not regress by more than 3%, and every standard Local gate remains
within its threshold. If dispatch is below the profiling threshold or the A/B
gate fails, record and reject L3 before L4.

### L4. Tune compact exact-result retention by measured cardinality

- Re-profile the latest accepted parent. Proceed only if bitmap copying or
  enumeration remains material for exact-cache hits above the current 64-ID
  compact limit.
- Benchmark compact limits of 64, 96, 128, and 256 IDs across empty, singleton,
  8, 45, 64/65, 96/97, 128/129, 256/257, and 4,095-result workloads. Include
  high-churn queries that exercise eviction under the existing 64 KiB cache
  budget.
- Select a limit from the complete latency/retained-memory matrix; do not tune
  it from the 45-result production case, which is already compact.
- Preserve bitmap fallback for wider results and every search with exclusions.
  Charge slice capacity to the existing cache budget exactly once.

Acceptance: at least one representative result class above 64 IDs improves by
10% or more, no class regresses by more than 3%, warm retained memory grows by
no more than 5%, eviction churn does not increase allocations, and the latest
accepted production-shaped parent remains statistically stable. If no tested
limit passes all conditions, retain 64 and reject L4.

### L5. Optimize cold and high-churn Local execution

- Profile cache-miss and alternating-query workloads against the latest
  accepted parent and identify whether ranking, child materialization, bitmap
  intersection, or result enumeration is dominant.
- Implement only the dominant bounded optimization: earlier empty-intersection
  termination, a revised direct-ID/cardinality cutover, or reduced duplicate
  child materialization. Add adversarial tests for inaccurate learned
  cardinalities before changing a threshold.
- Measure cold Local, two-query warm Local, working sets just below and above
  cache capacity, selective, wildcard-heavy, range-heavy, nested-`All`, and
  large-result searches. Verify that the ordinary warm exact-cache hit is not
  lengthened for functionality it does not use.
- Reject intra-search goroutines at this latency unless a standalone prototype
  first shows that useful work exceeds scheduling and synchronization cost.
  Parallelism remains a batch-level optimization and must preserve result order
  and cancellation semantics.

Acceptance: the targeted miss/high-churn workload improves by at least 10%, no
new warm-Local allocation appears, ordinary warm Local and parallel batches do
not regress by more than 3%, retained memory stays within the existing bounded
budgets, and all correctness gates pass. Record the chosen threshold and the
failed alternatives before closing the Local queue.

### Local queue verification gate

For L2--L5, run focused tests plus the following after every candidate and
again after removing any rejected implementation:

```sh
go test ./...
go test -race ./...
go test -run '^$' -bench '^BenchmarkProductionShapeSearch/(Index|Local)$' \
  -benchmem -benchtime=1s -count=5 .
go test -run '^$' -bench '^BenchmarkProductionShapeParallelLocalBatch100$' \
  -benchmem -benchtime=1s -count=5 .
go test -run '^$' -bench '^BenchmarkProductionScaleSearch/' \
  -benchmem -benchtime=500ms -count=3 .
go test -run '^$' -bench '^BenchmarkWarmLocalResultCardinality/' \
  -benchmem -benchtime=1s -count=5 .
go test -run '^$' -bench '^BenchmarkProductionShapeLocalRetainedMemory$' \
  -benchtime=3x -count=3 .
git diff --check
```

For each latency decision, use seven interleaved one-second runs of separately
prebuilt parent and candidate test binaries in addition to the normal gate.
Preserve the exact commands, raw medians, paired win count, bytes/op,
allocations/op, retained-memory totals, machine, and Go version in
`ROADMAP_HISTORY.md`. A later step may rely only on an earlier result that has
passed this gate and been committed.
