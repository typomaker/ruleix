# Optimization registry

This document is the canonical registry of Ruleix performance work. It records
outcomes rather than chronological working notes. Detailed patches, profiles,
and removed benchmark harnesses remain available in Git.

Statuses are **accepted**, **rejected**, or **open**. Numbers are measurements
only when an entry identifies a baseline and candidate; otherwise the entry is
a design conclusion or hypothesis.

## Accepted optimizations

| Date | Area | Decision and evidence |
| --- | --- | --- |
| 2026-09-08 | Local cache | Split grouped query validation from ordinary prepared-slot lookup (`a89b6de`). Against `v0.8.3`, warm production `Local.Search` recovered from 230.1 to 220.4 ns/op; the `v0.8.2` control was 221.1 ns/op. |
| 2026-09-07 | Local cache | Treat `Local.Search` as the memory-for-speed path: retain wide exact results by entry count and reuse wide scratch bitmaps, while `Index.Search` keeps its 64 KiB pool cap (`9a206c2`). A 250,000-match query changed from 798 to 132 us/op and 124,921 to 0 B/op; retained memory intentionally changed from 2,344 to 1,076,008 B/Local. |
| 2026-09-07 | Equality antonyms | Compile strict wildcard-complement equality components and order intersections by selectivity (`f0b0d68`, `66a7c64`, `e69c83a`). The merged-main 4x4 early-empty case changed from 9,588 to 7,829 ns/op; broader gates were neutral. The maintained design is in [strict-equality-antonyms.md](strict-equality-antonyms.md). |
| 2026-09-03 | Lossy ordered build | Use one least-populated adjacent-pair merge and globally choose the smallest release (`49f0827`). Against `f10fca5`, production candidates changed from 3,802 to 358, Index search from 126.7 to 32.3 us/op, and Local search from 12.1 to 1.59 us/op. The owner accepted the measured build cost: 25.0 to 798.6 ms and 12.57 to 118.34 MB/op. |
| 2026-09-03 | Lossy ordered build | Scan ordered blocks directly when selecting a merge instead of allocating a flattened item slice (`2f12a7f`). Focused build traffic fell by about 89%: 283.5 to 30.7 MB/op for two children and 745.7 to 80.0 MB/op for four. |
| 2026-09-03 | Lossy ordered build | Reuse build-scoped ordered block backing arrays and use typed value accounting (`75da29d`). Against `e03fbc7`, the two-child case changed from 30.7 to 13.9 MB/op and 789,089 to 248,655 allocs/op; search gates remained comparable. |
| 2026-09-03 | Ordered state | Finalize and discard build-only ordered precision state (`50f5696`). Exact and lossy focused search controls remained effectively unchanged at about 68.5 and 97.3 ns/op with zero allocations. |
| 2026-09-02 | Dependency | Upgrade Roaring to v2.26.0 (`94ad57f`). Correctness and race suites passed. No performance claim was made for the dependency update alone. |
| 2026-09-02 | Lossy ranges | Add one aggregate level for wide lossy ordered postings (`0598735`). A focused 1,024-leaf range changed from 269,577 to 60,737 ns/op and 6,160 to 3,856 B/op; production search allocation classes were preserved. |
| 2026-09-02 | Compound ranges | Move `Between` and `CompareBy` to selective fused comparator-key layouts (`eb7a3dd`). This retained common ordered primitives while avoiding the rejected generic rule layout. Correctness, no-false-negative, race, and production gates passed after follow-up fixes. |
| 2026-09-01 | Ordered layout | Share the standalone ordered search layout between Exact and Lossy (`9be762a`). Against `a791a30`, selective adaptive and unknown-estimate paths improved 15.0% and 12.2% with unchanged allocation classes. |
| 2026-09-01 | Equality layout | Share posting and lookup primitives between Exact and Lossy (`a791a30`, later completed by `cae0d03` and `d1082cf`). Exact keeps comparable level-0 keys; lossy generations use compiled hashed keys. |
| 2026-09-01 | Equality codecs | Compile scalar, named-array, recursive-array, struct, complex, pointer, and time codecs during build (`59f8b73`, `e23a37c`). Focused warm Local measurements remained allocation-free and showed no search regression. |
| 2026-09-01 | Streaming Lossy build | Replace static representation ladders with generation-based streaming pressure and shared rebuild primitives (`3e739e4` through `0b73376`). The maintained design is in [lossy-index.md](lossy-index.md). |
| 2026-08-31 | Equality lookup | Specialize equality rules with up to four fixed values (`d81c9ca`). The cutover was chosen from comparable string-miss measurements; larger fixed shapes lost to map lookup. |
| 2026-08-29 | All execution | Canonicalize equivalent `All` aliases, reuse physical sources, and use direct-ID filtering for small candidates (`3863de3`, `7ca6dee`, `57ad985`). These changes removed redundant materialization and intersection work. |
| 2026-08-24 | All planning | Reuse cardinality estimates, propagate empty checks, and filter ordered/range children from existing candidates (`8e0578c` and related v0.7 commits). These are maintained in [index-architecture.md](index-architecture.md). |
| 2026-08-24 | Local lifecycle | Recycle cleared Local contexts and their bounded caches (`v0.7.0`). Warm search retains zero measured allocations for supported cached paths. |
| 2026-08-24 | Inspection | Move runtime observations off ordinary searches and sample explicitly inspected Local contexts (`v0.8.0`). The maintained contract is in [inspect-api.md](inspect-api.md). |

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
