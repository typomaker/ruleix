# Local candidate masks and Build columns

The baseline is `409a51b`. The production dump contains 96,630 constraint
rows and 1,447 distinct modifier IDs. The workload opens one Local per
request, searches 100 similar offers, and closes it. The 64 buyer requests
are split by the size of the buyer's original posting before filtering.
Hardware and toolchain are Apple M1 Max, macOS 26.6.2, Go 1.27.3, and
`GOMAXPROCS=1`.

## Baseline profiles

Two 50,000-request profiles per scenario produced the following ranges.
Allocations are per request.

| Scenario | Mean | p99 | p99.9 | Bytes | Allocations |
| --- | ---: | ---: | ---: | ---: | ---: |
| Mixed | 55.76–55.77 µs | 89.29–89.46 µs | 112.75–126.83 µs | 921 | 17 |
| At most eight IDs | 50.30–50.31 µs | 69.54–70.96 µs | 97.50–101.42 µs | 802 | 11 |
| 9–16 IDs | 73.64–73.79 µs | 94.67–96.58 µs | 126.46–139.13 µs | 1,390 | 43 |

The 9–16 ID profile included 1.30 s cumulative in ranking and 0.87 s
in candidate validation out of approximately 3.91 s of CPU samples.
The at-most-eight case accounted for 0.81 of 2.94 sampled seconds in
ranking.

## First mask trials

The initial implementation admitted an immutable Roaring planning posting
of 9–16 IDs and reused it only after resolving the leading operand again.
This excluded compact equality postings. In the broad group, the two
35,000-request runs took about 80 µs on the prototype against 74 µs on the
baseline and kept 43 allocations/request.

A follow-up admitted immutable equality set pointers, including the
compact representation. It checks the source identity on each offer and
uses the same ID slots to apply reached equality and small Between
predicates. Unsupported operands take the ordinary planner. In the first
interleaved series, the broad mean fell from 73.98 to 64.33 µs and
allocations fell from 43 to 19. The mixed mean moved from 55.83 to
54.27 µs and allocations from 17 to 12. The later checkpoint records latency, correctness, and memory controls.

## First column trials

The independent prototype keeps the ordinary ranking and result bitmap
path. Exact Include columns encode the matching value ordinal and provide
explicit wildcard and multiple-value states. The multiple-value state
checks the original posting. Exact small Between columns encode endpoint
positions and retain separate wildcard semantics on each side.

The first implementation improved the broad mean from 73.98 to 69.87 µs
in the same series but increased allocations from 43 to 143 and worsened
p99.9. It passed a slice backed by a local ID array to each column's
interface method. The follow-up passes the fixed-size array by value to
remove that potential escape. The allocation profile attributes about 5.12 million sampled objects
to the first collector across 50,000 broad requests. The revised collector
had no separately attributed allocation objects and restored 43
allocations/request. It took 66.95 µs in the matching profiled run
against 73.93 µs on the baseline. The broad p99.9 was 123.08 vs
135.58 µs in that profiled pair.

## Differential check

`TestProductionDumpLocalMatchesIndex` compares Local matches against
the Index path for 6,400 grouped and 6,400 rotating offers. It passed
on the baseline and all four independent prototypes. The test requires
the optional `RULEIX_PRODUCTION_DUMP` fixture.

## Combined and memory controls

The original combined prototype took 49.18–49.23 µs per request with
rotating offer keys, versus 55.56 µs for the first mask-only sample and
56.73–56.88 µs for the column-only samples. Its p99.9 was 98.46–101.79 µs
across the two 45,000-request runs. The combined prototype retained about
26.9 KB more per index and 128 B more per fresh Local in the first paired
heap measurements. Its fresh Local allocation count fell from 31 to 26.

The storage mitigation places the mask in ordinary Local storage and
keeps the inspector buffer in sampled Local storage. The compact column
mitigation uses byte-sized equality ordinals and reads the existing
Between endpoint dictionary. A separate combined variant enables only
the Between projection to isolate how much benefit requires dense
equality columns.

