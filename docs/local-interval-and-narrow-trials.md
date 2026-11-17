# Local interval masks and narrow direct execution

The baseline is `a4ac2fd`. The 96,630-row dump workload has 64 customer
requests with 100 offers and one Local per request. Both orders contain
the same offers. The rotating order visits positions with stride 37.
Measurements run on Apple M1 Max, Go 1.27.3 and `GOMAXPROCS=1`.

## Interval opportunity

The probe samples the leading exact equality posting by resolving the
currently admitted mask source or ranking operands on the same Local.
Explicit diagnostic ranking can update that Local's plan. The numbers
describe the sampled workload and are independent of timed benchmarks.
An offer is eligible when the sampled source contains at most 16 IDs
and each remaining operand supports direct candidate masks.

The three varying non-time fields are store, slot weekday and slot
type. The buyer is fixed in a request and the other equality fields
are absent in this snapshot. The probe counts boundary crossings by
comparing each leading candidate's truth values for both endpoints of
each compiled Between predicate. Final mask counts use the resulting IDs
and are summed per request.

| Metric from 6,400 offers | Grouped | Rotating |
| --- | ---: | ---: |
| Offers repeating an earlier non-time key in the same request | 4,230 | 4,230 |
| Offers with an eligible leading posting | 6,400 | 6,400 |
| Adjacent offers retaining the same eligible leading posting | 6,336 | 6,336 |
| Adjacent offers retaining both leading posting and non-time key | 4,230 | 0 |
| Same guard with a changed time input | 4,230 | 0 |
| Same guard with a candidate endpoint truth-value transition | 2,331 | 0 |
| Same guard with unchanged final matches | 3,950 | 0 |
| Sum of distinct final masks across requests | 364 | 364 |

The grouped sequence has enough repeated guards to test a bounded
last-interval cache. Rotating offers test its admission overhead.
The prototype checks the leading source identity and immutable
equality postings before reusing a result. It compiles inclusive or
exclusive limits independently for each query endpoint and verifies
presence before reusing the region.

## Narrow execution prototypes

The first variant extends the existing mask to 1–8 IDs and handles
the singleton posting representation. The prepared variant compiles
the supported root operands at Build, uses the existing leading
operand selection on the first offer, and reuses the verified compact
source for subsequent offers. The independent variants preserve the
existing broad path for the 9–16 candidate cohort.

Two interleaved 35,000-request samples gave 50.33 → 37.54 µs
mean and 70.48 → 50.25 µs p99 in the narrow cohort for the
prepared variant. The mixed mean moved from 50.55 to 40.45 µs.
The narrow and mixed allocation counts stayed at 11 and 12 per
request. The first interval cache combined with narrow execution
gave about 45 µs narrow and 48 µs mixed, slower than narrow
execution alone. An additional trial moves equality resolution
ahead of the time predicates, reuses the resolved postings on
cache misses, and compares that path with interval reuse
disabled to separate the effects.

## Admission metadata and interval cost

A second prepared variant compiled a Boolean admission flag instead of
a per-operand interface table. Two Build and memory samples measured
about 151.9 → 151.5 ms per Build, 2.58 MB retained per index on both
versions, and 3,427 B retained per searched fresh Local. Fresh Local
allocation traffic stayed at 3,829 B and 26 allocations per request.
The flag costs no separate heap allocation.

The first interval prototype increased fresh Local retention from 3,427
to 3,555 B. In two 35,000-request broad samples it increased mean time
from 49.05 to 55.47 µs. Combined with narrow admission it gave 45.16 µs
in the narrow group against 37.54 µs for the prepared variant alone.
The cache performs additional guard resolution and tracks per-endpoint
regions. The follow-up shares equality resolution with the miss path.

The first combined cache spent 1.06 s of 3.03 s cumulative CPU samples
in interval lookup, construction, and its child operations in the
55,000-request narrow profile. The paired non-cache run measured
39.43 µs mean against 45.61 µs for the combined cache.

The follow-up resolved equality postings once and measured the same
filter order with interval reuse enabled and disabled. In two
interleaved runs, interval reuse improved the mixed mean from 41.28
to 40.15 µs and mixed p99.9 from 93.83 to 90.94 µs against that
specific control. The prepared direct path measured 42.07 µs mean
and 87.58 µs p99.9. With rotating offers, interval p99.9 was 98.02
µs, compared with 94.63 µs for the matched cache-disabled path and
89.10 µs for prepared direct execution. This isolates the small
benefit of reuse in grouped offers from the evaluation order and
the cost of guard checks when the next offer changes the key.

