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

### 1. Freeze current behavior and define planner fixtures

- Add table-driven fixtures that expose every exact and lossy representation
  level available to equality and ordered leaves: accounted bytes, precision
  or bucket granularity, and whether compilation succeeds at a given limit.
- Add regression cases for 16-child `All` schemas in which one large leaf
  causes the exact total to exceed the aggregate limit while the other 15
  leaves can remain exact.
- Record the current proportional allocation outcome only as a migration
  baseline; assert conservative-result correctness and hard memory accounting,
  not the old allocation itself.
- Add skewed, equal-size, minimum-budget, nested-`All`, wildcard-heavy, and
  representation-tie fixtures before changing the planner.

Acceptance: the fixtures fail if memory accounting exceeds the configured
limit or an exact match is omitted, and they make the intended difference
between the old proportional allocator and the new selective allocator
observable.

### 2. Expose ordered discrete representation ladders internally

- Replace repeated arbitrary-limit probing in aggregate planning with an
  internal planner contract that can enumerate or advance through a leaf's
  candidates from exact to minimum viable representation.
- For every candidate retain accounted bytes, deterministic precision metadata,
  and the compiled rule needed for final materialization.
- Deduplicate candidates with identical accounted size and behavior so a
  downgrade step always releases memory or reaches the terminal minimum.
- Keep the single-rule public behavior and API unchanged.

Acceptance: equality and ordered unit tests cover monotonic candidate ordering,
exact and minimum endpoints, stable enumeration, unsupported scalar errors,
and accounting overflow.

### 3. Implement selective aggregate downgrade planning

- Calculate the aggregate exact usage and return the all-exact plan immediately
  when it fits.
- Otherwise place every leaf's next coarser candidate into a deterministic
  selector keyed by bytes released, current usage, and schema order.
- Apply the best downgrade, enqueue that leaf's following downgrade, and repeat
  until the aggregate fits or no candidate remains.
- Materialize only the final selected candidate for each leaf where practical;
  bound temporary build memory if candidate compilation must retain payloads.
- Remove the proportional headroom allocation and smallest-upgrade
  redistribution path after parity tests cover all former success and failure
  cases.

Acceptance: in the 16-child skewed case, only the minimum set of leaves needed
to meet the limit becomes lossy; unrelated small leaves remain exact. The sum
of accounted child memory never exceeds the aggregate limit and the same input
always selects the same representations.

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