The two 65,000-request comparisons gave these medians after moving mask
storage out of the sampled Local:

| Scenario and metric | Baseline | Byte equality columns and Between dictionary | Between dictionary projection |
| --- | ---: | ---: | ---: |
| Mixed mean | 55.79 µs | 49.23 µs | 51.15 µs |
| Mixed p99.9 | 113.79 µs | 93.69 µs | 100.77 µs |
| 9–16 ID mean | 74.56 µs | 40.21 µs | 49.01 µs |
| 9–16 ID p99.9 | 140.06 µs | 88.13 µs | 99.63 µs |
| Rotating p99.9 | 125.77 µs | 102.17 µs | 101.81 µs |
| Retained bytes per index | 2,583,553 | 2,586,301 | 2,582,672 |
| Retained bytes per fresh Local | 4,355 | 3,427 | 3,427 |

The broad and rotating runs used 35,000 requests each. The indexes
were retained and measured in 10-build samples after two garbage
collections, and the Local heap samples retained 1,000 searched contexts.
The byte equality variant still increases the index's retained heap.
The Between projection retained the existing packed endpoint dictionary
and prepared query endpoint positions once per side per offer.

The four-bit equality column trial allocated about 0.5 B per ID for
eligible operands with at most thirteen distinct values. It kept
wildcard and multiple-value sentinels. Separate two-sample runs gave
97.81 µs mixed p99.9 and 91.10 µs broad p99.9. Projecting Between
coordinates in the mask without additional columns gave 98.60 µs and
93.94 µs in the same series. The packed equality path retained about
1.7 KB more per index than the projection-only path and used seven
additional allocations per Build. Its rotating p99.9 samples were
103.71 and 126.13 µs against 98.54 and 98.83 µs for projection only.

## Implementation

The Local mask stores up to sixteen ID slots belonging to the ranked
leading exact equality posting. Admission requires a posting larger than
the ordinary eight-ID scan, no exclusions, and supported operands across
the root conjunction. Every offer resolves the source posting again
before it can reuse those slots. A change clears the cached source and
uses the ordinary plan to evaluate the offer and discover a new source.

Equality predicates resolve the query posting once per reached operand.
For a small Between dictionary, two searches over ordered endpoint
positions prepare the inclusive comparisons for the whole offer. The
dictionary lookup then checks each active slot. Existing wildcard
bitmaps handle missing stored endpoints, and absent query endpoints
allow only stored wildcards. The final set bits are decoded in source
ID order. The exact result cache still gets the first chance to answer
a query. Closing the Local clears its mask.

Plain and sampled Local pools have separate inline storage layouts.
The plain pool carries the candidate slots and the sampled pool carries
the inspector counter chunk. Index search and observed execution use
the ordinary planner.

## Reproduction

Use the same benchmark sources on both revisions. Copy
`production_dump_workload_test.go` and
`production_dump_local_differential_test.go` into the baseline checkout
when compiling the controls. Set `RULEIX_PRODUCTION_DUMP` to the local
dump path and `GOMAXPROCS=1` for every timed process.

