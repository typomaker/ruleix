# Roadmap

This file is the active implementation plan for reducing search work and
temporary bitmap materialization. Completed work and rejected experiments
belong in [`ROADMAP_HISTORY.md`](ROADMAP_HISTORY.md); release-facing behavior
belongs in [`CHANGELOG.md`](CHANGELOG.md). Do not keep completed, superseded,
or unrelated proposals here.

## Objective

Finish the operation-cost `All` executor by proving that every retained local
state has a byte bound. Shared learning was removed after it failed its
cold-start evidence gate. The deterministic planner, adaptive operation loop,
and representation-specific operation audit are complete; their measured
results are recorded in
[`ROADMAP_HISTORY.md`](ROADMAP_HISTORY.md).

The current Apple M1 Max production-shaped cutover measurements are the
reference point, not a success claim:

| Path | Latency | Temporary memory | Allocations |
| --- | ---: | ---: | ---: |
| `Index.Search` | 41.5--42.1 us/op | 73,571--73,572 B/op | 31 allocs/op |
| warm `Local.Search` | 569.0--570.2 ns/op | 0 B/op | 0 allocs/op |

The rewrite is successful only when it materially reduces `Index.Search`
temporary bytes or latency on production-shaped and focused workloads while
preserving zero-allocation warm `Local.Search`. A focused microbenchmark win is
not sufficient for final cutover if production allocation traffic increases.

## Non-negotiable constraints

- Preserve the public API, exact matching semantics, first-insertion result
  order, duplicate external-ID behavior, and all `Inspect` observations.
- Keep the built `Index` immutable and ordinary concurrent `Index.Search`
  lock-free. Planner learning may publish only from sampled `Local` contexts.
- Keep `uint32` internal IDs and Roaring bitmaps for broad candidate sets.
- Keep the common `All` planning path allocation-free for up to eight children
  and reuse bounded pooled storage for larger groups.
- Never materialize a complete child result once per candidate as an implicit
  `MatchID` fallback. Unsupported operations must remain explicit.
- Treat learned statistics as optional hints. Missing, stale, discarded, or
  adversarial profiles must affect performance only, never correctness.
- Introduce no public planner API until internal benchmarks demonstrate a need.
- Accept, reject, or revise every optimization using repeatable benchmark
  evidence. Remove rejected production code and record the result in history.

## Required benchmark and correctness gate

Run the focused tests introduced by each step and, before accepting every
executor change, run at least:

```sh
go test ./...
go test -race ./...
go test -run '^$' -bench '^BenchmarkProductionShapeSearch/(Index|Local)$' \
  -benchmem -benchtime=1s -count=5 .
go test -run '^$' -bench '^BenchmarkProductionShapeParallelLocalBatch100$' \
  -benchmem -benchtime=1s -count=5 .
go test -run '^$' -bench '^BenchmarkProductionScaleSearch/' \
  -benchmem -benchtime=500ms -count=3 .
go test -run '^$' -bench '^BenchmarkProductionShapeLocalRetainedMemory$' \
  -benchtime=3x -count=3 .
```

Compare medians against the parent commit and the reference above. Also run
empty, selective, wildcard-heavy, range-heavy, large-result, nested-`All`,
high-churn, and inaccurate-estimate cases. Each new or changed benchmark must
include a nearby comment containing its latest local result and a complete
reproduction command.

An implementation step may be accepted when it provides a clear focused win
and does not materially regress the production matrix. Treat a repeatable
regression above 3% in latency, any new warm-`Local` allocation, or unexplained
growth in `Index.Search` allocation count or bytes as a failed gate. Record
noisy results and rerun with a longer benchtime before deciding.

## Implementation plan

The differential cutover and cleanup gate is complete; its evidence is recorded
in `ROADMAP_HISTORY.md`. The map-backed and partially representation-retained
source-ID prototypes in history reject those intermediate implementations, not
the integrated identity design below: their infrastructure cost was measured
before unconditional operand elimination, inspector fan-out, removal of the
old linear path, and map-free compiled ordinals were present together.

### 1. Run one integrated physical-source identity experiment

Performance is decided once, after the complete experimental executor exists.
Intermediate milestones have correctness and structural gates only; do not
reject or retain production pieces from their standalone timings. Keep the
baseline and integrated variants callable from one benchmark binary until the
final decision so machine drift cannot be mistaken for a 3--6% effect.

#### A. Build a reproducible A/B harness

- Keep the current executor unchanged as the baseline. Add an internal,
  test-selectable execution mode that builds and searches the same schema and
  data through the integrated identity executor without a public option or API.
  Both variants must run as sibling sub-benchmarks in one process, alternate
  order across repetitions, return identical result slices, and expose
  test-only counters for materializations, intersections, `Contains` checks,
  skipped operands, mask tests, and physical inspector executions.
- Correct the duplicate benchmark contract: exercise the public root `All`
  path through `searchAllMatches`, and assert from counters whether the baseline
  linear equality deduplication actually ran. Keep separate nested-`All` cases
  for `allRule.searchRanked`; never label one path as the other's baseline.
- Commit the complete experimental implementation or a test-only replay of it
  before measuring. A reproduction command must execute both variants; a
  history-only description and a command that can run only the retained
  baseline are not sufficient evidence.

#### B. Compile the complete identity representation

- During collision-checked bitmap interning, assign a build-scoped `uint32`
  physical source ID only after fingerprint, cardinality, and `Bitmap.Equals`
  confirm equality. Store representation-native source metadata beside each
  equality wildcard and concrete posting; perform no bitmap-pointer-to-ID map
  lookup during search.
