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

## Production dump Local request benchmark

`BenchmarkProductionDumpLocalRequest100` builds the supplied selection
schema from a November 17, 2026 JSON snapshot with 96,630 constraint rows
and 1,447 distinct modifier UUIDs. Each row uses `modifier_uuid` as the
result ID. Columns outside the schema are ignored. Schema fields that are
NULL throughout the snapshot contribute wildcard rules, and the loader
rejects non-null values in those columns. The dump stays outside Git.
Its SHA-256 is
`ad126179e389201fdacc5b2ab5ff202e76506340ee2d9b3f5207a96d14ec9472`.

The workload cycles through 64 distinct customers. Each request starts at
one observed offer for that customer and takes 100 distinct offers from the
snapshot's 150 observed store and slot combinations, ordered by store, day,
type, and slot midpoint. Customer UUID and activity time stay fixed within
the request. The activity time is the latest activity start in the dump.
Across the 6,336 adjacent offer pairs, 4,354 change one field, 1,128 change
two, and 854 change three. A separate scan of the snapshot predicts 526
offers with at least one matching modifier across the 6,400 offers. These
sequences are generated from indexed conditions and the actual production
request distribution is unknown.

One benchmark operation opens a Local, searches the offers sequentially
while reusing the result slice, and closes the Local. Loading, index
construction, offer generation, and request validation run outside the
timer. `TestProductionDumpLocalRequest` uses a small inline snapshot to
check adjacent offer changes, hits, misses, and modifier UUID deduplication
without requiring an external file.

```sh
RULEIX_PRODUCTION_DUMP=/path/to/snapshot.json GOMAXPROCS=1 \
  go test . -run '^$' -bench '^BenchmarkProductionDumpLocalRequest100$' \
  -benchtime=2s -count=3
```

On Apple M1 Max, macOS 26.6.2/arm64, Go 1.27.3, the three samples were
87,703, 87,405, and 89,939 ns/request. The median was 87,703 ns/request,
877.0 ns/offer, 2,392 B/request, and 63 allocs/request. The benchmark was
introduced on top of `f8536c9`. This is the first measurement for this
workload. Verified with `go test ./...` and `git diff --check`.

### Comparator-ordered `Between` cache overflow

The baseline was `9884d08` and the candidate was its working tree with
comparator-ordered `Between` overflow. Runs used Apple M1 Max, macOS 26.6.2,
Go 1.27.3, and `GOMAXPROCS=1`. Both binaries use the same snapshot and harness
described above. For quantiles, record elapsed time around each complete
request into a preallocated slice and compute nearest-rank percentiles after
the benchmark timer stops.

Three interleaved runs of 50,000 requests each gave these per-request medians:

| Metric | Baseline | Ordered overflow |
| --- | ---: | ---: |
| Mean latency | 83.92 µs | 83.71 µs |
| p99 | 194.58 µs | 194.50 µs |
| p99.9 | 258.83 µs | 260.04 µs |
| Bytes allocated | 2,391 | 2,391 |
| Allocations | 63 | 63 |

p99.9 varied from 256.50 to 273.83 µs across the baseline runs and from
258.08 to 343.29 µs across the candidate runs. The complete request shows
no consistent tail-latency improvement. The 70,000-request CPU and
allocation profiles also showed that bitmap planning and candidate
validation remain substantial parts of request processing.

In a separate alternating 30,000-request comparison with `v0.8.2`
(`7f32ddc`), median p99.9 was 187.56 µs on the tag and 246.79 µs on ordered
overflow. The same harness was built against the tag's dependencies and
Go 1.23 module language version, with a Go 1.24 build constraint on the
benchmark file for `testing.B.Loop`.

In `BenchmarkLocalBetweenReuse`, interleaved 250 ms Repeat, Alternate,
Cycle3, and Cycle4 Local controls stayed within 1 ns/op of their baseline
medians (60.7, 68.5, 71.6, and 74.0 ns/op), with 16 B/op and one allocation
each. A separate `WorkingSet1024/Local` profile run with
`-benchtime=3000000x -cpuprofile -memprofile` moved from 1,274 to 127 ns/op
with 18 B/op and one allocation in both samples. Cumulative CPU samples
inside cache lookup fell from 3.08 s in the linear scan to 0.10 s in the
binary search. The fixed iteration count avoids different warmup
amortization in the allocation metrics.

The retained-Local gate used `BenchmarkProductionShapeLocalRetainedMemory`
with `-benchtime=100x` and two runs per binary in alternating order. Cold,
warm, adaptive, and adversarial shapes held about 2,968, 94,192, 112,928, and
2,221,721 retained bytes per Local on both binaries. The index build uses the
same representation in both revisions.

