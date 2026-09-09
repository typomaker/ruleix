# Optimization registry

This document is the canonical registry of Ruleix performance work. It records
outcomes rather than chronological working notes. Detailed patches, profiles,
and removed benchmark harnesses remain available in Git.

Statuses are **accepted**, **rejected**, or **open**. Numbers are measurements
only when an entry identifies a baseline and candidate; otherwise the entry is
a design conclusion or hypothesis.

## Accepted optimizations

| Date | Name | Area | Decision and evidence |
| --- | --- | --- | --- |
| 2026-09-08 | Prepared-Key Fast Path | Local cache | Split grouped query validation from ordinary prepared-slot lookup (`a89b6de`). Against `v0.8.3`, warm production `Local.Search` recovered from 230.1 to 220.4 ns/op; the `v0.8.2` control was 221.1 ns/op. |
| 2026-09-07 | Wide Local Reuse | Local cache | Treat `Local.Search` as the memory-for-speed path: retain wide exact results by entry count and reuse wide scratch bitmaps, while `Index.Search` keeps its 64 KiB pool cap (`9a206c2`). A 250,000-match query changed from 798 to 132 us/op and 124,921 to 0 B/op; retained memory intentionally changed from 2,344 to 1,076,008 B/Local. |
| 2026-09-07 | Selective Antonym Intersection | Equality antonyms | Compile strict wildcard-complement equality components and order intersections by selectivity (`f0b0d68`, `66a7c64`, `e69c83a`). The merged-main 4x4 early-empty case changed from 9,588 to 7,829 ns/op; broader gates were neutral. The maintained design is in [strict-equality-antonyms.md](strict-equality-antonyms.md). |
| 2026-09-03 | Atomic Adjacent Compaction | Lossy ordered build | Use one least-populated adjacent-pair merge and globally choose the smallest release (`49f0827`). Against `f10fca5`, production candidates changed from 3,802 to 358, Index search from 126.7 to 32.3 us/op, and Local search from 12.1 to 1.59 us/op. The owner accepted the measured build cost: 25.0 to 798.6 ms and 12.57 to 118.34 MB/op. |
| 2026-09-03 | Direct Block Selection | Lossy ordered build | Scan ordered blocks directly when selecting a merge instead of allocating a flattened item slice (`2f12a7f`). Focused build traffic fell by about 89%: 283.5 to 30.7 MB/op for two children and 745.7 to 80.0 MB/op for four. |
| 2026-09-03 | Build-Scoped Block Reuse | Lossy ordered build | Reuse build-scoped ordered block backing arrays and use typed value accounting (`75da29d`). Against `e03fbc7`, the two-child case changed from 30.7 to 13.9 MB/op and 789,089 to 248,655 allocs/op; search gates remained comparable. |
| 2026-09-03 | Precision-State Finalization | Ordered state | Finalize and discard build-only ordered precision state (`50f5696`). Exact and lossy focused search controls remained effectively unchanged at about 68.5 and 97.3 ns/op with zero allocations. |
| 2026-09-02 | Roaring 2.26 Foundation | Dependency | Upgrade Roaring to v2.26.0 (`94ad57f`). Correctness and race suites passed. No performance claim was made for the dependency update alone. |
| 2026-09-02 | Wide Range Aggregation | Lossy ranges | Add one aggregate level for wide lossy ordered postings (`0598735`). A focused 1,024-leaf range changed from 269,577 to 60,737 ns/op and 6,160 to 3,856 B/op; production search allocation classes were preserved. |
| 2026-09-02 | Fused Compound Range Layout | Compound ranges | Move `Between` and `CompareBy` to selective fused comparator-key layouts (`eb7a3dd`). This retained common ordered primitives while avoiding the rejected generic rule layout. Correctness, no-false-negative, race, and production gates passed after follow-up fixes. |
| 2026-09-01 | Unified Ordered Search | Ordered layout | Share the standalone ordered search layout between Exact and Lossy (`9be762a`). Against `a791a30`, selective adaptive and unknown-estimate paths improved 15.0% and 12.2% with unchanged allocation classes. |
| 2026-09-01 | Unified Equality Postings | Equality layout | Share posting and lookup primitives between Exact and Lossy (`a791a30`, later completed by `cae0d03` and `d1082cf`). Exact keeps comparable level-0 keys; lossy generations use compiled hashed keys. |
| 2026-09-01 | Compiled Equality Codecs | Equality codecs | Compile scalar, named-array, recursive-array, struct, complex, pointer, and time codecs during build (`59f8b73`, `e23a37c`). Focused warm Local measurements remained allocation-free and showed no search regression. |
| 2026-09-01 | Streaming Generation Pressure | Streaming Lossy build | Replace static representation ladders with generation-based streaming pressure and shared rebuild primitives (`3e739e4` through `0b73376`). The maintained design is in [lossy-index.md](lossy-index.md). |
| 2026-08-31 | Quaternary Equality Cutover | Equality lookup | Specialize equality rules with up to four fixed values (`d81c9ca`). The cutover was chosen from comparable string-miss measurements; larger fixed shapes lost to map lookup. |
| 2026-08-29 | Physical Source Executor | All execution | Canonicalize equivalent `All` aliases, compile and reuse physical bitmap sources, and gate direct-ID filtering to small candidates (`3863de3`, `7a77d53` through `72d496c`, `57ad985`). First/second cold physical-identity controls changed from 5.00/4.55 to 1.51/1.17 us; stable warm Local was within 0.7%. |
| 2026-08-29 | Exact Result Short Circuit | Local exact results | Cache bounded exact `All` intersections, retain compact internal IDs, and validate semantic query keys before child lookup (`3f121bf`, `7052118`, `6381837`, `562f1a3`, `8d82a44`). The original compact-ID step changed warm Local from 546.8 to 407.4 ns/op; early query validation changed it from 404.5 to 228.1 ns/op. Later `9a206c2` intentionally replaced byte limits with fixed entry counts. |
| 2026-08-29 | Costed All Executor | All execution | Compile cached capabilities and choose sources, filters, and direct checks by cardinality and measured work (`4e18b0e`, `5e2858d` through `f006194`, `57ad985`). The capability step improved warm and parallel Local by 5.2% and 5.4%; the broader executor gate is included in the `v0.8.1` to `v0.8.2` release comparison. |
| 2026-08-25 | Equality Identity Deduplication | Equality identity | Deduplicate equality results and shared wildcard materialization by immutable physical identity (`caff3cb`, `7235523`, `af878fd`). The later physical-source compiler subsumed this representation while retaining the behavior. |
| 2026-08-24 | Adaptive Candidate Narrowing | All planning | Reuse cardinality estimates, propagate empty checks, replan after narrowing, and stop on disjoint bitmap ranges (`8e0578c`, `c51c08b`, `05154e6`, `a99f92f`, `8f9b958`). Filter ordered, `Between`, and `CompareBy` children through existing candidates (`e3169c1`, `ffd7a32`, `4f6ad11`). These paths remain in the adaptive executor described by [index-architecture.md](index-architecture.md). |
| 2026-08-24 | Routed Ordered Aggregates | Ordered search | Use 64-item blocks, logical block routing, and bounded aggregates for wide ranges (`a384977`, `f50d22b`, `6086669`). These choices remain in `orderedIndex`; the historical focused and production controls are preserved at the named commits. |
| 2026-08-24 | Exact All Flattening | Compilation | Flatten exact nested `All` nodes while preserving inspected and Lossy boundaries (`7dd464d`). This removes intermediate intersections and is covered by the rule and differential correctness suites. |
| 2026-08-24 | Recyclable Adaptive Locals | Local lifecycle | Adapt node caches from two to four entries, recycle admitted bitmaps and cleared Local contexts, and reuse learned per-context plans (`9b14ecf`, `2446e00`, `36639b6`, `6c9c6fb`). Warm search retains zero measured allocations for supported cached paths. |
| 2026-08-24 | Sampled Inspector Isolation | Inspection | Move runtime observations off ordinary searches and sample explicitly inspected Local contexts (`6499b0b`, released in `v0.8.0`). The maintained contract is in [inspect-api.md](inspect-api.md). |
| 2026-08-24 | Cardinality-Aware Decoding | Result decoding | Avoid an allocating iterator for small `All` results and batch wide results (`6b14c9a`, building on `00493bd`). The current cutover is 4,096 IDs. |
| 2026-08-19 | Inline Equality Leaves | Equality lookup | Replace map-backed leaves with unary and binary immutable forms when possible (`e899deb`, released in `v0.5.1`). Focused lookup improved 5.1-6.2% and retained rule memory fell 26.3-35.7%; broad end-to-end search remained neutral because output enumeration dominated. |
| 2026-08-19 | Compact COW Search Core | Core search layout | Use direct optional getters, compact postings, build-time bitmap interning, extracted exclusions, and copy-on-write scratch bitmaps (`0c1a447`, `9e86574`, `e8b9828`, `3e00ba5`, `a2c857f`, `483ddec`, released in `v0.5.0`). Against `v0.4.2`, Index/Local search improved 19.6%/81.2%, search B/op fell 48.0%/95.0%, and Build improved 25.4% with 69.6% less B/op. |