The differential check matched all 12,800 grouped and rotating offers
against Index.Search for each interval variant. A focused prototype
test enumerated lower and upper query coordinates independently,
including missing values, exact endpoint equality and stored
wildcards. Every accepted region returned the same bits as direct
candidate validation.

The equality-first trial then tested operand ordering directly,
without allocating an interval state. The first ordering sweep had
wide tails across the binaries. A separate 55,000-request broad
profile measured 52.32 µs on the baseline, 51.68 µs with the flag
and 51.52 µs with reordered operands. A further variant limits
reordering to the new path for at most eight IDs.

With ordering limited to the narrow mask path, the two interleaved
samples measured 32.37 µs narrow mean and 92.52 µs narrow p99.9.
The flag-only control measured 40.82 and 107.13 µs. The broad
mean was 50.90 µs against 50.80 µs on the baseline. The two mixed
p99.9 samples had a higher median than the flag-only trial, so full
request tails require the separate longer worktree comparison.

Three longer 60,000-request worktree runs gave 51.36 → 36.33 µs
mixed mean and 101.58 → 87.04 µs mixed p99.9. The narrow runs gave
51.00 → 31.97 µs and 103.38 → 77.33 µs p99.9. The broad mean
was 49.58 → 50.02 µs, while broad p99.9 varied between 94.63 and
185.96 µs in the candidate samples. A longer head-to-head repeat
compares the reordered and flag-only variants against the baseline
to check that tail before selecting the patch.

## Reproduction

Set `RULEIX_PRODUCTION_DUMP` to the local dump path and run the same
commands on `a4ac2fd` and the candidate. Alternate the binaries for
latency and allocation comparisons. The timed workload uses Local
for every offer. The optional differential test also calls Index
to check results.

```bash
export GOMAXPROCS=1
go test -c -o ruleix.test .
./ruleix.test -test.run '^TestProductionDumpLocalMatchesIndex$'
./ruleix.test -test.run '^$' -test.bench '^BenchmarkProductionDumpLocalRequest100$' -test.benchtime=60000x -test.benchmem
RULEIX_PLANNER_BUCKET=narrow ./ruleix.test -test.run '^$' -test.bench '^BenchmarkProductionDumpLocalRequest100$' -test.benchtime=35000x -test.benchmem
RULEIX_PLANNER_BUCKET=broad ./ruleix.test -test.run '^$' -test.bench '^BenchmarkProductionDumpLocalRequest100$' -test.benchtime=35000x -test.benchmem
RULEIX_PLANNER_ROTATE=1 ./ruleix.test -test.run '^$' -test.bench '^BenchmarkProductionDumpLocalRequest100$' -test.benchtime=35000x -test.benchmem
./ruleix.test -test.run '^$' -test.bench '^BenchmarkProductionDump(Build|Retained)Probe$' -test.benchtime=10x -test.benchmem
./ruleix.test -test.run '^$' -test.bench '^BenchmarkProductionDump(FreshLocal100|LocalRetainedProbe)$' -test.benchtime=1000x -test.benchmem
./ruleix.test -test.run '^$' -test.bench '^BenchmarkProduction(JoinLocal|ShapeSearch|ShapeLossySearch|ShapeExactCacheEntryHit|ShapeLocalClose)$' -test.benchtime=300ms -test.benchmem
```

The final head-to-head latency check uses 80,000 mixed and 65,000
broad requests per sample. CPU and allocation profiles for the
narrow path use 55,000 requests and the test binary's
`-test.cpuprofile` and `-test.memprofile` flags.

In the 80,000-request mixed head-to-head, the medians were 50.79,
41.84, and 35.59 µs mean for the baseline, flag-only path, and
narrow reordered path. Mixed p99.9 was 101.29, 87.25, and 81.63 µs.
The 65,000-request broad follow-up gave p99.9 of 100.17, 95.88,
and 104.17 µs, respectively. The next variant moves narrow mask
execution into a separate function so the broad mask still runs
its established dispatch loop.

## Selected execution

Build marks eligible exact root conjunctions and records their equality
operands in a byte-sized bitset. The normal planner selects the leading
operand from the first offer. A compact or singleton posting can seed
the narrow mask, and subsequent offers must resolve the same immutable
source before reusing its ID slots. The narrow loop resolves reached
equality values before applying Between. The broad mask uses the
original dispatch loop. Shape checks, source changes, exclusions, and
sampled observation retain the ordinary fallback.