Broad controls used `BenchmarkProductionJoinLocal` and
`BenchmarkProductionShapeLocalClose` at 200 ms and
`BenchmarkProductionShapeLossySearch` at 400 ms. Cold joined batches
remained near 1.20 ms with approximately 629,570 B/op, and the close
control remained near 247 µs with approximately 155,930 B/op. Lossy Local
search stayed near 431–435 ns/op with zero allocations.

The test suite covers comparator-equivalent timestamps, missing bounds, and
insertion and lookup across the overflow cutover. The changed executable
lines had 100% coverage (31 of 31) from `go test -covermode=count ./...`.

#### `All` plan refresh experiment

The first trial removed the second saved-plan lookup after ranking fresh
operands. `TestCompactBetweenMatchesBaseline` found extra matches because the
failed saved plan had left bitmaps in slots with a different operand order.
Overwriting the bitmap on every freshly ranked operand corrected all
differential tests. With that correction, removing the refresh reduced
dump p99 from 191.17 to 177.54 µs and p99.9 from 263.08 to 223.13 µs in
interleaved runs of 50,000 requests, with 2,391 to 2,159 B/request and
63 allocations on both variants.

The unrestricted omission increased `ProductionShapeLocalClose` from
246.45 to 412.97 µs and from approximately 155,908 to 157,694 B/op in
5,000-iteration profile runs. Cumulative CPU in
`intersectRankedInOrderObserved` grew from 0.12 to 0.44 s. Candidate
validation within that path grew from 0.03 to 0.26 s as broad searches
lost the cheaper validation that the refreshed plan made available.
The interleaved 200 ms cold joined-batch control also slowed from about
1.21 to 1.42 ms. Scope checks included warm joined batches, exact cache
hits, lossy searches, and skewed equality.

Skipping refresh only when the first ranked bitmap was already available
with at most eight candidates and all operands supported direct validation
preserved the cold controls. That variant and ordered overflow alone had
similar request times. In three 30,000-request interleaved runs, their
p99.9 ranges were 246.58–275.38 and 254.88–257.04 µs, respectively. In
70,000-request profiles they measured 254.00 and 253.29 µs. Neither refresh
variant is included in the candidate. Improving plan refresh remains open.

## Release measurements

### Immediate unbounded Local cache checkpoint

On 2026-09-12, baseline `d0e6daa` and the candidate working tree were measured
on Apple M1 Max, macOS/arm64, Go 1.26, with `-benchmem`, 300-500 ms runs, and
three to five samples. Production exact-result hits stayed at zero allocations:
the baseline medians were 188.6 and 190.5 ns/op for its first and second slots,
while the candidate's unbounded reverse-recency lookup measured about 190-192
ns/op for both queries. Repeated `Between` Local measured about 58 ns/op on
both revisions, and the four-value cycle remained about 70 ns/op. The new
1,024-value `Between` working set measured about 1.22 us/op versus 2.53 us/op
for `Index.Search`; comparable equality and exclusion caches use hash lookup
instead of a linear overflow scan. All reported warm cache paths retained their
existing allocation classes.

The joined-query checkpoint uses the identical
`BenchmarkProductionJoinLocal` harness on `v0.8.4` (`b7d0ef9`), the pre-change
main baseline `d0e6daa`, and candidate `2eb4fec`. Each Local processes a
reflected `5 x 5 x 2 x 2` Cartesian product: 100 queries where adjacent values
change exactly one of activity day, platform version, slot type, or DBS flag.
Runs were interleaved with `GOMAXPROCS=1`, `-benchmem`, `-benchtime=300ms`, and
five samples on Apple M1 Max with Go 1.26.

| Revision | Index batch, ns | Cold Local batch, ns | Warm Local batch, ns | Warm allocs/op |
| --- | ---: | ---: | ---: | ---: |
| `v0.8.4` (`b7d0ef9`) | 1,788,243 | 1,298,559 | 1,186,206 | 1,400 |
| Baseline (`d0e6daa`) | 1,729,227 | 1,271,996 | 1,155,796 | 1,400 |
| Candidate (`2eb4fec`) | 1,770,979 | 1,198,984 | 237,107 | 0 |

The candidate warm batch is 4.87x faster than the immediate baseline and
5.00x faster than the latest release. Cold Local improved 5.7% and 7.7%
respectively. Candidate cold filling retained every query immediately, changing
the measured cold allocation shape from about 934 KiB and 1,401 allocations to
628 KiB and 2,141 allocations per batch; the extra small allocations become
retained cache state rather than repeated warm work. A separate interleaved
`20x`, three-sample retained-memory run measured 241,258 B/Local for the
candidate versus 150,634 B/Local for both controls, a deliberate 60.2% increase
for the larger retained working set.

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
