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

### Candidate cardinality and the request tail

At `419334a`, 51 of the 64 buyers in the dump workload had at most eight
distinct modifier IDs in their customer UUID posting. The other 13 had
9–16 IDs. In a mixed 50,000-request run, 41 of the 51 observations at or
above the global p99.9 came from the 13 buyers. Within the two groups,
p99 was 94.42 and 214.38 µs, respectively. Group membership uses the raw
customer posting before other predicates.

Separate 60,000-request profile runs selected one group at a time via a
temporary benchmark overlay. Hardware, toolchain, snapshot and
`GOMAXPROCS` matched the preceding checkpoint. The up-to-eight-ID group
averaged 67.19 µs and 802 B/op (11 allocs/op). The larger group averaged
134.35 µs and 8,630 B/op (269 allocs/op). In the larger group's CPU profile,
`validateCandidateBitmap` accounted for 3.29 of 8.18 sampled seconds,
including 2.70 s in `Between.matchesID`. The second saved-plan lookup
after ranking fresh operands accounted for 0.03 s.

A trial reused child bitmaps from an unsuccessful saved-plan evaluation
by restoring schema order before ranking again. Across three interleaved
30,000-request samples per binary, baseline and trial medians were 85.37
and 85.88 µs mean, 205.25 and 206.29 µs p99, and 281.79 and 282.83 µs
p99.9. Both allocated 2,392 B and 63 objects per request. A 60,000-request
profile restricted to larger buyer postings confirmed that the trial
did not remove the dominant per-ID range validation. The bitmap reuse
trial did not establish a reduction in tail latency.

Two targeted validation trials also left the dominant work in place.
The first applied the existing candidate filter when an unmaterialized
range-like operand ranked first for 9–32 candidates. The second skipped
per-posting probes after a failed whole-block membership test in
descending ordered lookup. Two interleaved 35,000-request runs per
binary, selected from the larger buyer group, left allocations at
8,629 B/op and 269 allocs/op. The p99 ranges were 224–247 µs for the
baseline, 234–249 µs for the filter, and 231–232 µs for the whole-block
probe. Their 60,000-request profiles gave 2.54, 2.53, and 2.49 sampled
CPU seconds in candidate validation. Neither variant established a
net speedup.
Expanding the filter to other ranked operands and probing the aggregate
before scanning partially covered blocks also left the larger-buyer
mean near 135–143 µs with 269 allocations per request. The modified
paths remained outside the production patch.

#### Direct validation of ready Local candidates

The accepted candidate takes the direct-ID path for 9–16 candidates when
the leading bitmap is already available and every operand supports direct
ID validation. It writes the matches into the existing result bitmap while
reading the leading bitmap in place. The exact-result cache keeps its
normal eligibility rules. A preliminary direct-output trial also saved
time but bypassed that cache. A trial that copied the leading bitmap
before validation preserved the cache but used 66 allocations/request in
the larger buyer group, compared with 43 for direct validation.

Three alternating 50,000-request mixed samples against `419334a` gave
these per-request medians. Tests were built with the same temporary
benchmark overlay, which selects buyers by the number of distinct
`modifier_uuid` values for their customer UUID and times every request.

| Metric | Baseline | Candidate |
| --- | ---: | ---: |
| Mean latency | 81.85 µs | 76.84 µs |
| p99 | 195.92 µs | 150.71 µs |
| p99.9 | 255.46 µs | 168.25 µs |
| p99, up to eight buyer IDs | 96.33 µs | 96.00 µs |
| p99, 9–16 buyer IDs | 221.38 µs | 158.38 µs |
| Bytes allocated | 2,391 | 921 |
| Allocations | 63 | 17 |

Two samples per binary at 22,000 requests also restricted the workload
to the larger buyer group and repeated the first one or four offers
throughout each request. With one distinct offer, p99 was 237.50 versus
210.96 µs and allocations were 612 versus 216. With four distinct offers,
p99 was 263.77 versus 241.81 µs and allocations were 428 versus 124.

The larger-buyer CPU and allocation profiles used 60,000 requests.
Mean latency fell from 135.05 to 111.77 µs, with allocations moving from
8,630 B and 269 allocations to 1,390 B and 43 allocations. Cumulative
CPU samples in request execution fell from 5.41 to 4.72 s. General bitmap
intersection within it fell from 3.88 to 0.07 s. The new direct path
still validates individual IDs and avoids the generic bitmap preparation.

Two alternating 100-iteration retained-memory runs per binary measured
approximately 2,968, 94,192, 112,928, and 2,221,722 retained bytes per
Local for the cold, warm, adaptive and adversarial shapes. The change
uses existing scratch and result bitmaps and has no index build changes.

The control suite included exact and lossy production searches, skewed
equality, extracted exclusions, warm exact hits, joined batches and
Local teardown. Larger differences in the 200 ms controls prompted
isolated repeats. At 700 ms and three runs per binary, warm joined
Local remained near 245 µs/batch with zero allocations and cold joined
Local moved from 1.266 to 1.279 ms/batch with 2,141 allocations. The
isolated 600 ms joined Index control was 1.799 vs 1.782 ms per batch
with 2,004 allocations. The isolated 500 ms Local exclusion controls
were 2.983 vs 2.983 µs for ten candidates, and 4.041 vs 3.990 µs for
100 candidates, both with four allocations. Matching CPU and
allocation profiles at 2,600 Index batches and 750,000 ten-candidate
Local exclusion searches did not reproduce the short-run slowdown.

