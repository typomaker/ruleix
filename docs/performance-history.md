# Performance history

This document contains the canonical comparable release and checkpoint
measurements for Ruleix. Raw output, one-off harnesses, and investigation
diaries are not retained in the working tree; Git preserves them at the
revisions named below.

Ruleix does not yet have a single aggregate release benchmark. Therefore the
changelog must not imply that every release has an overall benchmark result.
Once the benchmark is defined, each release should report its result here and
link to this document.

## Measurement policy

- Compare the previous release and candidate in separate worktrees on the same
  idle machine, using identical benchmark code and data.
- Record revisions, Go and OS versions, hardware, `GOMAXPROCS`, command,
  duration/count, time, B/op, allocs/op, retained index memory, and retained
  Local memory.
- Use medians from multiple runs. Treat differences near 1–2% as noise unless
  a longer interleaved run confirms them.
- A checkpoint is not a release result until the measured code is tagged.
- Search latency, allocations, retained memory, correctness, or result quality
  may not regress. A documented build-cost increase is allowed only under the
  exception in [project-governance.md](project-governance.md).

## Release measurements

### `v0.8.3` to `v0.8.4`

Measured 2026-09-08 on Apple M1 Max, macOS arm64, Go 1.26.0,
`GOMAXPROCS=1`; five 1-second runs of the production search paths. The `v0.8.2`
control was built with the same harness.

| Path | `v0.8.2` | `v0.8.3` | `v0.8.4` | Outcome |
| --- | ---: | ---: | ---: | --- |
| `Index.Search` | 35.58 us/op | 32.45 us/op | 32.46 us/op | `v0.8.3` gain retained |
| warm `Local.Search` | 221.1 ns/op | 230.1 ns/op | 220.4 ns/op | 4.2% regression corrected |
| parallel Local, ns/search | 229.9 | 233.2 | 226.4 | parity restored |
| Index allocations | 28 | 28 | 28 | unchanged |
| Local allocations | 0 | 0 | 0 | unchanged |

`v0.8.3` improved uncached Index search by 8.8% against `v0.8.2`, but regressed
warm Local by 4.1%. CPU and compiler analysis localized the regression to
group-aware query validation added to the ordinary cache-hit loop. Commit
`a89b6de` split that path and produced `v0.8.4`. Retained index and Local
controls remained within noise. Historical reproduction details are available
at tag `v0.8.4` before the repository-history cleanup.

### `v0.8.1` to `v0.8.2`

Measured 2026-08-31 on Apple M1 Max, Go 1.26.0, `GOMAXPROCS=1`. The measured
`v0.8.2` commit was `d81c9ca`; changes before the tag affected only benchmarks,
documentation, and lint formatting.

| Path | Change from `v0.8.1` | Change from `v0.7.1` |
| --- | ---: | ---: |
| `Index.Search` | 18.4% faster | 19.7% faster |
| warm `Local.Search` | 59.5% faster | 91.4% faster |
| parallel Local | 59.0% faster | 90.8% faster |

Build time matched `v0.8.1`. Build traffic differed from `v0.8.1` by only
0.6% B/op and 0.2% allocs/op, while retained index memory rose 0.4%. Against
`v0.7.1`, build traffic was 4.9% higher in B/op and 22.0% higher in allocs/op.

### `v0.4.2` to `v0.5.0`

Production-shaped workload with 38,098 constraints on Apple M1 Max; medians of
five runs with 50 iterations.

| Scenario | `v0.4.2` | `v0.5.0` | Change |
| --- | ---: | ---: | ---: |
| `Index.Search` | 115.8 us | 93.1 us | -19.6% |
| Index memory/op | 208.2 KB | 108.3 KB | -48.0% |
| Index allocations | 74 | 33 | -55.4% |
| `Local.Search` | 36.2 us | 6.8 us | -81.2% |
| Local memory/op | 152.4 KB | 7.7 KB | -95.0% |
| Local allocations | 50 | 7 | -86.0% |
| Build | 44.6 ms | 33.3 ms | -25.4% |
| Build memory/op | 14.83 MB | 4.51 MB | -69.6% |
| Build allocations | 311,254 | 24,587 | -92.1% |

The improvement came from fewer temporary bitmaps, compact postings, and
removal of duplicate structures.

## Significant checkpoints

These entries guide future comparisons but are not release-wide results.

| Date | Revisions | Result |
| --- | --- | --- |
| 2026-09-07 | `v0.8.2` to post-chunk-removal HEAD | Removing unused ID chunking restored Local parity while preserving an 11.27% Index improvement. |
| 2026-09-07 | Local cache policy | A 250,000-match warm query changed from 798 to 132 us/op and 124,921 to 0 B/op; retained memory increased from 2,344 to 1,076,008 B/Local by design. |
| 2026-09-03 | `f10fca5` to atomic-pair selector | Production candidates changed from 3,802 to 358, Index from 126.7 to 32.3 us/op, and Local from 12.1 to 1.59 us/op. Build changed from 25.0 to 798.6 ms and 12.57 to 118.34 MB/op; the owner accepted this search-first trade-off. |
| 2026-09-03 | `50f5696` to direct block scan | Focused lossy ordered build allocation traffic fell about 89% without a search allocation change. |
| 2026-09-02 | `328a45e` range aggregate | A focused 1,024-leaf range changed from 269,577 to 60,737 ns/op and 6,160 to 3,856 B/op. |
| 2026-09-01 | `a791a30` common ordered layout | Selective adaptive and unknown-estimate searches improved 15.0% and 12.2%; allocation classes were unchanged. |
| 2026-08-29 | `v0.8.1` to `72d496c` | Index changed from 43.14 to 33.66 us/op and 73,394 to 40,851 B/op; warm Local remained 564 ns/op with zero allocations. Build memory increased 4.9% and allocations 9.6%, later mitigated by the equality-class compiler. |
| 2026-08-24 | `v0.7.1` to `6499b0b` | A 7.5% Local regression was localized to an unnecessary lossy planning lookup in exact schemas and corrected before release. |
| 2026-08-24 | `v0.6.0` to `18a0bb2` | Index was within noise, but Local and parallel Local regressed 47.0% and 25.7%; the checkpoint was blocked and not treated as a release result. |

Optimization-specific rationale and rejected alternatives belong in
[optimization-decisions.md](optimization-decisions.md).