## Rejected optimizations

| Date | Area | Rejection and evidence |
| --- | --- | --- |
| 2026-09-07 | ID chunking | Remove global contiguous ID chunks (`a78b647`). They saved at most 15.8% posting memory, amplified candidates up to 9.9x, slowed search up to 3.4x, and introduced a separate 36.3% warm Local regression through per-ID slice copying. |
| 2026-09-07 | Lossy directory | Reject linear directory, intermediate ladder, and adaptive sharding variants (`9fd0263`, `b858f32`, `c425a8c`). They did not preserve the required search, allocation, and retained-memory gates. |
| 2026-09-07 | Equality antonym tree | Revert the adaptive antonym tree model (`18ff564` through `be3df36`). The simpler component model supplied the measurable benefit without speculative semantics. |
| 2026-09-03 | Equality lookup | Reject `index+1` map sentinels and manually unrolled string hashing (`b621693`). Interleaved tests were neutral or 1–1.5% slower; profiles showed no net CPU gain. |
| 2026-09-03 | Local equality cache | Reject a per-Local semantic-to-physical-key cache and unrestricted wide-result reuse (`4126439`). The first regressed repeated/rotating paths by 2.4%/4.2% and retained an extra wrapper per leaf; the second was neutral and added a special path. |
| 2026-09-03 | Frozen equality lookup | Reject the tested frozen lookup prototype (`d62908c`). It did not establish a mode-independent retained-layout benefit sufficient to replace the map contract. |
| 2026-09-02 | Bitmap-only ranges | Reject materializing `Between` and `CompareBy` unions before intersection (`884cfd1`). Attempts reduced some latency but increased production allocation traffic to as much as 71,433 B/op and 34 allocs/op. |
| 2026-09-02 | Generic compound ordered rule | Reject the first common `Between`/`CompareBy` ordered-rule prototype (`328a45e`). It reduced candidates but regressed uncached production latency from 26,075 to 76,379 ns/op; profiles localized extra range-container searches. |
| 2026-09-02 | Range alternatives | Reject a second aggregate level and bitmap-only/fused scratch retries (`23364f9`, `94ad57f` era experiments). They failed the no-search-regression or allocation gates. |
| 2026-09-02 | Lossy quality planners | Reject posting-mass splitting, dense ordered items, build-selected equality salts, a static Pareto frontier, and greedy quality scoring (`480995e`, `2e3ae79`, `8e356c2`). None improved the complete production gate without unacceptable build or search costs. |
| 2026-08-29 | Duplicate bitmap scan | Reject per-query scans for duplicate cached equality bitmaps (`8de84ba`). Build-time physical identity already removed redundant work more cheaply. |
| 2026-08-23 | Shared planner profiles | Remove cross-query `All` planner profiles (`v0.8.2`). They did not demonstrate a cold-start benefit; deterministic per-Local planning remained. |