| Scenario | Mean, baseline to candidate | p99, baseline to candidate | p99.9, baseline to candidate | Allocations |
| --- | ---: | ---: | ---: | ---: |
| Mixed | 50.56 → 35.51 µs | 69.79 → 61.96 µs | 99.83 → 85.38 µs | 12 |
| At most eight IDs | 50.58 → 31.48 µs | 69.79 → 47.00 µs | 97.88 → 74.79 µs | 11 |
| 9–16 IDs | 49.10 → 49.13 µs | 65.25 → 64.63 µs | 100.38 → 100.83 µs | 19 |
| Rotating | 51.55 → 36.23 µs | 73.60 → 62.52 µs | 108.04 → 83.98 µs | 12 |

Mixed and broad numbers are medians of three interleaved samples of
80,000 and 65,000 requests per binary. Narrow and rotating numbers
use two samples of 35,000 requests. Allocation counts were the same
on both versions.

Bounded interval reuse remains experimental. Sharing equality
resolution recovered some of the cost on grouped offers. The
equivalent cache-disabled path established that reuse itself had
little tail benefit compared with the direct executor and added cost
on rotating offers. The selected path keeps one immutable source
posting and its ID slots in the existing Local mask.

Two Build samples per revision gave 152.55 → 150.45 ms, about
46,370 allocations, and about 2.58 MB of retained index memory
on both revisions. The fresh Local comparison measured 26
allocations and 3,829 allocated bytes per request, with 3,427 B
of retained heap per searched Local. The mitigated interval
prototype retained 3,555 B in the same control.

Representative exact Index search took 27.75 → 27.63 µs and
exact Local search took 214.45 → 213.80 ns. The lossy Local
control took 488.80 → 479.45 ns and retained its 358-candidate
result and zero allocations. Warm joined Local searches took
232.30 → 233.43 µs per batch and zero allocations. These
representative controls used paired 250 ms benchmark samples.

The full suite passed with the production dump enabled, including
the 12,800-offer differential check and focused tests for singleton
postings, source changes, supported shapes, and Local reset.
`go vet ./...` and `git diff --check` passed. Changed production
coverage was 64 of 64 executable lines.

## Comparison with v0.8.2

The benchmark compares tag `v0.8.2` (`7f32ddc`) with `4de8383`
on the same 96,630-row dump. Both binaries use Go 1.27.3 on Apple
M1 Max, macOS 26.6.2, with `GOMAXPROCS=1` during measurement. The
tag checkout uses its original production dependencies. Only the
three dump test files and their UUID dependency were copied into
the temporary tag checkout. The test files use a Go 1.24 build
constraint there for the current benchmark loop.

Median latency and allocation measurements come from three samples
per binary in old/current/current/old/old/current order. The mixed
samples use 60,000 requests and each other scenario uses 35,000.
Every timed request searches 100 offers through one Local.

| Scenario | Metric | v0.8.2 | 4de8383 |
| --- | --- | ---: | ---: |
| Mixed | Mean | 74.79 µs | 35.45 µs |
| Mixed | p99 | 132.17 µs | 61.83 µs |
| Mixed | p99.9 | 204.63 µs | 83.75 µs |
| Mixed | Allocated bytes | 3,053 | 845 |
| Mixed | Allocations | 89 | 12 |
| At most eight IDs | p99.9 | 140.67 µs | 76.38 µs |
| 9–16 IDs | p99.9 | 462.58 µs | 97.63 µs |
| Rotating | p99.9 | 205.58 µs | 90.25 µs |

The three broad p99.9 samples spanned 447.17–576.96 µs on the tag
and 94.33–111.33 µs on the candidate. The differential test passed
on both builds for all 12,800 grouped and rotating offers, comparing
each revision's Local output with its Index output. The latency
commands are the same as in the reproduction section above.

Two alternating Build samples of ten indexes per binary measured
158.97 → 151.41 ms, 4,734,814 → 4,782,772 allocated bytes, and
45,672 → 46,369 allocations per Build. Retained index heap was
2,585,692 → 2,581,447 B per index. In 1,000-request fresh Local
samples, allocation traffic was 7,184 → 3,829 B and 106 → 26
allocations per request. Retained Local heap was 4,711 → 3,427 B.