```bash
go test -c -o ruleix.test .
./ruleix.test -test.run '^TestProductionDumpLocalMatchesIndex$'
./ruleix.test -test.run '^$' -test.bench '^BenchmarkProductionDumpLocalRequest100$' -test.benchtime=60000x -test.benchmem
RULEIX_PLANNER_BUCKET=narrow ./ruleix.test -test.run '^$' -test.bench '^BenchmarkProductionDumpLocalRequest100$' -test.benchtime=35000x -test.benchmem
RULEIX_PLANNER_BUCKET=broad ./ruleix.test -test.run '^$' -test.bench '^BenchmarkProductionDumpLocalRequest100$' -test.benchtime=35000x -test.benchmem
RULEIX_PLANNER_ROTATE=1 ./ruleix.test -test.run '^$' -test.bench '^BenchmarkProductionDumpLocalRequest100$' -test.benchtime=35000x -test.benchmem
./ruleix.test -test.run '^$' -test.bench '^BenchmarkProductionDump(Build|Retained)Probe$' -test.benchtime=10x -test.benchmem
./ruleix.test -test.run '^$' -test.bench '^BenchmarkProductionDump(FreshLocal100|LocalRetainedProbe)$' -test.benchtime=1000x -test.benchmem
./ruleix.test -test.run '^$' -test.bench '^BenchmarkProduction(JoinLocal|ShapeSearch|ShapeLossySearch|ShapeExactCacheEntryHit|ShapeLocalClose)$' -test.benchtime=250ms -test.benchmem
```

Alternate the binaries and run each scenario in its own process. CPU and
allocation profiles for the broad cohort used 60,000 requests and the
test binary's `-test.cpuprofile` and `-test.memprofile` flags.

## Worktree checkpoint

Three interleaved samples per scenario compare the production source above
with `409a51b`. The narrow cohort includes 51 requests, and the broad
cohort includes 13. All numbers are per request of 100 offers.

| Scenario | Mean, baseline to candidate | p99, baseline to candidate | p99.9, baseline to candidate | Allocations, baseline to candidate |
| --- | ---: | ---: | ---: | ---: |
| Mixed | 56.00 → 50.64 µs | 90.00 → 69.58 µs | 119.58 → 97.42 µs | 17 → 12 |
| 9–16 IDs | 74.55 → 49.35 µs | 96.42 → 65.67 µs | 140.58 → 100.29 µs | 43 → 19 |
| Rotating | 58.16 → 51.57 µs | 97.96 → 73.42 µs | 125.25 → 102.96 µs | 17 → 12 |
| At most eight IDs | 50.57 → 50.62 µs | 69.67 → 70.63 µs | 99.75 → 103.21 µs | 11 → 11 |

The narrow mean ranges were 50.31–50.78 and 50.49–50.72 µs. The
p99.9 ranges were 95.71–106.58 and 97.29–105.13 µs. The production
path checks the admission threshold before calling the mask
construction helper.

Two heap and Build samples per revision measured 149.68 → 151.58 ms per
Build, approximately 46,369 allocations on both, and 2,582,459 →
2,582,517 retained bytes per index. Fresh Local allocation traffic moved
from 4,833 to 3,829 B and from 31 to 26 allocations per request. Live
Local retention moved from 4,355 to 3,427 B.

The joined Local controls measured 1.222 → 1.205 ms per cold
100-offer batch and 233.6 → 234.1 µs per warm batch. The exact shape
measured 214.7 → 214.3 ns per Local search. Lossy Local took
488.8 → 487.5 ns, with the same memory budget and candidate counts
on both paths. The exact and lossy Index controls preserved their
allocation counts. Warm exact Local cache hits stayed near 190 ns
and zero allocations.

In paired broad CPU profiles, cumulative ranking fell from 1.48 s
out of 4.74 s sampled to 0.10 s out of 3.28 s sampled. The profiles
measured 74.44 → 48.93 µs mean. This confirms that the new path spends
less time ranking the same small posting on successive offers.

The existing Between dictionary provided the chosen balance of search
latency, Build work, and retained index size. Dense Include ordinals
remain an experimental alternative. The byte layout increased retained
index memory, and the four-bit mitigation reduced that cost at a
smaller mixed-workload benefit and a higher rotating tail in the
paired samples.

The full Go test suite passed with the dump enabled. Focused tests
cover posting identity changes, bitmap and compact sources, resetting
the mask on Close, sampled inspector isolation, and inclusive endpoint
comparisons with wildcards. Changed production-line coverage was
127 of 129 executable lines (98.4%).