## Open optimization opportunities

These are hypotheses, not performance claims:

- Define one stable aggregate release benchmark before publishing benchmark
  results in every changelog entry. It must cover production-shaped Index and
  Local search, parallel Local search, Build, allocations, retained index
  memory, and retained Local memory on fixed data and hardware.
- Reduce remaining Roaring clone/container traffic during lossy generation
  rebuild without pooling objects beyond a build lifetime.
- Consider a frozen equality container only if it releases the mutable map,
  preserves the common Exact/Lossy rule shape, and improves retained memory as
  well as lookup time.
- Revisit allocation-free fused range intersection only if the destination
  bitmap can remain caller-owned and all public search paths remain neutral or
  better.
- Measure a root-`All` executor for `Index.Visit` and `Local.Visit`. The current
  `Visit` path calls generic `root.search`, so it does not use candidate scans,
  physical-source deduplication, learned Local plans, or compact exact results.
  Any prototype must preserve insertion order and immediate callback stopping,
  and must compare both Visit APIs rather than extrapolating from Search.
- Measure post-exclusion exact-result reuse for `Local.Search`. Query-key hits
  are currently bypassed whenever extracted exclusions exist, even though the
  later bitmap-result cache can still be reused before applying exclusions.
  A candidate must include exclusion query identity in the collision-safe key
  or prove that the cached value is post-exclusion, and must account for the
  retained IDs and keys.