The focused test checks the 16-entry boundary, verifies that the source
bitmap stays intact, and verifies that the remaining rule runs direct
ID checks without materializing a bitmap. The complete test suite and
`go vet ./...` passed. Changed executable line coverage was 13 of 13.

#### Reusing one intersection across Local searches

The baseline for this experiment was `21e3206`. The dump benchmark uses
the latest activity start in the snapshot as the fixed request time. At
that time all 1,447 modifier IDs match the activity rule, so intersecting
activity with a buyer posting does not reduce it. In the 13 requests whose
buyer had 9–16 candidates, the store stayed the same in 1,044 adjacent
offer pairs. Intersecting the buyer and store postings removed at least
half the candidates in 1,005 of those pairs. This analysis counts distinct
modifier IDs per posting and does not assume those two postings will
always be the leading operands in an actual search.

The prototype used one entry in the root Local plan. It observed the
leading ready bitmap and the smallest other ready bitmap. It computed
their intersection on the second consecutive use of the pair and reused
it only when it removed at least half of the leading candidates. Both
versions used the existing `Local.Search` and schema. Matching bitmap
identities was sufficient because the prototype borrowed only immutable
postings or child cache entries that live for the Local's lifetime.

The bitmap variant used the same temporary benchmark overlay, toolchain,
machine and snapshot as the preceding experiment. Three alternating
50,000-request samples gave median p99 of 162.04 vs 153.21 µs and p99.9 of
203.50 vs 214.96 µs. Bytes per request increased from 921 to 1,087 and
allocations from 17 to 23. In 60,000-request CPU and allocation profiles
of the larger buyer group, direct candidate validation fell from 3.45 to
2.78 sampled CPU seconds. Pair selection and construction added 0.41 s,
and the sampled allocation profile attributed an estimated 1.35 million
allocation objects to the new pair path, mostly Roaring array
container copies. The synthetic warm Local retained a further 32 bytes
per context in two alternating 100-iteration measurements per binary.

Replacing the cached bitmap with an inline list of at most eight IDs
removed those allocation objects. At the same 50,000-request run length,
the plan-embedded list moved p99 from 158.00 to 147.13 µs and p99.9 from
214.46 to 184.79 µs, with one outlying candidate p99.9 of 354.71 µs.
Allocations stayed at 17 per request, but allocated bytes rose from 921
to 985. The retained-Local test also showed a 64-byte increase for the
adaptive case. The next variant moved the list to the reusable bitmap
pool and removed an unused observer fallback in that pool.

The pooled variant reused the one entry until the selected pair changed
or the Local closed. Three alternate mixed runs of 60,000 requests per
binary gave these medians:

| Metric per request | `21e3206` | Pooled intersection |
| --- | ---: | ---: |
| Mean latency | 77.28 µs | 75.68 µs |
| p99 | 151.42 µs | 139.42 µs |
| p99.9 | 171.42 µs | 160.46 µs |
| p99, 9–16 buyer IDs | 159.63 µs | 147.79 µs |
| p99, up to eight buyer IDs | 96.83 µs | 96.21 µs |
| Bytes allocated | 921 | 921 |
| Allocations | 17 | 17 |

The larger-buyer CPU and allocation profiles used 70,000 requests.
Mean latency fell from 111.60 to 105.50 µs, with 1,391 B and 43 allocations
per request on both binaries. Request execution used 5.68 vs 5.64 sampled
CPU seconds. Two alternating 100-iteration retained-memory runs per
binary measured approximately 128 fewer retained bytes per Local on
the cold, warm, adaptive and adversarial shapes. The pooled list takes
less space than the unused inline observer fallback it replaces.

The 350 ms controls covered cold and warm joined searches, exact-cache
hits, exclusions, lossy search, and Local teardown. Warm joined Local
was 236.89 vs 237.34 µs per batch with zero allocations. An apparent
short-run lossy Index slowdown did not recur in three isolated 600 ms
runs or in 75,000-iteration CPU and allocation profiles. The isolated
medians were 30.70 vs 30.48 µs, with 24 allocations on both versions.

Focused tests cover admission, nonselective and empty intersections,
rejection by remaining rules, immutable source bitmaps, and Local
reset. The full suite and `go vet ./...` passed. Changed executable
line coverage was 55 of 55.

## Build-time range lookup

The [small Between endpoint dictionary](between-small-bounds.md) records
the dump-based evaluation of build-time endpoint lookup and within-block
cardinality prefixes.

## Local candidate preparation

The [Local candidate validation trials](local-candidate-validation.md)
record the Include lookup, saved-plan lookup and cached-intersection trials.

## Ordered Build and Local lookup

The [ordered Build and Local planning experiments](build-and-local-lookup.md)
record the duplicate insertion, small Between interning, and Include memo
measurements against `2f913e2`.

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