- Give every provably identical ordered/range operation a canonical operation
  ID containing representation ownership, bound role, direction, inclusivity,
  wildcard policy, comparator policy, and stable query-bound identity. Use it
  only for unconditional aliases of the same proven operation. Never merge
  independently opaque getters/comparators or ranges that merely happen to
  materialize equal results for one query.
- Compile each `All` directly into unique physical operands. Remove
  unconditional same-rule and same-operation aliases before node-cache
  construction, ranking, insertion, and execution. Attach all logical wrapper
  and `Inspect` sites to the remaining operand rather than retaining duplicate
  executable children.
- For query-dependent equality wildcard/posting pairs, assign dense duplicate
  class ordinals directly in the compiled operand. Temporary maps are allowed
  during `Build` but must be discarded before the immutable `Index` is
  published. Retain no global pointer-ID map, per-`All` class map, sparse-ID
  mask, or query-dependent identity state.

#### C. Execute the integrated design without transitional overhead

- Unique operands and unconditional aliases execute with no identity branch:
  unique operands remain ordinary compiled operations, while unconditional
  aliases no longer exist in the executable operand slice.
- Conditional equality operands use one dense ordinal and one stack `uint64`
  checked mask for up to 64 classes. Larger groups reuse bounded zeroed words
  from existing pooled scratch storage. The operation performs at most one
  bit test/set and no loop, map lookup, pointer lookup, or packed-key search.
- Remove the old inline equality-key linear scan from the integrated variant;
  do not pay for both mechanisms. Preserve it unchanged in the baseline mode.
- Execute a shared physical operand once and fan its observations out to every
  attached `Inspect` and exact-details site. Confirm that insertion, planning,
  materialization, intersection, candidate validation, cache hit/miss, and
  result-cardinality observations preserve their logical multiplicity without
  repeating the physical search operation.
- Preserve existing exact-intersection cache input identities and epochs.
  Cached or temporary results inherit operation identity only when construction
  proved it; equal contents from unrelated operations never become aliases.

#### D. Pass correctness before collecting performance evidence

- Add table tests for 0, partial, and full duplication with 2, 4, 8, 64, and
  more than 64 classes; equality wildcard/concrete combinations; forced
  fingerprint collisions; nested `All`; cache replacement and epoch changes;
  similar non-equivalent rules; duplicate external IDs; and empty, selective,
  broad, and exclusion-heavy results.
- Cover equality, ordered, `Between`, and `CompareBy` aliases. For range forms,
  prove both that canonical aliases execute once and that coincident results
  from distinct operations execute independently.
- Compare baseline and integrated result order and every `Inspect` snapshot
  over the generated differential matrix and fuzz corpus. Assert operation
  counters as well as results so tests detect an optimization that is correct
  only because it silently performed duplicate work.
- Run `go test ./...` and `go test -race ./...`. Fix correctness failures before
  benchmarking, but do not use intermediate latency to remove individual parts
  of the integrated design.

#### E. Measure the complete design in one decision run

- Benchmark sibling `Baseline` and `Integrated` variants for every combination
  of 2, 4, and 8 children; 0%, partial, and 100% duplication; equality with and
  without wildcards; ordered, `Between`, `CompareBy`; nested groups; and
  multiple inspected aliases. Include a non-duplicate control whose compiled
  hot path contains no mask check.
- Split lifecycle costs into distinct benchmarks: `Index.Search`; `Local()` and
  `Close()` without search; first `Search` on pre-created cold locals; second-use
  admission; and stable warm `Local.Search`. Also report build time and
  allocations, retained bytes per index, retained bytes per cold/warm/adaptive
  local, and test-only operation counts.
- Run focused A/B benchmarks with `-benchtime=2s -count=10` and analyze paired
  samples with `benchstat`. Alternate variant order and repeat any delta near
  the 3% boundary with `-benchtime=5s -count=15`. Record raw commands, hardware,
  Go version, medians, confidence intervals, bytes, allocations, and operation
  counts beside each benchmark and in history.
- Run the complete production gate from this roadmap for both modes in the same
  binary, including parallel, scale, retained-memory, build, wildcard-heavy,
  range-heavy, large-result, high-churn, inaccurate-estimate, and lossy cases.

**Accept the integrated executor when**

- Differential, fuzz, ordinary, and race tests pass; every skipped operation
  has a collision-checked immutable source identity or complete canonical
  operation proof; all `Inspect` observations match the baseline.
- Full and partial duplicate workloads show a repeatable end-to-end latency or
  retained-byte win explained by lower physical-operation counters. Warm
  `Local.Search` remains at 0 B/op and 0 allocs/op.
- Zero-duplicate controls execute no mask checks and remain within 3% latency
  of baseline. Production `Index.Search`, warm/parallel `Local`, build,
  retained-memory, scale, and lossy gates have no material regression or new
  unexplained allocation class.

**Reject the integrated executor when**

- The completed map-free design still fails the gates above. Remove all
  experimental production code together, retain the A/B harness when it is
  useful as a regression benchmark, and record results as rejection of this
  exact integrated architecture. Do not generalize the decision to a design
  that was not executable in the recorded comparison.

**Result**

One reproducible experiment determines whether the combined savings from
build-time operand elimination, representation-native source identity, a
constant-time checked mask, and inspector fan-out outweigh their total cost.
No incomplete layer is accepted or rejected on its standalone performance.