- Profile the small-root `All` `roaring.FastAnd` result allocation against a
  caller-owned pooled intersection. The current up-to-eight-child path returns
  a fresh bitmap, while the larger-child path already uses a pooled bitmap.
  Earlier sequential-intersection experiments were rejected, so this should be
  retried only with the exact current physical-source and cardinality shapes.

## Recording a decision

Add one row with the date, area, status, baseline and candidate revisions,
environment, comparable command, time/allocation/retained-memory result, and
rationale. Keep raw output outside the repository; Git preserves removed
harnesses and historical reports. A hypothesis must be labelled as such.

## Repository audit 2026-09-08

The historical cleanup removed 36 files and 6,469 lines consisting of focused
benchmark harnesses, root-level benchmark reports, dated measurements, and
experiment diaries. Unique outcomes were consolidated into this registry and
[performance-history.md](performance-history.md).

Three `_benchmark_test.go` files remain because ordinary correctness tests use
their shared schemas and fixtures: `benchmark_test.go`,
`lossy_all_benchmark_test.go`, and `production_shape_benchmark_test.go`.
Separating those fixtures is deferred until an aggregate release benchmark is
designed; deleting them now would duplicate or weaken the correctness suite.

Verification: local Markdown links resolve, `go test ./...` and
`go test -race ./...` pass, and `git diff --check` passes. `make lint` still
reports 30 pre-existing findings in unchanged files; the cleanup introduces no
new lint findings.

## Registry verification 2026-09-09

The accepted table was checked against current source ownership with `git log`,
`git blame`, and the named revisions. Every accepted implementation remains on
`main`; none is present only in a reverted commit. The audit corrected the
physical-source row, which previously cited A/B harness commit `7ca6dee` as if
it were the implementation, and restored separately measurable accepted work
that the 2026-09-08 history consolidation had folded into broad release rows.

The three new opportunities above are static code observations, not measured
performance claims. No production code or benchmark was changed for this
audit. Verification for this documentation-only change: Markdown links,
`go test ./...`, `go test -race ./...`, `make lint`, and `git diff --check`.

Each accepted entry received a unique, mechanism-focused name on 2026-09-09.
The names are labels only and do not alter the recorded decisions or evidence.
